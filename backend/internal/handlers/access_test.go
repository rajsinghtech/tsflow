package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/rajsinghtech/tsflow/backend/internal/access"
	"github.com/rajsinghtech/tsflow/backend/internal/config"
	"github.com/rajsinghtech/tsflow/backend/internal/database"
	"tailscale.com/client/tailscale/apitype"
	"tailscale.com/tailcfg"
)

func TestWhoAmIWithoutIdentity(t *testing.T) {
	h := NewHandlers(nil, nil, nil, "test")
	router := gin.New()
	router.GET("/health", h.HealthCheck)
	api := router.Group("/api")
	api.GET("/health", h.HealthCheck)
	api.GET("/whoami", h.WhoAmI)

	for _, path := range []string{"/health", "/api/health", "/api/whoami"} {
		code, body := serve(router, http.MethodGet, path)
		if code != http.StatusOK {
			t.Fatalf("%s status=%d body=%s", path, code, body)
		}
		if path == "/api/whoami" && !bytes.Equal(bytes.TrimSpace(body), []byte(`{"autoscope":"off"}`)) {
			t.Fatalf("whoami = %s", body)
		}
	}
}

func TestAccessFiltersTailnetsAndKeepsHealthOpen(t *testing.T) {
	server := httptest.NewServer(tailnetHTTP(t, map[string]string{
		"alpha.example": "alpha-device",
		"beta.example":  "beta-device",
	}))
	defer server.Close()
	store := newAPIStore(t)
	h := registryHandlers(t, store, []config.TailnetSpec{
		{ID: "alpha", Name: "alpha.example", APIURL: server.URL, APIKey: "alpha-secret"},
		{ID: "beta", Name: "beta.example", APIURL: server.URL, APIKey: "beta-secret"},
	})

	const capName = "example.com/cap/tsflow"
	whois := &accessStub{fn: func(addr string) (*apitype.WhoIsResponse, error) {
		if addr != "100.64.0.5:9" {
			return nil, nil
		}
		return &apitype.WhoIsResponse{
			Node:        &tailcfg.Node{Name: "ada.example.ts.net."},
			UserProfile: &tailcfg.UserProfile{LoginName: "ada@example.com", DisplayName: "Ada", Groups: []string{"group:ops"}},
			CapMap: tailcfg.PeerCapMap{
				tailcfg.PeerCapability(capName): {`{"tailnets":["alpha"]}`},
			},
		}, nil
	}}
	cfg := config.Access{
		Enabled:    true,
		Mode:       config.AccessModeTsnet,
		Capability: capName,
		Autoscope:  config.AccessAutoscopeGroups,
		GroupGrants: map[string]config.Grant{
			"group:ops": {Tailnets: []string{"beta"}},
		},
	}

	router := gin.New()
	router.GET("/health", h.HealthCheck)
	api := router.Group("/api")
	api.GET("/health", h.HealthCheck)
	api.Use(access.Middleware(cfg, whois))
	api.GET("/whoami", h.WhoAmI)
	api.GET("/tailnets", h.ListTailnets)
	api.GET("/devices", h.GetDevices)

	code, body := serveFrom(router, http.MethodGet, "/health", "203.0.113.8:9", nil)
	if code != http.StatusOK || !bytes.Contains(body, []byte(`"status":"healthy"`)) {
		t.Fatalf("root health = %d %s", code, body)
	}
	code, body = serveFrom(router, http.MethodGet, "/api/health", "203.0.113.8:9", nil)
	if code != http.StatusOK || !bytes.Contains(body, []byte(`"status":"healthy"`)) {
		t.Fatalf("api health = %d %s", code, body)
	}

	code, body = serveFrom(router, http.MethodGet, "/api/devices?tailnet=beta", "203.0.113.8:9", nil)
	if code != http.StatusForbidden || !bytes.Contains(body, []byte("tailscale identity unavailable")) {
		t.Fatalf("unauthenticated devices = %d %s", code, body)
	}

	code, body = serveFrom(router, http.MethodGet, "/api/tailnets", "100.64.0.5:9", nil)
	if code != http.StatusOK || !bytes.Contains(body, []byte(`"id":"alpha"`)) || !bytes.Contains(body, []byte(`"id":"beta"`)) {
		t.Fatalf("tailnets = %d %s", code, body)
	}
	alphaAt := bytes.Index(body, []byte(`"id":"alpha"`))
	betaAt := bytes.Index(body, []byte(`"id":"beta"`))
	if alphaAt < 0 || betaAt < 0 || alphaAt > betaAt {
		t.Fatalf("tailnet order = %s", body)
	}

	code, body = serveFrom(router, http.MethodGet, "/api/devices?tailnet=alpha", "100.64.0.5:9", nil)
	if code != http.StatusOK || !bytes.Contains(body, []byte("alpha-device")) || bytes.Contains(body, []byte("beta-device")) {
		t.Fatalf("alpha devices = %d %s", code, body)
	}
	code, body = serveFrom(router, http.MethodGet, "/api/devices?tailnet=beta", "100.64.0.5:9", nil)
	if code != http.StatusOK || !bytes.Contains(body, []byte("beta-device")) || bytes.Contains(body, []byte("alpha-device")) {
		t.Fatalf("beta devices = %d %s", code, body)
	}
	code, body = serveFrom(router, http.MethodGet, "/api/devices?tailnet=secret", "100.64.0.5:9", nil)
	if code != http.StatusForbidden || !bytes.Contains(body, []byte(`tailnet \"secret\" is not permitted`)) {
		t.Fatalf("secret tailnet = %d %s", code, body)
	}

	code, body = serveFrom(router, http.MethodGet, "/api/whoami", "100.64.0.5:9", nil)
	if code != http.StatusOK {
		t.Fatalf("whoami = %d %s", code, body)
	}
	var who struct {
		Login       string   `json:"login"`
		Autoscope   string   `json:"autoscope"`
		Tailnets    []string `json:"tailnets"`
		DeviceScope struct {
			Tags []string `json:"tags"`
		} `json:"deviceScope"`
	}
	if err := json.Unmarshal(body, &who); err != nil {
		t.Fatal(err)
	}
	if who.Login != "ada@example.com" || who.Autoscope != "groups" || len(who.DeviceScope.Tags) != 1 || who.DeviceScope.Tags[0] != "tag:ops" {
		t.Fatalf("whoami payload = %+v", who)
	}
	if len(who.Tailnets) != 2 || who.Tailnets[0] != "alpha" || who.Tailnets[1] != "beta" {
		t.Fatalf("whoami tailnets = %v", who.Tailnets)
	}

	onlyAlpha := cfg
	onlyAlpha.GroupGrants = nil
	onlyAlpha.Autoscope = config.AccessAutoscopeOff
	limited := gin.New()
	api = limited.Group("/api")
	api.Use(access.Middleware(onlyAlpha, whois))
	api.GET("/tailnets", h.ListTailnets)
	api.GET("/devices", h.GetDevices)
	code, body = serveFrom(limited, http.MethodGet, "/api/tailnets", "100.64.0.5:9", nil)
	if code != http.StatusOK || !bytes.Contains(body, []byte(`"id":"alpha"`)) || bytes.Contains(body, []byte(`"id":"beta"`)) {
		t.Fatalf("filtered tailnets = %d %s", code, body)
	}
	code, body = serveFrom(limited, http.MethodGet, "/api/devices?tailnet=beta", "100.64.0.5:9", nil)
	if code != http.StatusForbidden || !bytes.Contains(body, []byte("not permitted")) {
		t.Fatalf("beta forbidden = %d %s", code, body)
	}
	code, _ = serveFrom(limited, http.MethodGet, "/api/devices?tailnet="+database.DefaultTailnetID, "100.64.0.5:9", nil)
	if code != http.StatusForbidden {
		t.Fatalf("default status=%d", code)
	}
}

