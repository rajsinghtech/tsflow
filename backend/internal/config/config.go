package config

import (
	"errors"
	"log"
	"os"
	"path/filepath"
	"strings"
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
		TsnetServe:                 os.Getenv("TSFLOW_SERVE") == "true",
		TsnetHostname:              getEnvWithDefault("TSFLOW_HOSTNAME", "tsflow"),
		TsnetTags:                  parseTags(os.Getenv("TSFLOW_TAGS")),
		TsnetFunnel:                os.Getenv("TSFLOW_FUNNEL") == "true",
		TsnetStateDir:              getEnvWithDefault("TSFLOW_STATE_DIR", filepath.Join(".", "data", "tsnet-state")),
		TsnetClientID:              os.Getenv("TS_CLIENT_ID"),
		TsnetIDToken:               os.Getenv("TS_ID_TOKEN"),
		TsnetAudience:              os.Getenv("TS_AUDIENCE"),
	}
}

// Validate validates the configuration
func (c *Config) Validate() error {
	hasAPIKey := c.TailscaleAPIKey != ""
	hasOAuth := c.TailscaleOAuthClientID != "" && c.TailscaleOAuthClientSecret != ""
	hasWIF := c.TsnetClientID != ""

	if !hasAPIKey && !hasOAuth {
		return errors.New("either TAILSCALE_API_KEY or both TAILSCALE_OAUTH_CLIENT_ID and TAILSCALE_OAUTH_CLIENT_SECRET must be provided")
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

// parseScopes parses a comma-separated string of OAuth scopes
func parseScopes(scopesStr string) []string {
	if scopesStr == "" {
		return []string{"all:read"}
	}
	scopes := strings.Split(scopesStr, ",")
	for i, scope := range scopes {
		scopes[i] = strings.TrimSpace(scope)
	}
	return scopes
}

// parseTags parses a comma-separated string of ACL tags
func parseTags(tagsStr string) []string {
	if tagsStr == "" {
		return nil
	}
	tags := strings.Split(tagsStr, ",")
	for i, tag := range tags {
		tags[i] = strings.TrimSpace(tag)
	}
	return tags
}

// parseCORSOrigins parses a comma-separated string of allowed CORS origins
// Returns nil to indicate all origins allowed (for development)
func parseCORSOrigins(originsStr string) []string {
	if originsStr == "" {
		return nil // Allow all origins when not specified
	}
	origins := strings.Split(originsStr, ",")
	for i, origin := range origins {
		origins[i] = strings.TrimSpace(origin)
	}
	return origins
}
