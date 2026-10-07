package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
)

func corsRouter(environment string, origins []string) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(cors.New(CORSConfig(environment, origins)))
	router.GET("/api/policy", func(c *gin.Context) { c.String(http.StatusOK, "secret") })
	return router
}

func corsGet(router http.Handler, origin string) (int, string) {
	req := httptest.NewRequest(http.MethodGet, "http://tsflow.example.ts.net/api/policy", nil)
	req.Header.Set("Origin", origin)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec.Code, rec.Header().Get("Access-Control-Allow-Origin")
}

func TestCORSDevelopmentDoesNotReflectForeignOrigins(t *testing.T) {
	router := corsRouter("development", nil)
	for _, origin := range []string{"https://evil.example", "http://localhost.evil.example", "http://127.0.0.1.evil.example", "null"} {
		if _, allowed := corsGet(router, origin); allowed != "" {
			t.Errorf("development allowed origin %q (Access-Control-Allow-Origin %q)", origin, allowed)
		}
	}
	for _, origin := range []string{"http://localhost:3000", "http://127.0.0.1:5173", "http://[::1]:3000", "http://app.localhost:3000"} {
		if code, allowed := corsGet(router, origin); code != http.StatusOK || allowed != origin {
			t.Errorf("development origin %q = %d %q, want allowed", origin, code, allowed)
		}
	}
}

func TestCORSProductionAndExplicitOrigins(t *testing.T) {
	if _, allowed := corsGet(corsRouter("production", nil), "http://localhost:3000"); allowed != "" {
		t.Fatalf("production allowed a cross-origin request: %q", allowed)
	}
	router := corsRouter("production", []string{"https://dash.example.com"})
	if _, allowed := corsGet(router, "https://dash.example.com"); allowed != "https://dash.example.com" {
		t.Fatalf("explicit origin not allowed: %q", allowed)
	}
	if _, allowed := corsGet(router, "https://evil.example"); allowed != "" {
		t.Fatalf("explicit list allowed another origin: %q", allowed)
	}
	if _, allowed := corsGet(corsRouter("development", []string{"https://dash.example.com"}), "http://localhost:3000"); allowed != "" {
		t.Fatalf("explicit list should replace the development loopback default: %q", allowed)
	}
}
