package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func init() {
	gin.SetMode(gin.TestMode)
}

func TestHealthCheck(t *testing.T) {
	startTime := time.Now().Add(-10 * time.Second)
	h := &Handlers{
		startTime: startTime,
		version:   "test-version",
	}
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/health", nil)

	h.HealthCheck(c)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}

	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp["status"] != "healthy" {
		t.Errorf("expected status=healthy, got %v", resp["status"])
	}
	if resp["service"] != "tsflow-backend" {
		t.Errorf("expected service=tsflow-backend, got %v", resp["service"])
	}
	if resp["version"] != "test-version" {
		t.Errorf("expected version=test-version, got %v", resp["version"])
	}
	if uptime, ok := resp["uptime"].(float64); !ok || uptime < 10 {
		t.Errorf("expected uptime >= 10, got %v", resp["uptime"])
	}
}

func TestParseTimeRange_Defaults(t *testing.T) {
	h := &Handlers{}
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/test", nil)

	start, end, err := h.parseTimeRange(c)
	if err != nil {
		t.Fatal(err)
	}

	// Default start should be ~1 hour ago
	if time.Since(start) < 55*time.Minute || time.Since(start) > 65*time.Minute {
		t.Errorf("default start should be ~1h ago, got %v ago", time.Since(start))
	}
	// Default end should be ~now
	if time.Since(end) > 5*time.Second {
		t.Errorf("default end should be ~now, got %v ago", time.Since(end))
	}
}

func TestParseTimeRange_CustomTimes(t *testing.T) {
	h := &Handlers{}
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/test?start=2025-01-01T00:00:00Z&end=2025-01-01T01:00:00Z", nil)

	start, end, err := h.parseTimeRange(c)
	if err != nil {
		t.Fatal(err)
	}
	if start.Format(time.RFC3339) != "2025-01-01T00:00:00Z" {
		t.Errorf("unexpected start: %v", start)
	}
	if end.Format(time.RFC3339) != "2025-01-01T01:00:00Z" {
		t.Errorf("unexpected end: %v", end)
	}
}

func TestParseTimeRange_EndBeforeStart(t *testing.T) {
	h := &Handlers{}
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/test?start=2025-01-02T00:00:00Z&end=2025-01-01T00:00:00Z", nil)

	_, _, err := h.parseTimeRange(c)
	if err == nil {
		t.Error("expected error for end before start")
	}
}

func TestParseTimeRange_TooLargeRange(t *testing.T) {
	h := &Handlers{}
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/test?start=2024-01-01T00:00:00Z&end=2025-01-01T00:00:00Z", nil)

	_, _, err := h.parseTimeRange(c)
	if err == nil {
		t.Error("expected error for range > 90 days")
	}
}

func TestParseTimeRange_InvalidFormat(t *testing.T) {
	h := &Handlers{}
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/test?start=not-a-date", nil)

	_, _, err := h.parseTimeRange(c)
	if err == nil {
		t.Error("expected error for invalid start time")
	}
}

func TestResolveNodeOwner_NilPoller(t *testing.T) {
	h := &Handlers{}
	if owner := h.resolveNodeOwner("device1"); owner != "" {
		t.Errorf("expected empty owner with nil poller, got %q", owner)
	}
}
