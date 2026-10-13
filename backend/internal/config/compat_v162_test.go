package config_test

// Config compatibility with v1.6.2.
//
// v162Load and v162Validate below are a frozen copy of the v1.6.2 config
// loader and validator (backend/internal/config/config.go at the v1.6.2 tag)
// plus the backend and retention logic from its main.go. Every config shape
// v1.6.2 accepted is loaded by both the frozen copy and the current code, and
// the results must match: same accept/reject decision, same values for every
// field v1.6.2 had, same poller settings, one tailnet with id default, and
// every feature added since v1.6.2 left off.
//
// Do not edit the frozen copy to make a test pass. A difference here means an
// existing install would behave differently after upgrading.

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/rajsinghtech/tsflow/backend/internal/config"
	"github.com/rajsinghtech/tsflow/backend/internal/services"
)

type v162Config struct {
	TailscaleAPIKey            string
	TailscaleTailnet           string
	TailscaleAPIURL            string
	TailscaleOAuthClientID     string
	TailscaleOAuthClientSecret string
	TailscaleOAuthScopes       []string
	Port                       string
	Environment                string
	AllowedCORSOrigins         []string
	TsnetServe                 bool
	TsnetHostname              string
	TsnetTags                  []string
	TsnetFunnel                bool
	TsnetStateDir              string
	TsnetClientID              string
	TsnetIDToken               string
	TsnetAudience              string
	FlowBackend                string
	FlowObjectStoreBucket      string
	FlowObjectStorePrefix      string
	FlowObjectStoreEndpoint    string
	FlowObjectStoreRegion      string
	FlowObjectStoreAccessKey   string
	FlowObjectStoreSecretKey   string
	FlowObjectStorePathStyle   bool
	FlowObjectStoreLookback    string
	FlowObjectStoreMaxObjects  int
	PollInterval               string
	InitialBackfill            string
	Retention                  string
}

func v162Load() *v162Config {
	return &v162Config{
		TailscaleAPIKey:            v162Fallback("TAILSCALE_API_KEY"),
		TailscaleTailnet:           v162Default("TAILSCALE_TAILNET", "-"),
		TailscaleAPIURL:            v162Default("TAILSCALE_API_URL", "https://api.tailscale.com"),
		TailscaleOAuthClientID:     v162Fallback("TAILSCALE_OAUTH_CLIENT_ID"),
		TailscaleOAuthClientSecret: v162Fallback("TAILSCALE_OAUTH_CLIENT_SECRET"),
		TailscaleOAuthScopes:       v162List(v162Fallback("TAILSCALE_OAUTH_SCOPES"), []string{"all:read"}),
		Port:                       v162Default("PORT", "8080"),
		Environment:                v162Default("ENVIRONMENT", "development"),
		AllowedCORSOrigins:         v162List(v162Fallback("ALLOWED_CORS_ORIGINS"), nil),
		TsnetServe:                 v162Bool(os.Getenv("TSFLOW_SERVE"), false),
		TsnetHostname:              v162Default("TSFLOW_HOSTNAME", "tsflow"),
		TsnetTags:                  v162List(os.Getenv("TSFLOW_TAGS"), nil),
		TsnetFunnel:                v162Bool(os.Getenv("TSFLOW_FUNNEL"), false),
		TsnetStateDir:              v162Default("TSFLOW_STATE_DIR", filepath.Join(".", "data", "tsnet-state")),
		TsnetClientID:              os.Getenv("TS_CLIENT_ID"),
		TsnetIDToken:               os.Getenv("TS_ID_TOKEN"),
		TsnetAudience:              os.Getenv("TS_AUDIENCE"),
		FlowBackend:                strings.ToLower(strings.TrimSpace(v162Default("TSFLOW_FLOW_BACKEND", ""))),
		FlowObjectStoreBucket:      v162Default("TSFLOW_S3_BUCKET", v162Default("TAILSCALE_LOGS_S3_BUCKET", "tailscale-logs")),
		FlowObjectStorePrefix:      v162Default("TSFLOW_S3_PREFIX", v162Default("TAILSCALE_LOGS_S3_PREFIX", "network/")),
		FlowObjectStoreEndpoint:    v162First("TSFLOW_S3_ENDPOINT", "TAILSCALE_LOGS_S3_ENDPOINT", "AWS_ENDPOINT_URL", "endpoint"),
		FlowObjectStoreRegion:      v162Default("TSFLOW_S3_REGION", v162Default("AWS_REGION", v162Default("AWS_DEFAULT_REGION", v162First("region")))),
		FlowObjectStoreAccessKey:   v162First("TSFLOW_S3_ACCESS_KEY_ID", "TAILSCALE_LOGS_S3_ACCESS_KEY", "AWS_ACCESS_KEY_ID"),
		FlowObjectStoreSecretKey:   v162First("TSFLOW_S3_SECRET_ACCESS_KEY", "TAILSCALE_LOGS_S3_SECRET_KEY", "AWS_SECRET_ACCESS_KEY"),
		FlowObjectStorePathStyle:   v162Bool(v162Default("TSFLOW_S3_PATH_STYLE", "true"), true),
		FlowObjectStoreLookback:    v162Default("TSFLOW_S3_LOOKBACK", "15m"),
		FlowObjectStoreMaxObjects:  v162PositiveInt(v162Default("TSFLOW_S3_MAX_OBJECTS_PER_POLL", "500")),
		PollInterval:               v162Default("TSFLOW_POLL_INTERVAL", "5m"),
		InitialBackfill:            v162Default("TSFLOW_INITIAL_BACKFILL", "6h"),
		Retention:                  v162Fallback("TSFLOW_RETENTION"),
	}
}

