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
	Port                       string
	Environment                string
	AllowedCORSOrigins         []string
	// tsnet serve mode
	TsnetServe    bool
	TsnetHostname string
	TsnetTags     []string
	TsnetFunnel   bool
	TsnetStateDir string
	// tsnet workload identity federation
	TsnetClientID string
	TsnetIDToken  string
	TsnetAudience string
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
	PollInterval              string
	InitialBackfill           string
	Retention                 string
}

// Load loads configuration from environment variables
// Supports both TAILSCALE_* and VITE_TAILSCALE_* prefixes for backwards compatibility
func Load() *Config {
	return &Config{
		TailscaleAPIKey:            getEnvWithFallback("TAILSCALE_API_KEY"),
		TailscaleTailnet:           getEnvWithDefault("TAILSCALE_TAILNET", "-"),
		TailscaleAPIURL:            getEnvWithDefault("TAILSCALE_API_URL", "https://api.tailscale.com"),
		TailscaleOAuthClientID:     getEnvWithFallback("TAILSCALE_OAUTH_CLIENT_ID"),
		TailscaleOAuthClientSecret: getEnvWithFallback("TAILSCALE_OAUTH_CLIENT_SECRET"),
		TailscaleOAuthScopes:       parseScopes(getEnvWithFallback("TAILSCALE_OAUTH_SCOPES")),
		Port:                       getEnvWithDefault("PORT", "8080"),
		Environment:                getEnvWithDefault("ENVIRONMENT", "development"),
		AllowedCORSOrigins:         parseCORSOrigins(getEnvWithFallback("ALLOWED_CORS_ORIGINS")),
		TsnetServe:                 parseBool(os.Getenv("TSFLOW_SERVE"), false),
		TsnetHostname:              getEnvWithDefault("TSFLOW_HOSTNAME", "tsflow"),
		TsnetTags:                  parseTags(os.Getenv("TSFLOW_TAGS")),
		TsnetFunnel:                parseBool(os.Getenv("TSFLOW_FUNNEL"), false),
		TsnetStateDir:              getEnvWithDefault("TSFLOW_STATE_DIR", filepath.Join(".", "data", "tsnet-state")),
		TsnetClientID:              os.Getenv("TS_CLIENT_ID"),
		TsnetIDToken:               os.Getenv("TS_ID_TOKEN"),
		TsnetAudience:              os.Getenv("TS_AUDIENCE"),
		FlowBackend:                strings.ToLower(strings.TrimSpace(getEnvWithDefault("TSFLOW_FLOW_BACKEND", ""))),
		FlowObjectStoreBucket:      getEnvWithDefault("TSFLOW_S3_BUCKET", getEnvWithDefault("TAILSCALE_LOGS_S3_BUCKET", "tailscale-logs")),
		FlowObjectStorePrefix:      getEnvWithDefault("TSFLOW_S3_PREFIX", getEnvWithDefault("TAILSCALE_LOGS_S3_PREFIX", "network/")),
		FlowObjectStoreEndpoint:    firstEnv("TSFLOW_S3_ENDPOINT", "TAILSCALE_LOGS_S3_ENDPOINT", "AWS_ENDPOINT_URL", "endpoint"),
		FlowObjectStoreRegion:      getEnvWithDefault("TSFLOW_S3_REGION", getEnvWithDefault("AWS_REGION", getEnvWithDefault("AWS_DEFAULT_REGION", firstEnv("region")))),
		FlowObjectStoreAccessKey:   firstEnv("TSFLOW_S3_ACCESS_KEY_ID", "TAILSCALE_LOGS_S3_ACCESS_KEY", "AWS_ACCESS_KEY_ID"),
		FlowObjectStoreSecretKey:   firstEnv("TSFLOW_S3_SECRET_ACCESS_KEY", "TAILSCALE_LOGS_S3_SECRET_KEY", "AWS_SECRET_ACCESS_KEY"),
		FlowObjectStorePathStyle:   parseBool(getEnvWithDefault("TSFLOW_S3_PATH_STYLE", "true"), true),
		FlowObjectStoreLookback:    getEnvWithDefault("TSFLOW_S3_LOOKBACK", "15m"),
		FlowObjectStoreMaxObjects:  parsePositiveInt(getEnvWithDefault("TSFLOW_S3_MAX_OBJECTS_PER_POLL", "500")),
		PollInterval:               getEnvWithDefault("TSFLOW_POLL_INTERVAL", "5m"),
		InitialBackfill:            getEnvWithDefault("TSFLOW_INITIAL_BACKFILL", "6h"),
		Retention:                  getEnvWithFallback("TSFLOW_RETENTION"),
	}
}

