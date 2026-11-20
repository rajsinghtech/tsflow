package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func clearAuditEnv(t *testing.T) {
	for _, k := range []string{"TSFLOW_AUDIT_ENABLED", "TSFLOW_AUDIT_DB_PATH", "TSFLOW_AUDIT_RETENTION", "TSFLOW_AUDIT_POLL_INTERVAL",
		"TSFLOW_AUDIT_BACKFILL", "TSFLOW_AUDIT_MAX_OBJECTS_PER_POLL", "TSFLOW_AUDIT_VIEWERS", "TSFLOW_AUDIT_BACKEND",
		"TSFLOW_AUDIT_BUCKET", "TSFLOW_AUDIT_PREFIX", "TSFLOW_AUDIT_REGION", "TSFLOW_AUDIT_ENDPOINT", "TSFLOW_AUDIT_AUTH",
		"TSFLOW_AUDIT_ROLE_ARN", "TSFLOW_AUDIT_WEB_IDENTITY_TOKEN_FILE"} {
		t.Setenv(k, "")
	}
}

func TestLoadAuditDefaults(t *testing.T) {
	clearAuditEnv(t)
	a, err := LoadAudit("/data/tsflow.db")
	if err != nil {
		t.Fatal(err)
	}
	if a.Enabled {
		t.Fatal("audit must be off by default")
	}
	if a.DBPath != "/data/audit.db" || a.Retention != 400*24*time.Hour || a.Backfill != 90*24*time.Hour || a.PollInterval != 2*time.Minute {
		t.Fatalf("defaults: %+v", a)
	}
	if len(a.Viewers) != 0 {
		t.Fatal("no viewers by default")
	}
}

func TestLoadAuditFromEnv(t *testing.T) {
	clearAuditEnv(t)
	t.Setenv("TSFLOW_AUDIT_ENABLED", "true")
	t.Setenv("TSFLOW_AUDIT_RETENTION", "90d")
	t.Setenv("TSFLOW_AUDIT_POLL_INTERVAL", "30s")
	t.Setenv("TSFLOW_AUDIT_VIEWERS", "ada@example.com, group:auditors")
	t.Setenv("TSFLOW_AUDIT_BACKEND", "GCS")
	t.Setenv("TSFLOW_AUDIT_BUCKET", "example-audit")
	a, err := LoadAudit("")
	if err != nil {
		t.Fatal(err)
	}
	if !a.Enabled || a.Retention != 90*24*time.Hour || a.PollInterval != 30*time.Second || len(a.Viewers) != 2 || a.Default.Backend != "gcs" {
		t.Fatalf("env: %+v", a)
	}
	for key, bad := range map[string]string{
		"TSFLOW_AUDIT_ENABLED":       "maybe",
		"TSFLOW_AUDIT_RETENTION":     "ten days",
		"TSFLOW_AUDIT_POLL_INTERVAL": "1s",
		"TSFLOW_AUDIT_BACKEND":       "ftp",
	} {
		t.Run(key, func(t *testing.T) {
			t.Setenv(key, bad)
			if _, err := LoadAudit(""); err == nil {
				t.Fatalf("%s=%s should fail", key, bad)
			}
		})
	}
}

func TestResolveAudit(t *testing.T) {
	def := AuditSource{Backend: AuditBackendGCS, Bucket: "shared-audit"}
	single := TailnetSpec{ID: "default"}.ResolveAudit(def, true)
	if single.Backend != AuditBackendGCS || single.Prefix != DefaultAuditPrefix || single.Auth != ObjectStoreAuthGCSADC {
		t.Fatalf("single tailnet: %+v", single)
	}
	if got := (TailnetSpec{ID: "b"}).ResolveAudit(def, false); got.Backend != AuditBackendOff {
		t.Fatalf("a tailnet without a block in a multi-tailnet file stays off: %+v", got)
	}
	block := TailnetSpec{ID: "c", Audit: &AuditSource{Prefix: "c/"}}.ResolveAudit(def, false)
	if block.Backend != AuditBackendGCS || block.Bucket != "shared-audit" || block.Prefix != "c/" {
		t.Fatalf("block inherits defaults: %+v", block)
	}
	if got := (TailnetSpec{ID: "d"}).ResolveAudit(AuditSource{}, true); got.Backend != AuditBackendOff {
		t.Fatalf("no default means off: %+v", got)
	}
}

func TestParseLongDuration(t *testing.T) {
	for raw, want := range map[string]time.Duration{"400d": 400 * 24 * time.Hour, "72h": 72 * time.Hour, "0": 0, "0d": 0} {
		got, err := ParseLongDuration(raw)
		if err != nil || got != want {
			t.Errorf("%s: %v %v", raw, got, err)
		}
	}
	if FormatLongDuration(400*24*time.Hour) != "400d" || FormatLongDuration(90*time.Minute) != "1h30m0s" {
		t.Error("FormatLongDuration")
	}
	for _, raw := range []string{"-1d", "xd", "-5h", "soon"} {
		if _, err := ParseLongDuration(raw); err == nil {
			t.Errorf("%s should fail", raw)
		}
	}
}

func TestTailnetsFileAuditBlock(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("ALPHA_KEY", "alpha-key")
	t.Setenv("TAILSCALE_TAILNET", "")
	t.Setenv("TAILSCALE_API_KEY", "")
	path := filepath.Join(dir, "tailnets.yaml")
	body := `
tailnets:
  - id: alpha
    tailnet: alpha.example.com
    api_key_env: ALPHA_KEY
    audit:
      backend: gcs
      bucket: example-audit-logs
      prefix: alpha/
  - id: beta
    tailnet: beta.example.com
    api_key_env: ALPHA_KEY
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := validConfig()
	cfg.TailnetsFile = path
	specs, err := cfg.ResolveTailnets()
	if err != nil {
		t.Fatal(err)
	}
	if specs[0].Audit == nil || specs[0].Audit.Bucket != "example-audit-logs" || specs[0].Audit.Prefix != "alpha/" || specs[1].Audit != nil {
		t.Fatalf("audit blocks: %+v %+v", specs[0].Audit, specs[1].Audit)
	}

	bad := strings.Replace(body, "prefix: alpha/", "prefx: alpha/", 1)
	if err := os.WriteFile(path, []byte(bad), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := cfg.ResolveTailnets(); err == nil || !strings.Contains(err.Error(), `unknown field "prefx"`) {
		t.Fatalf("unknown audit field: %v", err)
	}
	bad = strings.Replace(body, "backend: gcs", "backend: ftp", 1)
	if err := os.WriteFile(path, []byte(bad), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := cfg.ResolveTailnets(); err == nil {
		t.Fatal("unknown audit backend should fail")
	}
}
