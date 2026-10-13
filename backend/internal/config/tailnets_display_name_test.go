package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func resolveFile(t *testing.T, name, body string) ([]TailnetSpec, error) {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := validConfig()
	cfg.TailnetsFile = path
	return cfg.ResolveTailnets()
}

func TestTailnetDisplayNameIsOptional(t *testing.T) {
	clearSingleTailnetEnv(t)
	t.Setenv("HOME_TAILSCALE_API_KEY", "key")

	specs, err := resolveFile(t, "tailnets.yaml", `
tailnets:
  - id: home
    tailnet: home.example.com
    api_key_env: HOME_TAILSCALE_API_KEY
    display_name: " Home "
  - id: lab
    tailnet: lab.example.com
    api_key_env: HOME_TAILSCALE_API_KEY
    displayName: Lab
  - id: cloud
    tailnet: cloud.example.com
    api_key_env: HOME_TAILSCALE_API_KEY
  - id: blank
    tailnet: blank.example.com
    api_key_env: HOME_TAILSCALE_API_KEY
    display_name: "   "
  - id: same
    tailnet: same.example.com
    api_key_env: HOME_TAILSCALE_API_KEY
    display_name: Same
    displayName: Same
  - id: block
    tailnet: block.example.com
    display_name: Block
    auth:
      type: api_key
      api_key_env: HOME_TAILSCALE_API_KEY
`)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string][2]string{
		"home":  {"home.example.com", "Home"},
		"lab":   {"lab.example.com", "Lab"},
		"cloud": {"cloud.example.com", ""},
		"blank": {"blank.example.com", ""},
		"same":  {"same.example.com", "Same"},
		"block": {"block.example.com", "Block"},
	}
	if len(specs) != len(want) {
		t.Fatalf("specs = %+v", specs)
	}
	for _, spec := range specs {
		w := want[spec.ID]
		if spec.Name != w[0] || spec.DisplayName != w[1] {
			t.Errorf("%s: name=%q displayName=%q, want %q %q", spec.ID, spec.Name, spec.DisplayName, w[0], w[1])
		}
		// The label never reaches the Tailscale client.
		if got := spec.ServiceConfig(nil).TailscaleTailnet; got != w[0] {
			t.Errorf("%s: service tailnet = %q, want %q", spec.ID, got, w[0])
		}
	}

	specs, err = resolveFile(t, "tailnets.json",
		`{"tailnets":[{"id":"a","tailnet":"a.example","api_key_env":"HOME_TAILSCALE_API_KEY","displayName":"Alpha"},{"id":"b","tailnet":"b.example","api_key_env":"HOME_TAILSCALE_API_KEY"}]}`)
	if err != nil {
		t.Fatal(err)
	}
	if specs[0].DisplayName != "Alpha" || specs[1].DisplayName != "" {
		t.Fatalf("json display names = %q %q", specs[0].DisplayName, specs[1].DisplayName)
	}
}

func TestTailnetDisplayNameRejectsBadValues(t *testing.T) {
	clearSingleTailnetEnv(t)
	t.Setenv("HOME_TAILSCALE_API_KEY", "key")
	cases := []struct{ name, body, want string }{
		{"conflict", "tailnets:\n- {id: a, tailnet: a.example, api_key_env: HOME_TAILSCALE_API_KEY, display_name: A, displayName: B}\n", "sets both display_name and displayName"},
		{"not a string", "tailnets:\n- {id: a, tailnet: a.example, api_key_env: HOME_TAILSCALE_API_KEY, display_name: [a]}\n", "display_name must be a string"},
		{"wrong case", "tailnets:\n- {id: a, tailnet: a.example, api_key_env: HOME_TAILSCALE_API_KEY, DisplayName: A}\n", "unknown field"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := resolveFile(t, "bad.yaml", tc.body)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestSingleTailnetHasNoDisplayName(t *testing.T) {
	cfg := validConfig()
	cfg.TailscaleAPIKey = "key"
	cfg.TailscaleTailnet = "example.com"
	specs, err := cfg.ResolveTailnets()
	if err != nil {
		t.Fatal(err)
	}
	if len(specs) != 1 || specs[0].DisplayName != "" || specs[0].Name != "example.com" {
		t.Fatalf("single tailnet spec = %+v", specs)
	}
}
