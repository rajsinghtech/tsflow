package services

import (
	"encoding/json"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/rajsinghtech/tsflow/backend/internal/database"
)

func TestRollingCacheUsesHalfOpenRangesAndCompleteCoverage(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Minute)
	cache := NewRollingWindowCache(10 * time.Minute)
	cache.Update(
		[]database.NodePairAggregate{{Bucket: now.Add(-time.Minute).Unix(), SrcNodeID: "a", DstNodeID: "b", TrafficType: "virtual"}},
		nil, nil, nil,
	)

	if got := cache.GetNodePairs(now.Add(-time.Minute), now); len(got) != 1 {
		t.Fatalf("expected one bucket in half-open range, got %d", len(got))
	}
	if got := cache.GetNodePairs(now, now.Add(time.Minute)); len(got) != 0 {
		t.Fatalf("expected end boundary to be excluded, got %d", len(got))
	}
	if !cache.HasNodePairDataFor(now.Add(-time.Minute), now) {
		t.Fatal("expected complete pair coverage")
	}
	if cache.HasNodePairDataFor(now.Add(-2*time.Minute), now) {
		t.Fatal("partial pair coverage should miss")
	}
	if cache.HasBandwidthDataFor(now.Add(-time.Minute), now) {
		t.Fatal("pair data must not satisfy bandwidth coverage")
	}
}

func TestRollingCacheUnalignedRangeMatchesGetterCoverage(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Minute)
	cache := NewRollingWindowCache(10 * time.Minute)
	cache.Update(
		[]database.NodePairAggregate{{Bucket: now.Unix(), SrcNodeID: "a", DstNodeID: "b", TrafficType: "virtual"}},
		nil, nil, nil,
	)

	start := now.Add(30 * time.Second)
	end := now.Add(time.Minute)
	if got := cache.GetNodePairs(start, end); len(got) != 0 {
		t.Fatalf("getter returned %d bucket(s), want none before the bucket start", len(got))
	}
	if cache.HasNodePairDataFor(start, end) {
		t.Fatal("unaligned range must not report coverage for an excluded bucket")
	}
}

func TestRollingCacheMergesProtocolAndPortMetadata(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Minute)
	cache := NewRollingWindowCache(10 * time.Minute)
	cache.Update([]database.NodePairAggregate{{
		Bucket: now.Add(-time.Minute).Unix(), SrcNodeID: "a", DstNodeID: "b", TrafficType: "virtual",
		TxBytes: 100, Protocols: "[6]", ProtocolBytes: `{"6":100}`,
		Ports: `[{"port":443,"proto":6,"bytes":100}]`,
	}}, nil, nil, nil)
	cache.Update([]database.NodePairAggregate{{
		Bucket: now.Add(-time.Minute).Unix(), SrcNodeID: "a", DstNodeID: "b", TrafficType: "virtual",
		TxBytes: 300, Protocols: "[17]", ProtocolBytes: `{"17":300}`,
		Ports: `[{"port":53,"proto":17,"bytes":300}]`,
	}}, nil, nil, nil)

	got := cache.GetNodePairs(now.Add(-time.Minute), now)
	if len(got) != 1 || got[0].TxBytes != 400 {
		t.Fatalf("unexpected merged pair: %+v", got)
	}
	if got[0].Protocols != "[17,6]" {
		t.Fatalf("protocol order = %s, want [17,6]", got[0].Protocols)
	}
	var ports []database.PortStat
	if err := json.Unmarshal([]byte(got[0].Ports), &ports); err != nil {
		t.Fatal(err)
	}
	if len(ports) != 2 || ports[0].Port != 53 || ports[1].Port != 443 {
		t.Fatalf("merged ports = %+v", ports)
	}
}

func TestRollingCacheMergesDirectionalMetadata(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Minute)
	cache := NewRollingWindowCache(10 * time.Minute)
	cache.Update([]database.NodePairAggregate{{
		Bucket: now.Unix(), SrcNodeID: "a", DstNodeID: "b", TrafficType: "virtual",
		TxBytes: 100, Protocols: "[6]", ProtocolBytes: `{"6":100}`,
		TxProtocolBytes: `{"6":100}`, TxPorts: `[{"port":443,"proto":6,"bytes":100}]`, DirectionalPorts: true,
	}}, nil, nil, nil)
	cache.Update([]database.NodePairAggregate{{
		Bucket: now.Unix(), SrcNodeID: "a", DstNodeID: "b", TrafficType: "virtual",
		RxBytes: 40, Protocols: "[17]", ProtocolBytes: `{"17":40}`,
		RxProtocolBytes: `{"17":40}`, RxPorts: `[{"port":53,"proto":17,"bytes":40}]`, DirectionalPorts: true,
	}}, nil, nil, nil)

	pairs := cache.GetNodePairs(now, now.Add(time.Minute))
	if len(pairs) != 1 || !pairs[0].DirectionalPorts {
		t.Fatalf("cached pair = %+v", pairs)
	}
	if pairs[0].TxProtocolBytes != `{"6":100}` || pairs[0].RxProtocolBytes != `{"17":40}` {
		t.Fatalf("directional protocol bytes = tx %s rx %s", pairs[0].TxProtocolBytes, pairs[0].RxProtocolBytes)
	}
	var txPorts, rxPorts []database.PortStat
	if err := json.Unmarshal([]byte(pairs[0].TxPorts), &txPorts); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(pairs[0].RxPorts), &rxPorts); err != nil {
		t.Fatal(err)
	}
	if len(txPorts) != 1 || txPorts[0].Port != 443 || len(rxPorts) != 1 || rxPorts[0].Port != 53 {
		t.Fatalf("directional cached ports = tx %+v rx %+v", txPorts, rxPorts)
	}
}