func (c *v162Config) validate() error {
	hasAPIKey := c.TailscaleAPIKey != ""
	hasOAuth := c.TailscaleOAuthClientID != "" && c.TailscaleOAuthClientSecret != ""
	hasWIF := c.TsnetClientID != ""
	backend := strings.ToLower(strings.TrimSpace(c.FlowBackend))
	if backend != "" && backend != "api" && backend != "s3" {
		return errors.New("TSFLOW_FLOW_BACKEND must be either api or s3")
	}
	hasObjectCredentials := c.FlowObjectStoreBucket != "" && c.FlowObjectStoreEndpoint != "" &&
		c.FlowObjectStoreAccessKey != "" && c.FlowObjectStoreSecretKey != ""
	effective := backend
	if effective == "" {
		effective = "api"
		if hasObjectCredentials {
			effective = "s3"
		}
	}
	if effective == "api" && !hasAPIKey && !hasOAuth {
		return errors.New("api flow backend requires credentials")
	}
	if effective == "s3" && !hasObjectCredentials {
		return errors.New("s3 flow backend requires object store credentials")
	}
	if effective == "s3" {
		u, err := url.Parse(c.FlowObjectStoreEndpoint)
		if err != nil || u.Scheme == "" || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
			return errors.New("bad TSFLOW_S3_ENDPOINT")
		}
		if err := v162Duration(c.FlowObjectStoreLookback, false); err != nil {
			return err
		}
		if c.FlowObjectStoreMaxObjects <= 0 {
			return errors.New("bad TSFLOW_S3_MAX_OBJECTS_PER_POLL")
		}
	}
	u, err := url.Parse(c.TailscaleAPIURL)
	if c.TailscaleAPIURL == "" || err != nil || u.Scheme == "" || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return errors.New("bad TAILSCALE_API_URL")
	}
	if err := v162Duration(c.PollInterval, false); err != nil {
		return err
	}
	if err := v162Duration(c.InitialBackfill, false); err != nil {
		return err
	}
	if c.Retention != "" {
		if err := v162Duration(c.Retention, true); err != nil {
			return err
		}
	}
	if port, err := strconv.Atoi(c.Port); err != nil || port < 1 || port > 65535 {
		return errors.New("bad PORT")
	}
	if c.TsnetServe {
		if !hasOAuth && !hasWIF {
			return errors.New("serve needs OAuth or WIF")
		}
		if hasWIF {
			if (c.TsnetIDToken == "") == (c.TsnetAudience == "") {
				return errors.New("WIF needs exactly one of TS_ID_TOKEN or TS_AUDIENCE")
			}
			if len(c.TsnetTags) == 0 {
				return errors.New("WIF needs TSFLOW_TAGS")
			}
		}
	}
	return nil
}

// v162Poller is the poller setup from the v1.6.2 main.go.
type v162PollerSettings struct {
	Backend         string
	PollInterval    time.Duration
	InitialBackfill time.Duration
	Retention       time.Duration
	Lookback        time.Duration
}

func (c *v162Config) poller() v162PollerSettings {
	p := v162PollerSettings{Retention: 30 * 24 * time.Hour}
	p.PollInterval, _ = time.ParseDuration(c.PollInterval)
	p.InitialBackfill, _ = time.ParseDuration(c.InitialBackfill)
	if c.Retention != "" {
		p.Retention, _ = time.ParseDuration(c.Retention)
	}
	p.Backend = c.FlowBackend
	if p.Backend == "" {
		p.Backend = "api"
		if c.FlowObjectStoreEndpoint != "" && c.FlowObjectStoreAccessKey != "" && c.FlowObjectStoreSecretKey != "" {
			p.Backend = "s3"
		}
	}
	if p.Backend == "s3" && c.Retention == "" {
		p.Retention = 0
	}
	p.Lookback, _ = time.ParseDuration(c.FlowObjectStoreLookback)
	return p
}

