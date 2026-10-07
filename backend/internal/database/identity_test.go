package database

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func TestMergeMetadataCopiesCreatorLogin(t *testing.T) {
	devices := mergeMetadata([]NodeMetadata{
		{NodeID: "nBuild001CNTRL", Hostname: "build", Tags: []string{"tag:prod"}, IPs: []string{"100.64.0.21"}},
		{NodeID: "424242", Owner: "ada@example.com", Tags: []string{"tag:prod"}, IPs: []string{"100.64.0.21", "10.1.0.4"}},
		{NodeID: "nOther01CNTRL", Owner: "bob@example.com", Hostname: "laptop", Tags: []string{"tag:ops"}, IPs: []string{"100.64.0.8"}},
		{NodeID: "nLan0001CNTRL", Hostname: "left", IPs: []string{"10.1.1.5"}},
		{NodeID: "nLan0002CNTRL", Hostname: "right", Owner: "cara@example.com", IPs: []string{"10.1.1.5"}},
		{NodeID: "nStable01CNTRL", IPs: []string{"100.64.0.9"}},
		{NodeID: "nStable02CNTRL", Owner: "x@example.com", IPs: []string{"100.64.0.9"}},
		{NodeID: "111", Owner: "one@example.com", IPs: []string{"100.64.0.10"}},
		{NodeID: "222", Owner: "two@example.com", IPs: []string{"100.64.0.10"}},
	})

	build := deviceByID(devices, "nBuild001CNTRL")
	if build == nil || build.canonical != "nBuild001CNTRL" || build.owner != "ada@example.com" || !containsString(build.ids, "424242") {
		t.Fatalf("tagged device = %#v", build)
	}
	if deviceByID(devices, "424242") != build {
		t.Fatal("numeric flow id was not merged onto the tagged device")
	}
	if got := len(matchingDevices(devices, IdentityQuery{User: "ada@example.com"})); got != 1 {
		t.Fatalf("login matches = %d", got)
	}
	if got := len(matchingDevices(devices, IdentityQuery{Tag: "prod"})); got != 1 {
		t.Fatalf("tag matches = %d", got)
	}
	if got := len(matchingDevices(devices, IdentityQuery{Tag: "prod", User: "bob@example.com"})); got != 0 {
		t.Fatalf("tag and other login matched %d devices", got)
	}
	if got := matchingDevices(devices, IdentityQuery{Q: "build"}); len(got) != 1 || got[0].owner != "ada@example.com" {
		t.Fatalf("name query = %#v", got)
	}
	if got := matchingDevices(devices, IdentityQuery{Q: "100.64.0.21"}); len(got) != 1 {
		t.Fatalf("address query = %d", len(got))
	}
	if left, right := deviceByID(devices, "nLan0001CNTRL"), deviceByID(devices, "nLan0002CNTRL"); left == nil || right == nil || left == right {
		t.Fatal("a shared LAN address merged two devices")
	}
	if a, b := deviceByID(devices, "nStable01CNTRL"), deviceByID(devices, "nStable02CNTRL"); a == nil || b == nil || a == b || a.owner == "x@example.com" {
		t.Fatal("two stable ids sharing an address were merged")
	}
	if a, b := deviceByID(devices, "111"), deviceByID(devices, "222"); a == nil || b == nil || a == b {
		t.Fatal("two numeric ids sharing an address were merged")
	}
}

