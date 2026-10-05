package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rajsinghtech/tsflow/backend/internal/database"
)

func TestResolveTailnetsFromEnvIsDefault(t *testing.T) {
	cfg := validConfig()
	cfg.TailscaleAPIKey = "key"
	cfg.TailscaleTailnet = "example.com"
	cfg.TailscaleAPIURL = "https://api.tailscale.com"
	cfg.TailscaleOAuthScopes = []string{"all:read"}

	specs, err := cfg.ResolveTailnets()
	if err != nil {
		t.Fatal(err)
	}
	if len(specs) != 1 {
		t.Fatalf("specs = %d, want 1", len(specs))
	}
	got := specs[0]
	if got.ID != database.DefaultTailnetID || got.Name != "example.com" || got.APIKey != "key" || got.APIURL != cfg.TailscaleAPIURL {
		t.Fatalf("default spec = %+v", got)
	}
	if got.AuthMode != TailscaleAuthAPIKey || got.WIFClientID != "" || got.WIFIDToken != "" || got.WIFIDTokenFile != "" || got.WIFAudience != "" {
		t.Fatalf("default spec auth = %+v", got)
	}
	if got.S3Prefix != "" {
		t.Fatalf("env spec should inherit the process prefix, got %q", got.S3Prefix)
	}
	serviceCfg := got.ServiceConfig(cfg)
	if serviceCfg.TailscaleAPIKey != cfg.TailscaleAPIKey || serviceCfg.TailscaleTailnet != cfg.TailscaleTailnet || serviceCfg.TailscaleAPIURL != cfg.TailscaleAPIURL {
		t.Fatalf("service config = %+v, want the single-tailnet settings", serviceCfg)
	}
}

