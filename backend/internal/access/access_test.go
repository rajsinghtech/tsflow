package access

import (
	"bytes"
	"context"
	"encoding/json"
	"log"
	"mime"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/rajsinghtech/tsflow/backend/internal/config"
	"tailscale.com/client/tailscale/apitype"
	"tailscale.com/tailcfg"
)

func init() {
	gin.SetMode(gin.TestMode)
}

type stubWhoIs struct {
	calls []string
	fn    func(addr string) (*apitype.WhoIsResponse, error)
}

func (s *stubWhoIs) WhoIs(_ context.Context, remoteAddr string) (*apitype.WhoIsResponse, error) {
	s.calls = append(s.calls, remoteAddr)
	if s.fn == nil {
		return nil, errIdentityUnavailable
	}
	return s.fn(remoteAddr)
}

func capMap(capability string, values ...string) tailcfg.PeerCapMap {
	raw := make([]tailcfg.RawMessage, 0, len(values))
	for _, value := range values {
		raw = append(raw, tailcfg.RawMessage(value))
	}
	return tailcfg.PeerCapMap{tailcfg.PeerCapability(capability): raw}
}

func who(login, name, node string, groups []string, caps tailcfg.PeerCapMap) *apitype.WhoIsResponse {
	return &apitype.WhoIsResponse{
		Node: &tailcfg.Node{Name: node},
		UserProfile: &tailcfg.UserProfile{
			LoginName:   login,
			DisplayName: name,
			Groups:      groups,
		},
		CapMap: caps,
	}
}

func headerConfig() config.Access {
	cfg := &config.Config{
		TailscaleAPIKey: "key",
		TailscaleAPIURL: "https://api.tailscale.com",
		Port:            "8080",
		PollInterval:    "5m",
		InitialBackfill: "6h",
		Access: config.Access{
			Mode:             config.AccessModeHeader,
			Capability:       "example.com/cap/tsflow",
			CapabilityHeader: config.DefaultCapabilityHeader,
			TrustedProxies:   "10.0.0.0/8, 127.0.0.1",
			GroupsHeader:     "X-Tsflow-Groups",
			GroupGrantsRaw:   `{"group:eng":{"tailnets":["beta"]},"ops@example.com":{}}`,
			Autoscope:        config.AccessAutoscopeGroups,
		},
	}
	if err := cfg.Validate(); err != nil {
		panic(err)
	}
	return cfg.Access
}

func TestGrantUnionAndDeviceScope(t *testing.T) {
	grants, err := config.ParseGrantMap([]byte(`{"group:eng":{"tailnets":["alpha"]},"ops@example.com":{},"group:lab":{"tailnets":[]}}`))
	if err != nil {
		t.Fatal(err)
	}
	var allow Allow
	allow.Add(grants["group:eng"])
	allow.Add(grants["group:lab"])
	if allow.All || !allow.Permits("alpha") || allow.Permits("lab") {
		t.Fatalf("explicit union = %+v", allow.TailnetIDs())
	}
	allow.Add(grants["ops@example.com"])
	if !allow.All || !allow.Permits("lab") {
		t.Fatal("omitted tailnets should allow every id")
	}

	scope := DeviceScopeFor(config.AccessAutoscopeGroups, "ada@example.com", []string{"group:eng", "ops@example.com", "group:other"}, grants)
	if scope == nil || strings.Join(scope.Tags, ",") != "tag:eng" || strings.Join(scope.Owners, ",") != "ops@example.com" {
		t.Fatalf("scope = %+v", scope)
	}
	userScope := DeviceScopeFor(config.AccessAutoscopeUser, "Ada@example.com", nil, nil)
	if userScope == nil || len(userScope.Owners) != 1 || userScope.Owners[0] != "Ada@example.com" {
		t.Fatalf("user scope = %+v", userScope)
	}
	if DeviceScopeFor(config.AccessAutoscopeOff, "ada@example.com", nil, nil) != nil {
		t.Fatal("off scope should be unset")
	}
}

