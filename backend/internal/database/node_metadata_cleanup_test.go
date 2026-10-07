package database

import (
	"context"
	"sort"
	"testing"
	"time"
)

func TestCleanupPrunesStaleUnreferencedNodeMetadata(t *testing.T) {
	store := setupTestDB(t)
	ctx := context.Background()
	const other = "other.example.com"

	for _, tn := range []string{DefaultTailnetID, other} {
		if err := store.UpsertNodeMetadata(ctx, tn, []NodeMetadata{
			{NodeID: "gone", Name: "gone.example.ts.net"},
			{NodeID: "still-in-data", Name: "busy.example.ts.net"},
			{NodeID: "fresh", Name: "fresh.example.ts.net"},
		}); err != nil {
			t.Fatal(err)
		}
	}
	// Age everything but "fresh" past retention.
	if _, err := store.db.ExecContext(ctx,
		`UPDATE node_metadata SET updated_at = datetime('now', '-30 days') WHERE node_id != 'fresh'`); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Minute).Unix()
	if err := store.UpsertNodePairAggregates(ctx, DefaultTailnetID, []NodePairAggregate{{
		Bucket: now, SrcNodeID: "peer", DstNodeID: "still-in-data", TrafficType: "virtual",
		TxBytes: 10, TxPkts: 1, FlowCount: 1, Protocols: "[6]", ProtocolBytes: `{"6":10}`, Ports: "[]",
	}}); err != nil {
		t.Fatal(err)
	}

	if _, err := store.Cleanup(ctx, DefaultTailnetID, 48*time.Hour); err != nil {
		t.Fatal(err)
	}

	ids := func(tn string) []string {
		nodes, err := store.GetNodeMetadata(ctx, tn)
		if err != nil {
			t.Fatal(err)
		}
		out := make([]string, 0, len(nodes))
		for _, n := range nodes {
			out = append(out, n.NodeID)
		}
		sort.Strings(out)
		return out
	}
	if got := ids(DefaultTailnetID); len(got) != 2 || got[0] != "fresh" || got[1] != "still-in-data" {
		t.Fatalf("default tailnet metadata = %v, want [fresh still-in-data]", got)
	}
	// Cleanup is per tailnet.
	if got := ids(other); len(got) != 3 {
		t.Fatalf("other tailnet metadata = %v, want all three kept", got)
	}

	// A non-positive retention keeps everything.
	if _, err := store.Cleanup(ctx, other, 0); err != nil {
		t.Fatal(err)
	}
	if got := ids(other); len(got) != 3 {
		t.Fatalf("retention 0 pruned metadata: %v", got)
	}
}
