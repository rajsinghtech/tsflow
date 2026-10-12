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

func TestDeviceTimelineLeavesTheDeviceOutOfItsPeers(t *testing.T) {
	store := setupTestDB(t)
	ctx := context.Background()
	const base int64 = 1_699_999_200
	// A device can report flows to its own address. That row is not a peer.
	insertRankPair(t, store, DefaultTailnetID, base+60, "bravo", "bravo", "virtual", 0, 0, 7)
	insertRankPair(t, store, DefaultTailnetID, base+120, "bravo", "alpha", "virtual", 30, 3, 1)
	start := time.Unix(base, 0).UTC()
	timeline, err := store.GetDeviceTimeline(ctx, DefaultTailnetID, "bravo", start, start.Add(time.Hour), TimelineQuery{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(timeline.Peers) != 1 || timeline.Peers[0].PeerID != "alpha" || timeline.HasMore {
		t.Fatalf("peers = %#v", timeline.Peers)
	}
	virtual, _, _, _ := sumTimeline(timeline.Buckets)
	if virtual != 33 {
		t.Fatalf("virtual bytes = %d", virtual)
	}
}

func TestDeviceTimelineCountsEveryIDOfTheDevice(t *testing.T) {
	store := setupTestDB(t)
	ctx := context.Background()
	base := time.Now().UTC().Truncate(time.Minute).Add(-10 * time.Minute).Unix()
	const (
		stable = "nBuild001CNTRL"
		legacy = "424242"
		peer   = "nPeer0001CNTRL"
		other  = "nOther01CNTRL"
	)
	if err := store.UpsertNodeMetadata(ctx, DefaultTailnetID, []NodeMetadata{
		{NodeID: stable, Hostname: "build", IPs: []string{"100.64.0.21"}},
		{NodeID: legacy, Hostname: "", Owner: "ada@example.com", IPs: []string{"100.64.0.21"}},
		{NodeID: peer, Hostname: "peer", IPs: []string{"100.64.0.30"}},
		{NodeID: other, Hostname: "other", IPs: []string{"100.64.0.40"}},
	}); err != nil {
		t.Fatal(err)
	}
	insertRankPair(t, store, DefaultTailnetID, base, stable, peer, "virtual", 100, 10, 1)
	insertRankPair(t, store, DefaultTailnetID, base+60, legacy, peer, "virtual", 40, 5, 2)
	insertRankPair(t, store, DefaultTailnetID, base+120, other, legacy, "virtual", 7, 3, 1)
	// Traffic between two ids of the same device is not a peer.
	insertRankPair(t, store, DefaultTailnetID, base+180, legacy, stable, "virtual", 1, 1, 1)
	insertRankPair(t, store, DefaultTailnetID, base+240, other, peer, "virtual", 999, 0, 1)

	start := time.Unix(base, 0).UTC()
	end := time.Unix(base+600, 0).UTC()
	for _, id := range []string{stable, legacy} {
		timeline, err := store.GetDeviceTimeline(ctx, DefaultTailnetID, id, start, end, TimelineQuery{Limit: 10})
		if err != nil {
			t.Fatal(err)
		}
		if timeline.NodeID != id || timeline.Hostname != "build" {
			t.Fatalf("%s: meta = %#v", id, timeline)
		}
		var tx, rx int64
		for _, bucket := range timeline.Buckets {
			tx += bucket.Virtual.TxBytes
			rx += bucket.Virtual.RxBytes
		}
		// 100+40 sent to peer, 3 sent to other, 1 to itself; 10+5 from peer, 7 from other, 1 from itself.
		if tx != 144 || rx != 23 {
			t.Fatalf("%s: tx=%d rx=%d buckets=%#v", id, tx, rx, timeline.Buckets)
		}
		if len(timeline.Peers) != 2 || timeline.Peers[0].PeerID != peer || timeline.Peers[0].TotalBytes != 155 ||
			timeline.Peers[0].FlowCount != 3 || timeline.Peers[1].PeerID != other || timeline.Peers[1].TotalBytes != 10 {
			t.Fatalf("%s: peers = %#v", id, timeline.Peers)
		}
	}

	// A device with one id reads exactly as before.
	single, err := store.GetDeviceTimeline(ctx, DefaultTailnetID, other, start, end, TimelineQuery{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if single.Hostname != "other" || len(single.Peers) != 2 || single.Peers[0].PeerID != peer || single.Peers[0].TotalBytes != 999 ||
		single.Peers[1].PeerID != legacy || single.Peers[1].TotalBytes != 10 {
		t.Fatalf("single-id peers = %#v", single.Peers)
	}
}
