package middleware

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func warnRouter(trusted []string, logs *[]string) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(warnUntrustedForwarded(trusted, func(f string, a ...any) {
		*logs = append(*logs, fmt.Sprintf(f, a...))
	}))
	router.GET("/ip", func(c *gin.Context) { c.Status(http.StatusOK) })
	return router
}

func hit(router http.Handler, remote, header, value string) {
	req := httptest.NewRequest(http.MethodGet, "/ip", nil)
	req.RemoteAddr = remote
	if header != "" {
		req.Header.Set(header, value)
	}
	router.ServeHTTP(httptest.NewRecorder(), req)
}

func TestWarnUntrustedForwardedLogsOnce(t *testing.T) {
	var logs []string
	router := warnRouter(nil, &logs)
	hit(router, "192.0.2.7:4000", "", "")
	if len(logs) != 0 {
		t.Fatalf("warned without a forwarded header: %q", logs)
	}
	hit(router, "192.0.2.7:4000", "X-Forwarded-For", "198.51.100.1")
	hit(router, "192.0.2.8:4000", "X-Real-IP", "198.51.100.2")
	if len(logs) != 1 {
		t.Fatalf("logs = %q, want one warning", logs)
	}
	if !strings.Contains(logs[0], "192.0.2.7") || !strings.Contains(logs[0], "TSFLOW_TRUSTED_PROXIES") {
		t.Fatalf("warning = %q, want the peer and the setting", logs[0])
	}
}

func TestWarnUntrustedForwardedQuietForTrustedProxy(t *testing.T) {
	var logs []string
	router := warnRouter([]string{"10.0.0.0/8", "192.0.2.9"}, &logs)
	hit(router, "10.1.2.3:4000", "X-Forwarded-For", "198.51.100.1")
	hit(router, "192.0.2.9:4000", "X-Forwarded-For", "198.51.100.1")
	hit(router, "[::ffff:10.1.2.3]:4000", "X-Forwarded-For", "198.51.100.1")
	if len(logs) != 0 {
		t.Fatalf("warned for a trusted proxy: %q", logs)
	}
	hit(router, "192.0.2.7:4000", "X-Forwarded-For", "198.51.100.1")
	if len(logs) != 1 {
		t.Fatalf("logs = %q, want a warning for the untrusted peer", logs)
	}
}
