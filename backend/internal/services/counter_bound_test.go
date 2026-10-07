package services

import (
	"context"
	"testing"
	"time"

	"github.com/rajsinghtech/tsflow/backend/internal/database"
	tailscale "tailscale.com/client/tailscale/v2"
)

// Two records that each fit in int64 can still overflow a SQLite SUM over
// the window. One bogus counter would then fail every stats read for the
// whole retention period.
func TestObjectStoreHugeCountersDoNotBreakWindowSums(t *testing.T) {
	base := time.Date(2026, 5, 8, 13, 0, 0, 0, time.UTC)
	objects := []testObject{
		testFlowObjectAt(t, "network/2026/05/08/2026-05-08-13-00-00.ndjson", "node-a", base, 1<<62, false),
		testFlowObjectAt(t, "network/2026/05/08/2026-05-08-13-01-00.ndjson", "node-a", base.Add(time.Minute), 1<<62, false),
		testFlowObjectAt(t, "network/2026/05/08/2026-05-08-13-02-00.ndjson", "node-a", base.Add(2*time.Minute), 1000, false),
	}
	source, server := newTestObjectStore(t, objects, len(objects))
	defer server.Close()
	poller, db := newObjectStoreTestPoller(t, source, len(objects))
	ctx := context.Background()
	if err := poller.pollObjectStore(ctx, base.Add(-time.Minute), base.Add(5*time.Minute)); err != nil {
		t.Fatal(err)
	}

	stats, err := db.store.GetTrafficStats(ctx, database.DefaultTailnetID, base.Add(-time.Minute), base.Add(5*time.Minute))
	if err != nil {
		t.Fatalf("traffic stats over a window with implausible counters: %v", err)
	}
	var total int64
	for _, bucket := range stats {
		total += bucket.VirtualBytes
	}
	if total != 1000 {
		t.Fatalf("virtual bytes = %d, want only the plausible 1000-byte record", total)
	}
	pairs, err := db.store.GetNodePairAggregates(ctx, database.DefaultTailnetID, base.Add(-time.Minute), base.Add(5*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	for _, pair := range pairs {
		if pair.TxBytes < 0 || pair.RxBytes < 0 {
			t.Fatalf("pair has negative bytes after aggregation: %+v", pair)
		}
	}
}

func TestConvertTailscaleLogSkipsImplausibleCounters(t *testing.T) {
	poller := NewPoller(nil, nil, DefaultPollerConfig())
	flows := poller.convertTailscaleLog(tailscale.NetworkFlowLog{
		NodeID: "node-a",
		Start:  time.Date(2026, 5, 8, 13, 45, 0, 0, time.UTC),
		VirtualTraffic: []tailscale.TrafficStats{
			{Proto: 6, Src: "100.64.0.1:1234", Dst: "100.64.0.2:443", TxBytes: 1 << 62, TxPkts: 1},
			{Proto: 6, Src: "100.64.0.1:1234", Dst: "100.64.0.2:443", TxBytes: 10, TxPkts: 1, RxBytes: 1 << 50, RxPkts: 1},
			{Proto: 6, Src: "100.64.0.1:1234", Dst: "100.64.0.2:443", TxBytes: maxFlowCounter, TxPkts: 1},
		},
	})
	if len(flows) != 1 || flows[0].TxBytes != maxFlowCounter {
		t.Fatalf("converted flows = %+v, want only the record at the bound", flows)
	}
}

func TestConvertMapLogSkipsImplausibleCounters(t *testing.T) {
	poller := NewPoller(nil, nil, DefaultPollerConfig())
	flows := poller.convertMapLog(map[string]any{
		"nodeId": "node-a",
		"start":  "2026-05-08T13:45:00Z",
		"virtualTraffic": []any{
			map[string]any{"proto": float64(6), "src": "100.64.0.1:1234", "dst": "100.64.0.2:443", "txBytes": float64(1 << 62), "txPkts": float64(1)},
			map[string]any{"proto": float64(6), "src": "100.64.0.1:1234", "dst": "100.64.0.2:443", "txBytes": float64(10), "txPkts": float64(1 << 50)},
			map[string]any{"proto": float64(6), "src": "100.64.0.1:1234", "dst": "100.64.0.2:443", "txBytes": float64(10), "txPkts": float64(1)},
		},
	})
	if len(flows) != 1 || flows[0].TxBytes != 10 {
		t.Fatalf("converted flows = %+v, want only the plausible row", flows)
	}
}
