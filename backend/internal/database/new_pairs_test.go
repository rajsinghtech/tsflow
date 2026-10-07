package database

import (
	"context"
	"testing"
	"time"
)

func TestNewPairsLookbackAndRollup(t *testing.T) {
	store := setupTestDB(t)
	ctx := context.Background()
	const base int64 = 1_699_999_200 // aligned to an hour
	if err := store.UpsertNodeMetadata(ctx, DefaultTailnetID, []NodeMetadata{
		{NodeID: "a", Hostname: "alpha"},
		{NodeID: "b", Hostname: "bravo"},
		{NodeID: "c", Hostname: "charlie"},
	}); err != nil {
		t.Fatal(err)
	}
	// Seen three days before the window: inside a 7d lookback, outside 24h.
	insertRankPair(t, store, DefaultTailnetID, base-3*24*3600, "a", "b", "virtual", 10, 1, 1)
	// Seen eight days before: outside the default lookback. The window row is new.
	insertRankPair(t, store, DefaultTailnetID, base-8*24*3600, "a", "c", "virtual", 4, 1, 1)
	insertRankPair(t, store, DefaultTailnetID, base+30, "a", "c", "virtual", 6, 1, 1)
	// New in the window. A physical sighting in the lookback does not count.
	insertRankPair(t, store, DefaultTailnetID, base+60, "b", "c", "virtual", 30, 5, 2)
	insertRankPair(t, store, DefaultTailnetID, base+120, "b", "c", "subnet", 8, 1, 1)
	// Repeated from the 3d lookback.
	insertRankPair(t, store, DefaultTailnetID, base+180, "a", "b", "virtual", 100, 1, 3)
	insertRankPair(t, store, DefaultTailnetID, base-24*3600, "b", "c", "physical", 9, 0, 1)
	insertRankPair(t, store, DefaultTailnetID, base+240, "a", "127.3.3.40", "physical", 5000, 0, 4)
	insertRankPair(t, store, "beta", base+60, "z", "y", "virtual", 900, 0, 1)

	start := time.Unix(base, 0).UTC()
	end := time.Unix(base+3600, 0).UTC()
	pairs, more, err := store.ListNewPairs(ctx, DefaultTailnetID, start, end, NewPairQuery{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if more || len(pairs) != 2 {
		t.Fatalf("default new pairs = %#v more=%v", pairs, more)
	}
	// b->c is new. a->c was last seen 8d ago, outside the 7d lookback. a->b was seen 3d ago.
	if pairs[0].SrcNodeID != "b" || pairs[0].DstNodeID != "c" || pairs[0].SrcHostname != "bravo" ||
		pairs[0].TotalBytes != 44 || pairs[0].FlowCount != 3 || !pairs[0].FirstSeen.Equal(time.Unix(base+60, 0).UTC()) {
		t.Fatalf("newest pair = %#v", pairs[0])
	}
	if pairs[1].SrcNodeID != "a" || pairs[1].DstNodeID != "c" || pairs[1].TotalBytes != 7 || pairs[1].DstHostname != "charlie" {
		t.Fatalf("older new pair = %#v", pairs[1])
	}
	for _, pair := range pairs {
		if pair.DstNodeID == "127.3.3.40" || pair.TotalBytes == 5000 || pair.SrcNodeID == "z" {
			t.Fatalf("default list included physical or another tailnet: %#v", pairs)
		}
	}

	day, _, err := store.ListNewPairs(ctx, DefaultTailnetID, start, end, NewPairQuery{Limit: 10, Lookback: 24 * time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	// a->b's previous sighting is outside 24h, so the window row is new too.
	if len(day) != 3 || day[0].SrcNodeID != "a" || day[0].DstNodeID != "b" || day[0].TotalBytes != 101 {
		t.Fatalf("24h lookback = %#v", day)
	}

	physical, _, err := store.ListNewPairs(ctx, DefaultTailnetID, start, end, NewPairQuery{Limit: 10, TrafficTypes: []string{"physical"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(physical) != 1 || physical[0].DstNodeID != "127.3.3.40" || physical[0].TotalBytes != 5000 {
		t.Fatalf("physical new pairs = %#v", physical)
	}

	if err := store.backfillHourRollups(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.ExecContext(ctx, `DELETE FROM node_pairs WHERE tailnet_id = ? AND bucket >= ? AND bucket < ?`, DefaultTailnetID, base-3*24*3600, base-3*24*3600+3600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.ExecContext(ctx, `DELETE FROM node_pairs WHERE tailnet_id = ? AND bucket >= ? AND bucket < ?`, DefaultTailnetID, base, base+3600); err != nil {
		t.Fatal(err)
	}
	rolled, _, err := store.ListNewPairs(ctx, DefaultTailnetID, start, end, NewPairQuery{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(rolled) != 2 || rolled[0].SrcNodeID != "b" || rolled[0].TotalBytes != 44 || rolled[1].SrcNodeID != "a" || rolled[1].DstNodeID != "c" {
		t.Fatalf("rollup new pairs = %#v", rolled)
	}

	page, more, err := store.ListNewPairs(ctx, DefaultTailnetID, start, end, NewPairQuery{Limit: 1, Offset: 1})
	if err != nil {
		t.Fatal(err)
	}
	if more || len(page) != 1 || page[0].SrcNodeID != "a" || page[0].DstNodeID != "c" {
		t.Fatalf("page = %#v more=%v", page, more)
	}
	if _, _, err := store.ListNewPairs(ctx, DefaultTailnetID, start, end, NewPairQuery{Lookback: time.Minute}); err == nil {
		t.Fatal("short lookback was accepted")
	}
	none, more, err := store.ListNewPairs(ctx, DefaultTailnetID, time.Unix(base+10*24*3600, 0), time.Unix(base+10*24*3600+60, 0), NewPairQuery{Limit: 5})
	if err != nil {
		t.Fatal(err)
	}
	if len(none) != 0 || more {
		t.Fatalf("empty window = %#v more=%v", none, more)
	}
}

func TestNewPairsLeaveOutSelfPairs(t *testing.T) {
	store := setupTestDB(t)
	ctx := context.Background()
	const base int64 = 1_699_999_200
	insertRankPair(t, store, DefaultTailnetID, base+60, "a", "a", "virtual", 500, 0, 1)
	insertRankPair(t, store, DefaultTailnetID, base+120, "a", "b", "virtual", 10, 0, 1)
	insertRankPair(t, store, DefaultTailnetID, base+180, "b", "b", "subnet", 0, 0, 1)

	pairs, more, err := store.ListNewPairs(ctx, DefaultTailnetID, time.Unix(base, 0).UTC(), time.Unix(base+3600, 0).UTC(), NewPairQuery{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if more || len(pairs) != 1 || pairs[0].SrcNodeID != "a" || pairs[0].DstNodeID != "b" {
		t.Fatalf("new pairs = %#v more=%v, want only a->b", pairs, more)
	}
}