func TestIdentityFilterUsesMergedLoginAndSkipsPhysical(t *testing.T) {
	store := setupTestDB(t)
	ctx := context.Background()
	base := time.Now().UTC().Truncate(time.Minute).Add(-2 * time.Minute).Unix()
	const (
		stable = "nBuild001CNTRL"
		legacy = "424242"
		other  = "nOther01CNTRL"
		peer   = "peer"
	)
	if err := store.UpsertNodeMetadata(ctx, DefaultTailnetID, []NodeMetadata{
		{NodeID: stable, Hostname: "build", Tags: []string{"tag:prod"}, IPs: []string{"100.64.0.21"}},
		{NodeID: legacy, Owner: "ada@example.com", Hostname: "build", Tags: []string{"tag:prod"}, IPs: []string{"100.64.0.21"}},
		{NodeID: other, Hostname: "laptop", Owner: "bob@example.com", Tags: []string{"tag:ops"}, IPs: []string{"100.64.0.8"}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertNodeMetadata(ctx, "beta", []NodeMetadata{
		{NodeID: "nBeta0001CNTRL", Hostname: "beta", Owner: "ada@example.com", Tags: []string{"tag:prod"}, IPs: []string{"100.64.0.21"}},
	}); err != nil {
		t.Fatal(err)
	}
	insertRankPair(t, store, DefaultTailnetID, base, stable, peer, "virtual", 100, 10, 1)
	insertRankPair(t, store, DefaultTailnetID, base, legacy, peer, "virtual", 40, 5, 2)
	insertRankPair(t, store, DefaultTailnetID, base, other, peer, "virtual", 900, 1, 1)
	insertRankPair(t, store, DefaultTailnetID, base, stable, "127.3.3.40", "physical", 5000, 0, 4)
	insertRankPair(t, store, "beta", base, "nBeta0001CNTRL", peer, "virtual", 800, 0, 1)

	start := time.Unix(base, 0).UTC()
	end := time.Unix(base+60, 0).UTC()
	talkers, _, err := store.ListRankedTalkers(ctx, DefaultTailnetID, start, end, RankQuery{Limit: 10, User: "Ada@Example.com"})
	if err != nil {
		t.Fatal(err)
	}
	if len(talkers) != 1 || talkers[0].NodeID != stable || talkers[0].Hostname != "build" || talkers[0].Owner != "ada@example.com" ||
		talkers[0].TxBytes != 140 || talkers[0].RxBytes != 15 || talkers[0].TotalBytes != 155 || talkers[0].FlowCount != 3 {
		t.Fatalf("login talkers = %#v", talkers)
	}
	byTag, _, err := store.ListRankedTalkers(ctx, DefaultTailnetID, start, end, RankQuery{Limit: 10, Tag: "tag:prod"})
	if err != nil {
		t.Fatal(err)
	}
	if len(byTag) != 1 || byTag[0].NodeID != stable || byTag[0].TotalBytes != 155 {
		t.Fatalf("tag talkers = %#v", byTag)
	}
	pairs, _, err := store.ListRankedPairs(ctx, DefaultTailnetID, start, end, RankQuery{Limit: 10, Tag: "prod"})
	if err != nil {
		t.Fatal(err)
	}
	if len(pairs) != 1 || pairs[0].SrcNodeID != stable || pairs[0].DstNodeID != peer || pairs[0].TotalBytes != 155 ||
		pairs[0].FlowCount != 3 || pairs[0].SrcOwner != "ada@example.com" {
		t.Fatalf("tag pairs = %#v", pairs)
	}
	physical, _, err := store.ListRankedPairs(ctx, DefaultTailnetID, start, end, RankQuery{Limit: 10, Tag: "prod", TrafficTypes: []string{"physical"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(physical) != 1 || physical[0].DstNodeID != "127.3.3.40" || physical[0].TotalBytes != 5000 {
		t.Fatalf("physical pairs = %#v", physical)
	}
	none, _, err := store.ListRankedTalkers(ctx, DefaultTailnetID, start, end, RankQuery{Limit: 10, User: "nobody@example.com"})
	if err != nil {
		t.Fatal(err)
	}
	if len(none) != 0 {
		t.Fatalf("missing login = %#v", none)
	}
	if _, _, err := store.ListRankedTalkers(ctx, DefaultTailnetID, start, end, RankQuery{Tag: string(make([]byte, 200))}); err == nil {
		t.Fatal("oversized tag was accepted")
	}
}

func TestIdentityFilterReadsHourRollup(t *testing.T) {
	store := setupTestDB(t)
	ctx := context.Background()
	const base int64 = 1_699_999_200
	if err := store.UpsertNodeMetadata(ctx, DefaultTailnetID, []NodeMetadata{
		{NodeID: "nBuild001CNTRL", Hostname: "build", Tags: []string{"tag:prod"}, IPs: []string{"100.64.0.21"}},
		{NodeID: "424242", Owner: "ada@example.com", IPs: []string{"100.64.0.21"}},
	}); err != nil {
		t.Fatal(err)
	}
	insertRankPair(t, store, DefaultTailnetID, base, "nBuild001CNTRL", "peer", "virtual", 20, 2, 1)
	insertRankPair(t, store, DefaultTailnetID, base+60, "424242", "peer", "subnet", 5, 1, 1)
	insertRankPair(t, store, DefaultTailnetID, base+3600, "nBuild001CNTRL", "peer", "virtual", 7, 1, 1)
	if err := store.backfillHourRollups(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.ExecContext(ctx, `DELETE FROM node_pairs WHERE tailnet_id = ? AND bucket >= ? AND bucket < ?`, DefaultTailnetID, base, base+3600); err != nil {
		t.Fatal(err)
	}
	start := time.Unix(base, 0).UTC()
	end := time.Unix(base+3600+1800, 0).UTC()
	talkers, _, err := store.ListRankedTalkers(ctx, DefaultTailnetID, start, end, RankQuery{Limit: 10, User: "ada@example.com"})
	if err != nil {
		t.Fatal(err)
	}
	if len(talkers) != 1 || talkers[0].NodeID != "nBuild001CNTRL" || talkers[0].TotalBytes != 36 || talkers[0].FlowCount != 3 {
		t.Fatalf("rolled talkers = %#v", talkers)
	}
}

func TestIdentityFilterScales(t *testing.T) {
	store := setupTestDB(t)
	ctx := context.Background()
	const nodes = 20000
	metadata := make([]NodeMetadata, 0, nodes+1)
	for i := 0; i < nodes; i++ {
		metadata = append(metadata, NodeMetadata{
			NodeID:   fmt.Sprintf("n%08dCNTRL", i),
			Hostname: fmt.Sprintf("host-%d", i),
			Owner:    fmt.Sprintf("user%d@example.com", i),
			Tags:     []string{"tag:fleet"},
			IPs:      []string{fmt.Sprintf("100.%d.%d.%d", 64+(i/65536)%64, (i/256)%256, i%256)},
		})
	}
	metadata = append(metadata, NodeMetadata{
		NodeID: "515151", Owner: "ada@example.com", Tags: []string{"tag:prod"}, IPs: []string{"100.64.0.21"},
	})
	metadata[21].Owner = ""
	metadata[21].Tags = []string{"tag:prod"}
	metadata[21].IPs = []string{"100.64.0.21"}
	metadata[21].Hostname = "build"
	if err := store.UpsertNodeMetadata(ctx, DefaultTailnetID, metadata); err != nil {
		t.Fatal(err)
	}
	base := time.Now().UTC().Truncate(time.Minute).Add(-time.Minute).Unix()
	insertRankPair(t, store, DefaultTailnetID, base, metadata[21].NodeID, "peer", "virtual", 10, 1, 1)
	insertRankPair(t, store, DefaultTailnetID, base, "515151", "peer", "virtual", 4, 1, 1)

	start := time.Unix(base, 0).UTC()
	end := time.Unix(base+60, 0).UTC()
	begun := time.Now()
	talkers, _, err := store.ListRankedTalkers(ctx, DefaultTailnetID, start, end, RankQuery{Limit: 5, Tag: "prod"})
	elapsed := time.Since(begun)
	if err != nil {
		t.Fatal(err)
	}
	if len(talkers) != 1 || talkers[0].NodeID != metadata[21].NodeID || talkers[0].Owner != "ada@example.com" || talkers[0].TotalBytes != 16 {
		t.Fatalf("scaled talkers = %#v", talkers)
	}
	if elapsed > 2*time.Second {
		t.Fatalf("tag filter over %d devices took %s", nodes, elapsed)
	}
}

func deviceByID(devices []*mergedDevice, id string) *mergedDevice {
	for _, device := range devices {
		if containsString(device.ids, id) {
			return device
		}
	}
	return nil
}
