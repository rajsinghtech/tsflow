package access

import (
	"bytes"
	"log"
	"net/http"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/rajsinghtech/tsflow/backend/internal/config"
)

func captureLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	original := log.Default().Writer()
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(original) })
	return &buf
}

func TestDeniedRequestLogsPeerAndForwardedFor(t *testing.T) {
	buf := captureLog(t)
	cfg := config.Access{
		Enabled:         true,
		Mode:            config.AccessModeHeader,
		Grants:          config.AccessGrantsIdentity,
		UserHeader:      config.DefaultUserHeader,
		TrustedPrefixes: []netip.Prefix{netip.MustParsePrefix("192.0.2.10/32")},
	}
	router := gin.New()
	router.Use(Middleware(cfg, nil))
	router.GET("/api/whoami", func(c *gin.Context) { c.Status(http.StatusNoContent) })

	headers := map[string]string{
		"X-Forwarded-For":        "100.64.0.7",
		config.DefaultUserHeader: "spoof@example.com",
	}
	code, _ := do(router, "198.51.100.4:4242", "/api/whoami?token=secret", headers)
	if code != http.StatusForbidden {
		t.Fatalf("status = %d", code)
	}
	out := buf.String()
	for _, want := range []string{
		`access denied: reason="missing access grant"`,
		"mode=header",
		`path="/api/whoami"`,
		"peer=198.51.100.4 ",
		`x_forwarded_for="100.64.0.7"`,
		"trusted_proxy=false",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("log missing %q: %s", want, out)
		}
	}
	for _, leak := range []string{"spoof@example.com", "token=secret"} {
		if strings.Contains(out, leak) {
			t.Fatalf("log leaked %q: %s", leak, out)
		}
	}

	// The same peer and reason is not logged again inside the window.
	buf.Reset()
	do(router, "198.51.100.4:4243", "/api/whoami", headers)
	if buf.Len() != 0 {
		t.Fatalf("repeat denial logged: %s", buf.String())
	}

	// A trusted proxy without identity is a different reason and is logged.
	do(router, "192.0.2.10:80", "/api/whoami", map[string]string{"X-Forwarded-For": "100.64.0.7"})
	if out := buf.String(); !strings.Contains(out, `reason="missing tailscale identity"`) || !strings.Contains(out, "trusted_proxy=true") {
		t.Fatalf("trusted proxy denial = %s", out)
	}
}

func TestDeniedRequestDebugLogsEveryDenial(t *testing.T) {
	buf := captureLog(t)
	cfg := config.Access{
		Enabled: true,
		Mode:    config.AccessModeTsnet,
		Grants:  config.AccessGrantsIdentity,
		Debug:   true,
	}
	router := gin.New()
	router.Use(Middleware(cfg, &stubWhoIs{}))
	router.GET("/", func(c *gin.Context) { c.Status(http.StatusNoContent) })
	do(router, "100.64.0.9:1", "/", nil)
	do(router, "100.64.0.9:1", "/", nil)
	if got := strings.Count(buf.String(), "access denied:"); got != 2 {
		t.Fatalf("debug denials logged = %d: %s", got, buf.String())
	}
	if strings.Contains(buf.String(), "trusted_proxy=") {
		t.Fatalf("tsnet mode logged a proxy check: %s", buf.String())
	}
}

func TestDenyLimiterWindowAndBound(t *testing.T) {
	now := time.Unix(0, 0)
	l := newDenyLimiter(time.Minute)
	l.now = func() time.Time { return now }
	if !l.allow("a") || l.allow("a") {
		t.Fatal("first allow then suppress")
	}
	now = now.Add(time.Minute)
	if !l.allow("a") {
		t.Fatal("allow again after the window")
	}
	for i := 0; i < denyLogMaxKeys*2; i++ {
		l.allow(string(rune('A' + i)))
	}
	if len(l.last) > denyLogMaxKeys {
		t.Fatalf("limiter grew to %d", len(l.last))
	}
	now = now.Add(time.Minute)
	if !l.allow("fresh") {
		t.Fatal("expired keys are pruned when full")
	}
}