func TestIdentityOnlyHeaderAllowsLoginAndRejectsSpoofedHeaders(t *testing.T) {
	server := httptest.NewServer(tailnetHTTP(t, map[string]string{
		"lab.example": "lab-device",
	}))
	defer server.Close()
	h := registryHandlers(t, newAPIStore(t), []config.TailnetSpec{{
		ID: "lab", Name: "lab.example", APIURL: server.URL, APIKey: "lab-secret",
	}})
	cfg := config.Access{
		Enabled:         true,
		Mode:            config.AccessModeHeader,
		Grants:          config.AccessGrantsIdentity,
		TrustedPrefixes: []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")},
		Autoscope:       config.AccessAutoscopeUser,
	}
	router := gin.New()
	router.GET("/health", h.HealthCheck)
	api := router.Group("/api")
	api.GET("/health", h.HealthCheck)
	api.Use(access.Middleware(cfg, nil))
	api.GET("/whoami", h.WhoAmI)
	api.GET("/tailnets", h.ListTailnets)
	api.GET("/devices", h.GetDevices)

	spoof := map[string]string{
		"Tailscale-User-Login": "ada@example.com",
		"Tailscale-User-Name":  "Ada",
	}
	code, body := serveFrom(router, http.MethodGet, "/api/devices", "203.0.113.10:9", spoof)
	if code != http.StatusForbidden || bytes.Contains(body, []byte("lab-device")) || bytes.Contains(body, []byte("ada@example.com")) {
		t.Fatalf("spoofed login = %d %s", code, body)
	}
	code, body = serveFrom(router, http.MethodGet, "/api/whoami", "203.0.113.10:9", spoof)
	if code != http.StatusForbidden {
		t.Fatalf("spoofed whoami = %d %s", code, body)
	}

	code, body = serveFrom(router, http.MethodGet, "/api/whoami", "10.1.2.3:9", map[string]string{
		"Tailscale-User-Name": "Ada",
	})
	if code != http.StatusForbidden || !bytes.Contains(body, []byte("missing tailscale identity")) {
		t.Fatalf("name without login = %d %s", code, body)
	}
	code, body = serveFrom(router, http.MethodGet, "/health", "203.0.113.10:9", spoof)
	if code != http.StatusOK {
		t.Fatalf("health = %d %s", code, body)
	}

	login := map[string]string{
		"Tailscale-User-Login": "ada@example.com",
		"Tailscale-User-Name":  "Ada Lovelace",
	}
	code, body = serveFrom(router, http.MethodGet, "/api/devices", "10.1.2.3:9", login)
	if code != http.StatusOK || !bytes.Contains(body, []byte("lab-device")) {
		t.Fatalf("allowed devices = %d %s", code, body)
	}
	code, body = serveFrom(router, http.MethodGet, "/api/tailnets", "10.1.2.3:9", login)
	if code != http.StatusOK || !bytes.Contains(body, []byte(`"id":"lab"`)) {
		t.Fatalf("tailnets = %d %s", code, body)
	}
	code, body = serveFrom(router, http.MethodGet, "/api/whoami", "10.1.2.3:9", login)
	if code != http.StatusOK {
		t.Fatalf("whoami = %d %s", code, body)
	}
	var who struct {
		Login       string   `json:"login"`
		Name        string   `json:"name"`
		Autoscope   string   `json:"autoscope"`
		Tailnets    []string `json:"tailnets"`
		DeviceScope struct {
			Owners []string `json:"owners"`
		} `json:"deviceScope"`
	}
	if err := json.Unmarshal(body, &who); err != nil {
		t.Fatal(err)
	}
	if who.Login != "ada@example.com" || who.Name != "Ada Lovelace" || who.Autoscope != "user" || len(who.Tailnets) != 1 || who.Tailnets[0] != "*" || len(who.DeviceScope.Owners) != 1 || who.DeviceScope.Owners[0] != "ada@example.com" {
		t.Fatalf("whoami payload = %+v", who)
	}
}

