package database

import (
	"context"
	"testing"
	"time"
)

func TestViewerDevicesUseExactCreatorLogin(t *testing.T) {
	store := setupTestDB(t)
	ctx := context.Background()
	base := time.Now().UTC().Truncate(time.Minute).Add(-time.Minute).Unix()
	if err := store.UpsertNodeMetadata(ctx, DefaultTailnetID, []NodeMetadata{
		{NodeID: "nBuild001CNTRL", Hostname: "build", Tags: []string{"tag:prod"}, IPs: []string{"100.64.0.21"}},
		{NodeID: "424242", Owner: "ada@example.com", IPs: []string{"100.64.0.21"}},
		{NodeID: "nBob00001CNTRL", Hostname: "laptop", Owner: "bob@example.com"},
		{NodeID: "nExtra001CNTRL", Hostname: "extra", Owner: "ada@example.com.extra"},
	}); err != nil {
		t.Fatal(err)
	}
	insertRankPair(t, store, DefaultTailnetID, base, "nBuild001CNTRL", "peer", "virtual", 20, 2, 1)
	insertRankPair(t, store, DefaultTailnetID, base, "424242", "peer", "virtual", 5, 1, 1)
	insertRankPair(t, store, DefaultTailnetID, base, "nBob00001CNTRL", "peer", "virtual", 90, 1, 1)

	start := time.Unix(base, 0).UTC()
	end := time.Unix(base+60, 0).UTC()
	devices, err := store.ListViewerDevices(ctx, DefaultTailnetID, "Ada@Example.com", start, end, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(devices) != 1 || devices[0].NodeID != "nBuild001CNTRL" || devices[0].Owner != "ada@example.com" || devices[0].TotalBytes != 28 {
		t.Fatalf("viewer devices = %#v", devices)
	}
	owns, err := store.ViewerOwns(ctx, DefaultTailnetID, "ada@example.com", "424242")
	if err != nil || !owns {
		t.Fatalf("numeric id owned=%v err=%v", owns, err)
	}
	owns, err = store.ViewerOwns(ctx, DefaultTailnetID, "ada@example.com", "nBob00001CNTRL")
	if err != nil || owns {
		t.Fatalf("bob owned=%v err=%v", owns, err)
	}
}