func TestResolveTailnetsFile(t *testing.T) {
	dir := t.TempDir()
	secretPath := filepath.Join(dir, "lab-secret")
	if err := os.WriteFile(secretPath, []byte(" lab-secret \n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEFAULT_TAILSCALE_API_KEY", "default-key")
	t.Setenv("LAB_OAUTH_CLIENT_ID", "lab-id")
	t.Setenv("TAILSCALE_TAILNET", "")
	t.Setenv("VITE_TAILSCALE_TAILNET", "")
	t.Setenv("TAILSCALE_API_KEY", "")
	t.Setenv("VITE_TAILSCALE_API_KEY", "")
	t.Setenv("TAILSCALE_OAUTH_CLIENT_ID", "")
	t.Setenv("VITE_TAILSCALE_OAUTH_CLIENT_ID", "")
	t.Setenv("TAILSCALE_OAUTH_CLIENT_SECRET", "")
	t.Setenv("VITE_TAILSCALE_OAUTH_CLIENT_SECRET", "")

	yamlPath := filepath.Join(dir, "tailnets.yaml")
	yamlBody := `
tailnets:
  - id: default
    tailnet: example.com
    api_key_env: DEFAULT_TAILSCALE_API_KEY
    s3_prefix: network/default/
  - id: lab
    tailnet: lab.example.com
    oauth_client_id_env: LAB_OAUTH_CLIENT_ID
    oauth_client_secret_file: ` + secretPath + `
    oauth_scopes:
      - all:read
      - devices:read
    s3_prefix: lab/network/
`
	if err := os.WriteFile(yamlPath, []byte(yamlBody), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg := validConfig()
	cfg.TailnetsFile = yamlPath
	cfg.TailscaleOAuthScopes = []string{"all:read"}
	specs, err := cfg.ResolveTailnets()
	if err != nil {
		t.Fatal(err)
	}
	if len(specs) != 2 || specs[0].ID != "default" || specs[1].ID != "lab" {
		t.Fatalf("specs = %+v", specs)
	}
	if specs[0].APIKey != "default-key" || specs[0].S3Prefix != "network/default/" || specs[0].Name != "example.com" {
		t.Fatalf("default entry = %+v", specs[0])
	}
	if specs[1].OAuthClientID != "lab-id" || specs[1].OAuthClientSecret != "lab-secret" || specs[1].S3Prefix != "lab/network/" {
		t.Fatalf("lab entry = %+v", specs[1])
	}
	if strings.Join(specs[1].OAuthScopes, ",") != "all:read,devices:read" {
		t.Fatalf("lab scopes = %v", specs[1].OAuthScopes)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("file config should validate: %v", err)
	}

	jsonPath := filepath.Join(dir, "tailnets.json")
	jsonBody := `{"tailnets":[{"id":"west","tailnet":"west.example.com","api_key_file":"` + secretPath + `","oauth_scopes":"all:read"}]}`
	if err := os.WriteFile(jsonPath, []byte(jsonBody), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg.TailnetsFile = jsonPath
	specs, err = cfg.ResolveTailnets()
	if err != nil {
		t.Fatal(err)
	}
	if len(specs) != 1 || specs[0].ID != "west" || specs[0].APIKey != "lab-secret" {
		t.Fatalf("json specs = %+v", specs)
	}
	if strings.Join(specs[0].OAuthScopes, ",") != "all:read" {
		t.Fatalf("json scopes = %v", specs[0].OAuthScopes)
	}
}

func TestResolveTailnetsRejectsBadFiles(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("OTHER_TAILSCALE_API_KEY", "key")
	t.Setenv("TAILSCALE_TAILNET", "")
	t.Setenv("TAILSCALE_API_KEY", "")
	t.Setenv("TAILSCALE_OAUTH_CLIENT_ID", "")
	t.Setenv("TAILSCALE_OAUTH_CLIENT_SECRET", "")
	t.Setenv("VITE_TAILSCALE_TAILNET", "")
	t.Setenv("VITE_TAILSCALE_API_KEY", "")
	t.Setenv("VITE_TAILSCALE_OAUTH_CLIENT_ID", "")
	t.Setenv("VITE_TAILSCALE_OAUTH_CLIENT_SECRET", "")

	cases := []struct {
		name string
		body string
		want string
	}{
		{name: "duplicate ids", body: "tailnets:\n- {id: a, tailnet: a.example, api_key_env: OTHER_TAILSCALE_API_KEY}\n- {id: a, tailnet: b.example, api_key_env: OTHER_TAILSCALE_API_KEY}\n", want: "duplicate tailnet id"},
		{name: "missing creds", body: "tailnets:\n- {id: a, tailnet: a.example}\n", want: "missing credentials"},
		{name: "missing id", body: "tailnets:\n- {tailnet: a.example, api_key_env: OTHER_TAILSCALE_API_KEY}\n", want: "missing id"},
		{name: "missing tailnet", body: "tailnets:\n- {id: a, api_key_env: OTHER_TAILSCALE_API_KEY}\n", want: "missing tailnet"},
		{name: "env and file", body: "tailnets:\n- {id: a, tailnet: a.example, api_key_env: OTHER_TAILSCALE_API_KEY, api_key_file: /tmp/key}\n", want: "both an environment variable and a file"},
		{name: "inline secret", body: "tailnets:\n- {id: a, tailnet: a.example, api_key: secret}\n", want: "unknown field"},
		{name: "empty list", body: "tailnets: []\n", want: "does not list any tailnets"},
		{name: "empty env", body: "tailnets:\n- {id: a, tailnet: a.example, api_key_env: UNSET_TAILSCALE_API_KEY}\n", want: "empty or unset"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(dir, tc.name+".yaml")
			if err := os.WriteFile(path, []byte(tc.body), 0o600); err != nil {
				t.Fatal(err)
			}
			cfg := validConfig()
			cfg.TailnetsFile = path
			_, err := cfg.ResolveTailnets()
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestValidateRejectsEnvAndFileTogether(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tailnets.yaml")
	if err := os.WriteFile(path, []byte("tailnets:\n- {id: a, tailnet: a.example, api_key_env: OTHER_TAILSCALE_API_KEY}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TAILSCALE_API_KEY", "legacy-key")
	cfg := validConfig()
	cfg.TailscaleAPIKey = "legacy-key"
	cfg.TailnetsFile = path
	err := cfg.Validate()
	if err == nil || !strings.Contains(err.Error(), "TSFLOW_TAILNETS_FILE cannot be combined") {
		t.Fatalf("Validate() = %v", err)
	}
}

func TestValidateRejectsFunnelForMultipleTailnets(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tailnets.yaml")
	body := "tailnets:\n- {id: a, tailnet: a.example, api_key_env: FUNNEL_KEY_A}\n- {id: b, tailnet: b.example, api_key_env: FUNNEL_KEY_B}\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FUNNEL_KEY_A", "a")
	t.Setenv("FUNNEL_KEY_B", "b")
	t.Setenv("TAILSCALE_TAILNET", "")
	t.Setenv("TAILSCALE_API_KEY", "")
	t.Setenv("TAILSCALE_OAUTH_CLIENT_ID", "")
	t.Setenv("TAILSCALE_OAUTH_CLIENT_SECRET", "")
	cfg := validConfig()
	cfg.TailnetsFile = path
	cfg.TsnetFunnel = true
	err := cfg.Validate()
	if err == nil || !strings.Contains(err.Error(), "TSFLOW_FUNNEL") {
		t.Fatalf("Validate() = %v", err)
	}

	cfg.TsnetFunnel = false
	if err := cfg.Validate(); err != nil {
		t.Fatalf("multiple tailnets without funnel should validate: %v", err)
	}

	single := validConfig()
	single.TailscaleAPIKey = "key"
	single.TsnetFunnel = true
	single.TsnetServe = true
	single.TailscaleOAuthClientID = "id"
	single.TailscaleOAuthClientSecret = "secret"
	if err := single.Validate(); err != nil {
		t.Fatalf("single tailnet funnel should stay valid: %v", err)
	}
}
