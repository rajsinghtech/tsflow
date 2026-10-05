package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/rajsinghtech/tsflow/backend/internal/database"
	"gopkg.in/yaml.v3"
)

// TailnetSpec is one resolved tailnet. Secrets have already been read from
// the environment or from files.
type TailnetSpec struct {
	ID                string
	Name              string
	APIURL            string
	APIKey            string
	OAuthClientID     string
	OAuthClientSecret string
	OAuthScopes       []string
	// AuthMode is api_key, oauth, wif, or empty when the entry has no API
	// credentials. Empty is only used for the single-tailnet env path when
	// no credential is configured.
	AuthMode string
	// WIF fields are set when AuthMode is wif. WIFIDToken is the token
	// captured at startup. WIFIDTokenFile is re-read when that token expires.
	WIFClientID    string
	WIFAudience    string
	WIFIDToken     string
	WIFIDTokenFile string
	// S3Prefix overrides the process object-store prefix when non-empty.
	S3Prefix string
}

// ServiceConfig returns the Tailscale client settings for this tailnet.
// Poll intervals and the object store stay on the process config.
func (s TailnetSpec) ServiceConfig(global *Config) *Config {
	apiURL := s.APIURL
	if apiURL == "" && global != nil {
		apiURL = global.TailscaleAPIURL
	}
	scopes := append([]string(nil), s.OAuthScopes...)
	if len(scopes) == 0 && global != nil {
		scopes = append([]string(nil), global.TailscaleOAuthScopes...)
	}
	return &Config{
		TailscaleAPIKey:            s.APIKey,
		TailscaleTailnet:           s.Name,
		TailscaleAPIURL:            apiURL,
		TailscaleOAuthClientID:     s.OAuthClientID,
		TailscaleOAuthClientSecret: s.OAuthClientSecret,
		TailscaleOAuthScopes:       scopes,
		TailscaleAuth:              s.AuthMode,
		TailscaleWIFClientID:       s.WIFClientID,
		TailscaleWIFAudience:       s.WIFAudience,
		TailscaleWIFIDToken:        s.WIFIDToken,
		TailscaleWIFIDTokenFile:    s.WIFIDTokenFile,
	}
}

// ResolveTailnets returns the tailnets this process should poll.
// With no TSFLOW_TAILNETS_FILE, the result is one entry with id default built
// from the single-tailnet environment variables.
func (c *Config) ResolveTailnets() ([]TailnetSpec, error) {
	if c == nil {
		return nil, fmt.Errorf("config is nil")
	}
	if strings.TrimSpace(c.TailnetsFile) == "" {
		spec, err := c.defaultTailnetSpec()
		if err != nil {
			return nil, err
		}
		return []TailnetSpec{spec}, nil
	}
	if c.singleTailnetSourceSet() {
		return nil, fmt.Errorf("TSFLOW_TAILNETS_FILE cannot be combined with TAILSCALE_TAILNET, TAILSCALE_API_KEY, or Tailscale OAuth client environment variables")
	}
	return parseTailnetsFile(c.TailnetsFile, c)
}

// singleTailnetSourceSet reports whether the process is also using the
// single-tailnet environment variables. TAILSCALE_TAILNET defaults to "-" in
// memory even when the variable is unset, so the name is checked from the
// environment. Credentials are checked from the loaded config, which already
// includes the VITE_ fallbacks.
func (c *Config) singleTailnetSourceSet() bool {
	if strings.TrimSpace(c.TailscaleAPIKey) != "" ||
		strings.TrimSpace(c.TailscaleOAuthClientID) != "" ||
		strings.TrimSpace(c.TailscaleOAuthClientSecret) != "" ||
		strings.TrimSpace(c.TailscaleAuth) != "" ||
		c.workloadIdentityFieldsSet() {
		return true
	}
	for _, key := range []string{"TAILSCALE_TAILNET", "VITE_TAILSCALE_TAILNET"} {
		if strings.TrimSpace(os.Getenv(key)) != "" {
			return true
		}
	}
	return false
}

