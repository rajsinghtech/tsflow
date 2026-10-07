package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func clientIPRouter(t *testing.T, trusted []string) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	router := gin.New()
	if err := ConfigureClientIP(router, trusted); err != nil {
		t.Fatalf("ConfigureClientIP: %v", err)
	}
	router.Use(RateLimitMiddleware(RateLimitConfig{RequestsPerMinute: 2, CleanupInterval: time.Hour, StaleAfter: time.Hour}))
	router.GET("/ip", func(c *gin.Context) { c.String(http.StatusOK, c.ClientIP()) })
	return router
}

func getIP(router http.Handler, remote, forwarded string) (int, string) {
	req := httptest.NewRequest(http.MethodGet, "/ip", nil)
	req.RemoteAddr = remote
	if forwarded != "" {
		req.Header.Set("X-Forwarded-For", forwarded)
	}
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	return w.Code, w.Body.String()
}

// Any client can send X-Forwarded-For. Unless the peer is a configured proxy,
// the header must not pick the rate-limit bucket or the logged client IP.
func TestClientIPIgnoresForwardedForWithoutTrustedProxies(t *testing.T) {
	router := clientIPRouter(t, nil)
	for i, spoof := range []string{"198.51.100.1", "198.51.100.2"} {
		code, ip := getIP(router, "192.0.2.7:4000", spoof)
		if code != http.StatusOK || ip != "192.0.2.7" {
			t.Fatalf("request %d: code=%d ip=%q, want the peer address", i, code, ip)
		}
	}
	if code, _ := getIP(router, "192.0.2.7:4000", "198.51.100.3"); code != http.StatusTooManyRequests {
		t.Fatalf("third request with a fresh X-Forwarded-For got %d, want 429", code)
	}
}

func TestClientIPUsesForwardedForFromTrustedProxy(t *testing.T) {
	router := clientIPRouter(t, []string{"10.0.0.0/8"})
	for _, client := range []string{"198.51.100.1", "198.51.100.2", "198.51.100.3"} {
		code, ip := getIP(router, "10.1.2.3:4000", "203.0.113.9, "+client)
		if code != http.StatusOK || ip != client {
			t.Fatalf("code=%d ip=%q, want %s", code, ip, client)
		}
	}
	// A client outside the proxy range still cannot pick its own address.
	if _, ip := getIP(router, "192.0.2.7:4000", "198.51.100.9"); ip != "192.0.2.7" {
		t.Fatalf("untrusted peer ip=%q", ip)
	}
}