func TestWhoIsModeFiltersTailnets(t *testing.T) {
	const capName = "example.com/cap/tsflow"
	whois := &stubWhoIs{fn: func(addr string) (*apitype.WhoIsResponse, error) {
		if addr != "100.64.0.8:1234" {
			t.Errorf("WhoIs addr = %q", addr)
		}
		return who("ada@example.com", "Ada Lovelace", "ada.example.ts.net.", []string{"group:eng"}, capMap(capName, `{"tailnets":["alpha"]}`)), nil
	}}
	cfg := config.Access{
		Enabled:    true,
		Mode:       config.AccessModeTsnet,
		Capability: capName,
		Autoscope:  config.AccessAutoscopeUser,
		GroupGrants: map[string]config.Grant{
			"group:eng": {Tailnets: []string{"beta"}},
		},
	}
	router := gin.New()
	router.Use(Middleware(cfg, whois))
	router.GET("/data", func(c *gin.Context) {
		ident, ok := FromGin(c)
		if !ok {
			t.Fatal("missing identity")
		}
		c.JSON(http.StatusOK, gin.H{
			"login":    ident.Login,
			"node":     ident.Node,
			"tailnets": ident.Allow.TailnetIDs(),
			"owners":   ident.DeviceScope.Owners,
		})
	})

	code, body := do(router, "100.64.0.8:1234", "/data", nil)
	if code != http.StatusOK || !bytes.Contains(body, []byte(`"alpha"`)) || !bytes.Contains(body, []byte(`"beta"`)) {
		t.Fatalf("whois allow = %d %s", code, body)
	}
	if !bytes.Contains(body, []byte(`"ada@example.com"`)) || !bytes.Contains(body, []byte(`ada.example.ts.net.`)) {
		t.Fatalf("identity = %s", body)
	}

	whois.fn = func(string) (*apitype.WhoIsResponse, error) {
		return who("ada@example.com", "Ada", "node.", nil, nil), nil
	}
	code, body = do(router, "100.64.0.8:1234", "/data", nil)
	if code != http.StatusForbidden || !bytes.Contains(body, []byte("missing access grant")) {
		t.Fatalf("missing grant = %d %s", code, body)
	}

	whois.fn = func(string) (*apitype.WhoIsResponse, error) {
		return who("ada@example.com", "Ada", "node.", nil, capMap(capName, `{"role":"viewer"}`)), nil
	}
	code, body = do(router, "100.64.0.8:1234", "/data", nil)
	if code != http.StatusForbidden || !bytes.Contains(body, []byte("invalid access grant")) {
		t.Fatalf("invalid grant = %d %s", code, body)
	}
}