func v162Fallback(key string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return os.Getenv("VITE_" + key)
}

func v162Default(key, def string) string {
	if v := v162Fallback(key); v != "" {
		return v
	}
	return def
}

func v162First(keys ...string) string {
	for _, k := range keys {
		if v := os.Getenv(k); v != "" {
			return v
		}
	}
	return ""
}

func v162List(s string, empty []string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	if len(out) == 0 {
		return empty
	}
	return out
}

func v162Bool(v string, def bool) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "true", "1", "t":
		return true
	case "false", "0", "f":
		return false
	}
	return def
}

func v162PositiveInt(s string) int {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil || n <= 0 {
		return 0
	}
	return n
}

func v162Duration(v string, allowZero bool) error {
	d, err := time.ParseDuration(strings.TrimSpace(v))
	if err != nil || (allowZero && d < 0) || (!allowZero && d <= 0) {
		return fmt.Errorf("bad duration %q", v)
	}
	return nil
}

// compatEnvKeys is every variable v1.6.2 or the current code reads, so each
// case starts from a clean environment.
var compatEnvKeys = []string{
	"TAILSCALE_API_KEY", "TAILSCALE_TAILNET", "TAILSCALE_API_URL",
	"TAILSCALE_OAUTH_CLIENT_ID", "TAILSCALE_OAUTH_CLIENT_SECRET", "TAILSCALE_OAUTH_SCOPES",
	"TAILSCALE_AUTH", "TAILSCALE_WIF_CLIENT_ID", "TAILSCALE_WIF_AUDIENCE",
	"TAILSCALE_WIF_ID_TOKEN", "TAILSCALE_WIF_ID_TOKEN_FILE",
	"PORT", "ENVIRONMENT", "ALLOWED_CORS_ORIGINS", "TSFLOW_TRUSTED_PROXIES",
	"TSFLOW_SERVE", "TSFLOW_HOSTNAME", "TSFLOW_TAGS", "TSFLOW_FUNNEL", "TSFLOW_STATE_DIR",
	"TSFLOW_HEALTH_PORT", "TS_CLIENT_ID", "TS_ID_TOKEN", "TS_ID_TOKEN_FILE", "TS_AUDIENCE",
	"TSFLOW_FLOW_BACKEND", "TSFLOW_S3_BUCKET", "TSFLOW_S3_PREFIX", "TSFLOW_S3_ENDPOINT",
	"TSFLOW_S3_REGION", "TSFLOW_S3_ACCESS_KEY_ID", "TSFLOW_S3_SECRET_ACCESS_KEY",
	"TSFLOW_S3_PATH_STYLE", "TSFLOW_S3_LOOKBACK", "TSFLOW_S3_MAX_OBJECTS_PER_POLL",
	"TSFLOW_S3_AUTH", "TSFLOW_S3_ROLE_ARN", "TSFLOW_S3_WEB_IDENTITY_TOKEN_FILE",
	"TAILSCALE_LOGS_S3_BUCKET", "TAILSCALE_LOGS_S3_PREFIX", "TAILSCALE_LOGS_S3_ENDPOINT",
	"TAILSCALE_LOGS_S3_ACCESS_KEY", "TAILSCALE_LOGS_S3_SECRET_KEY",
	"AWS_ENDPOINT_URL", "AWS_REGION", "AWS_DEFAULT_REGION", "AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY",
	"endpoint", "region",
	"TSFLOW_POLL_INTERVAL", "TSFLOW_INITIAL_BACKFILL", "TSFLOW_POLL_DELAY", "TSFLOW_RETENTION",
	"TSFLOW_GRAPH_CACHE_MB", "TSFLOW_TAILNETS_FILE", "TSFLOW_MCP_ENABLED", "TSFLOW_LOG_LEVEL",
	"TSFLOW_ACCESS_MODE", "TSFLOW_ACCESS_CAPABILITY", "TSFLOW_ACCESS_CAPABILITY_HEADER",
	"TSFLOW_ACCESS_USER_HEADER", "TSFLOW_ACCESS_NAME_HEADER", "TSFLOW_ACCESS_TRUSTED_PROXIES",
	"TSFLOW_ACCESS_GROUPS_HEADER", "TSFLOW_ACCESS_GROUP_GRANTS", "TSFLOW_ACCESS_GROUP_GRANTS_FILE",
	"TSFLOW_ACCESS_TAILSCALED_SOCKET", "TSFLOW_ACCESS_LOCAL_WHOIS", "TSFLOW_ACCESS_AUTOSCOPE",
	"TSFLOW_ACCESS_GRANTS",
}

