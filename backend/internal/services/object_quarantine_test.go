package services

import (
	"context"
	"testing"
	"time"

	"github.com/rajsinghtech/tsflow/backend/internal/database"
)

// A corrupt or vanished object never becomes readable. Retrying it forever
// pinned the poll cursor at that object, so every later poll re-listed and
// re-checked a window that kept growing, and the poller reported an error
// indefinitely.
func TestObjectStoreGivesUpOnPermanentlyUnreadableObjects(t *testing.T) {
	base := time.Date(2026, 5, 8, 13, 0, 0, 0, time.UTC)
	malformedKey := "network/2026/05/08/2026-05-08-13-10-00.ndjson"
	missingKey := "network/2026/05/08/2026-05-08-13-11-00.ndjson"
	truncatedKey := "network/2026/05/08/2026-05-08-13-12-00.ndjson.gz"
	goodKey := "network/2026/05/08/2026-05-08-13-40-00.ndjson"
	good := testFlowObjectAt(t, goodKey, "node-b", base.Add(40*time.Minute), 20, false)
	gz := compressedTestFlowObject(t, truncatedKey, "node-c", base.Add(12*time.Minute), 5, ".gz")
	gz.body = gz.body[:len(gz.body)/2]
	source, server := newTestObjectStore(t, []testObject{
		{key: malformedKey, body: []byte("{\"nodeId\": \"trunc\n")},
		{key: missingKey, body: nil},
		gz,
		good,
	}, 10)
	defer server.Close()
	poller, db := newObjectStoreTestPoller(t, source, 10)
	ctx := context.Background()
	now := base.Add(time.Hour)

	var lastErr error
	for attempt := 1; attempt <= maxObjectReadAttempts+1; attempt++ {
		state, err := db.store.GetPollState(ctx, database.DefaultTailnetID)
		if err != nil {
			t.Fatal(err)
		}
		start := state.LastPollEnd
		if start.IsZero() {
			start = base
		}
		lastErr = poller.pollObjectStore(ctx, start, now)
		if attempt < maxObjectReadAttempts && lastErr == nil {
			t.Fatalf("poll %d succeeded before the retry budget was spent", attempt)
		}
	}
	if lastErr != nil {
		t.Fatalf("poll after %d attempts still failing: %v", maxObjectReadAttempts, lastErr)
	}
	state, err := db.store.GetPollState(ctx, database.DefaultTailnetID)
	if err != nil {
		t.Fatal(err)
	}
	if state.LastPollEnd.Before(base.Add(40 * time.Minute)) {
		t.Fatalf("poll cursor = %v, want it past the unreadable objects", state.LastPollEnd)
	}
	for _, key := range []string{malformedKey, missingKey, truncatedKey, goodKey} {
		seen, err := db.store.IsObjectIngested(ctx, database.DefaultTailnetID, key)
		if err != nil {
			t.Fatal(err)
		}
		if !seen {
			t.Fatalf("object %s was not recorded, so it would be retried forever", key)
		}
	}
	pairs, err := db.store.GetNodePairAggregates(ctx, database.DefaultTailnetID, base, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(pairs) != 1 || pairs[0].TxBytes != 20 {
		t.Fatalf("pairs = %+v, want only the readable object's traffic", pairs)
	}
}

// A transient failure must still be retried: an object that becomes
// readable before the budget is spent is ingested with its traffic.
func TestObjectStoreRetriesTransientlyUnreadableObject(t *testing.T) {
	base := time.Date(2026, 5, 8, 13, 0, 0, 0, time.UTC)
	key := "network/2026/05/08/2026-05-08-13-10-00.ndjson"
	objects := []testObject{{key: key, body: nil}}
	source, server := newTestObjectStore(t, objects, 10)
	poller, db := newObjectStoreTestPoller(t, source, 10)
	ctx := context.Background()
	if err := poller.pollObjectStore(ctx, base, base.Add(time.Hour)); err == nil {
		t.Fatal("missing object should fail the first poll")
	}
	server.Close()

	source2, server2 := newTestObjectStore(t, []testObject{testFlowObjectAt(t, key, "node-a", base.Add(10*time.Minute), 7, false)}, 10)
	defer server2.Close()
	source.blobs = source2.blobs
	if err := poller.pollObjectStore(ctx, base, base.Add(time.Hour)); err != nil {
		t.Fatalf("object readable again: %v", err)
	}
	pairs, err := db.store.GetNodePairAggregates(ctx, database.DefaultTailnetID, base, base.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(pairs) != 1 || pairs[0].TxBytes != 7 {
		t.Fatalf("pairs = %+v, want the retried object's traffic", pairs)
	}
}

// Access errors are not permanent. Giving up on them would record every
// object as empty during an IAM or credential outage and lose the data.
func TestObjectStoreNeverGivesUpOnAccessDenied(t *testing.T) {
	base := time.Date(2026, 5, 8, 13, 0, 0, 0, time.UTC)
	key := "network/2026/05/08/2026-05-08-13-10-00.ndjson"
	source, server := newTestObjectStore(t, []testObject{{key: key, body: forbiddenTestBody}}, 10)
	defer server.Close()
	poller, db := newObjectStoreTestPoller(t, source, 10)
	ctx := context.Background()
	for attempt := 0; attempt < 2*maxObjectReadAttempts; attempt++ {
		if err := poller.pollObjectStore(ctx, base, base.Add(time.Hour)); err == nil {
			t.Fatalf("poll %d: access denied object reported success", attempt)
		}
	}
	seen, err := db.store.IsObjectIngested(ctx, database.DefaultTailnetID, key)
	if err != nil {
		t.Fatal(err)
	}
	if seen {
		t.Fatal("access-denied object was recorded as ingested and will never be retried")
	}
	state, err := db.store.GetPollState(ctx, database.DefaultTailnetID)
	if err != nil {
		t.Fatal(err)
	}
	if state.LastPollEnd.After(base.Add(10 * time.Minute)) {
		t.Fatalf("poll cursor = %v moved past the denied object", state.LastPollEnd)
	}
}
