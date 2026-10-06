package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func clearSingleTailnetEnv(t *testing.T) {
	t.Helper()
	for _, key := range []string{
		"TAILSCALE_TAILNET", "VITE_TAILSCALE_TAILNET",
		"TAILSCALE_API_KEY", "VITE_TAILSCALE_API_KEY",
		"TAILSCALE_OAUTH_CLIENT_ID", "VITE_TAILSCALE_OAUTH_CLIENT_ID",
		"TAILSCALE_OAUTH_CLIENT_SECRET", "VITE_TAILSCALE_OAUTH_CLIENT_SECRET",
		"TAILSCALE_AUTH", "VITE_TAILSCALE_AUTH",
		"TAILSCALE_WIF_CLIENT_ID", "TAILSCALE_WIF_AUDIENCE",
		"TAILSCALE_WIF_ID_TOKEN", "TAILSCALE_WIF_ID_TOKEN_FILE",
		"TSFLOW_S3_ACCESS_KEY_ID", "TSFLOW_S3_SECRET_ACCESS_KEY",
		"TAILSCALE_LOGS_S3_ACCESS_KEY", "TAILSCALE_LOGS_S3_SECRET_KEY",
		"TSFLOW_S3_PATH_STYLE", "VITE_TSFLOW_S3_PATH_STYLE",
	} {
		t.Setenv(key, "")
	}
}