func clearCompatEnv(t *testing.T) {
	t.Helper()
	for _, k := range compatEnvKeys {
		t.Setenv(k, "")
		os.Unsetenv(k)
		t.Setenv("VITE_"+k, "")
		os.Unsetenv("VITE_" + k)
	}
}

// s3Static is the object-store block from the v1.6.2 env.example.
var s3Static = map[string]string{
	"TSFLOW_S3_BUCKET":               "tailscale-logs",
	"TSFLOW_S3_PREFIX":               "network/",
	"TSFLOW_S3_ENDPOINT":             "http://garage.example.test:3900",
	"TSFLOW_S3_REGION":               "garage",
	"TSFLOW_S3_ACCESS_KEY_ID":        "access",
	"TSFLOW_S3_SECRET_ACCESS_KEY":    "secret",
	"TSFLOW_S3_PATH_STYLE":           "true",
	"TSFLOW_S3_LOOKBACK":             "15m",
	"TSFLOW_S3_MAX_OBJECTS_PER_POLL": "500",
}

func with(base map[string]string, extra ...string) map[string]string {
	out := map[string]string{}
	for k, v := range base {
		out[k] = v
	}
	for i := 0; i+1 < len(extra); i += 2 {
		out[extra[i]] = extra[i+1]
	}
	return out
}

func env(kv ...string) map[string]string { return with(nil, kv...) }

