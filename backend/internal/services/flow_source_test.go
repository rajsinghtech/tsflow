package services

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/rajsinghtech/tsflow/backend/internal/config"
)

func TestMixedTailnetFlowSources(t *testing.T) {
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
		"TSFLOW_S3_PATH_STYLE",
		"AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY", "AWS_SESSION_TOKEN",
		"AWS_ROLE_ARN", "AWS_WEB_IDENTITY_TOKEN_FILE",
	} {
		t.Setenv(key, "")
	}
	t.Setenv("PROD_TAILSCALE_API_KEY", "prod-key")
	t.Setenv("STAGING_TAILSCALE_API_KEY", "staging-key")
	t.Setenv("LAB_TAILSCALE_API_KEY", "lab-key")
	t.Setenv("AWS_EC2_METADATA_DISABLED", "true")

	dir := t.TempDir()
	tokenPath := filepath.Join(dir, "gcp-token")
	if err := os.WriteFile(tokenPath, []byte("google-id-token\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	envToken := filepath.Join(dir, "env-token")
	if err := os.WriteFile(envToken, []byte("env-token-should-not-be-used"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AWS_ROLE_ARN", "arn:aws:iam::123456789012:role/env-role")
	t.Setenv("AWS_WEB_IDENTITY_TOKEN_FILE", envToken)
	t.Setenv("AWS_ACCESS_KEY_ID", "env-access")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "env-secret")

	gcs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer gcs.Close()
	t.Setenv("STORAGE_EMULATOR_HOST", gcs.URL)

	body := `
tailnets:
  - id: prod
    tailnet: prod.example.com
    api_key_env: PROD_TAILSCALE_API_KEY
    flow_backend: gcs
    bucket: example-prod-flow-logs
    prefix: network/
  - id: staging
    tailnet: staging.example.com
    api_key_env: STAGING_TAILSCALE_API_KEY
    flow_backend: s3
    s3_auth: aws_default
    bucket: example-staging-flow-logs
    region: us-east-1
    role_arn: arn:aws:iam::123456789012:role/tsflow-reader
    web_identity_token_file: ` + tokenPath + `
    prefix: staging/network/
  - id: lab
    tailnet: lab.example.com
    api_key_env: LAB_TAILSCALE_API_KEY
    flow_backend: api
`
	path := filepath.Join(dir, "tailnets.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{
		TailscaleAPIURL:           "https://api.tailscale.com",
		Port:                      "8080",
		PollInterval:              "1h",
		InitialBackfill:           "20m",
		FlowObjectStorePrefix:     "network/",
		FlowObjectStoreLookback:   "15m",
		FlowObjectStoreMaxObjects: 500,
		TailnetsFile:              path,
	}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	specs, err := cfg.ResolveTailnets()
	if err != nil {
		t.Fatal(err)
	}
	base, err := PollerConfigFrom(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if base.FlowBackend != config.FlowBackendAPI {
		t.Fatalf("process backend = %s, want api", base.FlowBackend)
	}
	registry, err := NewRegistry(context.Background(), specs, nil, base)
	if err != nil {
		t.Fatal(err)
	}
	prod, ok := registry.Get("prod")
	if !ok {
		t.Fatal("missing prod")
	}
	staging, ok := registry.Get("staging")
	if !ok {
		t.Fatal("missing staging")
	}
	lab, ok := registry.Get("lab")
	if !ok {
		t.Fatal("missing lab")
	}

	if prod.Poller.config.FlowBackend != config.FlowBackendGCS || prod.Poller.objectStore == nil {
		t.Fatalf("prod backend=%s store=%v", prod.Poller.config.FlowBackend, prod.Poller.objectStore)
	}
	if prod.Poller.objectStore.cfg.Bucket != "example-prod-flow-logs" || prod.Poller.objectStore.cfg.AuthMode != config.ObjectStoreAuthGCSADC || prod.Poller.objectStore.cfg.Prefix != "network/" {
		t.Fatalf("prod source = %+v", prod.Poller.objectStore.cfg)
	}
	if _, ok := prod.Poller.objectStore.blobs.(*gcsBlobClient); !ok {
		t.Fatalf("prod client = %T", prod.Poller.objectStore.blobs)
	}

	if staging.Poller.config.FlowBackend != config.FlowBackendS3 || staging.Poller.objectStore == nil {
		t.Fatalf("staging backend=%s store=%v", staging.Poller.config.FlowBackend, staging.Poller.objectStore)
	}
	got := staging.Poller.objectStore.cfg
	if got.Bucket != "example-staging-flow-logs" || got.Region != "us-east-1" || got.AuthMode != config.ObjectStoreAuthAWSDefault ||
		got.RoleARN != "arn:aws:iam::123456789012:role/tsflow-reader" || got.WebIdentityTokenFile != tokenPath || got.Prefix != "staging/network/" {
		t.Fatalf("staging source = %+v", got)
	}
	if got.RoleARN == os.Getenv("AWS_ROLE_ARN") || got.WebIdentityTokenFile == envToken {
		t.Fatalf("staging source used process AWS env: %+v", got)
	}
	if _, ok := staging.Poller.objectStore.blobs.(*s3BlobClient); !ok {
		t.Fatalf("staging client = %T", staging.Poller.objectStore.blobs)
	}
	if staging.Poller.objectStore == prod.Poller.objectStore {
		t.Fatal("prod and staging share an object store client")
	}

	if lab.Poller.config.FlowBackend != config.FlowBackendAPI || lab.Poller.objectStore != nil {
		t.Fatalf("lab backend=%s store=%v", lab.Poller.config.FlowBackend, lab.Poller.objectStore)
	}
	if prod.Poller.tailnetID != "prod" || staging.Poller.tailnetID != "staging" || lab.Poller.tailnetID != "lab" {
		t.Fatalf("ids prod=%s staging=%s lab=%s", prod.Poller.tailnetID, staging.Poller.tailnetID, lab.Poller.tailnetID)
	}
	if prod.Service.apiKey != "prod-key" || staging.Service.apiKey != "staging-key" || lab.Service.apiKey != "lab-key" {
		t.Fatal("api keys were not kept per tailnet")
	}
}

func TestWebIdentityAssumeRoleIgnoresProcessAWSEnv(t *testing.T) {
	dir := t.TempDir()
	stagingToken := filepath.Join(dir, "staging")
	prodToken := filepath.Join(dir, "prod")
	envToken := filepath.Join(dir, "env")
	if err := os.WriteFile(stagingToken, []byte(" google-staging-token \n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(prodToken, []byte("google-prod-token"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(envToken, []byte("env-token"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AWS_ACCESS_KEY_ID", "env-access")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "env-secret")
	t.Setenv("AWS_SESSION_TOKEN", "")
	t.Setenv("AWS_ROLE_ARN", "arn:aws:iam::123456789012:role/env-role")
	t.Setenv("AWS_WEB_IDENTITY_TOKEN_FILE", envToken)
	t.Setenv("AWS_EC2_METADATA_DISABLED", "true")

	var mu sync.Mutex
	type call struct {
		role  string
		token string
		name  string
	}
	var calls []call
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		values, err := url.ParseQuery(string(body))
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		mu.Lock()
		calls = append(calls, call{
			role:  values.Get("RoleArn"),
			token: values.Get("WebIdentityToken"),
			name:  values.Get("RoleSessionName"),
		})
		mu.Unlock()
		access := "ASIASTAGING"
		if values.Get("RoleArn") == "arn:aws:iam::123456789012:role/prod-reader" {
			access = "ASIAPROD"
		}
		w.Header().Set("Content-Type", "text/xml")
		_, _ = io.WriteString(w, `<AssumeRoleWithWebIdentityResponse xmlns="https://sts.amazonaws.com/doc/2011-06-15/">
  <AssumeRoleWithWebIdentityResult>
    <Credentials>
      <AccessKeyId>`+access+`</AccessKeyId>
      <SecretAccessKey>secret</SecretAccessKey>
      <SessionToken>session</SessionToken>
      <Expiration>2026-10-07T15:15:15Z</Expiration>
    </Credentials>
    <AssumedRoleUser>
      <AssumedRoleId>AROAEXAMPLE:tsflow</AssumedRoleId>
      <Arn>arn:aws:sts::123456789012:assumed-role/tsflow-reader/tsflow</Arn>
    </AssumedRoleUser>
  </AssumeRoleWithWebIdentityResult>
  <ResponseMetadata><RequestId>example</RequestId></ResponseMetadata>
</AssumeRoleWithWebIdentityResponse>`)
	}))
	defer server.Close()

	retrieve := func(role, tokenFile string) (string, error) {
		awsCfg, err := loadObjectStoreAWSConfig(context.Background(), ObjectStoreConfig{
			Region:               "us-east-1",
			AuthMode:             config.ObjectStoreAuthAWSDefault,
			RoleARN:              role,
			WebIdentityTokenFile: tokenFile,
			stsEndpoint:          server.URL,
			AccessKey:            "struct-access",
			SecretKey:            "struct-secret",
		})
		if err != nil {
			return "", err
		}
		creds, err := awsCfg.Credentials.Retrieve(context.Background())
		if err != nil {
			return "", err
		}
		return creds.AccessKeyID, nil
	}
	stagingKey, err := retrieve("arn:aws:iam::123456789012:role/staging-reader", stagingToken)
	if err != nil {
		t.Fatal(err)
	}
	prodKey, err := retrieve("arn:aws:iam::123456789012:role/prod-reader", prodToken)
	if err != nil {
		t.Fatal(err)
	}
	if stagingKey != "ASIASTAGING" || prodKey != "ASIAPROD" {
		t.Fatalf("keys staging=%s prod=%s", stagingKey, prodKey)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(calls) != 2 {
		t.Fatalf("sts calls = %+v", calls)
	}
	if calls[0].role != "arn:aws:iam::123456789012:role/staging-reader" || calls[0].token != "google-staging-token" || calls[0].name != "tsflow" {
		t.Fatalf("staging call = %+v", calls[0])
	}
	if calls[1].role != "arn:aws:iam::123456789012:role/prod-reader" || calls[1].token != "google-prod-token" || calls[1].name != "tsflow" {
		t.Fatalf("prod call = %+v", calls[1])
	}
	for _, got := range calls {
		if strings.Contains(got.role, "env-role") || got.token == "env-token" {
			t.Fatalf("call used process AWS env: %+v", got)
		}
	}

	chain, err := loadObjectStoreAWSConfig(context.Background(), ObjectStoreConfig{
		Region:    "us-east-1",
		AuthMode:  config.ObjectStoreAuthAWSDefault,
		AccessKey: "struct-access",
		SecretKey: "struct-secret",
	})
	if err != nil {
		t.Fatal(err)
	}
	chainCreds, err := chain.Credentials.Retrieve(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if chainCreds.AccessKeyID != "env-access" {
		t.Fatalf("chain access key = %q", chainCreds.AccessKeyID)
	}
}
