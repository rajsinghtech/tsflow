package config

import (
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testJWT(exp int64, audience string) string {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none","typ":"JWT"}`))
	payload := base64.RawURLEncoding.EncodeToString([]byte(fmt.Sprintf(`{"exp":%d,"aud":%q}`, exp, audience)))
	sig := base64.RawURLEncoding.EncodeToString([]byte("sig"))
	return header + "." + payload + "." + sig
}

func clearAuthEnv(t *testing.T) {
	t.Helper()
	for _, key := range []string{
		"TAILSCALE_API_KEY", "VITE_TAILSCALE_API_KEY",
		"TAILSCALE_OAUTH_CLIENT_ID", "VITE_TAILSCALE_OAUTH_CLIENT_ID",
		"TAILSCALE_OAUTH_CLIENT_SECRET", "VITE_TAILSCALE_OAUTH_CLIENT_SECRET",
		"TAILSCALE_TAILNET", "VITE_TAILSCALE_TAILNET",
		"TAILSCALE_AUTH", "VITE_TAILSCALE_AUTH",
		"TAILSCALE_WIF_CLIENT_ID", "TAILSCALE_WIF_AUDIENCE",
		"TAILSCALE_WIF_ID_TOKEN", "TAILSCALE_WIF_ID_TOKEN_FILE",
		"TS_CLIENT_ID", "TS_ID_TOKEN", "TS_AUDIENCE",
		"TSFLOW_S3_AUTH", "TSFLOW_S3_ROLE_ARN",
		"TSFLOW_S3_ACCESS_KEY_ID", "TSFLOW_S3_SECRET_ACCESS_KEY",
		"TAILSCALE_LOGS_S3_ACCESS_KEY", "TAILSCALE_LOGS_S3_SECRET_KEY",
		"TSFLOW_S3_PATH_STYLE", "TSFLOW_S3_REGION", "TSFLOW_S3_ENDPOINT", "TSFLOW_S3_BUCKET",
		"AWS_REGION", "AWS_DEFAULT_REGION", "AWS_ENDPOINT_URL",
		"TSFLOW_FLOW_BACKEND",
	} {
		t.Setenv(key, "")
	}
}

func TestExistingAuthModesResolveUnchanged(t *testing.T) {
	t.Run("oauth", func(t *testing.T) {
		clearAuthEnv(t)
		t.Setenv("TAILSCALE_OAUTH_CLIENT_ID", "client-id")
		t.Setenv("TAILSCALE_OAUTH_CLIENT_SECRET", "client-secret")
		t.Setenv("TAILSCALE_OAUTH_SCOPES", "all:read")
		t.Setenv("TS_CLIENT_ID", "tsnet-client")
		t.Setenv("TS_ID_TOKEN", "tsnet-token")
		t.Setenv("TSFLOW_TAGS", "tag:tsflow")
		t.Setenv("TSFLOW_SERVE", "true")

		cfg := Load()
		if cfg.TailscaleAuth != "" || cfg.TailscaleOAuthClientID != "client-id" || cfg.TailscaleOAuthClientSecret != "client-secret" {
			t.Fatalf("oauth config = %+v", cfg)
		}
		if cfg.TsnetClientID != "tsnet-client" || cfg.TsnetIDToken != "tsnet-token" || cfg.TsnetAudience != "" {
			t.Fatalf("tsnet wif = client %q token %q audience %q", cfg.TsnetClientID, cfg.TsnetIDToken, cfg.TsnetAudience)
		}
		if cfg.FlowObjectStoreAuth != "" {
			t.Fatalf("object store auth = %q", cfg.FlowObjectStoreAuth)
		}
		if err := cfg.Validate(); err != nil {
			t.Fatal(err)
		}
		specs, err := cfg.ResolveTailnets()
		if err != nil {
			t.Fatal(err)
		}
		if len(specs) != 1 || specs[0].AuthMode != TailscaleAuthOAuth || specs[0].APIKey != "" || specs[0].OAuthClientID != "client-id" {
			t.Fatalf("spec = %+v", specs)
		}
	})

	t.Run("api key", func(t *testing.T) {
		clearAuthEnv(t)
		t.Setenv("TAILSCALE_API_KEY", "tskey-test")
		t.Setenv("TAILSCALE_TAILNET", "example.com")
		cfg := Load()
		if cfg.TailscaleAuth != "" || cfg.TailscaleAPIKey != "tskey-test" || cfg.TailscaleTailnet != "example.com" {
			t.Fatalf("api key config = %+v", cfg)
		}
		specs, err := cfg.ResolveTailnets()
		if err != nil {
			t.Fatal(err)
		}
		if specs[0].AuthMode != TailscaleAuthAPIKey || specs[0].APIKey != "tskey-test" || specs[0].WIFClientID != "" {
			t.Fatalf("spec = %+v", specs[0])
		}
		if err := cfg.Validate(); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("static s3 keys", func(t *testing.T) {
		clearAuthEnv(t)
		t.Setenv("TSFLOW_FLOW_BACKEND", "s3")
		t.Setenv("TSFLOW_S3_BUCKET", "flows")
		t.Setenv("TSFLOW_S3_ENDPOINT", "http://object-store.test")
		t.Setenv("TSFLOW_S3_ACCESS_KEY_ID", "access")
		t.Setenv("TSFLOW_S3_SECRET_ACCESS_KEY", "secret")
		t.Setenv("TSFLOW_S3_REGION", "garage")
		cfg := Load()
		if cfg.FlowObjectStoreAuth != "" || cfg.FlowObjectStoreAccessKey != "access" || cfg.FlowObjectStoreSecretKey != "secret" {
			t.Fatalf("s3 config = %+v", cfg)
		}
		if !cfg.FlowObjectStorePathStyle {
			t.Fatal("static object store should keep path style")
		}
		if err := cfg.Validate(); err != nil {
			t.Fatal(err)
		}
		backend, err := cfg.EffectiveFlowBackend()
		if err != nil || backend != FlowBackendS3 {
			t.Fatalf("backend = %s, %v", backend, err)
		}
	})

	t.Run("tsnet audience", func(t *testing.T) {
		clearAuthEnv(t)
		t.Setenv("TAILSCALE_OAUTH_CLIENT_ID", "client-id")
		t.Setenv("TAILSCALE_OAUTH_CLIENT_SECRET", "client-secret")
		t.Setenv("TS_CLIENT_ID", "tsnet-client")
		t.Setenv("TS_AUDIENCE", "api.tailscale.com/tsnet-client")
		t.Setenv("TSFLOW_TAGS", "tag:tsflow")
		t.Setenv("TSFLOW_SERVE", "true")
		cfg := Load()
		if cfg.TsnetClientID != "tsnet-client" || cfg.TsnetIDToken != "" || cfg.TsnetAudience != "api.tailscale.com/tsnet-client" {
			t.Fatalf("tsnet audience config = %+v", cfg)
		}
		if cfg.TailscaleAuth != "" || cfg.TailscaleWIFAudience != "" {
			t.Fatalf("tsnet audience leaked into API auth: %+v", cfg)
		}
		if err := cfg.Validate(); err != nil {
			t.Fatal(err)
		}
	})
}

func TestWorkloadIdentityEnv(t *testing.T) {
	tokenPath := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(tokenPath, []byte(" header.payload.sig \n"), 0o600); err != nil {
		t.Fatal(err)
	}

	t.Run("id token file", func(t *testing.T) {
		clearAuthEnv(t)
		t.Setenv("TAILSCALE_AUTH", "wif")
		t.Setenv("TAILSCALE_WIF_CLIENT_ID", "fed-client")
		t.Setenv("TAILSCALE_WIF_ID_TOKEN_FILE", tokenPath)
		cfg := Load()
		if err := cfg.Validate(); err != nil {
			t.Fatal(err)
		}
		specs, err := cfg.ResolveTailnets()
		if err != nil {
			t.Fatal(err)
		}
		got := specs[0]
		if got.AuthMode != TailscaleAuthWIF || got.WIFClientID != "fed-client" || got.WIFIDTokenFile != tokenPath || got.WIFIDToken != "" || got.APIKey != "" {
			t.Fatalf("spec = %+v", got)
		}
		serviceCfg := got.ServiceConfig(cfg)
		if serviceCfg.TailscaleAuth != TailscaleAuthWIF || serviceCfg.TailscaleWIFClientID != "fed-client" || serviceCfg.TailscaleWIFIDTokenFile != tokenPath {
			t.Fatalf("service config = %+v", serviceCfg)
		}
	})

	t.Run("id token value", func(t *testing.T) {
		clearAuthEnv(t)
		t.Setenv("TAILSCALE_AUTH", "WIF")
		t.Setenv("TAILSCALE_WIF_CLIENT_ID", "fed-client")
		t.Setenv("TAILSCALE_WIF_ID_TOKEN", "header.payload.sig")
		cfg := Load()
		if cfg.TailscaleAuth != TailscaleAuthWIF {
			t.Fatalf("auth = %q", cfg.TailscaleAuth)
		}
		if err := cfg.Validate(); err != nil {
			t.Fatal(err)
		}
		specs, err := cfg.ResolveTailnets()
		if err != nil {
			t.Fatal(err)
		}
		if specs[0].WIFIDToken != "header.payload.sig" || specs[0].WIFIDTokenFile != "" {
			t.Fatalf("spec = %+v", specs[0])
		}
	})

	t.Run("file and audience", func(t *testing.T) {
		clearAuthEnv(t)
		audience := "api.tailscale.com/fed-client"
		tokenPath := filepath.Join(t.TempDir(), "projected")
		if err := os.WriteFile(tokenPath, []byte(testJWT(time.Now().Add(time.Hour).Unix(), audience)), 0o600); err != nil {
			t.Fatal(err)
		}
		t.Setenv("TAILSCALE_AUTH", "wif")
		t.Setenv("TAILSCALE_WIF_CLIENT_ID", "fed-client")
		t.Setenv("TAILSCALE_WIF_AUDIENCE", audience)
		t.Setenv("TAILSCALE_WIF_ID_TOKEN_FILE", tokenPath)
		cfg := Load()
		if err := cfg.Validate(); err != nil {
			t.Fatal(err)
		}
		specs, err := cfg.ResolveTailnets()
		if err != nil {
			t.Fatal(err)
		}
		if specs[0].WIFAudience != audience || specs[0].WIFIDTokenFile != tokenPath || specs[0].AuthMode != TailscaleAuthWIF {
			t.Fatalf("spec = %+v", specs[0])
		}
	})

	t.Run("audience", func(t *testing.T) {
		clearAuthEnv(t)
		t.Setenv("TAILSCALE_AUTH", "wif")
		t.Setenv("TAILSCALE_WIF_CLIENT_ID", "fed-client")
		t.Setenv("TAILSCALE_WIF_AUDIENCE", "api.tailscale.com/fed-client")
		cfg := Load()
		if err := cfg.Validate(); err != nil {
			t.Fatal(err)
		}
		specs, err := cfg.ResolveTailnets()
		if err != nil {
			t.Fatal(err)
		}
		if specs[0].WIFAudience != "api.tailscale.com/fed-client" || specs[0].WIFIDToken != "" {
			t.Fatalf("spec = %+v", specs[0])
		}
	})

	cases := []struct {
		name   string
		set    func()
		mutate func(*Config)
		want   string
	}{
		{
			name: "fields without mode",
			set: func() {
				t.Setenv("TAILSCALE_WIF_CLIENT_ID", "fed-client")
				t.Setenv("TAILSCALE_WIF_AUDIENCE", "api.tailscale.com/fed-client")
			},
			want: "TAILSCALE_AUTH=wif",
		},
		{
			name: "missing client id",
			set: func() {
				t.Setenv("TAILSCALE_AUTH", "wif")
				t.Setenv("TAILSCALE_WIF_AUDIENCE", "api.tailscale.com/fed-client")
			},
			want: "client id",
		},
		{
			name: "missing token source",
			set: func() {
				t.Setenv("TAILSCALE_AUTH", "wif")
				t.Setenv("TAILSCALE_WIF_CLIENT_ID", "fed-client")
			},
			want: "one of an ID token",
		},
		{
			name: "two token sources",
			set: func() {
				t.Setenv("TAILSCALE_AUTH", "wif")
				t.Setenv("TAILSCALE_WIF_CLIENT_ID", "fed-client")
				t.Setenv("TAILSCALE_WIF_ID_TOKEN", "header.payload.sig")
				t.Setenv("TAILSCALE_WIF_ID_TOKEN_FILE", tokenPath)
			},
			want: "only one",
		},
		{
			name: "token audience mismatch",
			set: func() {
				t.Setenv("TAILSCALE_AUTH", "wif")
				t.Setenv("TAILSCALE_WIF_CLIENT_ID", "fed-client")
				t.Setenv("TAILSCALE_WIF_ID_TOKEN", testJWT(time.Now().Add(time.Hour).Unix(), "other-audience"))
				t.Setenv("TAILSCALE_WIF_AUDIENCE", "api.tailscale.com/fed-client")
			},
			want: "audience does not include",
		},
		{
			name: "combined with api key",
			set: func() {
				t.Setenv("TAILSCALE_AUTH", "wif")
				t.Setenv("TAILSCALE_API_KEY", "tskey-test")
				t.Setenv("TAILSCALE_WIF_CLIENT_ID", "fed-client")
				t.Setenv("TAILSCALE_WIF_AUDIENCE", "api.tailscale.com/fed-client")
			},
			want: "cannot be combined",
		},
		{
			name: "unknown mode",
			set: func() {
				t.Setenv("TAILSCALE_AUTH", "saml")
				t.Setenv("TAILSCALE_API_KEY", "tskey-test")
			},
			want: "TAILSCALE_AUTH must be oauth, api_key, or wif",
		},
		{
			name: "empty token file",
			set: func() {
				empty := filepath.Join(t.TempDir(), "empty")
				if err := os.WriteFile(empty, []byte(" \n"), 0o600); err != nil {
					t.Fatal(err)
				}
				t.Setenv("TAILSCALE_AUTH", "wif")
				t.Setenv("TAILSCALE_WIF_CLIENT_ID", "fed-client")
				t.Setenv("TAILSCALE_WIF_ID_TOKEN_FILE", empty)
			},
			want: "empty",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			clearAuthEnv(t)
			tc.set()
			cfg := Load()
			err := cfg.Validate()
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Validate() = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestObjectStoreAuthModes(t *testing.T) {
	t.Run("native gcs", func(t *testing.T) {
		clearAuthEnv(t)
		t.Setenv("TAILSCALE_API_KEY", "tskey-test")
		t.Setenv("TSFLOW_FLOW_BACKEND", "gcs")
		t.Setenv("TSFLOW_S3_BUCKET", "flow-logs")
		cfg := Load()
		if err := cfg.Validate(); err != nil {
			t.Fatal(err)
		}
		backend, err := cfg.EffectiveFlowBackend()
		if err != nil || backend != FlowBackendGCS {
			t.Fatalf("backend = %s, %v", backend, err)
		}
		if cfg.FlowObjectStoreEndpoint != "" || cfg.FlowObjectStoreAccessKey != "" {
			t.Fatalf("gcs config picked up static s3 settings: %+v", cfg)
		}
	})

	t.Run("gcs adc implies native reader", func(t *testing.T) {
		clearAuthEnv(t)
		t.Setenv("TAILSCALE_API_KEY", "tskey-test")
		t.Setenv("TSFLOW_S3_AUTH", "gcs_adc")
		t.Setenv("TSFLOW_S3_BUCKET", "flow-logs")
		cfg := Load()
		if err := cfg.Validate(); err != nil {
			t.Fatal(err)
		}
		backend, err := cfg.EffectiveFlowBackend()
		if err != nil || backend != FlowBackendGCS || cfg.FlowObjectStoreAuth != ObjectStoreAuthGCSADC {
			t.Fatalf("backend %s auth %s err %v", backend, cfg.FlowObjectStoreAuth, err)
		}
	})

	t.Run("aws default", func(t *testing.T) {
		clearAuthEnv(t)
		t.Setenv("TAILSCALE_API_KEY", "tskey-test")
		t.Setenv("TSFLOW_S3_AUTH", "aws_default")
		t.Setenv("TSFLOW_S3_BUCKET", "flow-logs")
		t.Setenv("TSFLOW_S3_REGION", "us-east-1")
		t.Setenv("AWS_ACCESS_KEY_ID", "ambient-access")
		t.Setenv("AWS_SECRET_ACCESS_KEY", "ambient-secret")
		cfg := Load()
		if cfg.FlowObjectStoreAuth != ObjectStoreAuthAWSDefault {
			t.Fatalf("auth = %q", cfg.FlowObjectStoreAuth)
		}
		if cfg.FlowObjectStorePathStyle {
			t.Fatal("aws_default should default to virtual-hosted style")
		}
		if cfg.FlowObjectStoreAccessKey != "ambient-access" {
			t.Fatalf("ambient key = %q", cfg.FlowObjectStoreAccessKey)
		}
		if err := cfg.Validate(); err != nil {
			t.Fatal(err)
		}
		backend, err := cfg.EffectiveFlowBackend()
		if err != nil || backend != FlowBackendS3 {
			t.Fatalf("backend = %s, %v", backend, err)
		}
	})

	t.Run("aws default explicit path style", func(t *testing.T) {
		clearAuthEnv(t)
		t.Setenv("TAILSCALE_API_KEY", "tskey-test")
		t.Setenv("TSFLOW_FLOW_BACKEND", "s3")
		t.Setenv("TSFLOW_S3_AUTH", "aws_default")
		t.Setenv("TSFLOW_S3_BUCKET", "flow-logs")
		t.Setenv("AWS_REGION", "eu-west-1")
		t.Setenv("TSFLOW_S3_PATH_STYLE", "true")
		t.Setenv("TSFLOW_S3_ROLE_ARN", "arn:aws:iam::123456789012:role/tsflow-reader")
		cfg := Load()
		if !cfg.FlowObjectStorePathStyle || cfg.FlowObjectStoreRegion != "eu-west-1" || cfg.FlowObjectStoreRoleARN == "" {
			t.Fatalf("aws config = %+v", cfg)
		}
		if err := cfg.Validate(); err != nil {
			t.Fatal(err)
		}
	})

	cases := []struct {
		name string
		set  func()
		want string
	}{
		{
			name: "gcs with static keys",
			set: func() {
				t.Setenv("TAILSCALE_API_KEY", "tskey-test")
				t.Setenv("TSFLOW_FLOW_BACKEND", "gcs")
				t.Setenv("TSFLOW_S3_AUTH", "gcs_adc")
				t.Setenv("TSFLOW_S3_ACCESS_KEY_ID", "access")
				t.Setenv("TSFLOW_S3_SECRET_ACCESS_KEY", "secret")
			},
			want: "does not use TSFLOW_S3_ACCESS_KEY_ID",
		},
		{
			name: "gcs with s3 endpoint",
			set: func() {
				t.Setenv("TAILSCALE_API_KEY", "tskey-test")
				t.Setenv("TSFLOW_FLOW_BACKEND", "gcs")
				t.Setenv("TSFLOW_S3_ENDPOINT", "https://storage.googleapis.com")
			},
			want: "does not use TSFLOW_S3_ENDPOINT",
		},
		{
			name: "gcs adc with s3 backend",
			set: func() {
				t.Setenv("TAILSCALE_API_KEY", "tskey-test")
				t.Setenv("TSFLOW_FLOW_BACKEND", "s3")
				t.Setenv("TSFLOW_S3_AUTH", "gcs_adc")
			},
			want: "set TSFLOW_FLOW_BACKEND=gcs",
		},
		{
			name: "unknown auth",
			set: func() {
				t.Setenv("TAILSCALE_API_KEY", "tskey-test")
				t.Setenv("TSFLOW_S3_AUTH", "imds")
			},
			want: "TSFLOW_S3_AUTH must be static, aws_default, or gcs_adc",
		},
		{
			name: "unknown backend",
			set: func() {
				t.Setenv("TAILSCALE_API_KEY", "tskey-test")
				t.Setenv("TSFLOW_FLOW_BACKEND", "minio")
			},
			want: "TSFLOW_FLOW_BACKEND must be api, s3, or gcs",
		},
		{
			name: "aws default with api backend",
			set: func() {
				t.Setenv("TAILSCALE_API_KEY", "tskey-test")
				t.Setenv("TSFLOW_FLOW_BACKEND", "api")
				t.Setenv("TSFLOW_S3_AUTH", "aws_default")
				t.Setenv("TSFLOW_S3_REGION", "us-east-1")
			},
			want: "requires TSFLOW_FLOW_BACKEND=s3",
		},
		{
			name: "aws default missing region",
			set: func() {
				t.Setenv("TAILSCALE_API_KEY", "tskey-test")
				t.Setenv("TSFLOW_S3_AUTH", "aws_default")
				t.Setenv("TSFLOW_S3_BUCKET", "flow-logs")
			},
			want: "requires a region",
		},
		{
			name: "aws default with static keys",
			set: func() {
				t.Setenv("TAILSCALE_API_KEY", "tskey-test")
				t.Setenv("TSFLOW_S3_AUTH", "aws_default")
				t.Setenv("TSFLOW_S3_BUCKET", "flow-logs")
				t.Setenv("TSFLOW_S3_REGION", "us-east-1")
				t.Setenv("TSFLOW_S3_ACCESS_KEY_ID", "access")
				t.Setenv("TSFLOW_S3_SECRET_ACCESS_KEY", "secret")
			},
			want: "does not use TSFLOW_S3_ACCESS_KEY_ID",
		},
		{
			name: "role without aws default",
			set: func() {
				t.Setenv("TAILSCALE_API_KEY", "tskey-test")
				t.Setenv("TSFLOW_S3_ROLE_ARN", "arn:aws:iam::123456789012:role/tsflow-reader")
			},
			want: "TSFLOW_S3_ROLE_ARN requires TSFLOW_S3_AUTH=aws_default",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			clearAuthEnv(t)
			tc.set()
			err := Load().Validate()
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Validate() = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestTailnetFileAuthBlock(t *testing.T) {
	dir := t.TempDir()
	tokenPath := filepath.Join(dir, "token")
	if err := os.WriteFile(tokenPath, []byte("header.payload.sig"), 0o600); err != nil {
		t.Fatal(err)
	}
	secretPath := filepath.Join(dir, "secret")
	if err := os.WriteFile(secretPath, []byte("lab-secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	clearAuthEnv(t)
	t.Setenv("LAB_WIF_CLIENT_ID", "lab-client")
	t.Setenv("WEST_TAILSCALE_API_KEY", "west-key")
	t.Setenv("LAB_OAUTH_CLIENT_ID", "lab-id")

	body := `
tailnets:
  - id: default
    tailnet: example.com
    s3_prefix: network/
    auth:
      type: wif
      client_id: fed-client
      id_token_file: ` + tokenPath + `
  - id: lab
    tailnet: lab.example.com
    auth:
      type: oauth
      client_id_env: LAB_OAUTH_CLIENT_ID
      client_secret_file: ` + secretPath + `
      scopes:
        - devices:read
  - id: west
    tailnet: west.example.com
    auth:
      type: api_key
      api_key_env: WEST_TAILSCALE_API_KEY
  - id: east
    tailnet: east.example.com
    auth:
      type: wif
      client_id_env: LAB_WIF_CLIENT_ID
      audience: api.tailscale.com/lab-client
`
	path := filepath.Join(dir, "tailnets.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := validConfig()
	cfg.TailnetsFile = path
	cfg.TailscaleOAuthScopes = []string{"all:read"}
	specs, err := cfg.ResolveTailnets()
	if err != nil {
		t.Fatal(err)
	}
	if len(specs) != 4 {
		t.Fatalf("specs = %d", len(specs))
	}
	if specs[0].AuthMode != TailscaleAuthWIF || specs[0].WIFClientID != "fed-client" || specs[0].WIFIDTokenFile != tokenPath || specs[0].S3Prefix != "network/" {
		t.Fatalf("default = %+v", specs[0])
	}
	if specs[1].AuthMode != TailscaleAuthOAuth || specs[1].OAuthClientID != "lab-id" || specs[1].OAuthClientSecret != "lab-secret" || strings.Join(specs[1].OAuthScopes, ",") != "devices:read" {
		t.Fatalf("lab = %+v", specs[1])
	}
	if specs[2].AuthMode != TailscaleAuthAPIKey || specs[2].APIKey != "west-key" || specs[2].OAuthClientID != "" {
		t.Fatalf("west = %+v", specs[2])
	}
	if specs[3].AuthMode != TailscaleAuthWIF || specs[3].WIFClientID != "lab-client" || specs[3].WIFAudience != "api.tailscale.com/lab-client" {
		t.Fatalf("east = %+v", specs[3])
	}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}

	jsonPath := filepath.Join(dir, "tailnets.json")
	jsonBody := `{"tailnets":[{"id":"json","tailnet":"json.example.com","auth":{"type":"wif","client_id":"json-client","id_token_env":"JSON_WIF_TOKEN"}}]}`
	if err := os.WriteFile(jsonPath, []byte(jsonBody), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("JSON_WIF_TOKEN", "json.payload.sig")
	cfg.TailnetsFile = jsonPath
	specs, err = cfg.ResolveTailnets()
	if err != nil {
		t.Fatal(err)
	}
	if len(specs) != 1 || specs[0].WIFIDToken != "json.payload.sig" || specs[0].WIFClientID != "json-client" {
		t.Fatalf("json spec = %+v", specs)
	}
}

func TestTailnetFileAuthBlockErrors(t *testing.T) {
	clearAuthEnv(t)
	t.Setenv("OTHER_TAILSCALE_API_KEY", "key")
	t.Setenv("WIF_TOKEN", "header.payload.sig")
	cases := []struct {
		name string
		body string
		want string
	}{
		{name: "inline token", body: "tailnets:\n- {id: a, tailnet: a.example, auth: {type: wif, client_id: c, id_token: secret}}\n", want: "unknown field"},
		{name: "flat and auth", body: "tailnets:\n- {id: a, tailnet: a.example, api_key_env: OTHER_TAILSCALE_API_KEY, auth: {type: api_key, api_key_env: OTHER_TAILSCALE_API_KEY}}\n", want: "both an auth block and credential fields"},
		{name: "missing client", body: "tailnets:\n- {id: a, tailnet: a.example, auth: {type: wif, audience: api.tailscale.com/c}}\n", want: "requires client_id"},
		{name: "two sources", body: "tailnets:\n- {id: a, tailnet: a.example, auth: {type: wif, client_id: c, id_token_env: WIF_TOKEN, id_token_file: /tmp/token}}\n", want: "only one"},
		{name: "bad type", body: "tailnets:\n- {id: a, tailnet: a.example, auth: {type: cert, api_key_env: OTHER_TAILSCALE_API_KEY}}\n", want: "auth type must be oauth, api_key, or wif"},
		{name: "wif scopes", body: "tailnets:\n- {id: a, tailnet: a.example, auth: {type: wif, client_id: c, audience: api.tailscale.com/c, scopes: [all:read]}}\n", want: "does not accept"},
		{name: "two client ids", body: "tailnets:\n- {id: a, tailnet: a.example, auth: {type: wif, client_id: c, client_id_env: LAB_WIF_CLIENT_ID, audience: api.tailscale.com/c}}\n", want: "only one client id source"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "tailnets.yaml")
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