// v162Shapes are configs v1.6.2 accepted or rejected, taken from its README,
// env.example, docker-compose.yml, k8s manifests and config.go.
var v162Shapes = []struct {
	name string
	env  map[string]string
}{
	// Tailscale API credentials.
	{"api key only", env("TAILSCALE_API_KEY", "tskey-api-x")},
	{"api key and tailnet", env("TAILSCALE_API_KEY", "tskey-api-x", "TAILSCALE_TAILNET", "example.com")},
	{"oauth client", env("TAILSCALE_OAUTH_CLIENT_ID", "cid", "TAILSCALE_OAUTH_CLIENT_SECRET", "csecret")},
	{"oauth with scopes", env("TAILSCALE_OAUTH_CLIENT_ID", "cid", "TAILSCALE_OAUTH_CLIENT_SECRET", "csecret", "TAILSCALE_OAUTH_SCOPES", "devices:core:read, logs:network:read")},
	{"oauth scopes only commas", env("TAILSCALE_OAUTH_CLIENT_ID", "cid", "TAILSCALE_OAUTH_CLIENT_SECRET", "csecret", "TAILSCALE_OAUTH_SCOPES", " , ")},
	{"api key and oauth (oauth wins)", env("TAILSCALE_API_KEY", "tskey-api-x", "TAILSCALE_OAUTH_CLIENT_ID", "cid", "TAILSCALE_OAUTH_CLIENT_SECRET", "csecret")},
	{"oauth id without secret falls back to api key", env("TAILSCALE_API_KEY", "tskey-api-x", "TAILSCALE_OAUTH_CLIENT_ID", "cid")},
	{"custom api url", env("TAILSCALE_API_KEY", "tskey-api-x", "TAILSCALE_API_URL", "http://127.0.0.1:9999")},
	{"vite prefixed api key", env("VITE_TAILSCALE_API_KEY", "tskey-api-x", "VITE_TAILSCALE_TAILNET", "example.com")},
	{"vite prefixed oauth", env("VITE_TAILSCALE_OAUTH_CLIENT_ID", "cid", "VITE_TAILSCALE_OAUTH_CLIENT_SECRET", "csecret", "VITE_TAILSCALE_API_URL", "https://api.example.test")},
	{"plain wins over vite", env("TAILSCALE_API_KEY", "plain", "VITE_TAILSCALE_API_KEY", "vite")},
	{"vite server settings", env("TAILSCALE_API_KEY", "k", "VITE_PORT", "9090", "VITE_ENVIRONMENT", "production", "VITE_TSFLOW_POLL_INTERVAL", "1m", "VITE_TSFLOW_RETENTION", "48h")},

	// docker-compose.yml passes every variable, so unset ones arrive empty.
	{"compose with empty passthroughs", env(
		"TAILSCALE_OAUTH_CLIENT_ID", "", "TAILSCALE_OAUTH_CLIENT_SECRET", "", "TAILSCALE_OAUTH_SCOPES", "",
		"TAILSCALE_API_KEY", "tskey-api-x", "TAILSCALE_TAILNET", "", "TAILSCALE_API_URL", "",
		"PORT", "8080", "ENVIRONMENT", "production")},
	{"compose oauth with empty api key", env(
		"TAILSCALE_OAUTH_CLIENT_ID", "cid", "TAILSCALE_OAUTH_CLIENT_SECRET", "csecret", "TAILSCALE_OAUTH_SCOPES", "",
		"TAILSCALE_API_KEY", "", "TAILSCALE_TAILNET", "", "TAILSCALE_API_URL", "", "PORT", "8080", "ENVIRONMENT", "production")},
	// k8s/secret.yaml rendered with only some values exported.
	{"k8s secret with empty object store keys", env(
		"ENVIRONMENT", "production", "PORT", "8080", "TAILSCALE_API_KEY", "tskey-api-x", "TAILSCALE_TAILNET", "example.com",
		"TAILSCALE_API_URL", "", "TAILSCALE_OAUTH_CLIENT_ID", "", "TAILSCALE_OAUTH_CLIENT_SECRET", "", "TAILSCALE_OAUTH_SCOPES", "",
		"TSFLOW_FLOW_BACKEND", "", "TSFLOW_S3_BUCKET", "", "TSFLOW_S3_PREFIX", "", "TSFLOW_S3_ENDPOINT", "", "TSFLOW_S3_REGION", "",
		"TSFLOW_S3_ACCESS_KEY_ID", "", "TSFLOW_S3_SECRET_ACCESS_KEY", "", "TSFLOW_S3_LOOKBACK", "", "TSFLOW_S3_MAX_OBJECTS_PER_POLL", "")},

	// Server, polling and storage settings.
	{"env.example api", with(s3Static, "TAILSCALE_API_KEY", "k", "TAILSCALE_TAILNET", "example.com", "TSFLOW_FLOW_BACKEND", "api", "PORT", "8080", "ENVIRONMENT", "development")},
	{"port and environment", env("TAILSCALE_API_KEY", "k", "PORT", "3000", "ENVIRONMENT", "production")},
	{"cors origins", env("TAILSCALE_API_KEY", "k", "ALLOWED_CORS_ORIGINS", "https://a.example.com, https://b.example.com")},
	{"poll settings", env("TAILSCALE_API_KEY", "k", "TSFLOW_POLL_INTERVAL", "30s", "TSFLOW_INITIAL_BACKFILL", "24h", "TSFLOW_RETENTION", "168h")},
	{"retention zero", env("TAILSCALE_API_KEY", "k", "TSFLOW_RETENTION", "0s")},
	{"backend upper case", env("TAILSCALE_API_KEY", "k", "TSFLOW_FLOW_BACKEND", " API ")},

	// S3-compatible object storage.
	{"env.example s3", with(s3Static, "TAILSCALE_API_KEY", "k", "TAILSCALE_TAILNET", "example.com", "TSFLOW_FLOW_BACKEND", "s3")},
	{"s3 without api credentials", with(s3Static, "TSFLOW_FLOW_BACKEND", "s3")},
	{"s3 auto detected", with(s3Static, "TAILSCALE_API_KEY", "k")},
	{"s3 auto detected default bucket", env("TAILSCALE_API_KEY", "k", "TSFLOW_S3_ENDPOINT", "https://s3.example.test", "TSFLOW_S3_ACCESS_KEY_ID", "a", "TSFLOW_S3_SECRET_ACCESS_KEY", "s")},
	{"s3 path style off", with(s3Static, "TSFLOW_FLOW_BACKEND", "s3", "TSFLOW_S3_PATH_STYLE", "false")},
	{"s3 path style garbage", with(s3Static, "TSFLOW_FLOW_BACKEND", "s3", "TSFLOW_S3_PATH_STYLE", "maybe")},
	{"s3 retention set", with(s3Static, "TSFLOW_FLOW_BACKEND", "s3", "TSFLOW_RETENTION", "720h")},
	{"s3 legacy TAILSCALE_LOGS_S3", env("TSFLOW_FLOW_BACKEND", "s3",
		"TAILSCALE_LOGS_S3_BUCKET", "logs", "TAILSCALE_LOGS_S3_PREFIX", "net/", "TAILSCALE_LOGS_S3_ENDPOINT", "http://minio.example.test:9000",
		"TAILSCALE_LOGS_S3_ACCESS_KEY", "a", "TAILSCALE_LOGS_S3_SECRET_KEY", "s")},
	{"s3 AWS env names", env("TSFLOW_FLOW_BACKEND", "s3", "TSFLOW_S3_BUCKET", "logs",
		"AWS_ENDPOINT_URL", "https://s3.us-west-2.amazonaws.com", "AWS_REGION", "us-west-2",
		"AWS_ACCESS_KEY_ID", "a", "AWS_SECRET_ACCESS_KEY", "s")},
	{"s3 AWS_DEFAULT_REGION", env("TSFLOW_FLOW_BACKEND", "s3", "TSFLOW_S3_BUCKET", "logs", "TSFLOW_S3_ENDPOINT", "https://s3.example.test",
		"AWS_DEFAULT_REGION", "eu-west-1", "AWS_ACCESS_KEY_ID", "a", "AWS_SECRET_ACCESS_KEY", "s")},
	{"s3 lower case endpoint and region", env("TSFLOW_FLOW_BACKEND", "s3", "TSFLOW_S3_BUCKET", "logs",
		"endpoint", "http://garage.example.test:3900", "region", "garage", "TSFLOW_S3_ACCESS_KEY_ID", "a", "TSFLOW_S3_SECRET_ACCESS_KEY", "s")},
	{"s3 vite prefixed", env("TSFLOW_FLOW_BACKEND", "s3", "VITE_TSFLOW_S3_BUCKET", "logs", "TSFLOW_S3_ENDPOINT", "http://garage.example.test:3900",
		"TSFLOW_S3_ACCESS_KEY_ID", "a", "TSFLOW_S3_SECRET_ACCESS_KEY", "s", "VITE_TSFLOW_S3_LOOKBACK", "30m")},
	{"aws keys in env do not switch an api install", env("TAILSCALE_API_KEY", "k", "AWS_ACCESS_KEY_ID", "a", "AWS_SECRET_ACCESS_KEY", "s")},
	{"api backend ignores bad lookback", env("TAILSCALE_API_KEY", "k", "TSFLOW_S3_LOOKBACK", "soon")},

	// tsnet serve mode.
	{"tsnet oauth", env("TAILSCALE_OAUTH_CLIENT_ID", "cid", "TAILSCALE_OAUTH_CLIENT_SECRET", "csecret",
		"TSFLOW_SERVE", "true", "TSFLOW_HOSTNAME", "tsflow", "TSFLOW_TAGS", "tag:tsflow")},
	{"tsnet funnel and state dir", env("TAILSCALE_OAUTH_CLIENT_ID", "cid", "TAILSCALE_OAUTH_CLIENT_SECRET", "csecret",
		"TSFLOW_SERVE", "1", "TSFLOW_FUNNEL", "t", "TSFLOW_STATE_DIR", "/data/tsnet-state", "TSFLOW_TAGS", "tag:a, tag:b")},
	{"tsnet wif audience", env("TAILSCALE_OAUTH_CLIENT_ID", "cid", "TAILSCALE_OAUTH_CLIENT_SECRET", "csecret",
		"TSFLOW_SERVE", "true", "TSFLOW_TAGS", "tag:tsflow", "TS_CLIENT_ID", "fed", "TS_AUDIENCE", "example.org")},
	{"tsnet wif id token", env("TAILSCALE_API_KEY", "k", "TSFLOW_SERVE", "true", "TSFLOW_TAGS", "tag:tsflow", "TS_CLIENT_ID", "fed", "TS_ID_TOKEN", "header.payload.sig")},
	{"ts wif vars without serve", env("TAILSCALE_API_KEY", "k", "TS_CLIENT_ID", "fed")},

	// Shapes v1.6.2 rejected. They must still be rejected.
	{"reject: no credentials", env()},
	{"reject: oauth without secret", env("TAILSCALE_OAUTH_CLIENT_ID", "cid")},
	{"reject: unknown backend", env("TAILSCALE_API_KEY", "k", "TSFLOW_FLOW_BACKEND", "kafka")},
	{"reject: s3 missing keys", env("TAILSCALE_API_KEY", "k", "TSFLOW_FLOW_BACKEND", "s3", "TSFLOW_S3_ENDPOINT", "http://x.example.test")},
	{"reject: s3 bad endpoint", with(s3Static, "TSFLOW_FLOW_BACKEND", "s3", "TSFLOW_S3_ENDPOINT", "garage:3900")},
	{"reject: s3 bad lookback", with(s3Static, "TSFLOW_FLOW_BACKEND", "s3", "TSFLOW_S3_LOOKBACK", "0")},
	{"reject: s3 bad max objects", with(s3Static, "TSFLOW_FLOW_BACKEND", "s3", "TSFLOW_S3_MAX_OBJECTS_PER_POLL", "-1")},
	{"reject: bad api url", env("TAILSCALE_API_KEY", "k", "TAILSCALE_API_URL", "${TAILSCALE_API_URL}")},
	{"reject: bad port", env("TAILSCALE_API_KEY", "k", "PORT", "http")},
	{"reject: bad poll interval", env("TAILSCALE_API_KEY", "k", "TSFLOW_POLL_INTERVAL", "0s")},
	{"reject: bad backfill", env("TAILSCALE_API_KEY", "k", "TSFLOW_INITIAL_BACKFILL", "yesterday")},
	{"reject: negative retention", env("TAILSCALE_API_KEY", "k", "TSFLOW_RETENTION", "-1h")},
	{"reject: tsnet with api key only", env("TAILSCALE_API_KEY", "k", "TSFLOW_SERVE", "true")},
	{"reject: tsnet wif without tags", env("TAILSCALE_API_KEY", "k", "TSFLOW_SERVE", "true", "TS_CLIENT_ID", "fed", "TS_AUDIENCE", "example.org")},
	{"reject: tsnet wif token and audience", env("TAILSCALE_API_KEY", "k", "TSFLOW_SERVE", "true", "TSFLOW_TAGS", "tag:x", "TS_CLIENT_ID", "fed", "TS_AUDIENCE", "a", "TS_ID_TOKEN", "t")},
	{"reject: tsnet wif no token source", env("TAILSCALE_API_KEY", "k", "TSFLOW_SERVE", "true", "TSFLOW_TAGS", "tag:x", "TS_CLIENT_ID", "fed")},
}