func (c *Config) defaultTailnetSpec() (TailnetSpec, error) {
	mode, err := c.tailscaleAuthMode()
	if err != nil {
		return TailnetSpec{}, err
	}
	return TailnetSpec{
		ID:                database.DefaultTailnetID,
		Name:              c.TailscaleTailnet,
		APIURL:            c.TailscaleAPIURL,
		APIKey:            c.TailscaleAPIKey,
		OAuthClientID:     c.TailscaleOAuthClientID,
		OAuthClientSecret: c.TailscaleOAuthClientSecret,
		OAuthScopes:       append([]string(nil), c.TailscaleOAuthScopes...),
		AuthMode:          mode,
		WIFClientID:       c.TailscaleWIFClientID,
		WIFAudience:       c.TailscaleWIFAudience,
		WIFIDToken:        c.TailscaleWIFIDToken,
		WIFIDTokenFile:    c.TailscaleWIFIDTokenFile,
	}, nil
}

func parseTailnetsFile(path string, global *Config) ([]TailnetSpec, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("TSFLOW_TAILNETS_FILE: %w", err)
	}
	if len(data) > 1<<20 {
		return nil, fmt.Errorf("TSFLOW_TAILNETS_FILE %s is larger than 1MB", path)
	}
	entries, err := decodeTailnetEntries(path, data)
	if err != nil {
		return nil, fmt.Errorf("TSFLOW_TAILNETS_FILE: %w", err)
	}
	if len(entries) == 0 {
		return nil, fmt.Errorf("TSFLOW_TAILNETS_FILE %s does not list any tailnets", path)
	}

	specs := make([]TailnetSpec, 0, len(entries))
	seen := make(map[string]struct{}, len(entries))
	for i, entry := range entries {
		spec, err := entry.resolve(i, global)
		if err != nil {
			return nil, fmt.Errorf("TSFLOW_TAILNETS_FILE: %w", err)
		}
		if _, ok := seen[spec.ID]; ok {
			return nil, fmt.Errorf("TSFLOW_TAILNETS_FILE: duplicate tailnet id %q", spec.ID)
		}
		seen[spec.ID] = struct{}{}
		specs = append(specs, spec)
	}
	return specs, nil
}

type tailnetFileEntry struct {
	ID                    string
	Tailnet               string
	APIURL                string
	APIKeyEnv             string
	APIKeyFile            string
	OAuthClientIDEnv      string
	OAuthClientIDFile     string
	OAuthClientSecretEnv  string
	OAuthClientSecretFile string
	OAuthScopes           []string
	S3Prefix              string
	Auth                  *tailnetAuthBlock
}

type tailnetAuthBlock struct {
	Type             string
	ClientID         string
	ClientIDEnv      string
	ClientIDFile     string
	ClientSecretEnv  string
	ClientSecretFile string
	APIKeyEnv        string
	APIKeyFile       string
	Audience         string
	IDTokenEnv       string
	IDTokenFile      string
	Scopes           []string
	scopesSet        bool
}

func (e tailnetFileEntry) resolve(index int, global *Config) (TailnetSpec, error) {
	where := fmt.Sprintf("tailnet entry %d", index+1)
	id := strings.TrimSpace(e.ID)
	if id == "" {
		return TailnetSpec{}, fmt.Errorf("%s is missing id", where)
	}
	if strings.ContainsAny(id, " \t\r\n") {
		return TailnetSpec{}, fmt.Errorf("%s has an id with whitespace", where)
	}
	where = fmt.Sprintf("tailnet %q", id)
	name := strings.TrimSpace(e.Tailnet)
	if name == "" {
		return TailnetSpec{}, fmt.Errorf("%s is missing tailnet", where)
	}
	apiURL := strings.TrimSpace(e.APIURL)
	if apiURL == "" && global != nil {
		apiURL = global.TailscaleAPIURL
	}
	if apiURL != "" {
		parsed, err := url.Parse(apiURL)
		if err != nil || parsed.Scheme == "" || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
			return TailnetSpec{}, fmt.Errorf("%s api_url must be an absolute http or https URL", where)
		}
	}

	if e.Auth != nil {
		if e.hasFlatCredentials() {
			return TailnetSpec{}, fmt.Errorf("%s sets both an auth block and credential fields", where)
		}
		return e.resolveAuthBlock(where, id, name, apiURL, global)
	}

	apiKey, err := readSecret(where+" api key", e.APIKeyEnv, e.APIKeyFile)
	if err != nil {
		return TailnetSpec{}, err
	}
	oauthID, err := readSecret(where+" oauth client id", e.OAuthClientIDEnv, e.OAuthClientIDFile)
	if err != nil {
		return TailnetSpec{}, err
	}
	oauthSecret, err := readSecret(where+" oauth client secret", e.OAuthClientSecretEnv, e.OAuthClientSecretFile)
	if err != nil {
		return TailnetSpec{}, err
	}

	hasAPI := apiKey != ""
	hasOAuth := oauthID != "" || oauthSecret != ""
	if hasAPI && hasOAuth {
		return TailnetSpec{}, fmt.Errorf("%s sets both an API key and OAuth credentials", where)
	}
	if hasOAuth && (oauthID == "" || oauthSecret == "") {
		return TailnetSpec{}, fmt.Errorf("%s is missing oauth client id or secret", where)
	}
	if !hasAPI && !hasOAuth {
		return TailnetSpec{}, fmt.Errorf("%s is missing credentials", where)
	}

	mode := TailscaleAuthAPIKey
	if hasOAuth {
		mode = TailscaleAuthOAuth
	}
	scopes := e.scopesOrDefault(global)
	prefix := strings.TrimSpace(e.S3Prefix)
	return TailnetSpec{
		ID:                id,
		Name:              name,
		APIURL:            apiURL,
		APIKey:            apiKey,
		OAuthClientID:     oauthID,
		OAuthClientSecret: oauthSecret,
		OAuthScopes:       scopes,
		AuthMode:          mode,
		S3Prefix:          prefix,
	}, nil
}