// Validate validates the configuration
func (c *Config) Validate() error {
	hasAPIKey := c.TailscaleAPIKey != ""
	hasOAuth := c.TailscaleOAuthClientID != "" && c.TailscaleOAuthClientSecret != ""
	hasWIF := c.TsnetClientID != ""
	backend := strings.ToLower(strings.TrimSpace(c.FlowBackend))
	if backend != "" && backend != "api" && backend != "s3" {
		return errors.New("TSFLOW_FLOW_BACKEND must be either api or s3")
	}

	hasObjectCredentials := c.FlowObjectStoreBucket != "" &&
		c.FlowObjectStoreEndpoint != "" &&
		c.FlowObjectStoreAccessKey != "" &&
		c.FlowObjectStoreSecretKey != ""
	effectiveBackend := backend
	if effectiveBackend == "" {
		// Preserve the historical auto-detection behavior only when the
		// backend was not explicitly selected.
		if hasObjectCredentials {
			effectiveBackend = "s3"
		} else {
			effectiveBackend = "api"
		}
	}

	if effectiveBackend == "api" && !hasAPIKey && !hasOAuth {
		return errors.New("api flow backend requires TAILSCALE_API_KEY or both TAILSCALE_OAUTH_CLIENT_ID and TAILSCALE_OAUTH_CLIENT_SECRET")
	}
	if effectiveBackend == "s3" && !hasObjectCredentials {
		return errors.New("s3 flow backend requires TSFLOW_S3_BUCKET, TSFLOW_S3_ENDPOINT, TSFLOW_S3_ACCESS_KEY_ID, and TSFLOW_S3_SECRET_ACCESS_KEY")
	}
	if effectiveBackend == "s3" {
		parsedEndpoint, err := url.Parse(c.FlowObjectStoreEndpoint)
		if err != nil || parsedEndpoint.Scheme == "" || parsedEndpoint.Host == "" {
			return errors.New("TSFLOW_S3_ENDPOINT must be a valid absolute URL")
		}
		if parsedEndpoint.Scheme != "http" && parsedEndpoint.Scheme != "https" {
			return errors.New("TSFLOW_S3_ENDPOINT must use http or https")
		}
		if err := validateDuration("TSFLOW_S3_LOOKBACK", c.FlowObjectStoreLookback, false); err != nil {
			return err
		}
		if c.FlowObjectStoreMaxObjects <= 0 {
			return errors.New("TSFLOW_S3_MAX_OBJECTS_PER_POLL must be a positive integer")
		}
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
	if c.Retention != "" {
		if err := validateDuration("TSFLOW_RETENTION", c.Retention, true); err != nil {
			return err
		}
	}
	port, err := strconv.Atoi(c.Port)
	if err != nil || port < 1 || port > 65535 {
		return errors.New("PORT must be a number between 1 and 65535")
	}

	if hasAPIKey && hasOAuth {
		log.Println("Both API key and OAuth credentials provided. OAuth will take precedence.")
	}

	if c.TsnetServe {
		if !hasOAuth && !hasWIF {
			return errors.New("TSFLOW_SERVE=true requires either OAuth credentials or workload identity federation (TS_CLIENT_ID)")
		}
		if hasWIF {
			if c.TsnetIDToken == "" && c.TsnetAudience == "" {
				return errors.New("workload identity federation requires TS_ID_TOKEN or TS_AUDIENCE")
			}
			if c.TsnetIDToken != "" && c.TsnetAudience != "" {
				return errors.New("only one of TS_ID_TOKEN or TS_AUDIENCE should be set for workload identity federation")
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
