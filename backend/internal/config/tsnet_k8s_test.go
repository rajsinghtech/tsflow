package config

import (
	"strings"
	"testing"
)

func tsnetWIFEnv(t *testing.T) {
	t.Helper()
	clearAuthEnv(t)
	t.Setenv("TAILSCALE_OAUTH_CLIENT_ID", "client-id")
	t.Setenv("TAILSCALE_OAUTH_CLIENT_SECRET", "client-secret")
	t.Setenv("TS_CLIENT_ID", "tsnet-client")
	t.Setenv("TSFLOW_TAGS", "tag:tsflow")
	t.Setenv("TSFLOW_SERVE", "true")
	t.Setenv("TSFLOW_HEALTH_PORT", "")
	t.Setenv("TS_ID_TOKEN_FILE", "")
}

func TestTsnetIDTokenFile(t *testing.T) {
	t.Run("file alone", func(t *testing.T) {
		tsnetWIFEnv(t)
		t.Setenv("TS_ID_TOKEN_FILE", " /var/run/tsflow/token ")
		cfg := Load()
		if cfg.TsnetIDTokenFile != "/var/run/tsflow/token" || cfg.TsnetIDToken != "" {
			t.Fatalf("token file = %q token = %q", cfg.TsnetIDTokenFile, cfg.TsnetIDToken)
		}
		if err := cfg.Validate(); err != nil {
			t.Fatal(err)
		}
	})
	for name, other := range map[string][2]string{
		"with token":    {"TS_ID_TOKEN", "jwt"},
		"with audience": {"TS_AUDIENCE", "api.tailscale.com/tsnet-client"},
	} {
		t.Run(name, func(t *testing.T) {
			tsnetWIFEnv(t)
			t.Setenv("TS_ID_TOKEN_FILE", "/var/run/tsflow/token")
			t.Setenv(other[0], other[1])
			err := Load().Validate()
			if err == nil || !strings.Contains(err.Error(), "only one of TS_ID_TOKEN, TS_ID_TOKEN_FILE, or TS_AUDIENCE") {
				t.Fatalf("err = %v", err)
			}
		})
	}
	t.Run("none", func(t *testing.T) {
		tsnetWIFEnv(t)
		err := Load().Validate()
		if err == nil || !strings.Contains(err.Error(), "TS_ID_TOKEN_FILE") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("without client id", func(t *testing.T) {
		tsnetWIFEnv(t)
		t.Setenv("TS_CLIENT_ID", "")
		t.Setenv("TS_ID_TOKEN_FILE", "/var/run/tsflow/token")
		err := Load().Validate()
		if err == nil || !strings.Contains(err.Error(), "TS_ID_TOKEN_FILE requires TS_CLIENT_ID") {
			t.Fatalf("err = %v", err)
		}
	})
}

func TestTsnetHealthPort(t *testing.T) {
	tsnetWIFEnv(t)
	t.Setenv("TS_ID_TOKEN", "jwt")
	t.Setenv("TSFLOW_HEALTH_PORT", "8080")
	cfg := Load()
	if cfg.TsnetHealthPort != "8080" {
		t.Fatalf("health port = %q", cfg.TsnetHealthPort)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}

	for _, bad := range []string{"0", "70000", "http"} {
		t.Setenv("TSFLOW_HEALTH_PORT", bad)
		if err := Load().Validate(); err == nil || !strings.Contains(err.Error(), "TSFLOW_HEALTH_PORT must be") {
			t.Fatalf("port %q err = %v", bad, err)
		}
	}

	t.Setenv("TSFLOW_HEALTH_PORT", "8080")
	t.Setenv("TSFLOW_SERVE", "false")
	if err := Load().Validate(); err == nil || !strings.Contains(err.Error(), "only used with TSFLOW_SERVE=true") {
		t.Fatalf("plain mode err = %v", err)
	}
}