func (e tailnetFileEntry) hasFlatCredentials() bool {
	return e.APIKeyEnv != "" || e.APIKeyFile != "" ||
		e.OAuthClientIDEnv != "" || e.OAuthClientIDFile != "" ||
		e.OAuthClientSecretEnv != "" || e.OAuthClientSecretFile != ""
}

func (e tailnetFileEntry) scopesOrDefault(global *Config) []string {
	scopes := append([]string(nil), e.OAuthScopes...)
	if len(scopes) == 0 && global != nil {
		scopes = append([]string(nil), global.TailscaleOAuthScopes...)
	}
	return scopes
}

func (e tailnetFileEntry) resolveAuthBlock(where, id, name, apiURL string, global *Config) (TailnetSpec, error) {
	block := e.Auth
	authType := strings.ToLower(strings.TrimSpace(block.Type))
	if block.scopesSet && len(e.OAuthScopes) > 0 {
		return TailnetSpec{}, fmt.Errorf("%s sets both oauth_scopes and auth.scopes", where)
	}
	scopes := append([]string(nil), block.Scopes...)
	if len(scopes) == 0 {
		scopes = e.scopesOrDefault(global)
	}
	spec := TailnetSpec{
		ID:          id,
		Name:        name,
		APIURL:      apiURL,
		OAuthScopes: scopes,
		AuthMode:    authType,
		S3Prefix:    strings.TrimSpace(e.S3Prefix),
	}
	switch authType {
	case TailscaleAuthAPIKey:
		if err := block.rejectNonAPIKeyFields(where); err != nil {
			return TailnetSpec{}, err
		}
		apiKey, err := readSecret(where+" api key", block.APIKeyEnv, block.APIKeyFile)
		if err != nil {
			return TailnetSpec{}, err
		}
		if apiKey == "" {
			return TailnetSpec{}, fmt.Errorf("%s is missing credentials", where)
		}
		spec.APIKey = apiKey
		return spec, nil
	case TailscaleAuthOAuth:
		if err := block.rejectNonOAuthFields(where); err != nil {
			return TailnetSpec{}, err
		}
		clientID, err := readSecret(where+" oauth client id", block.ClientIDEnv, block.ClientIDFile)
		if err != nil {
			return TailnetSpec{}, err
		}
		secret, err := readSecret(where+" oauth client secret", block.ClientSecretEnv, block.ClientSecretFile)
		if err != nil {
			return TailnetSpec{}, err
		}
		if clientID == "" || secret == "" {
			return TailnetSpec{}, fmt.Errorf("%s is missing oauth client id or secret", where)
		}
		spec.OAuthClientID = clientID
		spec.OAuthClientSecret = secret
		return spec, nil
	case TailscaleAuthWIF:
		if err := block.rejectNonWIFFields(where); err != nil {
			return TailnetSpec{}, err
		}
		clientID, err := resolveWIFClientID(where, block)
		if err != nil {
			return TailnetSpec{}, err
		}
		token, err := readSecret(where+" workload identity token", block.IDTokenEnv, "")
		if err != nil {
			return TailnetSpec{}, err
		}
		if err := validateWorkloadIdentity(where+" workload identity", clientID, block.Audience, token, block.IDTokenFile); err != nil {
			return TailnetSpec{}, err
		}
		spec.WIFClientID = clientID
		spec.WIFAudience = strings.TrimSpace(block.Audience)
		spec.WIFIDToken = token
		spec.WIFIDTokenFile = strings.TrimSpace(block.IDTokenFile)
		return spec, nil
	default:
		return TailnetSpec{}, fmt.Errorf("%s auth type must be oauth, api_key, or wif", where)
	}
}

