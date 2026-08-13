package services

import "testing"

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