func TestRollingCacheCountsUniquePairsAcrossTrafficTypes(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Minute)
	cache := NewRollingWindowCache(10 * time.Minute)
	cache.Update(
		[]database.NodePairAggregate{{Bucket: now.Unix(), SrcNodeID: "a", DstNodeID: "b", TrafficType: "virtual"}},
		nil, nil,
		[]database.TrafficStats{{Bucket: now.Unix(), VirtualBytes: 10, UniquePairs: 1}},
	)
	cache.Update(
		[]database.NodePairAggregate{{Bucket: now.Unix(), SrcNodeID: "c", DstNodeID: "d", TrafficType: "subnet"}},
		nil, nil,
		[]database.TrafficStats{{Bucket: now.Unix(), SubnetBytes: 20, UniquePairs: 1}},
	)

	stats := cache.GetTrafficStats(now, now.Add(time.Minute))
	if len(stats) != 1 || stats[0].UniquePairs != 2 {
		t.Fatalf("traffic stats = %+v, want two unique pairs", stats)
	}
}

func TestRollingCacheCountsDelimiterContainingPairsSeparately(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Minute)
	cache := NewRollingWindowCache(10 * time.Minute)
	cache.Update(
		[]database.NodePairAggregate{
			{Bucket: now.Unix(), SrcNodeID: "a|b", DstNodeID: "c", TrafficType: "virtual"},
			{Bucket: now.Unix(), SrcNodeID: "a", DstNodeID: "b|c", TrafficType: "virtual"},
		},
		nil, nil,
		[]database.TrafficStats{{Bucket: now.Unix(), VirtualBytes: 300, TotalFlows: 2, UniquePairs: 1}},
	)

	stats := cache.GetTrafficStats(now, now.Add(time.Minute))
	if len(stats) != 1 || stats[0].UniquePairs != 2 {
		t.Fatalf("traffic stats = %+v, want two delimiter-containing pairs", stats)
	}
}

func TestRollingCacheIndexedMatchesLinear(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Minute)
	bucket := now.Unix()
	older := now.Add(-2 * time.Hour).Unix()

	batches := []cacheBatch{
		{
			pairs: []database.NodePairAggregate{
				pair(bucket, "a", "b", "virtual", 100, 0, true),
				pair(bucket, "a", "b", "subnet", 5, 0, true),
				pair(bucket, "a|b", "c", "virtual", 7, 1, true),
				pair(bucket, "a", "b|c", "virtual", 8, 0, true),
				pair(bucket, "self", "self", "virtual", 3, 0, true),
				pair(bucket, "z", "a", "virtual", 9, 4, false),
				pair(bucket, "a", "b", "virtual", 20, 6, true),
			},
			bandwidth: []database.BandwidthBucket{{Time: now, TxBytes: 10, RxBytes: 1}},
			nodes: []database.NodeBandwidth{
				{Bucket: bucket, NodeID: "a", TxBytes: 10, RxBytes: 1},
				{Bucket: bucket, NodeID: "a", TxBytes: 4, RxBytes: 2},
			},
			stats: []database.TrafficStats{{Bucket: bucket, VirtualBytes: 10, UniquePairs: 1, TopPorts: `[{"port":443,"proto":6,"bytes":10}]`}},
		},
		{
			pairs: []database.NodePairAggregate{
				pair(bucket, "z", "a", "virtual", 1, 0, true),
				pair(bucket, "m", "n", "physical", 50, 0, true),
				pair(bucket, "a", "b", "virtual", 15, 0, false),
				pair(older, "old", "gone", "virtual", 1, 0, true),
			},
			bandwidth: []database.BandwidthBucket{{Time: now, TxBytes: 3, RxBytes: 0}},
			nodes:     []database.NodeBandwidth{{Bucket: bucket, NodeID: "m", TxBytes: 50}},
			stats:     []database.TrafficStats{{Bucket: bucket, PhysicalBytes: 50, UniquePairs: 1, TopPorts: `[{"port":22,"proto":6,"bytes":50}]`}},
		},
	}

	indexed := NewRollingWindowCache(time.Hour)
	linear := NewRollingWindowCache(time.Hour)
	for i, batch := range batches {
		indexed.Update(batch.pairs, batch.bandwidth, batch.nodes, batch.stats)
		linear.updateLinear(batch.pairs, batch.bandwidth, batch.nodes, batch.stats)
		assertCachesEqual(t, fmt.Sprintf("batch %d", i), indexed, linear, now.Add(-time.Minute), now.Add(time.Minute))
	}

	// A pruned bucket must not leave an index entry that the next update merges into.
	again := []database.NodePairAggregate{pair(older, "old", "gone", "virtual", 4, 0, true)}
	indexed.Update(again, nil, nil, nil)
	linear.updateLinear(again, nil, nil, nil)
	assertCachesEqual(t, "pruned", indexed, linear, now.Add(-time.Minute), now.Add(time.Minute))
	if _, ok := indexed.nodePairIndex[older]; ok {
		t.Fatal("pruned bucket kept an index entry")
	}
	if got := indexed.GetNodePairs(time.Unix(older, 0), time.Unix(older+60, 0)); len(got) != 0 {
		t.Fatalf("pruned pairs still visible: %+v", got)
	}
}

