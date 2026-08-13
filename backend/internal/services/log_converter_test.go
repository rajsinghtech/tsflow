package services

import (
	"testing"
	"time"

	tailscale "tailscale.com/client/tailscale/v2"
)

func TestExtractEndpointHandlesIPv4AndIPv6(t *testing.T) {
	tests := []struct {
		name string
		addr string
		ip   string
		port int
	}{
		{name: "ipv4 with port", addr: "192.0.2.10:443", ip: "192.0.2.10", port: 443},
		{name: "bracketed ipv6 with port", addr: "[2001:db8::10]:8443", ip: "2001:db8::10", port: 8443},
		{name: "bare ipv6", addr: "fd7a:115c:a1e0::1", ip: "fd7a:115c:a1e0::1", port: 0},
		{name: "opaque node id", addr: "n5ZfK4a5pz11CNTRL", ip: "n5ZfK4a5pz11CNTRL", port: 0},
		{name: "hostname with port", addr: "host.example:443", ip: "host.example", port: 443},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := extractIP(tt.addr); got != tt.ip {
				t.Fatalf("extractIP(%q) = %q, want %q", tt.addr, got, tt.ip)
			}
			if got := extractPort(tt.addr); got != tt.port {
				t.Fatalf("extractPort(%q) = %d, want %d", tt.addr, got, tt.port)
			}
		})
	}
}

func TestExtractEndpointRejectsMalformedPortsWithoutTruncatingHosts(t *testing.T) {
	tests := []struct {
		addr string
		ip   string
	}{
		{addr: "192.0.2.10:not-a-port", ip: "192.0.2.10"},
		{addr: "192.0.2.10:65536", ip: "192.0.2.10"},
		{addr: "[2001:db8::10]:70000", ip: "2001:db8::10"},
		{addr: "[2001:db8::10]bad", ip: "[2001:db8::10]bad"},
	}

	for _, tt := range tests {
		t.Run(tt.addr, func(t *testing.T) {
			if got := extractIP(tt.addr); got != tt.ip {
				t.Fatalf("extractIP(%q) = %q, want %q", tt.addr, got, tt.ip)
			}
			if got := extractPort(tt.addr); got != 0 {
				t.Fatalf("extractPort(%q) = %d, want 0", tt.addr, got)
			}
		})
	}
}

func TestConvertMapLogSkipsRowsWithMissingEndpoints(t *testing.T) {
	poller := NewPoller(nil, nil, DefaultPollerConfig())
	logMap := map[string]any{
		"nodeId": "node-a",
		"start":  "2026-05-08T13:45:00Z",
		"virtualTraffic": []any{
			map[string]any{
				"proto":   float64(6),
				"src":     "",
				"dst":     "100.64.0.2:443",
				"txBytes": float64(10),
			},
			map[string]any{
				"proto":   float64(6),
				"src":     "100.64.0.1:1234",
				"dst":     "100.64.0.2:443",
				"txBytes": float64(20),
			},
		},
	}

	flows := poller.convertMapLog(logMap)
	if len(flows) != 1 {
		t.Fatalf("converted %d flows, want one valid flow", len(flows))
	}
	if flows[0].SrcIP == "" || flows[0].DstIP == "" {
		t.Fatalf("converted flow contains blank endpoint: %+v", flows[0])
	}
}

func TestConvertTailscaleLogSkipsRowsWithMissingEndpoints(t *testing.T) {
	poller := NewPoller(nil, nil, DefaultPollerConfig())
	flows := poller.convertTailscaleLog(tailscale.NetworkFlowLog{
		Start: time.Date(2026, 5, 8, 13, 45, 0, 0, time.UTC),
		VirtualTraffic: []tailscale.TrafficStats{
			{Proto: 6, Dst: "100.64.0.2:443", TxBytes: 10},
			{Proto: 6, Src: "100.64.0.1:1234", Dst: "100.64.0.2:443", TxBytes: 20},
		},
	})
	if len(flows) != 1 {
		t.Fatalf("converted %d flows, want one valid flow", len(flows))
	}
}
