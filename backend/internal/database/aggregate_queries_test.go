package database

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

func TestAggregateQueriesUseHalfOpenRangesAndMergeMetadata(t *testing.T) {
	store := setupTestDB(t)
	ctx := context.Background()
	base := (time.Now().UTC().Unix() / 60) * 60

	if err := store.UpsertNodePairAggregates(ctx, []NodePairAggregate{
		{
			Bucket: base, SrcNodeID: "a", DstNodeID: "b", TrafficType: "virtual",
			TxBytes: 100, Protocols: "[6]", ProtocolBytes: `{"6":100}`,
			Ports: `[{"port":443,"proto":6,"bytes":100}]`,
		},
		{
			Bucket: base + 60, SrcNodeID: "a", DstNodeID: "b", TrafficType: "virtual",
			TxBytes: 300, Protocols: "[17]", ProtocolBytes: `{"17":300}`,
			Ports: `[{"port":53,"proto":17,"bytes":300}]`,
		},
		{
			Bucket: base + 120, SrcNodeID: "a", DstNodeID: "b", TrafficType: "virtual",
			TxBytes: 900, Protocols: "[1]", ProtocolBytes: `{"1":900}`,
			Ports: `[{"port":7,"proto":1,"bytes":900}]`,
		},
	}); err != nil {
		t.Fatal(err)
	}

	aggregates, err := store.GetNodePairAggregates(ctx, time.Unix(base, 0), time.Unix(base+120, 0), 60)
	if err != nil {
		t.Fatal(err)
	}
	if len(aggregates) != 1 {
		t.Fatalf("expected one merged aggregate, got %d", len(aggregates))
	}
	if aggregates[0].TxBytes != 400 {
		t.Fatalf("tx bytes = %d, want 400", aggregates[0].TxBytes)
	}
	if aggregates[0].ProtocolBytes != `{"6":100,"17":300}` {
		t.Fatalf("protocol bytes = %s", aggregates[0].ProtocolBytes)
	}
	if aggregates[0].Protocols != "[17,6]" {
		t.Fatalf("protocol order = %s, want [17,6]", aggregates[0].Protocols)
	}
	var ports []PortStat
	if err := json.Unmarshal([]byte(aggregates[0].Ports), &ports); err != nil {
		t.Fatal(err)
	}
	if len(ports) != 2 || ports[0].Port != 53 || ports[1].Port != 443 {
		t.Fatalf("ports = %+v", ports)
	}

	boundary, err := store.GetNodePairAggregates(ctx, time.Unix(base+120, 0), time.Unix(base+180, 0), 60)
	if err != nil {
		t.Fatal(err)
	}
	if len(boundary) != 1 || boundary[0].TxBytes != 900 {
		t.Fatalf("expected boundary bucket only, got %+v", boundary)
	}
}

