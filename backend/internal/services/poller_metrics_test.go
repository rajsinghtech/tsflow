package services

import (
	"context"
	"testing"
	"time"
)

func TestPollerObserverAndMetricsSnapshot(t *testing.T) {
	base := time.Date(2026, 5, 8, 13, 0, 0, 0, time.UTC)
	key := "network/2026/05/08/2026-05-08-13-10-00.ndjson"
	source, server := newTestObjectStore(t, []testObject{{key: key, body: []byte("{malformed\n")}}, 10)
	poller, _ := newObjectStoreTestPoller(t, source, 10)
	poller.config.InitialBackfill = time.Since(base)

	var errs, oks int
	poller.SetPollObserver(func(d time.Duration, err error) {
		if d < 0 {
			t.Errorf("negative duration %v", d)
		}
		if err != nil {
			errs++
		} else {
			oks++
		}
	})
	ctx := context.Background()
	poller.pollAndRecord(ctx, "Poll")
	snap := poller.MetricsSnapshot()
	if errs != 1 || oks != 0 || snap.PollErrors != 1 || !snap.LastSuccessTime.IsZero() {
		t.Fatalf("after a failed poll errs=%d oks=%d snapshot=%+v", errs, oks, snap)
	}
	server.Close()

	fixed, fixedServer := newTestObjectStore(t, []testObject{testFlowObjectAt(t, key, "node-a", base.Add(10*time.Minute), 7, false)}, 10)
	defer fixedServer.Close()
	source.blobs = fixed.blobs
	before := time.Now()
	poller.pollAndRecord(ctx, "Poll")
	snap = poller.MetricsSnapshot()
	if errs != 1 || oks != 1 {
		t.Fatalf("observer errs=%d oks=%d, want 1 and 1", errs, oks)
	}
	if snap.PollErrors != 1 || snap.LastSuccessTime.Before(before) || snap.FlowBackend != "s3" {
		t.Fatalf("snapshot after recovery = %+v", snap)
	}
	if poller.TailnetID() == "" {
		t.Fatal("empty tailnet id")
	}
}