func resolveWIFClientID(where string, block *tailnetAuthBlock) (string, error) {
	inline := strings.TrimSpace(block.ClientID)
	envName := strings.TrimSpace(block.ClientIDEnv)
	path := strings.TrimSpace(block.ClientIDFile)
	sources := 0
	if inline != "" {
		sources++
	}
	if envName != "" {
		sources++
	}
	if path != "" {
		sources++
	}
	if sources == 0 {
		return "", fmt.Errorf("%s workload identity requires client_id", where)
	}
	if sources != 1 {
		return "", fmt.Errorf("%s workload identity accepts only one client id source", where)
	}
	if inline != "" {
		return inline, nil
	}
	return readSecret(where+" workload identity client id", envName, path)
}

func (a *tailnetAuthBlock) rejectNonAPIKeyFields(where string) error {
	if a.ClientID != "" || a.ClientIDEnv != "" || a.ClientIDFile != "" ||
		a.ClientSecretEnv != "" || a.ClientSecretFile != "" ||
		a.Audience != "" || a.IDTokenEnv != "" || a.IDTokenFile != "" || a.scopesSet {
		return fmt.Errorf("%s auth type api_key only accepts api_key_env or api_key_file", where)
	}
	return nil
}

func (a *tailnetAuthBlock) rejectNonOAuthFields(where string) error {
	if a.ClientID != "" || a.APIKeyEnv != "" || a.APIKeyFile != "" ||
		a.Audience != "" || a.IDTokenEnv != "" || a.IDTokenFile != "" {
		return fmt.Errorf("%s auth type oauth does not accept api key, audience, or ID token fields", where)
	}
	return nil
}

func (a *tailnetAuthBlock) rejectNonWIFFields(where string) error {
	if a.APIKeyEnv != "" || a.APIKeyFile != "" || a.ClientSecretEnv != "" || a.ClientSecretFile != "" || a.scopesSet {
		return fmt.Errorf("%s auth type wif does not accept an API key, client secret, or scopes", where)
	}
	if strings.TrimSpace(a.IDTokenEnv) != "" && strings.TrimSpace(a.IDTokenFile) != "" {
		return fmt.Errorf("%s workload identity accepts only one of an ID token or an ID token file", where)
	}
	return nil
}

func readSecret(what, envName, path string) (string, error) {
	envName = strings.TrimSpace(envName)
	path = strings.TrimSpace(path)
	if envName != "" && path != "" {
		return "", fmt.Errorf("%s sets both an environment variable and a file", what)
	}
	if envName != "" {
		value := strings.TrimSpace(os.Getenv(envName))
		if value == "" {
			return "", fmt.Errorf("%s: environment variable %s is empty or unset", what, envName)
		}
		return value, nil
	}
	if path != "" {
		body, err := os.ReadFile(path)
		if err != nil {
			return "", fmt.Errorf("%s: %w", what, err)
		}
		value := strings.TrimSpace(string(body))
		if value == "" {
			return "", fmt.Errorf("%s: file %s is empty", what, path)
		}
		return value, nil
	}
	return "", nil
}

var tailnetEntryFields = map[string]struct{}{
	"id":                       {},
	"tailnet":                  {},
	"api_url":                  {},
	"api_key_env":              {},
	"api_key_file":             {},
	"oauth_client_id_env":      {},
	"oauth_client_id_file":     {},
	"oauth_client_secret_env":  {},
	"oauth_client_secret_file": {},
	"oauth_scopes":             {},
	"s3_prefix":                {},
	"auth":                     {},
}