func TestHeaderGroupGrantIgnoresUntrustedSource(t *testing.T) {
	server := httptest.NewServer(tailnetHTTP(t, map[string]string{
		"lab.example": "lab-device",
	}))
	defer server.Close()
	h := registryHandlers(t, newAPIStore(t), []config.TailnetSpec{{
		ID: "lab", Name: "lab.example", APIURL: server.URL, APIKey: "lab-secret",
	}})
	cfg := config.Access{
		Enabled:          true,
		Mode:             config.AccessModeHeader,
		CapabilityHeader: config.DefaultCapabilityHeader,
		TrustedPrefixes:  []netip.Prefix{netip.MustParsePrefix("127.0.0.1/32")},
		GroupsHeader:     "X-Tsflow-Groups",
		GroupGrants: map[string]config.Grant{
			"group:eng": {All: true},
		},
		Autoscope: config.AccessAutoscopeOff,
	}
	router := gin.New()
	api := router.Group("/api")
	api.Use(access.Middleware(cfg, nil))
	api.GET("/devices", h.GetDevices)
	api.GET("/whoami", h.WhoAmI)

	headers := map[string]string{
		"Tailscale-User-Login": "ada@example.com",
		"X-Tsflow-Groups":      "group:eng",
	}
	code, body := serveFrom(router, http.MethodGet, "/api/devices", "203.0.113.10:9", headers)
	if code != http.StatusForbidden || !bytes.Contains(body, []byte("missing access grant")) {
		t.Fatalf("spoofed = %d %s", code, body)
	}
	code, body = serveFrom(router, http.MethodGet, "/api/devices", "127.0.0.1:9", headers)
	if code != http.StatusOK || !bytes.Contains(body, []byte("lab-device")) {
		t.Fatalf("trusted group = %d %s", code, body)
	}
	code, body = serveFrom(router, http.MethodGet, "/api/whoami", "127.0.0.1:9", headers)
	if code != http.StatusOK || !bytes.Contains(body, []byte(`"login":"ada@example.com"`)) || bytes.Contains(body, []byte("deviceScope")) {
		t.Fatalf("whoami = %d %s", code, body)
	}
}

type accessStub struct {
	fn func(addr string) (*apitype.WhoIsResponse, error)
}

func (s *accessStub) WhoIs(_ context.Context, addr string) (*apitype.WhoIsResponse, error) {
	return s.fn(addr)
}

func serveFrom(handler http.Handler, method, target, remote string, headers map[string]string) (int, []byte) {
	req := httptest.NewRequest(method, target, nil)
	req.RemoteAddr = remote
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec.Code, rec.Body.Bytes()
}