func TestHeaderModeTrustsOnlyConfiguredProxies(t *testing.T) {
	cfg := headerConfig()
	cfg.Enabled = true
	cfg.Autoscope = config.AccessAutoscopeOff
	router := gin.New()
	router.Use(Middleware(cfg, nil))
	router.GET("/data", func(c *gin.Context) {
		ident, _ := FromGin(c)
		c.JSON(http.StatusOK, gin.H{
			"tailnets": ident.Allow.TailnetIDs(),
			"login":    ident.Login,
			"name":     ident.Name,
			"groups":   ident.Groups,
		})
	})

	headers := map[string]string{
		"Tailscale-User-Login":       "ada@example.com",
		"Tailscale-User-Name":        mime.QEncoding.Encode("utf-8", "Ada Löve"),
		"Tailscale-App-Capabilities": `{"example.com/cap/tsflow":[{"tailnets":["alpha"]}]}`,
		"X-Tsflow-Groups":            "group:eng, group:eng, ops@example.com",
		"X-Forwarded-For":            "100.64.0.9",
	}
	code, body := do(router, "203.0.113.9:443", "/data", headers)
	if code != http.StatusForbidden || !bytes.Contains(body, []byte("missing access grant")) || bytes.Contains(body, []byte("ada@example.com")) {
		t.Fatalf("spoofed headers = %d %s", code, body)
	}

	code, body = do(router, "10.1.2.3:443", "/data", headers)
	if code != http.StatusOK {
		t.Fatalf("trusted = %d %s", code, body)
	}
	var payload struct {
		Tailnets []string `json:"tailnets"`
		Login    string   `json:"login"`
		Name     string   `json:"name"`
		Groups   []string `json:"groups"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Login != "ada@example.com" || payload.Name != "Ada Löve" || strings.Join(payload.Tailnets, ",") != "*" {
		t.Fatalf("payload = %+v", payload)
	}
	if strings.Join(payload.Groups, ",") != "group:eng,ops@example.com" {
		t.Fatalf("groups = %v", payload.Groups)
	}

	bare := map[string]string{
		"Tailscale-User-Login":       "ada@example.com",
		"Tailscale-App-Capabilities": `{"example.com/cap/tsflow":[{"tailnets":["alpha"]}]}`,
	}
	code, body = do(router, "10.0.0.8:9", "/data", bare)
	if code != http.StatusOK || !bytes.Contains(body, []byte(`"alpha"`)) || bytes.Contains(body, []byte(`"*"`)) {
		t.Fatalf("capability only = %d %s", code, body)
	}
}

func TestHeaderWhoIsUsesForwardedPeer(t *testing.T) {
	cfg := headerConfig()
	cfg.Enabled = true
	cfg.Autoscope = config.AccessAutoscopeUser
	const capName = "example.com/cap/tsflow"
	whois := &stubWhoIs{fn: func(addr string) (*apitype.WhoIsResponse, error) {
		if addr != "100.64.0.8" {
			t.Errorf("WhoIs peer = %q", addr)
		}
		return who("ada@example.com", "Ada", "ada.example.ts.net.", nil, capMap(capName, `{"tailnets":["alpha"]}`)), nil
	}}
	router := gin.New()
	router.Use(Middleware(cfg, whois))
	router.GET("/data", func(c *gin.Context) {
		ident, _ := FromGin(c)
		c.JSON(http.StatusOK, gin.H{
			"tailnets": ident.Allow.TailnetIDs(),
			"login":    ident.Login,
			"owners":   ident.DeviceScope.Owners,
		})
	})
	headers := map[string]string{
		"X-Forwarded-For":            "203.0.113.4, 100.64.0.8",
		"Tailscale-User-Login":       "mallory@example.com",
		"Tailscale-App-Capabilities": `{"example.com/cap/tsflow":[{"tailnets":["gamma"]}]}`,
		"X-Tsflow-Groups":            "group:eng",
	}
	code, body := do(router, "10.0.0.2:4000", "/data", headers)
	if code != http.StatusOK || !bytes.Contains(body, []byte("alpha")) || !bytes.Contains(body, []byte("beta")) || bytes.Contains(body, []byte("gamma")) {
		t.Fatalf("whois proxy = %d %s", code, body)
	}
	if bytes.Contains(body, []byte("mallory@example.com")) || !bytes.Contains(body, []byte("ada@example.com")) {
		t.Fatalf("identity should come from WhoIs: %s", body)
	}
	if len(whois.calls) != 1 {
		t.Fatalf("calls = %v", whois.calls)
	}

	code, _ = do(router, "198.51.100.4:9", "/data", headers)
	if code != http.StatusForbidden || len(whois.calls) != 1 {
		t.Fatalf("untrusted whois calls = %d %v", code, whois.calls)
	}
}

func TestDebugLogOmitsIdentityUnlessEnabled(t *testing.T) {
	var buf bytes.Buffer
	original := log.Default().Writer()
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(original) })

	cfg := config.Access{
		Enabled:    true,
		Mode:       config.AccessModeTsnet,
		Capability: "example.com/cap/tsflow",
		Debug:      false,
	}
	whois := &stubWhoIs{fn: func(string) (*apitype.WhoIsResponse, error) {
		return who("hidden@example.com", "Hidden", "node.", nil, capMap(cfg.Capability, `{}`)), nil
	}}
	router := gin.New()
	router.Use(Middleware(cfg, whois))
	router.GET("/", func(c *gin.Context) { c.Status(http.StatusNoContent) })
	if code, _ := do(router, "100.64.0.1:1", "/", nil); code != http.StatusNoContent {
		t.Fatalf("status %d", code)
	}
	if bytes.Contains(buf.Bytes(), []byte("hidden@example.com")) {
		t.Fatalf("identity logged without debug: %s", buf.String())
	}

	buf.Reset()
	cfg.Debug = true
	router = gin.New()
	router.Use(Middleware(cfg, whois))
	router.GET("/", func(c *gin.Context) { c.Status(http.StatusNoContent) })
	if code, _ := do(router, "100.64.0.1:1", "/", nil); code != http.StatusNoContent {
		t.Fatalf("status %d", code)
	}
	if !bytes.Contains(buf.Bytes(), []byte("DEBUG access:")) || !bytes.Contains(buf.Bytes(), []byte("hidden@example.com")) {
		t.Fatalf("debug log = %s", buf.String())
	}
}

func TestCapabilityPresenceAllowsAllTailnets(t *testing.T) {
	allow, err := grantsFromValues(nil)
	if err != nil || !allow.All {
		t.Fatalf("nil values = %+v %v", allow, err)
	}
	allow, err = grantsFromValues([]json.RawMessage{json.RawMessage(`{"tailnets":["alpha"]}`), json.RawMessage(`{"tailnets":["beta"]}`)})
	if err != nil || allow.All || !allow.Permits("alpha") || !allow.Permits("beta") || allow.Permits("gamma") {
		t.Fatalf("union = %+v %v", allow.TailnetIDs(), err)
	}
}

func do(handler http.Handler, remote, target string, headers map[string]string) (int, []byte) {
	req := httptest.NewRequest(http.MethodGet, target, nil)
	req.RemoteAddr = remote
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec.Code, rec.Body.Bytes()
}