var tailnetAuthFields = map[string]struct{}{
	"type":               {},
	"client_id":          {},
	"client_id_env":      {},
	"client_id_file":     {},
	"client_secret_env":  {},
	"client_secret_file": {},
	"api_key_env":        {},
	"api_key_file":       {},
	"audience":           {},
	"id_token_env":       {},
	"id_token_file":      {},
	"scopes":             {},
}

func decodeTailnetEntries(path string, data []byte) ([]tailnetFileEntry, error) {
	ext := strings.ToLower(filepath.Ext(path))
	var asJSON bool
	switch ext {
	case ".json":
		asJSON = true
	case ".yaml", ".yml":
		asJSON = false
	default:
		trimmed := bytes.TrimSpace(data)
		asJSON = len(trimmed) > 0 && (trimmed[0] == '{' || trimmed[0] == '[')
	}
	var decoded any
	if asJSON {
		dec := json.NewDecoder(bytes.NewReader(data))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&decoded); err != nil {
			return nil, err
		}
		var extra any
		if err := dec.Decode(&extra); err != nil && err != io.EOF {
			return nil, fmt.Errorf("extra data after JSON value")
		}
	} else {
		dec := yaml.NewDecoder(bytes.NewReader(data))
		dec.KnownFields(true)
		if err := dec.Decode(&decoded); err != nil {
			return nil, err
		}
	}
	return entriesFromDecoded(decoded)
}

func entriesFromDecoded(decoded any) ([]tailnetFileEntry, error) {
	switch doc := decoded.(type) {
	case []any:
		return entriesFromList(doc)
	case map[string]any:
		for key := range doc {
			if key != "tailnets" {
				return nil, fmt.Errorf("unknown field %q", key)
			}
		}
		raw, ok := doc["tailnets"]
		if !ok || raw == nil {
			return nil, fmt.Errorf("missing tailnets list")
		}
		list, ok := raw.([]any)
		if !ok {
			return nil, fmt.Errorf("tailnets must be a list")
		}
		return entriesFromList(list)
	default:
		return nil, fmt.Errorf("document must be a list or an object with a tailnets list")
	}
}

func entriesFromList(list []any) ([]tailnetFileEntry, error) {
	entries := make([]tailnetFileEntry, 0, len(list))
	for i, item := range list {
		fields, ok := item.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("tailnet entry %d must be an object", i+1)
		}
		entry, err := entryFromMap(fields, i)
		if err != nil {
			return nil, err
		}
		entries = append(entries, entry)
	}
	return entries, nil
}

func entryFromMap(fields map[string]any, index int) (tailnetFileEntry, error) {
	where := fmt.Sprintf("tailnet entry %d", index+1)
	for key := range fields {
		if _, ok := tailnetEntryFields[key]; !ok {
			return tailnetFileEntry{}, fmt.Errorf("%s: unknown field %q", where, key)
		}
	}
	var entry tailnetFileEntry
	var err error
	if entry.ID, err = optionalString(fields, "id", where); err != nil {
		return tailnetFileEntry{}, err
	}
	if entry.Tailnet, err = optionalString(fields, "tailnet", where); err != nil {
		return tailnetFileEntry{}, err
	}
	if entry.APIURL, err = optionalString(fields, "api_url", where); err != nil {
		return tailnetFileEntry{}, err
	}
	if entry.APIKeyEnv, err = optionalString(fields, "api_key_env", where); err != nil {
		return tailnetFileEntry{}, err
	}
	if entry.APIKeyFile, err = optionalString(fields, "api_key_file", where); err != nil {
		return tailnetFileEntry{}, err
	}
	if entry.OAuthClientIDEnv, err = optionalString(fields, "oauth_client_id_env", where); err != nil {
		return tailnetFileEntry{}, err
	}
	if entry.OAuthClientIDFile, err = optionalString(fields, "oauth_client_id_file", where); err != nil {
		return tailnetFileEntry{}, err
	}
	if entry.OAuthClientSecretEnv, err = optionalString(fields, "oauth_client_secret_env", where); err != nil {
		return tailnetFileEntry{}, err
	}
	if entry.OAuthClientSecretFile, err = optionalString(fields, "oauth_client_secret_file", where); err != nil {
		return tailnetFileEntry{}, err
	}
	if raw, ok := fields["oauth_scopes"]; ok {
		entry.OAuthScopes, err = parseScopeField(raw, where+" oauth_scopes")
		if err != nil {
			return tailnetFileEntry{}, err
		}
	}
	if raw, ok := fields["s3_prefix"]; ok {
		prefix, err := stringValue(raw, where+" s3_prefix")
		if err != nil {
			return tailnetFileEntry{}, err
		}
		entry.S3Prefix = prefix
	}
	if raw, ok := fields["auth"]; ok && raw != nil {
		block, err := parseAuthBlock(raw, where)
		if err != nil {
			return tailnetFileEntry{}, err
		}
		entry.Auth = &block
	}
	return entry, nil
}

