package database

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestAggregateQueriesUseHalfOpenRangesAndMergeMetadata(t *testing.T) {
	store := setupTestDB(t)
	ctx := context.Background()
	base := (time.Now().UTC().Unix() / 60) * 60

	if err := store.UpsertNodePairAggregates(ctx, DefaultTailnetID, []NodePairAggregate{
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

	aggregates, err := store.GetNodePairAggregates(ctx, DefaultTailnetID, time.Unix(base, 0), time.Unix(base+120, 0))
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

	boundary, err := store.GetNodePairAggregates(ctx, DefaultTailnetID, time.Unix(base+120, 0), time.Unix(base+180, 0))
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
	if err := store.UpsertNodePairAggregates(ctx, DefaultTailnetID, []NodePairAggregate{{
		Bucket: base, SrcNodeID: "a", DstNodeID: "b", TrafficType: "virtual", TxBytes: 1, Protocols: "[6]",
	}}); err != nil {
		t.Fatal(err)
	}

	dataRange, err := store.GetDataRange(ctx, DefaultTailnetID)
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
	if err := store.UpsertTrafficStats(ctx, DefaultTailnetID, []TrafficStats{
		{Bucket: base, TCPBytes: 100, TotalFlows: 1, TopPorts: `[{"port":443,"proto":6,"bytes":100}]`},
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertTrafficStats(ctx, DefaultTailnetID, []TrafficStats{
		{Bucket: base, UDPBytes: 300, TotalFlows: 2, TopPorts: `[{"port":53,"proto":17,"bytes":300}]`},
	}); err != nil {
		t.Fatal(err)
	}

	stats, err := store.GetTrafficStats(ctx, DefaultTailnetID, time.Unix(base, 0), time.Unix(base+60, 0))
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
	if err := store.UpsertNodePairAggregates(ctx, DefaultTailnetID, []NodePairAggregate{
		{Bucket: base, SrcNodeID: "a", DstNodeID: "b", TrafficType: "virtual", TxBytes: 100, FlowCount: 2, Protocols: "[6]", ProtocolBytes: `{"6":100}`},
		{Bucket: base, SrcNodeID: "c", DstNodeID: "d", TrafficType: "subnet", TxBytes: 200, FlowCount: 3, Protocols: "[17]", ProtocolBytes: `{"17":200}`},
	}); err != nil {
		t.Fatal(err)
	}

	stats, err := store.GetTrafficStatsFromNodePairs(ctx, DefaultTailnetID, time.Unix(base, 0), time.Unix(base+60, 0))
	if err != nil {
		t.Fatal(err)
	}
	if len(stats) != 1 {
		t.Fatalf("stats = %+v, want one bucket", stats)
	}
	if stats[0].VirtualBytes != 100 || stats[0].SubnetBytes != 200 || stats[0].TotalFlows != 5 || stats[0].UniquePairs != 2 {
		t.Fatalf("stats = %+v", stats[0])
	}

	filtered, err := store.GetTrafficStatsFromNodePairsByTrafficTypes(ctx, DefaultTailnetID, time.Unix(base, 0), time.Unix(base+60, 0), []string{"virtual"})
	if err != nil {
		t.Fatal(err)
	}
	if len(filtered) != 1 || filtered[0].VirtualBytes != 100 || filtered[0].SubnetBytes != 0 || filtered[0].UniquePairs != 1 {
		t.Fatalf("filtered stats = %+v", filtered)
	}
}

func TestDerivedTrafficStatsDistinguishesDelimiterContainingPairs(t *testing.T) {
	store := setupTestDB(t)
	ctx := context.Background()
	base := (time.Now().UTC().Unix() / 60) * 60
	if err := store.UpsertNodePairAggregates(ctx, DefaultTailnetID, []NodePairAggregate{
		{Bucket: base, SrcNodeID: "a|b", DstNodeID: "c", TrafficType: "virtual", TxBytes: 100, FlowCount: 1, Protocols: "[6]", ProtocolBytes: `{"6":100}`},
		{Bucket: base, SrcNodeID: "a", DstNodeID: "b|c", TrafficType: "virtual", TxBytes: 200, FlowCount: 1, Protocols: "[17]", ProtocolBytes: `{"17":200}`},
	}); err != nil {
		t.Fatal(err)
	}

	stats, err := store.GetTrafficStatsFromNodePairs(ctx, DefaultTailnetID, time.Unix(base, 0), time.Unix(base+60, 0))
	if err != nil {
		t.Fatal(err)
	}
	if len(stats) != 1 {
		t.Fatalf("stats = %+v, want one bucket", stats)
	}
	if stats[0].VirtualBytes != 300 || stats[0].TotalFlows != 2 || stats[0].UniquePairs != 2 {
		t.Fatalf("stats = %+v, want 300 virtual bytes, two flows, and two unique pairs", stats[0])
	}
}

func TestDerivedTrafficStatsFallsBackForMalformedProtocolBytes(t *testing.T) {
	store := setupTestDB(t)
	ctx := context.Background()
	base := (time.Now().UTC().Unix() / 60) * 60
	if _, err := store.db.ExecContext(ctx, `
		INSERT INTO node_pairs
			(tailnet_id, bucket, src_node_id, dst_node_id, traffic_type, tx_bytes, protocols, protocol_bytes)
		VALUES (?, ?, 'a', 'b', 'virtual', 100, '[6,17]', 'not-json')
	`, DefaultTailnetID, base); err != nil {
		t.Fatal(err)
	}

	stats, err := store.GetTrafficStatsFromNodePairs(ctx, DefaultTailnetID, time.Unix(base, 0), time.Unix(base+60, 0))
	if err != nil {
		t.Fatal(err)
	}
	if len(stats) != 1 {
		t.Fatalf("stats = %+v, want one bucket", stats)
	}
	if stats[0].TCPBytes != 50 || stats[0].UDPBytes != 50 {
		t.Fatalf("protocol stats = %+v, want 50 TCP and 50 UDP", stats[0])
	}
}

func TestDerivedTrafficStatsIncludesPhysicalProtocolBytes(t *testing.T) {
	store := setupTestDB(t)
	ctx := context.Background()
	base := (time.Now().UTC().Unix() / 60) * 60
	if err := store.UpsertNodePairAggregates(ctx, DefaultTailnetID, []NodePairAggregate{
		{Bucket: base, SrcNodeID: "a", DstNodeID: "b", TrafficType: "physical", TxBytes: 125, Protocols: "[6]", ProtocolBytes: `{"6":125}`, Ports: `[{"port":27,"proto":0,"bytes":125}]`},
		{Bucket: base, SrcNodeID: "c", DstNodeID: "d", TrafficType: "virtual", TxBytes: 50, Protocols: "[17]", ProtocolBytes: `{"17":50}`, Ports: `[{"port":53,"proto":17,"bytes":50}]`},
	}); err != nil {
		t.Fatal(err)
	}

	stats, err := store.GetTrafficStatsFromNodePairs(ctx, DefaultTailnetID, time.Unix(base, 0), time.Unix(base+60, 0))
	if err != nil {
		t.Fatal(err)
	}
	if len(stats) != 1 || stats[0].TCPBytes != 0 || stats[0].UDPBytes != 50 || stats[0].OtherProtoBytes != 0 || stats[0].PhysicalBytes != 125 {
		t.Fatalf("stats = %+v, want physical bytes kept out of protocol totals", stats)
	}
	var ports []PortStat
	if err := json.Unmarshal([]byte(stats[0].TopPorts), &ports); err != nil {
		t.Fatal(err)
	}
	if len(ports) != 1 || ports[0].Port != 53 {
		t.Fatalf("top ports = %+v, want the virtual port only", ports)
	}

	included, err := store.GetTrafficStatsFromNodePairsByTrafficTypes(ctx, DefaultTailnetID, time.Unix(base, 0), time.Unix(base+60, 0), []string{"physical", "virtual"})
	if err != nil {
		t.Fatal(err)
	}
	if len(included) != 1 || included[0].TCPBytes != 125 || included[0].UDPBytes != 50 || included[0].PhysicalBytes != 125 {
		t.Fatalf("included stats = %+v, want physical protocol bytes when physical is requested", included)
	}
}

func TestDerivedTrafficStatsKeepsExitBytesSeparateFromVirtual(t *testing.T) {
	store := setupTestDB(t)
	ctx := context.Background()
	base := (time.Now().UTC().Unix() / 60) * 60
	if err := store.UpsertNodePairAggregates(ctx, DefaultTailnetID, []NodePairAggregate{
		{Bucket: base, SrcNodeID: "a", DstNodeID: "b", TrafficType: "virtual", TxBytes: 100, Protocols: "[6]", ProtocolBytes: `{"6":100}`},
		{Bucket: base, SrcNodeID: "c", DstNodeID: "d", TrafficType: "exit", TxBytes: 40, Protocols: "[17]", ProtocolBytes: `{"17":40}`},
	}); err != nil {
		t.Fatal(err)
	}

	stats, err := store.GetTrafficStatsFromNodePairs(ctx, DefaultTailnetID, time.Unix(base, 0), time.Unix(base+60, 0))
	if err != nil {
		t.Fatal(err)
	}
	if len(stats) != 1 || stats[0].VirtualBytes != 100 || stats[0].ExitBytes != 40 {
		t.Fatalf("traffic stats = %+v, want virtual 100 and exit 40", stats[0])
	}

	filtered, err := store.GetTrafficStatsFromNodePairsByTrafficTypes(ctx, DefaultTailnetID, time.Unix(base, 0), time.Unix(base+60, 0), []string{"exit"})
	if err != nil {
		t.Fatal(err)
	}
	if len(filtered) != 1 || filtered[0].VirtualBytes != 0 || filtered[0].ExitBytes != 40 {
		t.Fatalf("filtered exit stats = %+v, want virtual 0 and exit 40", filtered[0])
	}
}

func TestGetBandwidthByTrafficTypesUsesPairTotalOnce(t *testing.T) {
	store := setupTestDB(t)
	ctx := context.Background()
	base := (time.Now().UTC().Unix() / 60) * 60
	if err := store.UpsertNodePairAggregates(ctx, DefaultTailnetID, []NodePairAggregate{{
		Bucket: base, SrcNodeID: "a", DstNodeID: "b", TrafficType: "virtual",
		TxBytes: 100, RxBytes: 40, Protocols: "[6]", ProtocolBytes: `{"6":140}`,
	}}); err != nil {
		t.Fatal(err)
	}

	buckets, err := store.GetBandwidthByTrafficTypes(ctx, DefaultTailnetID, time.Unix(base, 0), time.Unix(base+60, 0), []string{"virtual"})
	if err != nil {
		t.Fatal(err)
	}
	if len(buckets) != 1 || buckets[0].TxBytes != 140 || buckets[0].RxBytes != 0 {
		t.Fatalf("filtered bandwidth = %+v, want one 140-byte TX bucket", buckets)
	}
}

func TestTrafficStatsRecomputesUniquePairsFromNodePairs(t *testing.T) {
	store := setupTestDB(t)
	ctx := context.Background()
	base := (time.Now().UTC().Unix() / 60) * 60
	if err := store.UpsertNodePairAggregates(ctx, DefaultTailnetID, []NodePairAggregate{
		{Bucket: base, SrcNodeID: "a", DstNodeID: "b", TrafficType: "virtual", TxBytes: 100, Protocols: "[6]", ProtocolBytes: `{"6":100}`},
		{Bucket: base, SrcNodeID: "c", DstNodeID: "d", TrafficType: "subnet", TxBytes: 200, Protocols: "[17]", ProtocolBytes: `{"17":200}`},
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertTrafficStats(ctx, DefaultTailnetID, []TrafficStats{{
		Bucket: base, TCPBytes: 100, TotalFlows: 2, UniquePairs: 1,
	}}); err != nil {
		t.Fatal(err)
	}

	stats, err := store.GetTrafficStats(ctx, DefaultTailnetID, time.Unix(base, 0), time.Unix(base+60, 0))
	if err != nil {
		t.Fatal(err)
	}
	if len(stats) != 1 || stats[0].UniquePairs != 2 {
		t.Fatalf("stats = %+v, want two unique pairs", stats)
	}
}

func TestGetNodeStatsPropagatesMalformedPortJSON(t *testing.T) {
	store := setupTestDB(t)
	ctx := context.Background()
	base := (time.Now().UTC().Unix() / 60) * 60
	if _, err := store.db.ExecContext(ctx, `
		INSERT INTO node_pairs
			(tailnet_id, bucket, src_node_id, dst_node_id, traffic_type, tx_bytes, ports)
		VALUES (?, ?, 'a', 'b', 'virtual', 100, 'not-json')
	`, DefaultTailnetID, base); err != nil {
		t.Fatal(err)
	}

	_, err := store.GetNodeStats(ctx, DefaultTailnetID, "a", time.Unix(base, 0), time.Unix(base+60, 0))
	if err == nil || !strings.Contains(err.Error(), "failed to decode node ports") {
		t.Fatalf("GetNodeStats error = %v, want malformed port error", err)
	}
}

func TestGetNodeStatsDoesNotDoubleCountSelfTraffic(t *testing.T) {
	store := setupTestDB(t)
	ctx := context.Background()
	base := (time.Now().UTC().Unix() / 60) * 60
	if err := store.UpsertNodePairAggregates(ctx, DefaultTailnetID, []NodePairAggregate{{
		Bucket: base, SrcNodeID: "a", DstNodeID: "a", TrafficType: "virtual",
		TxBytes: 100, RxBytes: 50, FlowCount: 1, Protocols: "[6]", Ports: "[]",
	}}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertNodeBandwidth(ctx, DefaultTailnetID, []NodeBandwidth{{
		Bucket: base, NodeID: "a", TxBytes: 100, RxBytes: 50,
	}}); err != nil {
		t.Fatal(err)
	}

	stats, err := store.GetNodeStats(ctx, DefaultTailnetID, "a", time.Unix(base, 0), time.Unix(base+60, 0))
	if err != nil {
		t.Fatal(err)
	}
	if len(stats.TopPeers) != 1 || stats.TopPeers[0].DstNodeID != "a" ||
		stats.TopPeers[0].TxBytes != 100 || stats.TopPeers[0].RxBytes != 50 ||
		stats.TopPeers[0].FlowCount != 1 {
		t.Fatalf("self peer stats = %+v, want one unduplicated peer", stats.TopPeers)
	}
}

func TestGetTopTalkersByTrafficTypesDoesNotDoubleCountSelfTraffic(t *testing.T) {
	store := setupTestDB(t)
	ctx := context.Background()
	base := (time.Now().UTC().Unix() / 60) * 60
	if err := store.UpsertNodePairAggregates(ctx, DefaultTailnetID, []NodePairAggregate{{
		Bucket: base, SrcNodeID: "a", DstNodeID: "a", TrafficType: "virtual",
		TxBytes: 100, RxBytes: 50, FlowCount: 1, Protocols: "[6]", Ports: "[]",
	}}); err != nil {
		t.Fatal(err)
	}

	talkers, err := store.GetTopTalkersByTrafficTypes(ctx, DefaultTailnetID, time.Unix(base, 0), time.Unix(base+60, 0), []string{"virtual"}, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(talkers) != 1 || talkers[0].NodeID != "a" ||
		talkers[0].TxBytes != 100 || talkers[0].RxBytes != 50 || talkers[0].TotalBytes != 150 {
		t.Fatalf("self talker stats = %+v, want one unduplicated self row", talkers)
	}
}

func TestGetTopTalkersDoesNotDoubleCountSelfTraffic(t *testing.T) {
	store := setupTestDB(t)
	ctx := context.Background()
	base := (time.Now().UTC().Unix() / 60) * 60
	if err := store.UpsertNodePairAggregates(ctx, DefaultTailnetID, []NodePairAggregate{{
		Bucket: base, SrcNodeID: "a", DstNodeID: "a", TrafficType: "virtual",
		TxBytes: 100, RxBytes: 0, FlowCount: 1, Protocols: "[6]", ProtocolBytes: `{"6":100}`, Ports: "[]",
	}}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertNodeBandwidth(ctx, DefaultTailnetID, []NodeBandwidth{{
		Bucket: base, NodeID: "a", TxBytes: 100, RxBytes: 50,
	}}); err != nil {
		t.Fatal(err)
	}

	talkers, err := store.GetTopTalkers(ctx, DefaultTailnetID, time.Unix(base, 0), time.Unix(base+60, 0), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(talkers) != 1 || talkers[0].NodeID != "a" ||
		talkers[0].TxBytes != 100 || talkers[0].RxBytes != 0 || talkers[0].TotalBytes != 100 {
		t.Fatalf("self talker stats = %+v, want one normalized self row", talkers)
	}
}

func TestNodeBandwidthAndStatsDeriveFromPairsInsteadOfLegacyNodeRows(t *testing.T) {
	store := setupTestDB(t)
	ctx := context.Background()
	base := (time.Now().UTC().Unix() / 60) * 60
	if err := store.UpsertNodePairAggregates(ctx, DefaultTailnetID, []NodePairAggregate{
		{Bucket: base, SrcNodeID: "a", DstNodeID: "a", TrafficType: "virtual", TxBytes: 100, RxBytes: 0, FlowCount: 1, Protocols: "[6]", ProtocolBytes: `{"6":100}`, Ports: "[]"},
		{Bucket: base, SrcNodeID: "a", DstNodeID: "b", TrafficType: "virtual", TxBytes: 25, RxBytes: 5, FlowCount: 1, Protocols: "[6]", ProtocolBytes: `{"6":30}`, Ports: "[]"},
	}); err != nil {
		t.Fatal(err)
	}
	// Simulate a legacy row from the old self-flow accounting, where the same
	// 100 bytes were stored once as TX and again as RX.
	if err := store.UpsertNodeBandwidth(ctx, DefaultTailnetID, []NodeBandwidth{{
		Bucket: base, NodeID: "a", TxBytes: 125, RxBytes: 105,
	}}); err != nil {
		t.Fatal(err)
	}

	buckets, err := store.GetNodeBandwidth(ctx, DefaultTailnetID, time.Unix(base, 0), time.Unix(base+60, 0), "a")
	if err != nil {
		t.Fatal(err)
	}
	if len(buckets) != 1 || buckets[0].TxBytes != 125 || buckets[0].RxBytes != 5 {
		t.Fatalf("node bandwidth = %+v, want TX 125 RX 5 from normalized pairs", buckets)
	}

	stats, err := store.GetNodeStats(ctx, DefaultTailnetID, "a", time.Unix(base, 0), time.Unix(base+60, 0))
	if err != nil {
		t.Fatal(err)
	}
	if stats.TotalTx != 125 || stats.TotalRx != 5 {
		t.Fatalf("node totals = TX %d RX %d, want 125 and 5", stats.TotalTx, stats.TotalRx)
	}
}

func TestNodeBandwidthAndStatsUseCoarseBucketTotals(t *testing.T) {
	store := setupTestDB(t)
	ctx := context.Background()
	base := (time.Now().UTC().Unix() / 60) * 60
	if err := store.UpsertNodePairAggregates(ctx, DefaultTailnetID, []NodePairAggregate{
		{Bucket: base, SrcNodeID: "a", DstNodeID: "b", TrafficType: "virtual", TxBytes: 10, RxBytes: 2, Protocols: "[6]", ProtocolBytes: `{"6":12}`, Ports: "[]"},
		{Bucket: base + 60, SrcNodeID: "a", DstNodeID: "b", TrafficType: "virtual", TxBytes: 20, RxBytes: 3, Protocols: "[6]", ProtocolBytes: `{"6":23}`, Ports: "[]"},
	}); err != nil {
		t.Fatal(err)
	}

	buckets, err := store.GetNodeBandwidth(ctx, DefaultTailnetID, time.Unix(base, 0), time.Unix(base+2*60, 0), "a")
	if err != nil {
		t.Fatal(err)
	}
	if len(buckets) != 2 || buckets[0].TxBytes != 10 || buckets[0].RxBytes != 2 || buckets[1].TxBytes != 20 || buckets[1].RxBytes != 3 {
		t.Fatalf("node bandwidth buckets = %+v, want separate minute totals", buckets)
	}
}

func TestDefaultRankingsOmitPhysicalUntilRequested(t *testing.T) {
	store := setupTestDB(t)
	ctx := context.Background()
	base := (time.Now().UTC().Unix() / 60) * 60
	if err := store.UpsertNodePairAggregates(ctx, DefaultTailnetID, []NodePairAggregate{
		{Bucket: base, SrcNodeID: "host", DstNodeID: "peer", TrafficType: "virtual", TxBytes: 30, RxBytes: 10, FlowCount: 1, Protocols: "[6]", ProtocolBytes: `{"6":40}`},
		{Bucket: base, SrcNodeID: "127.3.3.40", DstNodeID: "relay", TrafficType: "physical", TxBytes: 500, FlowCount: 4, Protocols: "[0]", ProtocolBytes: `{"0":500}`},
	}); err != nil {
		t.Fatal(err)
	}
	start, end := time.Unix(base, 0), time.Unix(base+60, 0)

	talkers, err := store.GetTopTalkers(ctx, DefaultTailnetID, start, end, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(talkers) != 2 || talkers[0].NodeID == "127.3.3.40" || talkers[0].TotalBytes != 40 {
		t.Fatalf("default talkers = %+v, want the virtual pair without the DERP relay", talkers)
	}
	pairs, err := store.GetTopPairs(ctx, DefaultTailnetID, start, end, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(pairs) != 1 || pairs[0].DstNodeID != "peer" || pairs[0].TotalBytes != 40 {
		t.Fatalf("default pairs = %+v, want the virtual pair only", pairs)
	}
	ranked, _, err := store.ListRankedTalkers(ctx, DefaultTailnetID, start, end, RankQuery{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(ranked) != 2 || ranked[0].NodeID == "127.3.3.40" || ranked[0].TotalBytes != 40 {
		t.Fatalf("default ranked talkers = %+v", ranked)
	}

	withPhysical, err := store.GetTopTalkersByTrafficTypes(ctx, DefaultTailnetID, start, end, []string{"physical", "virtual"}, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(withPhysical) == 0 || withPhysical[0].NodeID != "127.3.3.40" || withPhysical[0].TotalBytes != 500 {
		t.Fatalf("explicit physical talkers = %+v, want the DERP relay first", withPhysical)
	}
	nodeBW, err := store.GetNodeBandwidth(ctx, DefaultTailnetID, start, end, "host")
	if err != nil {
		t.Fatal(err)
	}
	if len(nodeBW) != 1 || nodeBW[0].TxBytes != 30 {
		t.Fatalf("node bandwidth = %+v, want virtual TX only", nodeBW)
	}
}

func TestCountDistinctPairsUsesRollupAcrossTheWindow(t *testing.T) {
	store := setupTestDB(t)
	ctx := context.Background()
	// A completed hour in the past so the rollup can absorb the minute rows.
	base := time.Now().UTC().Add(-48 * time.Hour).Truncate(time.Hour).Unix()
	if err := store.UpsertNodePairAggregates(ctx, DefaultTailnetID, []NodePairAggregate{
		{Bucket: base, SrcNodeID: "a", DstNodeID: "b", TrafficType: "virtual", TxBytes: 1, FlowCount: 1, Protocols: "[6]", ProtocolBytes: `{"6":1}`},
		{Bucket: base + 3600, SrcNodeID: "c", DstNodeID: "d", TrafficType: "virtual", TxBytes: 1, FlowCount: 1, Protocols: "[6]", ProtocolBytes: `{"6":1}`},
		{Bucket: base + 3600 + 60, SrcNodeID: "a", DstNodeID: "b", TrafficType: "physical", TxBytes: 9, FlowCount: 1, Protocols: "[0]", ProtocolBytes: `{"0":9}`},
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.backfillHourRollups(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.ExecContext(ctx, `DELETE FROM node_pairs WHERE tailnet_id = ? AND bucket >= ? AND bucket < ?`, DefaultTailnetID, base, base+2*3600); err != nil {
		t.Fatal(err)
	}

	start, end := time.Unix(base, 0).UTC(), time.Unix(base+2*3600, 0).UTC()
	got, err := store.CountDistinctPairs(ctx, DefaultTailnetID, start, end, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got != 2 {
		t.Fatalf("distinct pairs = %d, want 2 from the hourly rollup after minute rows were removed", got)
	}
	virtualOnly, err := store.CountDistinctPairs(ctx, DefaultTailnetID, start, end, []string{"virtual"})
	if err != nil {
		t.Fatal(err)
	}
	if virtualOnly != 2 {
		t.Fatalf("virtual distinct pairs = %d, want 2", virtualOnly)
	}
}
