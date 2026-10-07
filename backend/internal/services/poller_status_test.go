package services

import (
	"context"
	"testing"
	"time"
)

func TestPollerStatusClearsLastErrorAfterRecovery(t *testing.T) {
	base := time.Date(2026, 5, 8, 13, 0, 0, 0, time.UTC)
	key := "network/2026/05/08/2026-05-08-13-10-00.ndjson"
	source, server := newTestObjectStore(t, []testObject{{key: key, body: []byte("{malformed\n")}}, 10)
	poller, _ := newObjectStoreTestPoller(t, source, 10)
	poller.config.InitialBackfill = time.Since(base)
	ctx := context.Background()

	poller.pollAndRecord(ctx, "Poll")
	stats := poller.Stats()
	if stats["lastError"] == nil || stats["pollErrors"] != int64(1) {
		t.Fatalf("after a failed poll stats = %v, want lastError and pollErrors=1", stats)
	}
	server.Close()

	fixed, fixedServer := newTestObjectStore(t, []testObject{testFlowObjectAt(t, key, "node-a", base.Add(10*time.Minute), 7, false)}, 10)
	defer fixedServer.Close()
	source.blobs = fixed.blobs
	poller.pollAndRecord(ctx, "Poll")
	stats = poller.Stats()
	if _, stale := stats["lastError"]; stale {
		t.Fatalf("lastError still reported after a successful poll: %v", stats["lastError"])
	}
	if stats["pollErrors"] != int64(1) {
		t.Fatalf("pollErrors = %v, want the history kept at 1", stats["pollErrors"])
	}
}

func TestPollerStatusCountsObjectsIngestedBeforeAFailure(t *testing.T) {
	base := time.Date(2026, 5, 8, 13, 0, 0, 0, time.UTC)
	source, server := newTestObjectStore(t, []testObject{
		testFlowObjectAt(t, "network/2026/05/08/2026-05-08-13-05-00.ndjson", "node-a", base.Add(5*time.Minute), 7, false),
		{key: "network/2026/05/08/2026-05-08-13-10-00.ndjson", body: []byte("{malformed\n")},
	}, 10)
	defer server.Close()
	poller, _ := newObjectStoreTestPoller(t, source, 10)
	if err := poller.pollObjectStore(context.Background(), base, base.Add(time.Hour)); err == nil {
		t.Fatal("expected the malformed object to fail the poll")
	}
	stats := poller.Stats()
	if stats["totalPolled"] != int64(1) || stats["lastPollCount"] != 1 {
		t.Fatalf("stats = %v, want the one ingested flow counted", stats)
	}
	if last, _ := stats["lastPollTime"].(time.Time); last.IsZero() {
		t.Fatal("lastPollTime not set although an object was ingested")
	}
}
