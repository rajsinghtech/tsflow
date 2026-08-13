package services

import (
	"encoding/json"
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