func TestGetDataRangeSingleBucketReturnsBucketEnd(t *testing.T) {
	store := setupTestDB(t)
	ctx := context.Background()
	base := (time.Now().UTC().Unix() / 60) * 60
	if err := store.UpsertNodePairAggregates(ctx, []NodePairAggregate{{
		Bucket: base, SrcNodeID: "a", DstNodeID: "b", TrafficType: "virtual", TxBytes: 1, Protocols: "[6]",
	}}); err != nil {
		t.Fatal(err)
	}

	dataRange, err := store.GetDataRange(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !dataRange.Earliest.Equal(time.Unix(base, 0).UTC()) || !dataRange.Latest.Equal(time.Unix(base+60, 0).UTC()) {
		t.Fatalf("data range = %+v", dataRange)
	}
}

func TestTrafficStatsUpsertMergesTopPorts(t *testing.T) {
	store := setupTestDB(t)
	ctx := context.Background()
	base := (time.Now().UTC().Unix() / 60) * 60
	if err := store.UpsertTrafficStats(ctx, []TrafficStats{
		{Bucket: base, TCPBytes: 100, TotalFlows: 1, TopPorts: `[{"port":443,"proto":6,"bytes":100}]`},
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertTrafficStats(ctx, []TrafficStats{
		{Bucket: base, UDPBytes: 300, TotalFlows: 2, TopPorts: `[{"port":53,"proto":17,"bytes":300}]`},
	}); err != nil {
		t.Fatal(err)
	}

	stats, err := store.GetTrafficStats(ctx, time.Unix(base, 0), time.Unix(base+60, 0))
	if err != nil {
		t.Fatal(err)
	}
	if len(stats) != 1 || stats[0].TCPBytes != 100 || stats[0].UDPBytes != 300 || stats[0].TotalFlows != 3 {
		t.Fatalf("stats = %+v", stats)
	}
	var ports []PortStat
	if err := json.Unmarshal([]byte(stats[0].TopPorts), &ports); err != nil {
		t.Fatal(err)
	}
	if len(ports) != 2 || ports[0].Port != 53 || ports[1].Port != 443 {
		t.Fatalf("top ports = %+v", ports)
	}
}

func TestDerivedTrafficStatsUnionPairsAcrossTrafficTypes(t *testing.T) {
	store := setupTestDB(t)
	ctx := context.Background()
	base := (time.Now().UTC().Unix() / 60) * 60
	if err := store.UpsertNodePairAggregates(ctx, []NodePairAggregate{
		{Bucket: base, SrcNodeID: "a", DstNodeID: "b", TrafficType: "virtual", TxBytes: 100, FlowCount: 2, Protocols: "[6]", ProtocolBytes: `{"6":100}`},
		{Bucket: base, SrcNodeID: "c", DstNodeID: "d", TrafficType: "subnet", TxBytes: 200, FlowCount: 3, Protocols: "[17]", ProtocolBytes: `{"17":200}`},
	}); err != nil {
		t.Fatal(err)
	}

	stats, err := store.GetTrafficStatsFromNodePairs(ctx, time.Unix(base, 0), time.Unix(base+60, 0))
	if err != nil {
		t.Fatal(err)
	}
	if len(stats) != 1 {
		t.Fatalf("stats = %+v, want one bucket", stats)
	}
	if stats[0].VirtualBytes != 100 || stats[0].SubnetBytes != 200 || stats[0].TotalFlows != 5 || stats[0].UniquePairs != 2 {
		t.Fatalf("stats = %+v", stats[0])
	}

	filtered, err := store.GetTrafficStatsFromNodePairsByTrafficTypes(ctx, time.Unix(base, 0), time.Unix(base+60, 0), []string{"virtual"})
	if err != nil {
		t.Fatal(err)
	}
	if len(filtered) != 1 || filtered[0].VirtualBytes != 100 || filtered[0].SubnetBytes != 0 || filtered[0].UniquePairs != 1 {
		t.Fatalf("filtered stats = %+v", filtered)
	}
}

func TestTrafficStatsRecomputesUniquePairsFromNodePairs(t *testing.T) {
	store := setupTestDB(t)
	ctx := context.Background()
	base := (time.Now().UTC().Unix() / 60) * 60
	if err := store.UpsertNodePairAggregates(ctx, []NodePairAggregate{
		{Bucket: base, SrcNodeID: "a", DstNodeID: "b", TrafficType: "virtual", TxBytes: 100, Protocols: "[6]", ProtocolBytes: `{"6":100}`},
		{Bucket: base, SrcNodeID: "c", DstNodeID: "d", TrafficType: "subnet", TxBytes: 200, Protocols: "[17]", ProtocolBytes: `{"17":200}`},
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertTrafficStats(ctx, []TrafficStats{{
		Bucket: base, TCPBytes: 100, TotalFlows: 2, UniquePairs: 1,
	}}); err != nil {
		t.Fatal(err)
	}

	stats, err := store.GetTrafficStats(ctx, time.Unix(base, 0), time.Unix(base+60, 0))
	if err != nil {
		t.Fatal(err)
	}
	if len(stats) != 1 || stats[0].UniquePairs != 2 {
		t.Fatalf("stats = %+v, want two unique pairs", stats)
	}
}
