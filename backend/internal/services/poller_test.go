package services

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/rajsinghtech/tsflow/backend/internal/config"
	"github.com/rajsinghtech/tsflow/backend/internal/database"
)

func TestPollerStopCancelsInitialDeviceRefresh(t *testing.T) {
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
	poller := NewPoller(service, nil, DefaultPollerConfig())
	startedPoller := make(chan error, 1)
	go func() { startedPoller <- poller.Start(context.Background()) }()

	select {
	case <-started:
	case <-time.After(time.Second):
		poller.Stop()
		t.Fatal("initial device refresh did not reach test server")
	}

	stopped := make(chan struct{})
	go func() {
		poller.Stop()
		close(stopped)
	}()

	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("poller Stop did not cancel the in-flight refresh")
	}

	select {
	case err := <-startedPoller:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("poller Start did not return after cancellation")
	}
}

func TestPollerStartDoesNotWaitForDeviceRefresh(t *testing.T) {
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
	poller := NewPoller(service, nil, DefaultPollerConfig())

	startReturned := make(chan error, 1)
	go func() { startReturned <- poller.Start(context.Background()) }()
	select {
	case err := <-startReturned:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(250 * time.Millisecond):
		poller.Stop()
		t.Fatal("poller Start waited for the device refresh")
	}

	select {
	case <-started:
	case <-time.After(time.Second):
		poller.Stop()
		t.Fatal("background device refresh did not start")
	}
	poller.Stop()
}

func TestPollerS3OnlySkipsUnauthenticatedDeviceRefresh(t *testing.T) {
	requests := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests <- struct{}{}
		http.Error(w, "unexpected API request", http.StatusUnauthorized)
	}))
	defer server.Close()

	service := NewTailscaleService(&config.Config{
		TailscaleAPIURL:  server.URL,
		TailscaleTailnet: "example.com",
	})
	if service.HasCredentials() {
		t.Fatal("service without API credentials reported usable credentials")
	}

	poller := NewPoller(service, nil, PollerConfig{
		PollInterval:       time.Hour,
		InitialBackfill:    time.Hour,
		Retention:          0,
		CleanupInterval:    time.Hour,
		DeviceCacheRefresh: time.Hour,
		FlowBackend:        "s3",
	})
	if err := poller.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	poller.Stop()

	select {
	case <-requests:
		t.Fatal("S3-only poller attempted an unauthenticated device refresh")
	default:
	}
}

func TestPollerS3OnlyStartIngestsCompressedObjectAndStops(t *testing.T) {
	apiRequests := make(chan struct{}, 1)
	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		apiRequests <- struct{}{}
		http.Error(w, "unexpected API request", http.StatusUnauthorized)
	}))
	defer apiServer.Close()

	loggedAt := time.Now().UTC().Add(-time.Minute)
	key := "network/" + loggedAt.Format("2006/01/02/2006-01-02-15-04-05") + ".ndjson.gz"
	source, objectServer := newTestObjectStore(t, []testObject{
		compressedTestFlowObject(t, key, "node-s3", loggedAt, 37, ".gz"),
	}, 10)
	defer objectServer.Close()

	service := NewTailscaleService(&config.Config{
		TailscaleAPIURL:  apiServer.URL,
		TailscaleTailnet: "example.com",
	})
	if service.HasCredentials() {
		t.Fatal("test Tailscale service unexpectedly has credentials")
	}
	poller, db := newObjectStoreTestPollerWithService(t, source, 10, service)
	poller.config.InitialBackfill = time.Hour
	poller.config.PollInterval = time.Hour
	poller.config.CleanupInterval = time.Hour
	poller.config.Retention = 0

	if err := poller.Start(context.Background()); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	deadline := time.NewTimer(2 * time.Second)
	ticker := time.NewTicker(5 * time.Millisecond)
	defer deadline.Stop()
	defer ticker.Stop()
	for {
		seen, err := db.store.IsObjectIngested(ctx, key)
		if err != nil {
			t.Fatal(err)
		}
		if seen {
			break
		}
		select {
		case <-ticker.C:
		case <-deadline.C:
			t.Fatal("S3-only poller did not ingest the compressed object")
		}
	}

	pairs, err := db.store.GetNodePairAggregates(ctx, loggedAt.Add(-time.Minute), loggedAt.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if len(pairs) != 1 || pairs[0].TxBytes != 37 || pairs[0].FlowCount != 1 {
		t.Fatalf("S3-only aggregate = %+v, want one flow with 37 transmitted bytes", pairs)
	}
	select {
	case <-apiRequests:
		t.Fatal("S3-only poller attempted an unauthenticated Tailscale API request")
	default:
	}

	stopped := make(chan struct{})
	go func() {
		poller.Stop()
		close(stopped)
	}()
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("S3-only poller Stop did not complete")
	}
}