func parseAuthBlock(raw any, where string) (tailnetAuthBlock, error) {
	fields, ok := raw.(map[string]any)
	if !ok {
		return tailnetAuthBlock{}, fmt.Errorf("%s auth must be an object", where)
	}
	for key := range fields {
		if _, known := tailnetAuthFields[key]; !known {
			return tailnetAuthBlock{}, fmt.Errorf("%s auth: unknown field %q", where, key)
		}
	}
	var block tailnetAuthBlock
	var err error
	if block.Type, err = optionalString(fields, "type", where+" auth"); err != nil {
		return tailnetAuthBlock{}, err
	}
	if block.ClientID, err = optionalString(fields, "client_id", where+" auth"); err != nil {
		return tailnetAuthBlock{}, err
	}
	if block.ClientIDEnv, err = optionalString(fields, "client_id_env", where+" auth"); err != nil {
		return tailnetAuthBlock{}, err
	}
	if block.ClientIDFile, err = optionalString(fields, "client_id_file", where+" auth"); err != nil {
		return tailnetAuthBlock{}, err
	}
	if block.ClientSecretEnv, err = optionalString(fields, "client_secret_env", where+" auth"); err != nil {
		return tailnetAuthBlock{}, err
	}
	if block.ClientSecretFile, err = optionalString(fields, "client_secret_file", where+" auth"); err != nil {
		return tailnetAuthBlock{}, err
	}
	if block.APIKeyEnv, err = optionalString(fields, "api_key_env", where+" auth"); err != nil {
		return tailnetAuthBlock{}, err
	}
	if block.APIKeyFile, err = optionalString(fields, "api_key_file", where+" auth"); err != nil {
		return tailnetAuthBlock{}, err
	}
	if block.Audience, err = optionalString(fields, "audience", where+" auth"); err != nil {
		return tailnetAuthBlock{}, err
	}
	if block.IDTokenEnv, err = optionalString(fields, "id_token_env", where+" auth"); err != nil {
		return tailnetAuthBlock{}, err
	}
	if block.IDTokenFile, err = optionalString(fields, "id_token_file", where+" auth"); err != nil {
		return tailnetAuthBlock{}, err
	}
	if raw, ok := fields["scopes"]; ok {
		block.Scopes, err = parseScopeField(raw, where+" auth.scopes")
		if err != nil {
			return tailnetAuthBlock{}, err
		}
		block.scopesSet = true
	}
	return block, nil
}

func optionalString(fields map[string]any, key, where string) (string, error) {
	raw, ok := fields[key]
	if !ok || raw == nil {
		return "", nil
	}
	return stringValue(raw, where+" "+key)
}

func stringValue(raw any, what string) (string, error) {
	value, ok := raw.(string)
	if !ok {
		return "", fmt.Errorf("%s must be a string", what)
	}
	return value, nil
}

func parseScopeField(raw any, what string) ([]string, error) {
	switch value := raw.(type) {
	case string:
		scopes := splitList(value)
		if len(scopes) == 0 {
			return nil, fmt.Errorf("%s is empty", what)
		}
		return scopes, nil
	case []any:
		scopes := make([]string, 0, len(value))
		for _, item := range value {
			text, ok := item.(string)
			if !ok {
				return nil, fmt.Errorf("%s must be strings", what)
			}
			text = strings.TrimSpace(text)
			if text == "" {
				continue
			}
			scopes = append(scopes, text)
		}
		if len(scopes) == 0 {
			return nil, fmt.Errorf("%s is empty", what)
		}
		return scopes, nil
	default:
		return nil, fmt.Errorf("%s must be a string or a list", what)
	}
}

func splitList(value string) []string {
	var items []string
	for _, part := range strings.Split(value, ",") {
		if part = strings.TrimSpace(part); part != "" {
			items = append(items, part)
		}
	}
	return items
}
