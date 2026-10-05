package config

import (
	"strings"
	"testing"
)

func accessBase() *Config {
	cfg := validConfig()
	cfg.TailscaleAPIKey = "key"
	return cfg
}

func TestAccessDisabledByDefault(t *testing.T) {
	cfg := accessBase()
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	if cfg.Access.Enabled || cfg.Access.Mode != "" || cfg.Access.Autoscope != AccessAutoscopeOff {
		t.Fatalf("access = %+v", cfg.Access)
	}
}

func TestAccessValidation(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Config)
		want   string
		wantOK bool
		check  func(*testing.T, *Config)
	}{
		{
			name: "capability without identity source",
			mutate: func(c *Config) {
				c.Access.Capability = "example.com/cap/tsflow"
			},
			want: "TSFLOW_SERVE or TSFLOW_ACCESS_MODE=header",
		},
		{
			name: "header mode without trusted proxies",
			mutate: func(c *Config) {
				c.Access.Mode = "header"
				c.Access.Capability = "example.com/cap/tsflow"
			},
			want: "TSFLOW_ACCESS_TRUSTED_PROXIES",
		},
		{
			name: "tsnet mode without serve",
			mutate: func(c *Config) {
				c.Access.Mode = "tsnet"
				c.Access.Capability = "example.com/cap/tsflow"
			},
			want: "TSFLOW_SERVE=true",
		},
		{
			name: "groups header without grants",
			mutate: func(c *Config) {
				c.Access.Mode = "header"
				c.Access.TrustedProxies = "127.0.0.1/32"
				c.Access.GroupsHeader = "X-Tsflow-Groups"
			},
			want: "TSFLOW_ACCESS_GROUP_GRANTS",
		},
		{
			name: "both grant sources",
			mutate: func(c *Config) {
				c.Access.Mode = "header"
				c.Access.TrustedProxies = "10.0.0.0/8"
				c.Access.GroupsHeader = "X-Tsflow-Groups"
				c.Access.GroupGrantsRaw = `{"group:eng":{}}`
				c.Access.GroupGrantsFile = "grants.json"
			},
			want: "only one of",
		},
		{
			name: "role field rejected",
			mutate: func(c *Config) {
				c.TsnetServe = true
				c.Access.Capability = "example.com/cap/tsflow"
				c.Access.GroupGrantsRaw = `{"group:eng":{"tailnets":["default"],"role":"viewer"}}`
			},
			want: "unknown field",
		},
		{
			name: "bad capability name",
			mutate: func(c *Config) {
				c.TsnetServe = true
				c.Access.Capability = "not a capability"
			},
			want: "example.com/cap/tsflow",
		},
		{
			name: "autoscope groups needs a map",
			mutate: func(c *Config) {
				c.TsnetServe = true
				c.Access.Capability = "example.com/cap/tsflow"
				c.Access.Autoscope = "groups"
			},
			want: "TSFLOW_ACCESS_AUTOSCOPE=groups",
		},
		{
			name: "groups header on tsnet",
			mutate: func(c *Config) {
				c.TsnetServe = true
				c.Access.Capability = "example.com/cap/tsflow"
				c.Access.GroupsHeader = "X-Tsflow-Groups"
				c.Access.GroupGrantsRaw = `{"group:eng":{}}`
			},
			want: "TSFLOW_ACCESS_GROUPS_HEADER is only valid",
		},
		{
			name: "header with capability and cidr",
			mutate: func(c *Config) {
				c.Access.Mode = "proxy"
				c.Access.Capability = "example.com/cap/tsflow"
				c.Access.TrustedProxies = "10.1.2.3/8, 127.0.0.1"
			},
			wantOK: true,
			check: func(t *testing.T, c *Config) {
				if !c.Access.Enabled || c.Access.Mode != AccessModeHeader {
					t.Fatalf("access = %+v", c.Access)
				}
				if len(c.Access.TrustedPrefixes) != 2 {
					t.Fatalf("prefixes = %v", c.Access.TrustedPrefixes)
				}
				if c.Access.CapabilityHeader != DefaultCapabilityHeader {
					t.Fatalf("header = %q", c.Access.CapabilityHeader)
				}
			},
		},
		{
			name: "tsnet inferred from serve",
			mutate: func(c *Config) {
				c.TsnetServe = true
				c.TailscaleOAuthClientID = "id"
				c.TailscaleOAuthClientSecret = "secret"
				c.Access.Capability = "example.com/cap/tsflow"
				c.Access.Autoscope = "user"
			},
			wantOK: true,
			check: func(t *testing.T, c *Config) {
				if c.Access.Mode != AccessModeTsnet || c.Access.Autoscope != AccessAutoscopeUser {
					t.Fatalf("access = %+v", c.Access)
				}
			},
		},
		{
			name: "group map union fields",
			mutate: func(c *Config) {
				c.Access.Mode = "header"
				c.Access.TrustedProxies = "10.0.0.0/8"
				c.Access.GroupsHeader = "X-Tsflow-Groups"
				c.Access.GroupGrantsRaw = `{"group:eng":{"tailnets":["default","lab"]},"group:ops":{},"ops@example.com":{"tailnets":["*"]}}`
				c.Access.Autoscope = "groups"
			},
			wantOK: true,
			check: func(t *testing.T, c *Config) {
				eng := c.Access.GroupGrants["group:eng"]
				if eng.All || len(eng.Tailnets) != 2 {
					t.Fatalf("eng = %+v", eng)
				}
				if !c.Access.GroupGrants["group:ops"].All || !c.Access.GroupGrants["ops@example.com"].All {
					t.Fatalf("grants = %+v", c.Access.GroupGrants)
				}
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := accessBase()
			tc.mutate(cfg)
			err := cfg.Validate()
			if tc.wantOK {
				if err != nil {
					t.Fatal(err)
				}
				if tc.check != nil {
					tc.check(t, cfg)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Validate() = %v, want mention %q", err, tc.want)
			}
		})
	}
}

