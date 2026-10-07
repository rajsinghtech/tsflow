package database

import (
	"context"
	"testing"
	"time"
)

func TestDeviceTimelineSplitsTypesAndPagesPeers(t *testing.T) {
	store := setupTestDB(t)
	ctx := context.Background()
	const base int64 = 1_699_999_200
	if err := store.UpsertNodeMetadata(ctx, DefaultTailnetID, []NodeMetadata{
		{NodeID: "bravo", Hostname: "bravo"},
		{NodeID: "alpha", Hostname: "alpha"},
		{NodeID: "charlie", Hostname: "charlie"},
	}); err != nil {
		t.Fatal(err)
	}
	// Three hours so the chart bucket is an hour and complete hours use the rollup.
	insertRankPair(t, store, DefaultTailnetID, base+60, "bravo", "alpha", "virtual", 100, 10, 2)
	insertRankPair(t, store, DefaultTailnetID, base+120, "alpha", "bravo", "subnet", 40, 5, 1)
	insertRankPair(t, store, DefaultTailnetID, base+3600+60, "bravo", "charlie", "exit", 7, 1, 1)
	insertRankPair(t, store, DefaultTailnetID, base+3600+120, "bravo", "127.3.3.40", "physical", 5000, 0, 4)
	insertRankPair(t, store, "beta", base+60, "bravo", "alpha", "virtual", 900, 0, 1)

	start := time.Unix(base, 0).UTC()
	end := time.Unix(base+3*3600, 0).UTC()
	timeline, err := store.GetDeviceTimeline(ctx, DefaultTailnetID, "bravo", start, end, TimelineQuery{Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if timeline.Hostname != "bravo" || timeline.BucketSeconds != 3600 || timeline.HasMore != true || len(timeline.Peers) != 1 {
		t.Fatalf("timeline meta = %#v peers %#v", timeline, timeline.Peers)
	}
	if timeline.Peers[0].PeerID != "alpha" || timeline.Peers[0].Hostname != "alpha" || timeline.Peers[0].TotalBytes != 155 {
		t.Fatalf("first peer = %#v", timeline.Peers[0])
	}
	virtual, subnet, exit, physical := sumTimeline(timeline.Buckets)
	if virtual != 110 || subnet != 45 || exit != 8 || physical != 0 {
		t.Fatalf("bytes virtual=%d subnet=%d exit=%d physical=%d buckets=%#v", virtual, subnet, exit, physical, timeline.Buckets)
	}

	withPhysical, err := store.GetDeviceTimeline(ctx, DefaultTailnetID, "bravo", start, end, TimelineQuery{Limit: 10, TrafficTypes: []string{"physical"}})
	if err != nil {
		t.Fatal(err)
	}
	_, _, _, physical = sumTimeline(withPhysical.Buckets)
	if physical != 5000 || len(withPhysical.Peers) != 1 || withPhysical.Peers[0].PeerID != "127.3.3.40" {
		t.Fatalf("physical timeline peers=%#v bytes=%d", withPhysical.Peers, physical)
	}

	if err := store.backfillHourRollups(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.ExecContext(ctx, `DELETE FROM node_pairs WHERE tailnet_id = ? AND bucket >= ? AND bucket < ?`, DefaultTailnetID, base, base+2*3600); err != nil {
		t.Fatal(err)
	}
	rolled, err := store.GetDeviceTimeline(ctx, DefaultTailnetID, "bravo", start, end, TimelineQuery{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	virtual, subnet, exit, physical = sumTimeline(rolled.Buckets)
	if virtual != 110 || subnet != 45 || exit != 8 || physical != 0 || len(rolled.Peers) != 2 {
		t.Fatalf("rolled timeline bytes %d %d %d %d peers %#v", virtual, subnet, exit, physical, rolled.Peers)
	}

	if _, err := store.GetDeviceTimeline(ctx, DefaultTailnetID, "  ", start, end, TimelineQuery{}); err == nil {
		t.Fatal("blank node was accepted")
	}
}

func sumTimeline(buckets []TimelineBucket) (virtual, subnet, exit, physical int64) {
	for _, bucket := range buckets {
		virtual += bucket.Virtual.TxBytes + bucket.Virtual.RxBytes
		subnet += bucket.Subnet.TxBytes + bucket.Subnet.RxBytes
		exit += bucket.Exit.TxBytes + bucket.Exit.RxBytes
		if bucket.Physical != nil {
			physical += bucket.Physical.TxBytes + bucket.Physical.RxBytes
		}
	}
	return virtual, subnet, exit, physical
}
