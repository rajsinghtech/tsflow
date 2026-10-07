package mcpserver

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/rajsinghtech/tsflow/backend/internal/handlers"
	"github.com/rajsinghtech/tsflow/backend/internal/middleware"
)

// /mcp runs the same store queries as /api, so it must not be an unlimited
// side door around the /api rate limit.
func TestMCPRateLimited(t *testing.T) {
	gin.SetMode(gin.TestMode)
	limit := middleware.DefaultRateLimitConfig()
	limit.RequestsPerMinute = 2
	router := gin.New()
	Mount(router, Options{
		Enabled:   true,
		Handlers:  handlers.NewHandlers(nil, nil, nil, "test"),
		RateLimit: middleware.RateLimitMiddleware(limit),
	})
	server := httptest.NewServer(router)
	defer server.Close()

	statuses := make([]int, 0, 3)
	for range 3 {
		resp, err := server.Client().Post(server.URL+"/mcp", "application/json", strings.NewReader(`{}`))
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		statuses = append(statuses, resp.StatusCode)
	}
	if statuses[0] == http.StatusTooManyRequests || statuses[1] == http.StatusTooManyRequests {
		t.Fatalf("statuses = %v, first two requests should pass the limiter", statuses)
	}
	if statuses[2] != http.StatusTooManyRequests {
		t.Fatalf("statuses = %v, want the third request rate limited", statuses)
	}
}
