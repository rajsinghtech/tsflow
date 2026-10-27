package config_test

import (
	"testing"

	"github.com/rajsinghtech/tsflow/backend/internal/config"
)

// TestMetricsOffByDefault keeps /metrics opt-in: an install that never sets
// TSFLOW_METRICS_ADDR gets no metrics listener.
func TestMetricsOffByDefault(t *testing.T) {
	clearCompatEnv(t)
	t.Setenv("TSFLOW_METRICS_ADDR", "")
	t.Setenv("TSFLOW_METRICS_TRAFFIC", "")
	t.Setenv("TSFLOW_METRICS_TOP_TALKERS", "")
	t.Setenv("TAILSCALE_API_KEY", "k")
	cfg := config.Load()
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	if cfg.MetricsEnabled() || cfg.MetricsAddr != "" || cfg.MetricsListenAddr() != "" || cfg.MetricsSharesMainPort() {
		t.Fatalf("metrics on by default: addr=%q", cfg.MetricsAddr)
	}
	if cfg.MetricsTraffic || cfg.MetricsTopTalkerCount() != 0 {
		t.Fatalf("traffic metrics on by default: traffic=%v top=%d", cfg.MetricsTraffic, cfg.MetricsTopTalkerCount())
	}
}

func TestTrafficMetricsSettings(t *testing.T) {
	cases := []struct {
		addr, traffic, top string
		wantTraffic        bool
		wantTop            int
		wantErr            bool
	}{
		{addr: ":9090"},
		{addr: ":9090", traffic: "true", wantTraffic: true},
		{addr: ":9090", traffic: "1", wantTraffic: true},
		{addr: ":9090", traffic: "false"},
		{addr: ":9090", top: "0"},
		{addr: ":9090", top: "25", wantTop: 25},
		{addr: ":9090", top: "100", wantTop: 100},
		{addr: ":9090", top: "101", wantErr: true},
		{addr: ":9090", top: "-1", wantErr: true},
		{addr: ":9090", top: "lots", wantErr: true},
		{traffic: "true", wantErr: true},
		{top: "10", wantErr: true},
		{top: "0"},
		{traffic: "false"},
	}
	for _, tc := range cases {
		t.Run(tc.addr+"/"+tc.traffic+"/"+tc.top, func(t *testing.T) {
			clearCompatEnv(t)
			t.Setenv("TAILSCALE_API_KEY", "k")
			t.Setenv("TSFLOW_METRICS_ADDR", tc.addr)
			t.Setenv("TSFLOW_METRICS_TRAFFIC", tc.traffic)
			t.Setenv("TSFLOW_METRICS_TOP_TALKERS", tc.top)
			cfg := config.Load()
			err := cfg.Validate()
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected an error")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if cfg.MetricsTraffic != tc.wantTraffic || cfg.MetricsTopTalkerCount() != tc.wantTop {
				t.Fatalf("traffic=%v top=%d, want %v %d", cfg.MetricsTraffic, cfg.MetricsTopTalkerCount(), tc.wantTraffic, tc.wantTop)
			}
		})
	}
}

func TestMetricsAddr(t *testing.T) {
	cases := []struct {
		addr, port string
		serve      bool
		health     string
		listen     string
		shares     bool
		wantErr    bool
	}{
		{addr: ":9090", listen: ":9090"},
		{addr: "9090", listen: ":9090"},
		{addr: "127.0.0.1:9090", listen: "127.0.0.1:9090"},
		{addr: " :9090 ", listen: ":9090"},
		{addr: ":8080", listen: ":8080", shares: true},
		{addr: "0.0.0.0:8080", listen: "0.0.0.0:8080", shares: true},
		{addr: "127.0.0.1:8080", listen: "127.0.0.1:8080"},
		{addr: ":9000", port: "9000", listen: ":9000", shares: true},
		{addr: ":9090", serve: true, listen: ":9090"},
		{addr: ":8080", serve: true, listen: ":8080"},
		{addr: ":9090", serve: true, health: "9090", wantErr: true},
		{addr: "localhost", wantErr: true},
		{addr: ":0", wantErr: true},
		{addr: ":70000", wantErr: true},
		{addr: ":http", wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.addr, func(t *testing.T) {
			clearCompatEnv(t)
			t.Setenv("TSFLOW_METRICS_ADDR", tc.addr)
			t.Setenv("TSFLOW_HEALTH_PORT", tc.health)
			if tc.port != "" {
				t.Setenv("PORT", tc.port)
			}
			if tc.serve {
				t.Setenv("TSFLOW_SERVE", "true")
				t.Setenv("TAILSCALE_OAUTH_CLIENT_ID", "id")
				t.Setenv("TAILSCALE_OAUTH_CLIENT_SECRET", "secret")
			} else {
				t.Setenv("TAILSCALE_API_KEY", "k")
			}
			cfg := config.Load()
			err := cfg.Validate()
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected an error")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !cfg.MetricsEnabled() || cfg.MetricsListenAddr() != tc.listen || cfg.MetricsSharesMainPort() != tc.shares {
				t.Fatalf("listen=%q shares=%v, want %q %v", cfg.MetricsListenAddr(), cfg.MetricsSharesMainPort(), tc.listen, tc.shares)
			}
		})
	}
}
