package services

import (
	"testing"
	"time"

	tailscale "tailscale.com/client/tailscale/v2"
)

// Without destination logging, Tailscale blanks the public side of exit
// traffic. The client logs src=its address and no dst; the exit node logs
// dst=the client and no src. Both rows must be kept, and together they
// give the client's upload and download through the exit node once each.
func TestExitTrafficWithBlankPublicSideIsKept(t *testing.T) {
	poller := NewPoller(nil, nil, DefaultPollerConfig())
	start := "2026-05-08T13:45:00Z"
	client := poller.convertMapLog(map[string]any{
		"nodeId": "nClient", "start": start, "logged": "2026-05-08T13:45:06Z",
		"exitTraffic": []any{map[string]any{
			"src": "100.64.0.1:0", "txBytes": float64(100), "txPkts": float64(2), "rxBytes": float64(900), "rxPkts": float64(3),
		}},
	})
	exitNode := poller.convertMapLog(map[string]any{
		"nodeId": "nExit", "start": start, "logged": "2026-05-08T13:45:06Z",
		"exitTraffic": []any{map[string]any{
			"dst": "100.64.0.1:0", "txBytes": float64(900), "txPkts": float64(3), "rxBytes": float64(100), "rxPkts": float64(2),
		}},
	})
	if len(client) != 1 || client[0].SrcIP != "100.64.0.1" || client[0].DstIP != ExitInternetEndpoint {
		t.Fatalf("client exit row = %+v, want 100.64.0.1 -> %s", client, ExitInternetEndpoint)
	}
	if len(exitNode) != 1 || exitNode[0].SrcIP != ExitInternetEndpoint || exitNode[0].DstIP != "100.64.0.1" {
		t.Fatalf("exit node row = %+v, want %s -> 100.64.0.1", exitNode, ExitInternetEndpoint)
	}

	pairs, bandwidth, nodeBandwidth, stats := poller.aggregate(append(client, exitNode...))
	if len(pairs) != 1 {
		t.Fatalf("pairs = %+v, want one client/internet pair", pairs)
	}
	pair := pairs[0]
	if pair.SrcNodeID != "100.64.0.1" || pair.DstNodeID != ExitInternetEndpoint || pair.TrafficType != "exit" || pair.TxBytes != 100 || pair.RxBytes != 900 {
		t.Fatalf("pair = %+v, want 100 B up and 900 B down", pair)
	}
	if len(stats) != 1 || stats[0].ExitBytes != 1000 {
		t.Fatalf("traffic stats = %+v, want 1000 exit bytes", stats)
	}
	if len(bandwidth) != 1 || bandwidth[0].TxBytes != 1000 {
		t.Fatalf("bandwidth = %+v, want 1000 bytes", bandwidth)
	}
	var clientTx, clientRx int64
	for _, nb := range nodeBandwidth {
		if nb.NodeID == "100.64.0.1" {
			clientTx += nb.TxBytes
			clientRx += nb.RxBytes
		}
	}
	if clientTx != 100 || clientRx != 900 {
		t.Fatalf("client bandwidth tx=%d rx=%d, want 100/900", clientTx, clientRx)
	}
}

func TestTypedExitTrafficWithBlankPublicSideIsKept(t *testing.T) {
	poller := NewPoller(nil, nil, DefaultPollerConfig())
	at := time.Date(2026, 5, 8, 13, 45, 0, 0, time.UTC)
	flows := poller.convertTailscaleLog(tailscale.NetworkFlowLog{
		NodeID: "nExit", Start: at, Logged: at.Add(5 * time.Second),
		ExitTraffic: []tailscale.TrafficStats{
			{Dst: "[fd7a:115c:a1e0::1]:0", TxBytes: 900, TxPkts: 3, RxBytes: 100, RxPkts: 2},
			{TxBytes: 5, TxPkts: 1},
		},
	})
	if len(flows) != 1 || flows[0].SrcIP != ExitInternetEndpoint || flows[0].DstIP != "fd7a:115c:a1e0::1" || flows[0].TrafficType != "exit" {
		t.Fatalf("flows = %+v, want one internet -> client exit row", flows)
	}
}

// The stand-in applies only to exit traffic. A blank side on other types is
// still malformed.
func TestBlankEndpointsOutsideExitTrafficAreStillSkipped(t *testing.T) {
	poller := NewPoller(nil, nil, DefaultPollerConfig())
	flows := poller.convertMapLog(map[string]any{
		"nodeId": "nA", "start": "2026-05-08T13:45:00Z",
		"virtualTraffic": []any{map[string]any{"src": "100.64.0.1:1", "txBytes": float64(1), "rxBytes": float64(0), "txPkts": float64(1), "rxPkts": float64(0)}},
		"subnetTraffic":  []any{map[string]any{"dst": "10.0.0.1:443", "txBytes": float64(1), "rxBytes": float64(0), "txPkts": float64(1), "rxPkts": float64(0)}},
		"exitTraffic":    []any{map[string]any{"txBytes": float64(1), "rxBytes": float64(0), "txPkts": float64(1), "rxPkts": float64(0)}},
	})
	if len(flows) != 0 {
		t.Fatalf("flows = %+v, want none", flows)
	}
}