func TestRollingCacheIndexedPreservesPairOrder(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Minute)
	bucket := now.Unix()
	pairs := []database.NodePairAggregate{
		pair(bucket, "c", "d", "virtual", 1, 0, true),
		pair(bucket, "a", "b", "subnet", 1, 0, true),
		pair(bucket, "a", "b", "virtual", 1, 0, true),
		pair(bucket, "a", "b", "virtual", 2, 0, true),
		pair(bucket, "b", "a", "virtual", 1, 0, true),
	}
	indexed := NewRollingWindowCache(time.Hour)
	linear := NewRollingWindowCache(time.Hour)
	indexed.Update(pairs, nil, nil, nil)
	linear.updateLinear(pairs, nil, nil, nil)

	if !reflect.DeepEqual(indexed.nodePairs[bucket], linear.nodePairs[bucket]) {
		t.Fatalf("stored order differs\nindexed: %+v\nlinear:  %+v", indexed.nodePairs[bucket], linear.nodePairs[bucket])
	}
	if len(indexed.nodePairs[bucket]) != 4 {
		t.Fatalf("pair count = %d, want 4 distinct keys", len(indexed.nodePairs[bucket]))
	}
	if indexed.nodePairs[bucket][2].TxBytes != 3 {
		t.Fatalf("merged virtual a->b bytes = %d, want 3", indexed.nodePairs[bucket][2].TxBytes)
	}
}

type cacheBatch struct {
	pairs     []database.NodePairAggregate
	bandwidth []database.BandwidthBucket
	nodes     []database.NodeBandwidth
	stats     []database.TrafficStats
}

func pair(bucket int64, src, dst, trafficType string, tx, rx int64, directional bool) database.NodePairAggregate {
	proto := 6
	if trafficType == "physical" {
		proto = 17
	}
	row := database.NodePairAggregate{
		Bucket: bucket, SrcNodeID: src, DstNodeID: dst, TrafficType: trafficType,
		TxBytes: tx, RxBytes: rx, TxPkts: tx, RxPkts: rx, FlowCount: 1,
		Protocols: fmt.Sprintf("[%d]", proto), ProtocolBytes: fmt.Sprintf(`{"%d":%d}`, proto, tx+rx),
		Ports: fmt.Sprintf(`[{"port":443,"proto":%d,"bytes":%d}]`, proto, tx+rx),
	}
	if directional {
		row.DirectionalPorts = true
		row.TxProtocolBytes = fmt.Sprintf(`{"%d":%d}`, proto, tx)
		row.RxProtocolBytes = fmt.Sprintf(`{"%d":%d}`, proto, rx)
		row.TxPorts = fmt.Sprintf(`[{"port":443,"proto":%d,"bytes":%d}]`, proto, tx)
		row.RxPorts = fmt.Sprintf(`[{"port":53,"proto":%d,"bytes":%d}]`, proto, rx)
	}
	return row
}

func assertCachesEqual(t *testing.T, label string, indexed, linear *RollingWindowCache, start, end time.Time) {
	t.Helper()
	if !reflect.DeepEqual(indexed.nodePairs, linear.nodePairs) {
		t.Fatalf("%s stored pairs differ\nindexed: %#v\nlinear:  %#v", label, indexed.nodePairs, linear.nodePairs)
	}
	if !reflect.DeepEqual(indexed.GetNodePairs(start, end), linear.GetNodePairs(start, end)) {
		t.Fatalf("%s GetNodePairs differ", label)
	}
	if !reflect.DeepEqual(indexed.GetBandwidth(start, end), linear.GetBandwidth(start, end)) {
		t.Fatalf("%s bandwidth differ", label)
	}
	if !reflect.DeepEqual(indexed.GetNodeBandwidth(start, end, "a"), linear.GetNodeBandwidth(start, end, "a")) {
		t.Fatalf("%s node bandwidth differ", label)
	}
	if !reflect.DeepEqual(indexed.GetTrafficStats(start, end), linear.GetTrafficStats(start, end)) {
		t.Fatalf("%s traffic stats differ", label)
	}
}
