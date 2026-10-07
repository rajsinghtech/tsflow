package config

import (
	"slices"
	"testing"
)

func TestClientIPTrustedProxies(t *testing.T) {
	tests := []struct {
		name    string
		cfg     Config
		want    []string
		wantErr bool
	}{
		{name: "default trusts none", cfg: Config{}},
		{name: "explicit list", cfg: Config{TrustedProxies: "10.0.0.0/8, 192.0.2.1 ,fd00::/8"}, want: []string{"10.0.0.0/8", "192.0.2.1/32", "fd00::/8"}},
		{name: "invalid entry", cfg: Config{TrustedProxies: "10.0.0.0/8,proxy.example.com"}, wantErr: true},
		{
			name: "header access proxy is reused",
			cfg:  Config{Access: Access{Enabled: true, Mode: AccessModeHeader, TrustedProxies: "127.0.0.1"}},
			want: []string{"127.0.0.1/32"},
		},
		{
			name: "explicit list wins over header access proxy",
			cfg:  Config{TrustedProxies: "10.0.0.1", Access: Access{Enabled: true, Mode: AccessModeHeader, TrustedProxies: "127.0.0.1"}},
			want: []string{"10.0.0.1/32"},
		},
		{
			name: "access proxy ignored outside header mode",
			cfg:  Config{Access: Access{Enabled: true, Mode: AccessModeTsnet, TrustedProxies: "127.0.0.1"}},
		},
	}
	for _, tt := range tests {
		got, err := tt.cfg.ClientIPTrustedProxies()
		if (err != nil) != tt.wantErr {
			t.Fatalf("%s: err=%v, wantErr=%v", tt.name, err, tt.wantErr)
		}
		if !slices.Equal(got, tt.want) {
			t.Fatalf("%s: got %v, want %v", tt.name, got, tt.want)
		}
	}
}
