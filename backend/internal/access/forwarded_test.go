package access

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"tailscale.com/client/tailscale/apitype"
)

// whoIsByPeer identifies 100.64.0.8 as the victim and every other address
// as the attacker, so a test can tell which X-Forwarded-For entry was used.
func whoIsByPeer(t *testing.T) *stubWhoIs {
	t.Helper()
	const capName = "example.com/cap/tsflow"
	return &stubWhoIs{fn: func(addr string) (*apitype.WhoIsResponse, error) {
		if addr == "100.64.0.8" {
			return who("victim@example.com", "Victim", "victim.example.ts.net.", nil, capMap(capName, `{"tailnets":["alpha"]}`)), nil
		}
		return who("attacker@example.com", "Attacker", "attacker.example.ts.net.", nil, nil), nil
	}}
}

func forwardedRouter(t *testing.T, whois *stubWhoIs) http.Handler {
	t.Helper()
	cfg := headerConfig()
	cfg.Enabled = true
	cfg.GroupsHeader = ""
	router := gin.New()
	router.Use(Middleware(cfg, whois))
	router.GET("/data", func(c *gin.Context) {
		ident, _ := FromGin(c)
		c.String(http.StatusOK, ident.Login)
	})
	return router
}

func TestForwardedPeerUsesLastHeaderLine(t *testing.T) {
	// Some proxies append their own X-Forwarded-For line instead of joining
	// it onto the client's. The client's line comes first and must not be
	// read as the peer.
	whois := whoIsByPeer(t)
	req := httptest.NewRequest(http.MethodGet, "/data", nil)
	req.RemoteAddr = "10.0.0.2:4000"
	req.Header.Add("X-Forwarded-For", "100.64.0.8")
	req.Header.Add("X-Forwarded-For", "100.64.0.66")
	rec := httptest.NewRecorder()
	forwardedRouter(t, whois).ServeHTTP(rec, req)
	if len(whois.calls) != 1 || whois.calls[0] != "100.64.0.66" {
		t.Fatalf("WhoIs calls = %v, want only the proxy-appended peer 100.64.0.66", whois.calls)
	}
	if rec.Code == http.StatusOK && rec.Body.String() == "victim@example.com" {
		t.Fatalf("client-supplied X-Forwarded-For line impersonated the victim")
	}
}

func TestForwardedPeerFailsClosedOnUnparsableNearestEntry(t *testing.T) {
	// The nearest proxy's entry is the only one tsflow can trust. When it is
	// not an address (for example "unknown" or a bracketed IPv6 address
	// without a port), skipping it would trust a client-supplied entry.
	for _, xff := range []string{
		"100.64.0.8, unknown",
		"100.64.0.8, [fd7a:115c:a1e0::66]x",
		"100.64.0.8, ",
	} {
		t.Run(xff, func(t *testing.T) {
			whois := whoIsByPeer(t)
			code, body := do(forwardedRouter(t, whois), "10.0.0.2:4000", "/data", map[string]string{"X-Forwarded-For": xff})
			if code == http.StatusOK || len(whois.calls) != 0 {
				t.Fatalf("X-Forwarded-For %q = %d %s, WhoIs calls %v; want 403 without WhoIs", xff, code, body, whois.calls)
			}
		})
	}
}

func TestForwardedPeerAcceptsCommonForms(t *testing.T) {
	for xff, want := range map[string]string{
		"100.64.0.8":                     "100.64.0.8",
		"203.0.113.9, 100.64.0.8":        "100.64.0.8",
		"203.0.113.9,100.64.0.8:41641":   "100.64.0.8",
		"[fd7a:115c:a1e0::8]:41641":      "fd7a:115c:a1e0::8",
		"[fd7a:115c:a1e0::8]":            "fd7a:115c:a1e0::8",
		"203.0.113.9, fd7a:115c:a1e0::8": "fd7a:115c:a1e0::8",
	} {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("X-Forwarded-For", xff)
		got, err := forwardedPeer(req)
		if err != nil || got != want {
			t.Errorf("forwardedPeer(%q) = %q, %v; want %q", xff, got, err, want)
		}
	}
}