func TestParseGrant(t *testing.T) {
	all, err := ParseGrant([]byte(`{}`))
	if err != nil || !all.All {
		t.Fatalf("empty object = %+v %v", all, err)
	}
	star, err := ParseGrant([]byte(`{"tailnets":["default","*"]}`))
	if err != nil || !star.All {
		t.Fatalf("star = %+v %v", star, err)
	}
	listed, err := ParseGrant([]byte(`{"tailnets":["default","lab","default"]}`))
	if err != nil || listed.All || strings.Join(listed.Tailnets, ",") != "default,lab" {
		t.Fatalf("listed = %+v %v", listed, err)
	}
	none, err := ParseGrant([]byte(`{"tailnets":[]}`))
	if err != nil || none.All || len(none.Tailnets) != 0 {
		t.Fatalf("empty list = %+v %v", none, err)
	}
	if _, err := ParseGrant([]byte(`{"role":"viewer"}`)); err == nil {
		t.Fatal("role should be rejected")
	}
}

func TestLoadAccessFromEnv(t *testing.T) {
	for _, key := range []string{
		"TAILSCALE_API_KEY", "TSFLOW_ACCESS_CAPABILITY", "TSFLOW_ACCESS_MODE",
		"TSFLOW_ACCESS_TRUSTED_PROXIES", "TSFLOW_ACCESS_GROUPS_HEADER",
		"TSFLOW_ACCESS_GROUP_GRANTS", "TSFLOW_ACCESS_AUTOSCOPE", "TSFLOW_LOG_LEVEL",
		"TSFLOW_SERVE", "TSFLOW_FLOW_BACKEND",
	} {
		t.Setenv(key, "")
	}
	t.Setenv("TAILSCALE_API_KEY", "key")
	t.Setenv("TSFLOW_ACCESS_MODE", "header")
	t.Setenv("TSFLOW_ACCESS_CAPABILITY", " example.com/cap/tsflow ")
	t.Setenv("TSFLOW_ACCESS_TRUSTED_PROXIES", "127.0.0.1/32")
	t.Setenv("TSFLOW_ACCESS_GROUPS_HEADER", "X-Tsflow-Groups")
	t.Setenv("TSFLOW_ACCESS_GROUP_GRANTS", `{"group:eng":{"tailnets":["lab"]}}`)
	t.Setenv("TSFLOW_ACCESS_AUTOSCOPE", "groups")
	t.Setenv("TSFLOW_LOG_LEVEL", "debug")

	cfg := Load()
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	if !cfg.Access.Enabled || !cfg.Access.Debug || cfg.Access.Autoscope != AccessAutoscopeGroups {
		t.Fatalf("access = %+v", cfg.Access)
	}
	grant := cfg.Access.GroupGrants["group:eng"]
	if grant.All || len(grant.Tailnets) != 1 || grant.Tailnets[0] != "lab" {
		t.Fatalf("grant = %+v", grant)
	}
}
