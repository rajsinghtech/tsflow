package services

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rajsinghtech/tsflow/backend/internal/config"
	"github.com/rajsinghtech/tsflow/backend/internal/database"
)

// An API poll that returns no logs still succeeded, so the status must say
// when it ran and that it found nothing, like an object-store poll that finds
// no objects.
func TestAPIPollWithNoLogsUpdatesStatus(t *testing.T) {
	var body atomic.Value
	body.Store(`{"logs":[]}`)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body.Load().(string)))
	}))
	defer server.Close()
	store, err := database.NewSQLiteStore(t.TempDir() + "/poller.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := store.Init(ctx); err != nil {
		t.Fatal(err)
	}
	service := NewTailscaleService(&config.Config{TailscaleAPIURL: server.URL, TailscaleTailnet: "example.com"})
	poller := NewPoller(service, store, DefaultPollerConfig())

	base := time.Date(2026, 5, 8, 13, 0, 0, 0, time.UTC)
	before := time.Now()
	if err := poller.pollRange(ctx, base, base.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	stats := poller.Stats()
	if last, _ := stats["lastPollTime"].(time.Time); last.Before(before) {
		t.Fatalf("lastPollTime = %v after an empty poll, want the poll time", stats["lastPollTime"])
	}
	if stats["lastPollCount"] != 0 || stats["totalPolled"] != int64(0) {
		t.Fatalf("stats = %v, want no flows counted", stats)
	}

	body.Store(flowLogBody(base.Add(90*time.Second), "node-a", 7))
	if err := poller.pollRange(ctx, base.Add(time.Minute), base.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if stats = poller.Stats(); stats["lastPollCount"] != 1 || stats["totalPolled"] != int64(1) {
		t.Fatalf("stats = %v, want the one flow counted", stats)
	}
	afterData, _ := stats["lastPollTime"].(time.Time)

	body.Store(`{"logs":[]}`)
	if err := poller.pollRange(ctx, base.Add(2*time.Minute), base.Add(3*time.Minute)); err != nil {
		t.Fatal(err)
	}
	stats = poller.Stats()
	if stats["lastPollCount"] != 0 {
		t.Fatalf("lastPollCount = %v after an empty poll, want 0", stats["lastPollCount"])
	}
	if stats["totalPolled"] != int64(1) {
		t.Fatalf("totalPolled = %v, want the earlier flow kept", stats["totalPolled"])
	}
	if last, _ := stats["lastPollTime"].(time.Time); last.Before(afterData) {
		t.Fatalf("lastPollTime went back from %v to %v", afterData, last)
	}
}
