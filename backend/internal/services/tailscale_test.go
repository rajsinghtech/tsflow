package services

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/rajsinghtech/tsflow/backend/internal/config"
)

func TestChunkedNetworkLogValidation(t *testing.T) {
	service := NewTailscaleService(&config.Config{TailscaleAPIURL: "https://api.tailscale.com"})
	start := "2026-01-01T00:00:00Z"
	end := "2026-01-01T01:00:00Z"
	if _, err := service.GetNetworkLogsChunked(start, start, time.Hour); err == nil {
		t.Fatal("expected equal range to fail")
	}
	if _, err := service.GetNetworkLogsChunked(start, end, 0); err == nil {
		t.Fatal("expected zero chunk size to fail")
	}
	if _, err := service.GetNetworkLogsChunkedParallelWithContext(context.Background(), start, end, time.Hour, 0); err == nil {
		t.Fatal("expected zero concurrency to fail")
	}
	if _, err := service.GetNetworkLogsWithContext(context.Background(), end, start); err == nil {
		t.Fatal("expected reversed range to fail")
	}
}

func TestTailscaleServiceUsesConfiguredAPIURL(t *testing.T) {
	var requestPath, authorization string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestPath = r.URL.Path
		authorization = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"users":[]}`))
	}))
	defer server.Close()

	service := NewTailscaleService(&config.Config{
		TailscaleAPIURL:  server.URL,
		TailscaleAPIKey:  "test-key",
		TailscaleTailnet: "example.com",
	})
	if _, err := service.GetUsers(); err != nil {
		t.Fatal(err)
	}
	if requestPath != "/api/v2/tailnet/example.com/users" {
		t.Fatalf("request path=%q", requestPath)
	}
	if authorization != "Bearer test-key" {
		t.Fatalf("authorization=%q", authorization)
	}
	if !strings.HasPrefix(service.baseURL, server.URL) {
		t.Fatalf("base URL=%q", service.baseURL)
	}
}

func TestTailscaleServiceRequestsHonorCallerContext(t *testing.T) {
	started := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-r.Context().Done()
	}))
	defer server.Close()

	service := NewTailscaleService(&config.Config{
		TailscaleAPIURL:  server.URL,
		TailscaleAPIKey:  "test-key",
		TailscaleTailnet: "example.com",
	})
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		_, err := service.GetUsersWithContext(ctx)
		result <- err
	}()

	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("request did not reach test server")
	}
	cancel()

	select {
	case err := <-result:
		if err == nil || !strings.Contains(err.Error(), "context canceled") {
			t.Fatalf("request error = %v, want context cancellation", err)
		}
	case <-time.After(time.Second):
		t.Fatal("request did not stop after context cancellation")
	}
}

func TestTailscaleServiceNilChunkContextIsSafe(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"logs":[]}`))
	}))
	defer server.Close()

	service := NewTailscaleService(&config.Config{
		TailscaleAPIURL:  server.URL,
		TailscaleAPIKey:  "test-key",
		TailscaleTailnet: "example.com",
	})
	start := "2026-01-01T00:00:00Z"
	end := "2026-01-01T00:30:00Z"
	if _, err := service.GetNetworkLogsChunkedParallelWithContext(nil, start, end, time.Hour, 1); err != nil {
		t.Fatalf("nil context request failed: %v", err)
	}
}
