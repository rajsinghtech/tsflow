package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func TestRateLimitConfigZeroValuesDoNotPanic(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	// Only the rate set: the cleanup interval and stale age are zero. This
	// used to panic in time.NewTicker on the cleanup goroutine.
	r.Use(RateLimitMiddleware(RateLimitConfig{RequestsPerMinute: 2}))
	r.GET("/", func(c *gin.Context) { c.Status(http.StatusNoContent) })

	codes := make([]int, 0, 3)
	for range 3 {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
		codes = append(codes, rec.Code)
	}
	if codes[0] != http.StatusNoContent || codes[1] != http.StatusNoContent || codes[2] != http.StatusTooManyRequests {
		t.Fatalf("codes = %v, want two allowed then 429", codes)
	}
	// Give the cleanup goroutine a moment; a panic there kills the test binary.
	time.Sleep(20 * time.Millisecond)
}

func TestRateLimitConfigNormalize(t *testing.T) {
	def := DefaultRateLimitConfig()
	got := RateLimitConfig{}.normalize()
	if got != def {
		t.Fatalf("zero config = %+v, want defaults %+v", got, def)
	}
	got = RateLimitConfig{RequestsPerMinute: -5, CleanupInterval: -time.Second, StaleAfter: time.Second}.normalize()
	if got.RequestsPerMinute != def.RequestsPerMinute || got.CleanupInterval != def.CleanupInterval || got.StaleAfter != time.Minute {
		t.Fatalf("invalid config = %+v", got)
	}
	custom := RateLimitConfig{RequestsPerMinute: 7, CleanupInterval: time.Second, StaleAfter: 2 * time.Minute}
	if got := custom.normalize(); got != custom {
		t.Fatalf("valid config changed: %+v", got)
	}
}
