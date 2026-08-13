package config

import (
	"strings"
	"testing"
)

func validConfig() *Config {
	return &Config{TailscaleAPIURL: "https://api.tailscale.com", Port: "8080"}
}

func objectStoreConfig(c *Config) {
	c.FlowObjectStoreBucket = "flows"
	c.FlowObjectStoreEndpoint = "http://object-store.test"
	c.FlowObjectStoreAccessKey = "access"
	c.FlowObjectStoreSecretKey = "secret"
}

func TestValidateExplicitBackends(t *testing.T) {
	t.Run("api requires API credentials", func(t *testing.T) {
		cfg := validConfig()
		cfg.FlowBackend = "api"
		objectStoreConfig(cfg)
		if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "api flow backend") {
			t.Fatalf("expected API credential error, got %v", err)
		}
	})

	t.Run("s3 requires object store credentials", func(t *testing.T) {
		cfg := validConfig()
		cfg.FlowBackend = "s3"
		cfg.TailscaleAPIKey = "api-key"
		if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "s3 flow backend") {
			t.Fatalf("expected object-store credential error, got %v", err)
		}
	})

	t.Run("valid explicit backends", func(t *testing.T) {
		apiCfg := validConfig()
		apiCfg.FlowBackend = "api"
		apiCfg.TailscaleAPIKey = "api-key"
		if err := apiCfg.Validate(); err != nil {
			t.Fatalf("API backend should validate: %v", err)
		}

		s3Cfg := validConfig()
		s3Cfg.FlowBackend = "s3"
		objectStoreConfig(s3Cfg)
		if err := s3Cfg.Validate(); err != nil {
			t.Fatalf("S3 backend should validate: %v", err)
		}
	})
}

func TestValidateUnspecifiedBackendAutoDetection(t *testing.T) {
	t.Run("object store credentials select S3", func(t *testing.T) {
		cfg := validConfig()
		objectStoreConfig(cfg)
		if err := cfg.Validate(); err != nil {
			t.Fatalf("expected S3 auto-detection: %v", err)
		}
	})

	t.Run("API credentials select API", func(t *testing.T) {
		cfg := validConfig()
		cfg.TailscaleAPIKey = "api-key"
		if err := cfg.Validate(); err != nil {
			t.Fatalf("expected API auto-detection: %v", err)
		}
	})
}

func TestLoadParsesValidatedEnvironmentValues(t *testing.T) {
	for _, key := range []string{
		"TAILSCALE_API_KEY", "VITE_TAILSCALE_API_KEY", "TAILSCALE_API_URL", "VITE_TAILSCALE_API_URL",
		"PORT", "VITE_PORT", "TSFLOW_SERVE", "TSFLOW_FUNNEL", "TSFLOW_S3_PATH_STYLE",
		"TSFLOW_S3_MAX_OBJECTS_PER_POLL", "TAILSCALE_OAUTH_SCOPES", "TSFLOW_TAGS",
		"TAILSCALE_OAUTH_CLIENT_ID", "TAILSCALE_OAUTH_CLIENT_SECRET",
		"TSFLOW_FLOW_BACKEND", "VITE_TSFLOW_FLOW_BACKEND", "TSFLOW_S3_BUCKET", "TSFLOW_S3_PREFIX",
		"TSFLOW_S3_ENDPOINT", "TSFLOW_S3_REGION", "TSFLOW_S3_ACCESS_KEY_ID", "TSFLOW_S3_SECRET_ACCESS_KEY",
		"TAILSCALE_LOGS_S3_BUCKET", "TAILSCALE_LOGS_S3_PREFIX", "TAILSCALE_LOGS_S3_ENDPOINT",
		"TAILSCALE_LOGS_S3_ACCESS_KEY", "TAILSCALE_LOGS_S3_SECRET_KEY", "AWS_ENDPOINT_URL", "AWS_REGION",
		"AWS_DEFAULT_REGION", "AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY",
	} {
		t.Setenv(key, "")
	}
	t.Setenv("TAILSCALE_API_KEY", "key")
	t.Setenv("TSFLOW_FLOW_BACKEND", " api ")
	t.Setenv("TAILSCALE_OAUTH_CLIENT_ID", "client-id")
	t.Setenv("TAILSCALE_OAUTH_CLIENT_SECRET", "client-secret")
	t.Setenv("PORT", "9090")
	t.Setenv("TSFLOW_SERVE", "TrUe")
	t.Setenv("TSFLOW_FUNNEL", "not-a-bool")
	t.Setenv("TSFLOW_S3_PATH_STYLE", "false")
	t.Setenv("TSFLOW_S3_MAX_OBJECTS_PER_POLL", "42")
	t.Setenv("TAILSCALE_OAUTH_SCOPES", " all:read, , devices:read ")
	t.Setenv("TSFLOW_TAGS", " tag:one, ,tag:two ")

	cfg := Load()
	if cfg.FlowBackend != "api" {
		t.Fatalf("flow backend = %q, want api", cfg.FlowBackend)
	}
	if cfg.Port != "9090" || !cfg.TsnetServe || cfg.TsnetFunnel {
		t.Fatalf("unexpected boolean/port parsing: %+v", cfg)
	}
	if cfg.FlowObjectStorePathStyle || cfg.FlowObjectStoreMaxObjects != 42 {
		t.Fatalf("unexpected object-store parsing: %+v", cfg)
	}
	if got, want := strings.Join(cfg.TailscaleOAuthScopes, "|"), "all:read|devices:read"; got != want {
		t.Fatalf("scopes = %q, want %q", got, want)
	}
	if got, want := strings.Join(cfg.TsnetTags, "|"), "tag:one|tag:two"; got != want {
		t.Fatalf("tags = %q, want %q", got, want)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("loaded config should validate: %v", err)
	}
}

func TestValidateURLAndPort(t *testing.T) {
	for _, tc := range []struct {
		name string
		url  string
		port string
	}{
		{name: "missing scheme", url: "api.tailscale.com", port: "8080"},
		{name: "unsupported scheme", url: "ftp://api.tailscale.com", port: "8080"},
		{name: "bad port", url: "https://api.tailscale.com", port: "70000"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := validConfig()
			cfg.TailscaleAPIKey = "key"
			cfg.TailscaleAPIURL = tc.url
			cfg.Port = tc.port
			if err := cfg.Validate(); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}