func TestV162ConfigShapesBehaveTheSame(t *testing.T) {
	for _, tc := range v162Shapes {
		t.Run(tc.name, func(t *testing.T) {
			clearCompatEnv(t)
			for k, v := range tc.env {
				t.Setenv(k, v)
			}

			old := v162Load()
			oldErr := old.validate()
			cur := config.Load()
			curErr := cur.Validate()

			if (oldErr == nil) != (curErr == nil) {
				t.Fatalf("v1.6.2 validate err=%v, current err=%v", oldErr, curErr)
			}
			if strings.HasPrefix(tc.name, "reject:") != (oldErr != nil) {
				t.Fatalf("case name does not match v1.6.2 result: err=%v", oldErr)
			}

			// Every field v1.6.2 had must load to the same value.
			ov := reflect.ValueOf(old).Elem()
			cv := reflect.ValueOf(cur).Elem()
			for i := 0; i < ov.NumField(); i++ {
				name := ov.Type().Field(i).Name
				got := cv.FieldByName(name)
				if !got.IsValid() {
					t.Fatalf("field %s from v1.6.2 is gone", name)
				}
				if !reflect.DeepEqual(ov.Field(i).Interface(), got.Interface()) {
					t.Errorf("%s: v1.6.2 %#v, current %#v", name, ov.Field(i).Interface(), got.Interface())
				}
			}
			if oldErr != nil {
				return
			}

			// Features added after v1.6.2 stay off.
			if cur.MCPEnabled || cur.Access.Enabled || cur.TailnetsFile != "" || cur.TrustedProxies != "" ||
				cur.TsnetHealthPort != "" || cur.TailscaleAuth != "" || cur.FlowObjectStoreAuth != "" {
				t.Errorf("a feature added after v1.6.2 turned on: mcp=%v access=%v tailnets=%q trusted=%q health=%q auth=%q s3auth=%q",
					cur.MCPEnabled, cur.Access.Enabled, cur.TailnetsFile, cur.TrustedProxies, cur.TsnetHealthPort, cur.TailscaleAuth, cur.FlowObjectStoreAuth)
			}
			if n, err := cur.GraphCacheBytes(); err != nil || n != 256<<20 {
				t.Errorf("graph cache default = %d, %v", n, err)
			}

			// The single-tailnet variables still define the only tailnet.
			tailnets, err := cur.ResolveTailnets()
			if err != nil {
				t.Fatalf("ResolveTailnets: %v", err)
			}
			if len(tailnets) != 1 || tailnets[0].ID != "default" || tailnets[0].Name != old.TailscaleTailnet {
				t.Fatalf("tailnets = %+v, want one entry id default named %q", tailnets, old.TailscaleTailnet)
			}
			// No display name: the UI keeps labeling the tailnet by its name.
			if tailnets[0].DisplayName != "" {
				t.Errorf("display name = %q, want none", tailnets[0].DisplayName)
			}
			wantAuth := ""
			switch {
			case old.TailscaleOAuthClientID != "" && old.TailscaleOAuthClientSecret != "":
				wantAuth = config.TailscaleAuthOAuth
			case old.TailscaleAPIKey != "":
				wantAuth = config.TailscaleAuthAPIKey
			}
			if tailnets[0].AuthMode != wantAuth {
				t.Errorf("auth mode = %q, want %q", tailnets[0].AuthMode, wantAuth)
			}
			svc := tailnets[0].ServiceConfig(cur)
			if svc.TailscaleAPIURL != old.TailscaleAPIURL || !reflect.DeepEqual(svc.TailscaleOAuthScopes, old.TailscaleOAuthScopes) {
				t.Errorf("service config url=%q scopes=%v, want %q %v", svc.TailscaleAPIURL, svc.TailscaleOAuthScopes, old.TailscaleAPIURL, old.TailscaleOAuthScopes)
			}

			// Poller settings match the v1.6.2 startup path.
			want := old.poller()
			pc, err := services.PollerConfigFrom(cur)
			if err != nil {
				t.Fatalf("PollerConfigFrom: %v", err)
			}
			if pc.FlowBackend != want.Backend || pc.PollInterval != want.PollInterval ||
				pc.InitialBackfill != want.InitialBackfill || pc.Retention != want.Retention {
				t.Errorf("poller backend=%s interval=%s backfill=%s retention=%s, want %s %s %s %s",
					pc.FlowBackend, pc.PollInterval, pc.InitialBackfill, pc.Retention,
					want.Backend, want.PollInterval, want.InitialBackfill, want.Retention)
			}
			store := pc.ObjectStore
			if store.Bucket != old.FlowObjectStoreBucket || store.Prefix != old.FlowObjectStorePrefix ||
				store.Endpoint != old.FlowObjectStoreEndpoint || store.Region != old.FlowObjectStoreRegion ||
				store.AccessKey != old.FlowObjectStoreAccessKey || store.SecretKey != old.FlowObjectStoreSecretKey ||
				store.UsePathStyle != old.FlowObjectStorePathStyle || store.Lookback != want.Lookback ||
				store.MaxObjects != old.FlowObjectStoreMaxObjects {
				t.Errorf("object store = %+v, want v1.6.2 values", store)
			}
			if want.Backend == "s3" && store.AuthMode != "" && store.AuthMode != config.ObjectStoreAuthStatic {
				t.Errorf("s3 auth mode = %q, want static keys", store.AuthMode)
			}
		})
	}
}

