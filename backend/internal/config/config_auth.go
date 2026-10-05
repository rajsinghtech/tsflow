package config

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
)

const (
	// TailscaleAuthOAuth, TailscaleAuthAPIKey, and TailscaleAuthWIF are the
	// API credential modes. Empty means the historical resolution: OAuth when
	// both client fields are set, otherwise an API key.
	TailscaleAuthOAuth  = "oauth"
	TailscaleAuthAPIKey = "api_key"
	TailscaleAuthWIF    = "wif"

	FlowBackendAPI = "api"
	FlowBackendS3  = "s3"
	FlowBackendGCS = "gcs"

	// ObjectStoreAuthStatic is access key plus secret. ObjectStoreAuthAWSDefault
	// is the AWS SDK default credential chain. ObjectStoreAuthGCSADC is reserved
	// for a native GCS client and is rejected at startup.
	ObjectStoreAuthStatic     = "static"
	ObjectStoreAuthAWSDefault = "aws_default"
	ObjectStoreAuthGCSADC     = "gcs_adc"
)

// EffectiveFlowBackend resolves TSFLOW_FLOW_BACKEND and TSFLOW_S3_AUTH.
// An unset backend stays on the historical auto-detection path unless
// aws_default is selected explicitly.
func (c *Config) EffectiveFlowBackend() (string, error) {
	if c == nil {
		return "", fmt.Errorf("config is nil")
	}
	backend := strings.ToLower(strings.TrimSpace(c.FlowBackend))
	auth := c.objectStoreAuth()
	switch backend {
	case "", FlowBackendAPI, FlowBackendS3, FlowBackendGCS:
	default:
		return "", errors.New("TSFLOW_FLOW_BACKEND must be api, s3, or gcs")
	}
	switch auth {
	case "", ObjectStoreAuthStatic, ObjectStoreAuthAWSDefault, ObjectStoreAuthGCSADC:
	default:
		return "", errors.New("TSFLOW_S3_AUTH must be static, aws_default, or gcs_adc")
	}
	if backend == FlowBackendGCS || auth == ObjectStoreAuthGCSADC {
		if backend == FlowBackendS3 {
			return "", errors.New("TSFLOW_S3_AUTH=gcs_adc uses the native GCS reader; set TSFLOW_FLOW_BACKEND=gcs")
		}
		if backend == FlowBackendAPI {
			return "", errors.New("TSFLOW_S3_AUTH=gcs_adc requires TSFLOW_FLOW_BACKEND=gcs")
		}
		if auth != "" && auth != ObjectStoreAuthGCSADC {
			return "", errors.New("TSFLOW_FLOW_BACKEND=gcs requires TSFLOW_S3_AUTH=gcs_adc or an empty auth mode")
		}
		return FlowBackendGCS, nil
	}
	if auth == ObjectStoreAuthAWSDefault {
		if backend == FlowBackendAPI {
			return "", errors.New("TSFLOW_S3_AUTH=aws_default requires TSFLOW_FLOW_BACKEND=s3")
		}
		return FlowBackendS3, nil
	}
	if backend == "" {
		if c.hasStaticObjectStoreCredentials() {
			return FlowBackendS3, nil
		}
		return FlowBackendAPI, nil
	}
	return backend, nil
}

func (c *Config) objectStoreAuth() string {
	if c == nil {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(c.FlowObjectStoreAuth))
}

func (c *Config) hasStaticObjectStoreCredentials() bool {
	return c.FlowObjectStoreBucket != "" &&
		c.FlowObjectStoreEndpoint != "" &&
		c.FlowObjectStoreAccessKey != "" &&
		c.FlowObjectStoreSecretKey != ""
}

// tailscaleAuthMode resolves the single-tailnet API credential mode.
// The returned mode is oauth, api_key, wif, or empty when this process has
// no API credentials. WIF is returned only when TAILSCALE_AUTH=wif.
func (c *Config) tailscaleAuthMode() (string, error) {
	if c == nil {
		return "", fmt.Errorf("config is nil")
	}
	mode := strings.ToLower(strings.TrimSpace(c.TailscaleAuth))
	wifFields := c.workloadIdentityFieldsSet()
	switch mode {
	case "":
		if wifFields {
			return "", errors.New("workload identity federation fields require TAILSCALE_AUTH=wif")
		}
		if c.TailscaleOAuthClientID != "" && c.TailscaleOAuthClientSecret != "" {
			return TailscaleAuthOAuth, nil
		}
		if c.TailscaleAPIKey != "" {
			return TailscaleAuthAPIKey, nil
		}
		return "", nil
	case TailscaleAuthOAuth:
		if c.TailscaleOAuthClientID == "" || c.TailscaleOAuthClientSecret == "" {
			return "", errors.New("TAILSCALE_AUTH=oauth requires TAILSCALE_OAUTH_CLIENT_ID and TAILSCALE_OAUTH_CLIENT_SECRET")
		}
		if c.TailscaleAPIKey != "" {
			return "", errors.New("TAILSCALE_AUTH=oauth cannot be combined with TAILSCALE_API_KEY")
		}
		if wifFields {
			return "", errors.New("TAILSCALE_AUTH=oauth cannot be combined with workload identity federation fields")
		}
		return mode, nil
	case TailscaleAuthAPIKey:
		if c.TailscaleAPIKey == "" {
			return "", errors.New("TAILSCALE_AUTH=api_key requires TAILSCALE_API_KEY")
		}
		if c.TailscaleOAuthClientID != "" || c.TailscaleOAuthClientSecret != "" {
			return "", errors.New("TAILSCALE_AUTH=api_key cannot be combined with Tailscale OAuth client credentials")
		}
		if wifFields {
			return "", errors.New("TAILSCALE_AUTH=api_key cannot be combined with workload identity federation fields")
		}
		return mode, nil
	case TailscaleAuthWIF:
		if c.TailscaleAPIKey != "" || c.TailscaleOAuthClientID != "" || c.TailscaleOAuthClientSecret != "" {
			return "", errors.New("TAILSCALE_AUTH=wif cannot be combined with an API key or OAuth client")
		}
		if err := validateWorkloadIdentity("TAILSCALE_AUTH=wif", c.TailscaleWIFClientID, c.TailscaleWIFAudience, c.TailscaleWIFIDToken, c.TailscaleWIFIDTokenFile); err != nil {
			return "", err
		}
		return mode, nil
	default:
		return "", errors.New("TAILSCALE_AUTH must be oauth, api_key, or wif")
	}
}