func TestPollerStartWithCanceledContextStopsCleanly(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	poller := NewPoller(nil, nil, DefaultPollerConfig())

	if err := poller.Start(ctx); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if running, ok := poller.Stats()["running"].(bool); ok && !running {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("poller remained running after startup context cancellation")
}

func TestPollerStopIsSafeWhenCalledConcurrently(t *testing.T) {
	poller := NewPoller(nil, nil, DefaultPollerConfig())
	if err := poller.Start(context.Background()); err != nil {
		t.Fatal(err)
	}

	stopped := make(chan struct{}, 2)
	for i := 0; i < 2; i++ {
		go func() {
			poller.Stop()
			stopped <- struct{}{}
		}()
	}
	for i := 0; i < 2; i++ {
		select {
		case <-stopped:
		case <-time.After(time.Second):
			t.Fatal("concurrent poller Stop did not return")
		}
	}
}

func TestPollerStartRejectsUnsafeIntervals(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*PollerConfig)
	}{
		{name: "zero poll interval", edit: func(c *PollerConfig) { c.PollInterval = 0 }},
		{name: "negative cleanup interval", edit: func(c *PollerConfig) { c.CleanupInterval = -time.Second }},
		{name: "negative backfill", edit: func(c *PollerConfig) { c.InitialBackfill = -time.Second }},
		{name: "zero backfill", edit: func(c *PollerConfig) { c.InitialBackfill = 0 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := DefaultPollerConfig()
			tc.edit(&cfg)
			poller := NewPoller(nil, nil, cfg)
			if err := poller.Start(context.Background()); err == nil {
				t.Fatal("Start() unexpectedly accepted unsafe configuration")
			}
		})
	}
}

func TestPollChunkedPropagatesFailureAfterCommittedProgress(t *testing.T) {
	start := time.Date(2026, 5, 8, 12, 0, 0, 0, time.UTC)
	failingChunkStart := start.Add(maxPollChunk)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("start") == failingChunkStart.Format(time.RFC3339) {
			http.Error(w, "upstream failure", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"logs":[]}`))
	}))
	defer server.Close()

	store, err := database.NewSQLiteStore(t.TempDir() + "/poller.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.Init(context.Background()); err != nil {
		t.Fatal(err)
	}

	service := NewTailscaleService(&config.Config{
		TailscaleAPIURL:  server.URL,
		TailscaleTailnet: "example.com",
	})
	poller := NewPoller(service, store, DefaultPollerConfig())
	end := start.Add(2 * maxPollChunk)

	err = poller.pollChunked(context.Background(), start, end)
	if err == nil || !strings.Contains(err.Error(), "poll chunk 2/2 failed") {
		t.Fatalf("pollChunked error = %v, want second chunk failure", err)
	}

	state, err := store.GetPollState(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !state.LastPollEnd.Equal(failingChunkStart) {
		t.Fatalf("poll cursor = %v, want committed first-chunk end %v", state.LastPollEnd, failingChunkStart)
	}
}
