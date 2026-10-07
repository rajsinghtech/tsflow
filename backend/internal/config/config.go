package config

import (
	"errors"
	"fmt"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Config holds the application configuration
type Config struct {
	TailscaleAPIKey            string
	TailscaleTailnet           string
	TailscaleAPIURL            string
	TailscaleOAuthClientID     string
	TailscaleOAuthClientSecret string
	TailscaleOAuthScopes       []string
	// TailscaleAuth is oauth, api_key, or wif. Empty keeps the historical
	// resolution from the credential variables.
	TailscaleAuth           string
	TailscaleWIFClientID    string
	TailscaleWIFAudience    string
	TailscaleWIFIDToken     string
	TailscaleWIFIDTokenFile string
	Port                    string
	Environment             string
	AllowedCORSOrigins      []string
	// TrustedProxies lists the proxies (IPs or CIDRs, comma separated)
	// allowed to set the client address with X-Forwarded-For.
	TrustedProxies string
	// tsnet serve mode
	TsnetServe    bool
	TsnetHostname string
	TsnetTags     []string
	TsnetFunnel   bool
	TsnetStateDir string
	// TsnetHealthPort serves /health on a plain listener next to tsnet,
	// for probes that cannot reach the tailnet. Empty means off.
	TsnetHealthPort string
	// tsnet workload identity federation
	TsnetClientID string
	TsnetIDToken  string
	// TsnetIDTokenFile is read once at startup when TsnetIDToken is empty.
	TsnetIDTokenFile string
	TsnetAudience    string
	// flow log backend
	FlowBackend               string
	FlowObjectStoreBucket     string
	FlowObjectStorePrefix     string
	FlowObjectStoreEndpoint   string
	FlowObjectStoreRegion     string
	FlowObjectStoreAccessKey  string
	FlowObjectStoreSecretKey  string
	FlowObjectStorePathStyle  bool
	FlowObjectStoreLookback   string
	FlowObjectStoreMaxObjects int
	// FlowObjectStoreAuth is static, aws_default, or gcs_adc. Empty means
	// static when object-store credentials are configured.
	FlowObjectStoreAuth    string
	FlowObjectStoreRoleARN string
	// FlowObjectStoreWebIdentityTokenFile is an OIDC token used to assume
	// FlowObjectStoreRoleARN. Set means AssumeRoleWithWebIdentity, not the
	// AWS SDK default chain.
	FlowObjectStoreWebIdentityTokenFile string
	PollInterval                        string
	InitialBackfill                     string
	// PollDelay is how far behind now API polls end (TSFLOW_POLL_DELAY).
	PollDelay string
	Retention string
	// TailnetsFile is an optional YAML or JSON list of tailnets. When it is
	// empty, the single-tailnet environment variables are used as id default.
	TailnetsFile string
	// MCPEnabled serves a read-only Model Context Protocol endpoint at /mcp.
	// It stays off unless TSFLOW_MCP_ENABLED is set.
	MCPEnabled bool
	// Access is opt-in tailnet identity. The zero value leaves requests open.
	Access Access
}

// Load loads configuration from environment variables
// Supports both TAILSCALE_* and VITE_TAILSCALE_* prefixes for backwards compatibility
func Load() *Config {
	cfg := &Config{
		TailscaleAPIKey:            getEnvWithFallback("TAILSCALE_API_KEY"),
		TailscaleTailnet:           getEnvWithDefault("TAILSCALE_TAILNET", "-"),
		TailscaleAPIURL:            getEnvWithDefault("TAILSCALE_API_URL", "https://api.tailscale.com"),
		TailscaleOAuthClientID:     getEnvWithFallback("TAILSCALE_OAUTH_CLIENT_ID"),
		TailscaleOAuthClientSecret: getEnvWithFallback("TAILSCALE_OAUTH_CLIENT_SECRET"),
		TailscaleOAuthScopes:       parseScopes(getEnvWithFallback("TAILSCALE_OAUTH_SCOPES")),
		TailscaleAuth:              strings.ToLower(strings.TrimSpace(getEnvWithFallback("TAILSCALE_AUTH"))),
		TailscaleWIFClientID:       strings.TrimSpace(getEnvWithFallback("TAILSCALE_WIF_CLIENT_ID")),
		TailscaleWIFAudience:       strings.TrimSpace(getEnvWithFallback("TAILSCALE_WIF_AUDIENCE")),
		TailscaleWIFIDToken:        strings.TrimSpace(getEnvWithFallback("TAILSCALE_WIF_ID_TOKEN")),
		TailscaleWIFIDTokenFile:    strings.TrimSpace(getEnvWithFallback("TAILSCALE_WIF_ID_TOKEN_FILE")),
		Port:                       getEnvWithDefault("PORT", "8080"),
		Environment:                getEnvWithDefault("ENVIRONMENT", "development"),
		AllowedCORSOrigins:         parseCORSOrigins(getEnvWithFallback("ALLOWED_CORS_ORIGINS")),
		TrustedProxies:             strings.TrimSpace(os.Getenv("TSFLOW_TRUSTED_PROXIES")),
		TsnetServe:                 parseBool(os.Getenv("TSFLOW_SERVE"), false),
		TsnetHostname:              getEnvWithDefault("TSFLOW_HOSTNAME", "tsflow"),
		TsnetTags:                  parseTags(os.Getenv("TSFLOW_TAGS")),
		TsnetFunnel:                parseBool(os.Getenv("TSFLOW_FUNNEL"), false),
		TsnetStateDir:              getEnvWithDefault("TSFLOW_STATE_DIR", filepath.Join(".", "data", "tsnet-state")),
		TsnetClientID:              os.Getenv("TS_CLIENT_ID"),
		TsnetIDToken:               os.Getenv("TS_ID_TOKEN"),
		TsnetIDTokenFile:           strings.TrimSpace(os.Getenv("TS_ID_TOKEN_FILE")),
		TsnetHealthPort:            strings.TrimSpace(os.Getenv("TSFLOW_HEALTH_PORT")),
		TsnetAudience:              os.Getenv("TS_AUDIENCE"),
		FlowBackend:                strings.ToLower(strings.TrimSpace(getEnvWithDefault("TSFLOW_FLOW_BACKEND", ""))),
		FlowObjectStoreBucket:      getEnvWithDefault("TSFLOW_S3_BUCKET", getEnvWithDefault("TAILSCALE_LOGS_S3_BUCKET", "tailscale-logs")),
		FlowObjectStorePrefix:      getEnvWithDefault("TSFLOW_S3_PREFIX", getEnvWithDefault("TAILSCALE_LOGS_S3_PREFIX", "network/")),
		FlowObjectStoreEndpoint:    firstEnv("TSFLOW_S3_ENDPOINT", "TAILSCALE_LOGS_S3_ENDPOINT", "AWS_ENDPOINT_URL", "endpoint"),
		FlowObjectStoreRegion:      getEnvWithDefault("TSFLOW_S3_REGION", getEnvWithDefault("AWS_REGION", getEnvWithDefault("AWS_DEFAULT_REGION", firstEnv("region")))),
		FlowObjectStoreAccessKey:   firstEnv("TSFLOW_S3_ACCESS_KEY_ID", "TAILSCALE_LOGS_S3_ACCESS_KEY", "AWS_ACCESS_KEY_ID"),
		FlowObjectStoreSecretKey:   firstEnv("TSFLOW_S3_SECRET_ACCESS_KEY", "TAILSCALE_LOGS_S3_SECRET_KEY", "AWS_SECRET_ACCESS_KEY"),
		FlowObjectStorePathStyle:   loadObjectStorePathStyle(strings.ToLower(strings.TrimSpace(getEnvWithFallback("TSFLOW_S3_AUTH")))),
		FlowObjectStoreLookback:    getEnvWithDefault("TSFLOW_S3_LOOKBACK", "15m"),
		FlowObjectStoreMaxObjects:  parsePositiveInt(getEnvWithDefault("TSFLOW_S3_MAX_OBJECTS_PER_POLL", "500")),
		FlowObjectStoreAuth:        strings.ToLower(strings.TrimSpace(getEnvWithFallback("TSFLOW_S3_AUTH"))),
		FlowObjectStoreRoleARN:     strings.TrimSpace(getEnvWithFallback("TSFLOW_S3_ROLE_ARN")),
		PollInterval:               getEnvWithDefault("TSFLOW_POLL_INTERVAL", "5m"),
		InitialBackfill:            getEnvWithDefault("TSFLOW_INITIAL_BACKFILL", "6h"),
		PollDelay:                  getEnvWithDefault("TSFLOW_POLL_DELAY", "2m"),
		Retention:                  getEnvWithFallback("TSFLOW_RETENTION"),
		TailnetsFile:               strings.TrimSpace(os.Getenv("TSFLOW_TAILNETS_FILE")),
		MCPEnabled:                 parseBool(os.Getenv("TSFLOW_MCP_ENABLED"), false),
	}
	cfg.FlowObjectStoreWebIdentityTokenFile = strings.TrimSpace(getEnvWithFallback("TSFLOW_S3_WEB_IDENTITY_TOKEN_FILE"))
	cfg.Access = cfg.loadAccess()
	return cfg
}

// Validate validates the configuration
func (c *Config) Validate() error {
	hasAPIKey := c.TailscaleAPIKey != ""
	hasOAuth := c.TailscaleOAuthClientID != "" && c.TailscaleOAuthClientSecret != ""
	hasTsnetWIF := c.TsnetClientID != ""
	effectiveBackend, err := c.EffectiveFlowBackend()
	if err != nil {
		return err
	}
	apiMode := ""
	if c.TailnetsFile == "" {
		apiMode, err = c.tailscaleAuthMode()
		if err != nil {
			return err
		}
	}

	// A tailnet file carries credentials per entry. The single-tailnet
	// environment variables are not required in that mode, and combining the
	// two sources is rejected by ResolveTailnets.
	if c.TailnetsFile == "" && effectiveBackend == FlowBackendAPI && apiMode == "" {
		return errors.New("api flow backend requires TAILSCALE_API_KEY or both TAILSCALE_OAUTH_CLIENT_ID and TAILSCALE_OAUTH_CLIENT_SECRET")
	}
	if err := c.validateObjectStore(effectiveBackend); err != nil {
		return err
	}

	if c.TailscaleAPIURL == "" {
		return errors.New("TAILSCALE_API_URL must not be empty")
	}
	parsedAPIURL, err := url.Parse(c.TailscaleAPIURL)
	if err != nil || parsedAPIURL.Scheme == "" || parsedAPIURL.Host == "" {
		return errors.New("TAILSCALE_API_URL must be a valid absolute URL")
	}
	if parsedAPIURL.Scheme != "http" && parsedAPIURL.Scheme != "https" {
		return errors.New("TAILSCALE_API_URL must use http or https")
	}
	if err := validateDuration("TSFLOW_POLL_INTERVAL", c.PollInterval, false); err != nil {
		return err
	}
	if err := validateDuration("TSFLOW_INITIAL_BACKFILL", c.InitialBackfill, false); err != nil {
		return err
	}
	if strings.TrimSpace(c.PollDelay) != "" {
		if err := validateDuration("TSFLOW_POLL_DELAY", c.PollDelay, true); err != nil {
			return err
		}
	}
	if c.Retention != "" {
		if err := validateDuration("TSFLOW_RETENTION", c.Retention, true); err != nil {
			return err
		}
	}
	port, err := strconv.Atoi(c.Port)
	if err != nil || port < 1 || port > 65535 {
		return errors.New("PORT must be a number between 1 and 65535")
	}

	if c.TailscaleAuth == "" && hasAPIKey && hasOAuth {
		log.Println("Both API key and OAuth credentials provided. OAuth will take precedence.")
	}

	tailnets, err := c.ResolveTailnets()
	if err != nil {
		return err
	}
	if c.TsnetFunnel && len(tailnets) > 1 {
		return errors.New("TSFLOW_FUNNEL cannot be enabled when more than one tailnet is configured")
	}

	if err := c.prepareAccess(); err != nil {
		return err
	}

	if c.TsnetIDTokenFile != "" && !hasTsnetWIF {
		return errors.New("TS_ID_TOKEN_FILE requires TS_CLIENT_ID")
	}
	if c.TsnetHealthPort != "" {
		if !c.TsnetServe {
			return errors.New("TSFLOW_HEALTH_PORT is only used with TSFLOW_SERVE=true; PORT already serves /health")
		}
		hp, err := strconv.Atoi(c.TsnetHealthPort)
		if err != nil || hp < 1 || hp > 65535 {
			return errors.New("TSFLOW_HEALTH_PORT must be a number between 1 and 65535")
		}
	}

	if c.TsnetServe {
		if !hasOAuth && !hasTsnetWIF {
			return errors.New("TSFLOW_SERVE=true requires either OAuth credentials or workload identity federation (TS_CLIENT_ID)")
		}
		if hasTsnetWIF {
			sources := 0
			for _, v := range []string{c.TsnetIDToken, c.TsnetIDTokenFile, c.TsnetAudience} {
				if v != "" {
					sources++
				}
			}
			if sources == 0 {
				return errors.New("workload identity federation requires TS_ID_TOKEN, TS_ID_TOKEN_FILE, or TS_AUDIENCE")
			}
			if sources > 1 {
				return errors.New("only one of TS_ID_TOKEN, TS_ID_TOKEN_FILE, or TS_AUDIENCE should be set for workload identity federation")
			}
			if len(c.TsnetTags) == 0 {
				return errors.New("workload identity federation requires TSFLOW_TAGS to be set")
			}
		}
	}

	return nil
}

// getEnvWithDefault returns the environment variable value or a default value
func getEnvWithDefault(key, defaultValue string) string {
	if value := getEnvWithFallback(key); value != "" {
		return value
	}
	return defaultValue
}

// getEnvWithFallback checks both non-prefixed and VITE_ prefixed env vars for backwards compatibility
func getEnvWithFallback(key string) string {
	// First check without prefix
	if value := os.Getenv(key); value != "" {
		return value
	}
	// Fall back to VITE_ prefixed version
	if value := os.Getenv("VITE_" + key); value != "" {
		return value
	}
	return ""
}

func loadObjectStorePathStyle(auth string) bool {
	if value, ok := lookupNonEmpty("TSFLOW_S3_PATH_STYLE"); ok {
		return parseBool(value, true)
	}
	// AWS virtual-hosted style is the SDK default. Garage and other
	// S3-compatible stores keep the historical path-style default.
	if auth == ObjectStoreAuthAWSDefault {
		return false
	}
	return true
}

func (c *Config) validateObjectStore(backend string) error {
	if strings.TrimSpace(c.FlowObjectStoreRoleARN) != "" && c.objectStoreAuth() != ObjectStoreAuthAWSDefault {
		return errors.New("TSFLOW_S3_ROLE_ARN requires TSFLOW_S3_AUTH=aws_default")
	}
	if err := c.validateWebIdentityTokenFile(); err != nil {
		return err
	}
	switch backend {
	case FlowBackendGCS:
		return c.validateGCS()
	case FlowBackendS3:
	default:
		return nil
	}
	if c.objectStoreAuth() == ObjectStoreAuthAWSDefault {
		if strings.TrimSpace(c.FlowObjectStoreBucket) == "" {
			return errors.New("TSFLOW_S3_AUTH=aws_default requires TSFLOW_S3_BUCKET")
		}
		if strings.TrimSpace(c.FlowObjectStoreRegion) == "" {
			return errors.New("TSFLOW_S3_AUTH=aws_default requires a region (TSFLOW_S3_REGION or AWS_REGION)")
		}
		if staticObjectStoreEnvSet() {
			return errors.New("TSFLOW_S3_AUTH=aws_default does not use TSFLOW_S3_ACCESS_KEY_ID or TSFLOW_S3_SECRET_ACCESS_KEY")
		}
		if err := c.validateObjectStoreEndpoint(false); err != nil {
			return err
		}
	} else if !c.hasStaticObjectStoreCredentials() {
		return errors.New("s3 flow backend requires TSFLOW_S3_BUCKET, TSFLOW_S3_ENDPOINT, TSFLOW_S3_ACCESS_KEY_ID, and TSFLOW_S3_SECRET_ACCESS_KEY")
	} else if err := c.validateObjectStoreEndpoint(true); err != nil {
		return err
	}
	if err := validateDuration("TSFLOW_S3_LOOKBACK", c.FlowObjectStoreLookback, false); err != nil {
		return err
	}
	if c.FlowObjectStoreMaxObjects <= 0 {
		return errors.New("TSFLOW_S3_MAX_OBJECTS_PER_POLL must be a positive integer")
	}
	return nil
}

func (c *Config) validateWebIdentityTokenFile() error {
	file := strings.TrimSpace(c.FlowObjectStoreWebIdentityTokenFile)
	if file == "" {
		return nil
	}
	if c.objectStoreAuth() != ObjectStoreAuthAWSDefault {
		return errors.New("TSFLOW_S3_WEB_IDENTITY_TOKEN_FILE requires TSFLOW_S3_AUTH=aws_default")
	}
	if strings.TrimSpace(c.FlowObjectStoreRoleARN) == "" {
		return errors.New("TSFLOW_S3_WEB_IDENTITY_TOKEN_FILE requires TSFLOW_S3_ROLE_ARN")
	}
	return readNonEmptyFile("TSFLOW_S3_WEB_IDENTITY_TOKEN_FILE", file)
}

func (c *Config) validateGCS() error {
	if strings.TrimSpace(c.FlowObjectStoreBucket) == "" {
		return errors.New("TSFLOW_FLOW_BACKEND=gcs requires TSFLOW_S3_BUCKET")
	}
	if strings.TrimSpace(c.FlowObjectStoreEndpoint) != "" {
		return errors.New("TSFLOW_FLOW_BACKEND=gcs does not use TSFLOW_S3_ENDPOINT; S3-compatible GCS interop is TSFLOW_FLOW_BACKEND=s3 with static keys")
	}
	if staticObjectStoreEnvSet() {
		return errors.New("TSFLOW_FLOW_BACKEND=gcs does not use TSFLOW_S3_ACCESS_KEY_ID or TSFLOW_S3_SECRET_ACCESS_KEY")
	}
	if err := validateDuration("TSFLOW_S3_LOOKBACK", c.FlowObjectStoreLookback, false); err != nil {
		return err
	}
	if c.FlowObjectStoreMaxObjects <= 0 {
		return errors.New("TSFLOW_S3_MAX_OBJECTS_PER_POLL must be a positive integer")
	}
	return nil
}

func (c *Config) validateObjectStoreEndpoint(required bool) error {
	endpoint := strings.TrimSpace(c.FlowObjectStoreEndpoint)
	if endpoint == "" {
		if required {
			return errors.New("TSFLOW_S3_ENDPOINT must be a valid absolute URL")
		}
		return nil
	}
	parsedEndpoint, err := url.Parse(endpoint)
	if err != nil || parsedEndpoint.Scheme == "" || parsedEndpoint.Host == "" {
		return errors.New("TSFLOW_S3_ENDPOINT must be a valid absolute URL")
	}
	if parsedEndpoint.Scheme != "http" && parsedEndpoint.Scheme != "https" {
		return errors.New("TSFLOW_S3_ENDPOINT must use http or https")
	}
	return nil
}

func firstEnv(keys ...string) string {
	for _, key := range keys {
		if value := os.Getenv(key); value != "" {
			return value
		}
	}
	return ""
}

func parsePositiveInt(s string) int {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil || n <= 0 {
		return 0
	}
	return n
}

func validateDuration(name, value string, allowZero bool) error {
	d, err := time.ParseDuration(strings.TrimSpace(value))
	if err != nil || (allowZero && d < 0) || (!allowZero && d <= 0) {
		if allowZero {
			return fmt.Errorf("%s must be a duration of zero or greater", name)
		}
		return fmt.Errorf("%s must be a positive duration", name)
	}
	return nil
}

// parseScopes parses a comma-separated string of OAuth scopes
func parseScopes(scopesStr string) []string {
	if scopesStr == "" {
		return []string{"all:read"}
	}
	var scopes []string
	for _, scope := range strings.Split(scopesStr, ",") {
		if scope = strings.TrimSpace(scope); scope != "" {
			scopes = append(scopes, scope)
		}
	}
	if len(scopes) == 0 {
		return []string{"all:read"}
	}
	return scopes
}

// parseTags parses a comma-separated string of ACL tags
func parseTags(tagsStr string) []string {
	if tagsStr == "" {
		return nil
	}
	var tags []string
	for _, tag := range strings.Split(tagsStr, ",") {
		if tag = strings.TrimSpace(tag); tag != "" {
			tags = append(tags, tag)
		}
	}
	return tags
}

// parseCORSOrigins parses a comma-separated string of allowed CORS origins
// Returns nil to indicate all origins allowed (for development)
// ClientIPTrustedProxies returns the proxies allowed to supply the client
// address used for rate limiting and request logs. TSFLOW_TRUSTED_PROXIES
// wins; header access mode falls back to TSFLOW_ACCESS_TRUSTED_PROXIES,
// which already names the proxy in front of tsflow. Empty means the peer
// address is used.
func (c *Config) ClientIPTrustedProxies() ([]string, error) {
	raw, name := c.TrustedProxies, "TSFLOW_TRUSTED_PROXIES"
	if strings.TrimSpace(raw) == "" && c.Access.Enabled && c.Access.Mode == AccessModeHeader {
		raw, name = c.Access.TrustedProxies, "TSFLOW_ACCESS_TRUSTED_PROXIES"
	}
	var out []string
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		prefix, err := parseIPPrefix(part)
		if err != nil {
			return nil, fmt.Errorf("%s: %q: %w", name, part, err)
		}
		out = append(out, prefix.String())
	}
	return out, nil
}

func parseCORSOrigins(originsStr string) []string {
	if originsStr == "" {
		return nil // Allow all origins when not specified
	}
	var origins []string
	for _, origin := range strings.Split(originsStr, ",") {
		if origin = strings.TrimSpace(origin); origin != "" {
			origins = append(origins, origin)
		}
	}
	return origins
}

func parseBool(value string, defaultValue bool) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return defaultValue
	}
	switch strings.ToLower(value) {
	case "true", "1", "t":
		return true
	case "false", "0", "f":
		return false
	default:
		return defaultValue
	}
}