// TestV162DefaultsUnchanged pins the defaults an install with no optional
// settings gets.
func TestV162DefaultsUnchanged(t *testing.T) {
	clearCompatEnv(t)
	t.Setenv("TAILSCALE_API_KEY", "k")
	cur := config.Load()
	if err := cur.Validate(); err != nil {
		t.Fatal(err)
	}
	checks := map[string][2]string{
		"TailscaleTailnet":        {cur.TailscaleTailnet, "-"},
		"TailscaleAPIURL":         {cur.TailscaleAPIURL, "https://api.tailscale.com"},
		"Port":                    {cur.Port, "8080"},
		"Environment":             {cur.Environment, "development"},
		"TsnetHostname":           {cur.TsnetHostname, "tsflow"},
		"TsnetStateDir":           {cur.TsnetStateDir, filepath.Join(".", "data", "tsnet-state")},
		"FlowObjectStoreBucket":   {cur.FlowObjectStoreBucket, "tailscale-logs"},
		"FlowObjectStorePrefix":   {cur.FlowObjectStorePrefix, "network/"},
		"FlowObjectStoreLookback": {cur.FlowObjectStoreLookback, "15m"},
		"PollInterval":            {cur.PollInterval, "5m"},
		"InitialBackfill":         {cur.InitialBackfill, "6h"},
		"Retention":               {cur.Retention, ""},
	}
	for name, c := range checks {
		if c[0] != c[1] {
			t.Errorf("%s default = %q, want %q", name, c[0], c[1])
		}
	}
	if !reflect.DeepEqual(cur.TailscaleOAuthScopes, []string{"all:read"}) || !cur.FlowObjectStorePathStyle ||
		cur.FlowObjectStoreMaxObjects != 500 || cur.TsnetServe || cur.TsnetFunnel || cur.AllowedCORSOrigins != nil {
		t.Errorf("default scopes=%v pathStyle=%v maxObjects=%d serve=%v funnel=%v cors=%v",
			cur.TailscaleOAuthScopes, cur.FlowObjectStorePathStyle, cur.FlowObjectStoreMaxObjects, cur.TsnetServe, cur.TsnetFunnel, cur.AllowedCORSOrigins)
	}
	pc, err := services.PollerConfigFrom(cur)
	if err != nil {
		t.Fatal(err)
	}
	if pc.FlowBackend != "api" || pc.Retention != 30*24*time.Hour || pc.PollInterval != 5*time.Minute || pc.InitialBackfill != 6*time.Hour {
		t.Errorf("poller defaults = %+v", pc)
	}
}