func TestEnvTailnetFlowInheritsProcess(t *testing.T) {
	cfg := validConfig()
	cfg.TailscaleAPIKey = "key"
	cfg.TailscaleTailnet = "example.com"
	cfg.FlowBackend = FlowBackendS3
	cfg.FlowObjectStoreBucket = "flows"
	cfg.FlowObjectStoreEndpoint = "http://object-store.test"
	cfg.FlowObjectStoreAccessKey = "access"
	cfg.FlowObjectStoreSecretKey = "secret"
	cfg.FlowObjectStorePrefix = "network/"
	cfg.FlowObjectStoreRegion = "garage"
	cfg.FlowObjectStoreLookback = "15m"
	cfg.FlowObjectStoreMaxObjects = 500

	specs, err := cfg.ResolveTailnets()
	if err != nil {
		t.Fatal(err)
	}
	got := specs[0]
	if got.FlowBackend != "" || got.Bucket != "" || got.Region != "" || got.Endpoint != "" ||
		got.ObjectStoreAuth != "" || got.RoleARN != "" || got.WebIdentityTokenFile != "" || got.S3Prefix != "" {
		t.Fatalf("env spec should leave flow fields empty so they inherit: %+v", got)
	}
	base, err := cfg.processFlowSource()
	if err != nil {
		t.Fatal(err)
	}
	flow := got.ResolveFlow(base)
	if flow.Backend != FlowBackendS3 || flow.Bucket != "flows" || flow.Prefix != "network/" ||
		flow.Endpoint != "http://object-store.test" || flow.Region != "garage" || flow.Auth != ObjectStoreAuthStatic {
		t.Fatalf("resolved env flow = %+v", flow)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestTailnetFlowInheritance(t *testing.T) {
	clearSingleTailnetEnv(t)
	t.Setenv("HOME_TAILSCALE_API_KEY", "home-key")
	t.Setenv("LAB_TAILSCALE_API_KEY", "lab-key")
	dir := t.TempDir()
	body := `
tailnets:
  - id: home
    tailnet: example.com
    api_key_env: HOME_TAILSCALE_API_KEY
    s3_prefix: network/home/
  - id: lab
    tailnet: lab.example.com
    api_key_env: LAB_TAILSCALE_API_KEY
    bucket: example-lab-flow-logs
    region: eu-west-1
    prefix: lab/network/
`
	path := filepath.Join(dir, "tailnets.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := validConfig()
	cfg.TailnetsFile = path
	cfg.FlowBackend = FlowBackendS3
	cfg.FlowObjectStoreAuth = ObjectStoreAuthAWSDefault
	cfg.FlowObjectStoreBucket = "example-flow-logs"
	cfg.FlowObjectStoreRegion = "us-east-1"
	cfg.FlowObjectStorePrefix = "network/"
	cfg.FlowObjectStoreRoleARN = "arn:aws:iam::123456789012:role/tsflow-reader"
	cfg.FlowObjectStoreLookback = "15m"
	cfg.FlowObjectStoreMaxObjects = 500
	cfg.FlowObjectStorePathStyle = false

	specs, err := cfg.ResolveTailnets()
	if err != nil {
		t.Fatal(err)
	}
	if len(specs) != 2 {
		t.Fatalf("specs = %d", len(specs))
	}
	if specs[0].S3Prefix != "network/home/" || specs[0].FlowBackend != "" || specs[0].Bucket != "" ||
		specs[0].Region != "" || specs[0].ObjectStoreAuth != "" || specs[0].RoleARN != "" {
		t.Fatalf("prefix-only spec = %+v", specs[0])
	}
	if specs[1].S3Prefix != "lab/network/" || specs[1].Bucket != "example-lab-flow-logs" || specs[1].Region != "eu-west-1" || specs[1].FlowBackend != "" {
		t.Fatalf("partial spec = %+v", specs[1])
	}
	base, err := cfg.processFlowSource()
	if err != nil {
		t.Fatal(err)
	}
	home := specs[0].ResolveFlow(base)
	if home.Backend != FlowBackendS3 || home.Auth != ObjectStoreAuthAWSDefault || home.Bucket != "example-flow-logs" ||
		home.Region != "us-east-1" || home.Prefix != "network/home/" || home.RoleARN != cfg.FlowObjectStoreRoleARN || home.UsePathStyle {
		t.Fatalf("home flow = %+v", home)
	}
	lab := specs[1].ResolveFlow(base)
	if lab.Bucket != "example-lab-flow-logs" || lab.Region != "eu-west-1" || lab.Prefix != "lab/network/" ||
		lab.Auth != ObjectStoreAuthAWSDefault || lab.RoleARN != cfg.FlowObjectStoreRoleARN || lab.Backend != FlowBackendS3 {
		t.Fatalf("lab flow = %+v", lab)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestTailnetFlowPrefixAlias(t *testing.T) {
	clearSingleTailnetEnv(t)
	t.Setenv("ALIAS_TAILSCALE_API_KEY", "key")
	dir := t.TempDir()
	path := filepath.Join(dir, "tailnets.yaml")
	body := "tailnets:\n- {id: a, tailnet: a.example.com, api_key_env: ALIAS_TAILSCALE_API_KEY, prefix: network/a/, s3_prefix: network/a/}\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := validConfig()
	cfg.TailnetsFile = path
	specs, err := cfg.ResolveTailnets()
	if err != nil {
		t.Fatal(err)
	}
	if specs[0].S3Prefix != "network/a/" || specs[0].FlowBackend != "" {
		t.Fatalf("alias spec = %+v", specs[0])
	}
}

func TestTailnetFlowRejectsCombinations(t *testing.T) {
	clearSingleTailnetEnv(t)
	t.Setenv("FLOW_TAILSCALE_API_KEY", "key")
	tokenPath := filepath.Join(t.TempDir(), "empty-token")
	if err := os.WriteFile(tokenPath, []byte(" \n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		body string
		want string
		env  func()
	}{
		{
			name: "bad backend",
			body: "tailnets:\n- {id: a, tailnet: a.example.com, api_key_env: FLOW_TAILSCALE_API_KEY, flow_backend: minio}\n",
			want: "flow_backend must be api, s3, or gcs",
		},
		{
			name: "bad auth",
			body: "tailnets:\n- {id: a, tailnet: a.example.com, api_key_env: FLOW_TAILSCALE_API_KEY, flow_backend: s3, s3_auth: irsa}\n",
			want: "s3_auth must be static, aws_default, or gcs_adc",
		},
		{
			name: "both prefixes",
			body: "tailnets:\n- {id: a, tailnet: a.example.com, api_key_env: FLOW_TAILSCALE_API_KEY, prefix: network/, s3_prefix: lab/}\n",
			want: "sets both prefix and s3_prefix",
		},
		{
			name: "api with bucket",
			body: "tailnets:\n- {id: a, tailnet: a.example.com, api_key_env: FLOW_TAILSCALE_API_KEY, flow_backend: api, bucket: example-flow-logs}\n",
			want: "flow_backend api does not use",
		},
		{
			name: "bucket without backend",
			body: "tailnets:\n- {id: a, tailnet: a.example.com, api_key_env: FLOW_TAILSCALE_API_KEY, bucket: example-flow-logs}\n",
			want: "object store settings require flow_backend s3 or gcs",
		},
		{
			name: "gcs without bucket",
			body: "tailnets:\n- {id: a, tailnet: a.example.com, api_key_env: FLOW_TAILSCALE_API_KEY, flow_backend: gcs}\n",
			want: "flow_backend gcs requires a bucket",
		},
		{
			name: "gcs with endpoint",
			body: "tailnets:\n- {id: a, tailnet: a.example.com, api_key_env: FLOW_TAILSCALE_API_KEY, flow_backend: gcs, bucket: example-flow-logs, endpoint: https://storage.googleapis.com}\n",
			want: "does not use an endpoint",
		},
		{
			name: "gcs with role",
			body: "tailnets:\n- {id: a, tailnet: a.example.com, api_key_env: FLOW_TAILSCALE_API_KEY, flow_backend: gcs, bucket: example-flow-logs, role_arn: arn:aws:iam::123456789012:role/tsflow-reader}\n",
			want: "does not use role_arn",
		},
		{
			name: "gcs adc on s3",
			body: "tailnets:\n- {id: a, tailnet: a.example.com, api_key_env: FLOW_TAILSCALE_API_KEY, flow_backend: s3, s3_auth: gcs_adc, bucket: example-flow-logs}\n",
			want: "s3_auth gcs_adc requires flow_backend gcs",
		},
		{
			name: "aws default without region",
			body: "tailnets:\n- {id: a, tailnet: a.example.com, api_key_env: FLOW_TAILSCALE_API_KEY, flow_backend: s3, s3_auth: aws_default, bucket: example-flow-logs}\n",
			want: "requires a region",
		},
		{
			name: "aws default without bucket",
			body: "tailnets:\n- {id: a, tailnet: a.example.com, api_key_env: FLOW_TAILSCALE_API_KEY, flow_backend: s3, s3_auth: aws_default, region: us-east-1}\n",
			want: "requires a bucket",
		},
		{
			name: "aws default with static keys",
			body: "tailnets:\n- {id: a, tailnet: a.example.com, api_key_env: FLOW_TAILSCALE_API_KEY, flow_backend: s3, s3_auth: aws_default, bucket: example-flow-logs, region: us-east-1}\n",
			want: "does not use TSFLOW_S3_ACCESS_KEY_ID",
			env: func() {
				t.Setenv("TSFLOW_S3_ACCESS_KEY_ID", "access")
				t.Setenv("TSFLOW_S3_SECRET_ACCESS_KEY", "secret")
			},
		},
		{
			name: "role without aws default",
			body: "tailnets:\n- {id: a, tailnet: a.example.com, api_key_env: FLOW_TAILSCALE_API_KEY, flow_backend: s3, bucket: example-flow-logs, endpoint: http://object-store.test, role_arn: arn:aws:iam::123456789012:role/tsflow-reader}\n",
			want: "role_arn requires s3_auth aws_default",
		},
		{
			name: "token file without role",
			body: "tailnets:\n- {id: a, tailnet: a.example.com, api_key_env: FLOW_TAILSCALE_API_KEY, flow_backend: s3, s3_auth: aws_default, bucket: example-flow-logs, region: us-east-1, web_identity_token_file: " + tokenPath + "}\n",
			want: "web_identity_token_file requires role_arn",
		},
		{
			name: "empty token file",
			body: "tailnets:\n- {id: a, tailnet: a.example.com, api_key_env: FLOW_TAILSCALE_API_KEY, flow_backend: s3, s3_auth: aws_default, bucket: example-flow-logs, region: us-east-1, role_arn: arn:aws:iam::123456789012:role/tsflow-reader, web_identity_token_file: " + tokenPath + "}\n",
			want: "is empty",
		},
		{
			name: "s3 without static keys",
			body: "tailnets:\n- {id: a, tailnet: a.example.com, api_key_env: FLOW_TAILSCALE_API_KEY, flow_backend: s3, bucket: example-flow-logs}\n",
			want: "static object-store keys",
		},
		{
			name: "bad endpoint",
			body: "tailnets:\n- {id: a, tailnet: a.example.com, api_key_env: FLOW_TAILSCALE_API_KEY, flow_backend: s3, s3_auth: aws_default, bucket: example-flow-logs, region: us-east-1, endpoint: object-store.test}\n",
			want: "absolute http or https URL",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			clearSingleTailnetEnv(t)
			t.Setenv("FLOW_TAILSCALE_API_KEY", "key")
			if tc.env != nil {
				tc.env()
			}
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

func TestProcessWebIdentityTokenFile(t *testing.T) {
	tokenPath := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(tokenPath, []byte(" google-token \n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := validConfig()
	cfg.TailscaleAPIKey = "key"
	cfg.FlowObjectStoreAuth = ObjectStoreAuthAWSDefault
	cfg.FlowObjectStoreBucket = "example-flow-logs"
	cfg.FlowObjectStoreRegion = "us-east-1"
	cfg.FlowObjectStoreRoleARN = "arn:aws:iam::123456789012:role/tsflow-reader"
	cfg.FlowObjectStoreWebIdentityTokenFile = tokenPath
	cfg.FlowObjectStoreLookback = "15m"
	cfg.FlowObjectStoreMaxObjects = 500
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}

	missing := validConfig()
	missing.TailscaleAPIKey = "key"
	missing.FlowObjectStoreWebIdentityTokenFile = tokenPath
	err := missing.Validate()
	if err == nil || !strings.Contains(err.Error(), "TSFLOW_S3_WEB_IDENTITY_TOKEN_FILE requires TSFLOW_S3_AUTH=aws_default") {
		t.Fatalf("Validate() = %v", err)
	}

	noRole := validConfig()
	noRole.TailscaleAPIKey = "key"
	noRole.FlowObjectStoreAuth = ObjectStoreAuthAWSDefault
	noRole.FlowObjectStoreBucket = "example-flow-logs"
	noRole.FlowObjectStoreRegion = "us-east-1"
	noRole.FlowObjectStoreWebIdentityTokenFile = tokenPath
	noRole.FlowObjectStoreLookback = "15m"
	noRole.FlowObjectStoreMaxObjects = 500
	err = noRole.Validate()
	if err == nil || !strings.Contains(err.Error(), "TSFLOW_S3_WEB_IDENTITY_TOKEN_FILE requires TSFLOW_S3_ROLE_ARN") {
		t.Fatalf("Validate() = %v", err)
	}
}

func TestTailnetFlowInheritsProcessEndpointRejection(t *testing.T) {
	clearSingleTailnetEnv(t)
	t.Setenv("GCS_TAILSCALE_API_KEY", "key")
	path := filepath.Join(t.TempDir(), "tailnets.yaml")
	body := "tailnets:\n- {id: prod, tailnet: prod.example.com, api_key_env: GCS_TAILSCALE_API_KEY, flow_backend: gcs, bucket: example-prod-flow-logs}\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := validConfig()
	cfg.TailnetsFile = path
	cfg.FlowObjectStoreEndpoint = "http://object-store.test"
	_, err := cfg.ResolveTailnets()
	if err == nil || !strings.Contains(err.Error(), `tailnet "prod" flow_backend gcs does not use an endpoint`) {
		t.Fatalf("error = %v", err)
	}
}
