package services

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/rajsinghtech/tsflow/backend/internal/database"
)

func portTotals(t *testing.T, raw string) map[int]int64 {
	t.Helper()
	var ports []database.PortStat
	if err := json.Unmarshal([]byte(raw), &ports); err != nil {
		t.Fatalf("ports %q: %v", raw, err)
	}
	out := map[int]int64{}
	for _, p := range ports {
		out[p.Port] += p.Bytes
	}
	return out
}

// Both ends log an SSH session with themselves as src (as in Tailscale's
// documented example). The server's reply bytes belong to port 22, not to
// the client's ephemeral port.
func TestTopPortsUseServicePortForBothSidesOfAConnection(t *testing.T) {
	poller := NewPoller(nil, nil, DefaultPollerConfig())
	at := time.Date(2026, 5, 8, 13, 45, 0, 0, time.UTC)
	flow := func(node, src string, srcPort int, dst string, dstPort int, tx int64) database.FlowLog {
		return database.FlowLog{
			LoggedAt: at, NodeID: node, TrafficType: "virtual", Protocol: 6,
			SrcIP: src, SrcPort: srcPort, DstIP: dst, DstPort: dstPort,
			TxBytes: tx, TxPkts: 1,
		}
	}
	logs := []database.FlowLog{
		flow("nClient", "100.64.0.2", 49288, "100.64.0.1", 22, 100),
		flow("nServer", "100.64.0.1", 22, "100.64.0.2", 49288, 5000),
		// Both ports high: no way to tell, keep dst as before.
		flow("nClient", "100.64.0.2", 40000, "100.64.0.1", 51820, 7),
	}
	pairs, _, _, stats := poller.aggregate(logs)
	if len(stats) != 1 {
		t.Fatalf("stats = %+v", stats)
	}
	got := portTotals(t, stats[0].TopPorts)
	if got[22] != 5100 || got[51820] != 7 || len(got) != 2 {
		t.Fatalf("top ports = %v, want 22:5100 and 51820:7", got)
	}
	if len(pairs) != 1 {
		t.Fatalf("pairs = %+v", pairs)
	}
	pairPorts := portTotals(t, pairs[0].Ports)
	if pairPorts[22] != 5100 || pairPorts[49288] != 0 {
		t.Fatalf("pair ports = %v, want 22:5100 and no ephemeral port", pairPorts)
	}
}

func TestServicePort(t *testing.T) {
	cases := []struct {
		src, dst, want int
	}{
		{49288, 22, 22},   // client side
		{22, 49288, 22},   // server side
		{443, 61000, 443}, // macOS/Windows range
		{2049, 864, 864},  // dst not ephemeral: unchanged
		{40000, 51820, 51820},
		{0, 50000, 50000}, // no src port
		{8080, 0, 0},
	}
	for _, c := range cases {
		if got := servicePort(database.FlowLog{SrcPort: c.src, DstPort: c.dst}); got != c.want {
			t.Errorf("servicePort(src=%d, dst=%d) = %d, want %d", c.src, c.dst, got, c.want)
		}
	}
}