func (c *Config) workloadIdentityFieldsSet() bool {
	return strings.TrimSpace(c.TailscaleWIFClientID) != "" ||
		strings.TrimSpace(c.TailscaleWIFAudience) != "" ||
		strings.TrimSpace(c.TailscaleWIFIDToken) != "" ||
		strings.TrimSpace(c.TailscaleWIFIDTokenFile) != ""
}

// validateWorkloadIdentity checks one tailnet's WIF material.
// The token comes from idToken, idTokenFile, or audience alone. Audience may
// also be set next to a token or a file. In that case the token is exchanged
// and its aud claim must include the audience. File and inline token together
// are rejected.
func validateWorkloadIdentity(where, clientID, audience, idToken, idTokenFile string) error {
	clientID = strings.TrimSpace(clientID)
	audience = strings.TrimSpace(audience)
	idToken = strings.TrimSpace(idToken)
	idTokenFile = strings.TrimSpace(idTokenFile)
	if clientID == "" {
		return fmt.Errorf("%s requires a workload identity client id", where)
	}
	sources := 0
	if idToken != "" {
		sources++
	}
	if idTokenFile != "" {
		sources++
	}
	if sources > 1 {
		return fmt.Errorf("%s accepts only one of an ID token or an ID token file", where)
	}
	if sources == 0 && audience == "" {
		return fmt.Errorf("%s requires one of an ID token, an ID token file, or an audience", where)
	}
	if idTokenFile != "" {
		body, err := os.ReadFile(idTokenFile)
		if err != nil {
			return fmt.Errorf("%s ID token file: %w", where, err)
		}
		idToken = strings.TrimSpace(string(body))
		if idToken == "" {
			return fmt.Errorf("%s ID token file %s is empty", where, idTokenFile)
		}
	}
	if audience != "" && sources > 0 {
		if err := tokenHasAudience(idToken, audience); err != nil {
			return fmt.Errorf("%s %w", where, err)
		}
	}
	return nil
}

func tokenHasAudience(token, audience string) error {
	audiences, err := jwtAudiences(token)
	if err != nil {
		return err
	}
	for _, got := range audiences {
		if got == audience {
			return nil
		}
	}
	return fmt.Errorf("ID token audience does not include %s", audience)
}

func jwtAudiences(token string) ([]string, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, fmt.Errorf("ID token is not a JWT")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, fmt.Errorf("ID token payload is not valid base64")
	}
	var claims struct {
		Aud json.RawMessage `json:"aud"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return nil, fmt.Errorf("ID token claims are not valid JSON")
	}
	if len(claims.Aud) == 0 {
		return nil, fmt.Errorf("ID token is missing aud")
	}
	var one string
	if err := json.Unmarshal(claims.Aud, &one); err == nil && one != "" {
		return []string{one}, nil
	}
	var many []string
	if err := json.Unmarshal(claims.Aud, &many); err != nil || len(many) == 0 {
		return nil, fmt.Errorf("ID token aud must be a string or a list of strings")
	}
	return many, nil
}

func staticObjectStoreEnvSet() bool {
	for _, key := range []string{
		"TSFLOW_S3_ACCESS_KEY_ID",
		"TSFLOW_S3_SECRET_ACCESS_KEY",
		"TAILSCALE_LOGS_S3_ACCESS_KEY",
		"TAILSCALE_LOGS_S3_SECRET_KEY",
	} {
		if strings.TrimSpace(os.Getenv(key)) != "" {
			return true
		}
	}
	return false
}

func lookupNonEmpty(key string) (string, bool) {
	if value, ok := os.LookupEnv(key); ok && strings.TrimSpace(value) != "" {
		return strings.TrimSpace(value), true
	}
	if value, ok := os.LookupEnv("VITE_" + key); ok && strings.TrimSpace(value) != "" {
		return strings.TrimSpace(value), true
	}
	return "", false
}
