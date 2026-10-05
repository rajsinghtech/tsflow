package services

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rajsinghtech/tsflow/backend/internal/config"
)

func testIDToken(exp int64) string {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none","typ":"JWT"}`))
	payload := base64.RawURLEncoding.EncodeToString([]byte(`{"exp":` + strconv.FormatInt(exp, 10) + `}`))
	sig := base64.RawURLEncoding.EncodeToString([]byte("sig"))
	return header + "." + payload + "." + sig
}

func TestTailscaleIDTokenFuncRereadsFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(path, []byte(" one \n"), 0o600); err != nil {
		t.Fatal(err)
	}
	fn := tailscaleIDTokenFunc(&config.Config{TailscaleWIFIDTokenFile: path})
	got, err := fn()
	if err != nil || got != "one" {
		t.Fatalf("first token = %q, %v", got, err)
	}
	if err := os.WriteFile(path, []byte("two"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err = fn()
	if err != nil || got != "two" {
		t.Fatalf("second token = %q, %v", got, err)
	}
}

func TestWorkloadIdentityExchangesOncePerClient(t *testing.T) {
	token := testIDToken(time.Now().Add(time.Hour).Unix())
	var defaultExchanges atomic.Int32
	var labExchanges atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v2/oauth/token-exchange" {
			if err := r.ParseForm(); err != nil {
				t.Errorf("parse form: %v", err)
				http.Error(w, "bad form", http.StatusBadRequest)
				return
			}
			switch r.Form.Get("client_id") {
			case "default-client":
				if r.Form.Get("jwt") != token {
					t.Errorf("default jwt = %q", r.Form.Get("jwt"))
				}
				defaultExchanges.Add(1)
				_ = json.NewEncoder(w).Encode(map[string]any{
					"access_token": "default-access",
					"token_type":   "Bearer",
					"expires_in":   3600,
				})
			case "lab-client":
				labExchanges.Add(1)
				_ = json.NewEncoder(w).Encode(map[string]any{
					"access_token": "lab-access",
					"token_type":   "Bearer",
					"expires_in":   3600,
				})
			default:
				http.Error(w, "unknown client", http.StatusUnauthorized)
			}
			return
		}
		if got := r.Header.Get("Authorization"); got != "Bearer default-access" && got != "Bearer lab-access" {
			t.Errorf("authorization = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"users":[]}`))
	}))
	defer server.Close()

	newService := func(clientID, idToken string) *TailscaleService {
		return NewTailscaleService(&config.Config{
			TailscaleAPIURL:      server.URL,
			TailscaleTailnet:     "example.com",
			TailscaleAuth:        config.TailscaleAuthWIF,
			TailscaleWIFClientID: clientID,
			TailscaleWIFIDToken:  idToken,
		})
	}
	defaultService := newService("default-client", token)
	labService := newService("lab-client", token)
	if !defaultService.HasCredentials() || defaultService.apiKey != "" || !defaultService.useOAuth {
		t.Fatalf("default service credentials were not wired")
	}
	for i := 0; i < 2; i++ {
		if _, err := defaultService.GetUsers(); err != nil {
			t.Fatal(err)
		}
		if _, err := labService.GetUsers(); err != nil {
			t.Fatal(err)
		}
	}
	if defaultExchanges.Load() != 1 || labExchanges.Load() != 1 {
		t.Fatalf("exchanges default=%d lab=%d, want one per tailnet", defaultExchanges.Load(), labExchanges.Load())
	}
}

func TestObjectStoreAWSDefaultUsesCredentialChain(t *testing.T) {
	t.Setenv("AWS_ACCESS_KEY_ID", "env-access")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "env-secret")
	t.Setenv("AWS_SESSION_TOKEN", "")
	t.Setenv("AWS_EC2_METADATA_DISABLED", "true")

	awsCfg, err := loadObjectStoreAWSConfig(context.Background(), ObjectStoreConfig{
		Region:    "us-east-1",
		AuthMode:  config.ObjectStoreAuthAWSDefault,
		AccessKey: "struct-access",
		SecretKey: "struct-secret",
	})
	if err != nil {
		t.Fatal(err)
	}
	creds, err := awsCfg.Credentials.Retrieve(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if creds.AccessKeyID != "env-access" {
		t.Fatalf("access key = %q, want the default chain", creds.AccessKeyID)
	}

	staticCfg, err := loadObjectStoreAWSConfig(context.Background(), ObjectStoreConfig{
		Region:    "garage",
		AuthMode:  config.ObjectStoreAuthStatic,
		AccessKey: "struct-access",
		SecretKey: "struct-secret",
	})
	if err != nil {
		t.Fatal(err)
	}
	staticCreds, err := staticCfg.Credentials.Retrieve(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if staticCreds.AccessKeyID != "struct-access" {
		t.Fatalf("static access key = %q", staticCreds.AccessKeyID)
	}

	source, err := NewObjectStoreSource(context.Background(), ObjectStoreConfig{
		Bucket:   "flow-logs",
		Region:   "us-east-1",
		AuthMode: config.ObjectStoreAuthAWSDefault,
		RoleARN:  "arn:aws:iam::123456789012:role/tsflow-reader",
	})
	if err != nil {
		t.Fatal(err)
	}
	if source.cfg.AuthMode != config.ObjectStoreAuthAWSDefault || source.blobs == nil {
		t.Fatalf("source = %+v", source.cfg)
	}
}

func TestWIFSourceRefreshesBeforeExpiry(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	var reads atomic.Int32
	var exchanges atomic.Int32
	var lastJWT string
	first := testIDToken(now.Add(time.Hour).Unix())
	rotated := testIDToken(now.Add(3 * time.Hour).Unix())
	current := first
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Errorf("parse form: %v", err)
			http.Error(w, "bad form", http.StatusBadRequest)
			return
		}
		lastJWT = r.Form.Get("jwt")
		exchanges.Add(1)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "access",
			"token_type":   "Bearer",
			"expires_in":   7200,
		})
	}))
	defer server.Close()

	src := &wifSource{
		clientID:      "client",
		baseURL:       server.URL,
		httpClient:    server.Client(),
		refreshBefore: time.Minute,
		now:           func() time.Time { return now },
		loadIDToken: func() (string, error) {
			reads.Add(1)
			return current, nil
		},
	}
	if _, err := src.Token(); err != nil {
		t.Fatal(err)
	}
	if _, err := src.Token(); err != nil {
		t.Fatal(err)
	}
	if reads.Load() != 1 || exchanges.Load() != 1 {
		t.Fatalf("warm cache reads=%d exchanges=%d", reads.Load(), exchanges.Load())
	}

	now = now.Add(30 * time.Minute)
	if _, err := src.Token(); err != nil {
		t.Fatal(err)
	}
	if reads.Load() != 1 || exchanges.Load() != 1 {
		t.Fatalf("mid-life cache reads=%d exchanges=%d", reads.Load(), exchanges.Load())
	}

	current = rotated
	now = now.Add(29*time.Minute + 30*time.Second)
	tok, err := src.Token()
	if err != nil {
		t.Fatal(err)
	}
	if reads.Load() != 2 {
		t.Fatalf("reads = %d, want a refresh inside the lead window", reads.Load())
	}
	if exchanges.Load() != 1 {
		t.Fatalf("exchanges = %d, want the API token to stay cached", exchanges.Load())
	}
	if tok.AccessToken != "access" {
		t.Fatalf("token = %q", tok.AccessToken)
	}

	// The API token lasts two hours. Move just inside its lead window and
	// check the rotated JWT is what gets exchanged.
	src.mu.Lock()
	apiExpiry := src.api.Expiry
	src.mu.Unlock()
	now = apiExpiry.Add(-30 * time.Second)
	if _, err := src.Token(); err != nil {
		t.Fatal(err)
	}
	if exchanges.Load() != 2 || lastJWT != rotated {
		t.Fatalf("exchanges=%d jwt rotated=%v", exchanges.Load(), lastJWT == rotated)
	}
}

func TestGCSReaderRequiresApplicationDefaultCredentials(t *testing.T) {
	t.Setenv("STORAGE_EMULATOR_HOST", "")
	t.Setenv("GOOGLE_APPLICATION_CREDENTIALS", filepath.Join(t.TempDir(), "missing.json"))
	_, err := NewObjectStoreSource(context.Background(), ObjectStoreConfig{
		Bucket:   "flow-logs",
		AuthMode: config.ObjectStoreAuthGCSADC,
	})
	if err == nil || !strings.Contains(err.Error(), "application default credentials") {
		t.Fatalf("error = %v", err)
	}
}

func TestGCSReaderListsAndOpens(t *testing.T) {
	const bucket = "flow-logs"
	key := "network/2026/05/08/2026-05-08-13-45-00.ndjson"
	body := []byte("{\"nodeId\":\"n1\"}\n")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/o") && r.URL.Query().Get("alt") != "media" && !strings.Contains(r.URL.RawQuery, "alt=media") {
			if got := r.URL.Query().Get("prefix"); got != "" && !strings.HasPrefix(key, got) && !strings.HasPrefix(got, "network/") {
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"kind":"storage#objects","items":[{"kind":"storage#object","name":"` + key + `","bucket":"` + bucket + `","size":"` + strconv.Itoa(len(body)) + `","updated":"2026-05-08T13:45:01.000Z"}]}`))
			return
		}
		if strings.Contains(r.URL.Path, "2026-05-08-13-45-00.ndjson") || strings.Contains(r.URL.RawPath, "2026-05-08-13-45-00.ndjson") {
			_, _ = w.Write(body)
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()
	t.Setenv("STORAGE_EMULATOR_HOST", server.URL)

	source, err := NewObjectStoreSource(context.Background(), ObjectStoreConfig{
		Bucket:     bucket,
		Prefix:     "network/",
		AuthMode:   config.ObjectStoreAuthGCSADC,
		Lookback:   time.Hour,
		MaxObjects: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	objects, err := source.listObjects(context.Background(),
		time.Date(2026, 5, 8, 13, 0, 0, 0, time.UTC),
		time.Date(2026, 5, 8, 14, 0, 0, 0, time.UTC),
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(objects) != 1 || objects[0].key != key {
		t.Fatalf("objects = %+v", objects)
	}
	rc, err := source.blobs.Open(context.Background(), key)
	if err != nil {
		t.Fatal(err)
	}
	defer rc.Close()
	got, err := io.ReadAll(rc)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(body) {
		t.Fatalf("body = %q", got)
	}
}

func TestAWSDefaultPollerConfig(t *testing.T) {
	cfg := &config.Config{
		TailscaleAPIURL:           "https://api.tailscale.com",
		TailscaleAPIKey:           "tskey-test",
		PollInterval:              "5m",
		InitialBackfill:           "6h",
		FlowObjectStoreBucket:     "flow-logs",
		FlowObjectStoreRegion:     "us-east-1",
		FlowObjectStorePrefix:     "network/",
		FlowObjectStoreAuth:       config.ObjectStoreAuthAWSDefault,
		FlowObjectStoreRoleARN:    "arn:aws:iam::123456789012:role/tsflow-reader",
		FlowObjectStoreLookback:   "15m",
		FlowObjectStoreMaxObjects: 500,
	}
	pc, err := PollerConfigFrom(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if pc.FlowBackend != "s3" || pc.ObjectStore.AuthMode != config.ObjectStoreAuthAWSDefault || pc.ObjectStore.RoleARN == "" {
		t.Fatalf("poller config = %+v", pc)
	}
	if pc.ObjectStore.Endpoint != "" {
		t.Fatalf("endpoint = %q", pc.ObjectStore.Endpoint)
	}
}

func TestWorkloadIdentityServiceIgnoresAPIKeyHeader(t *testing.T) {
	// A WIF client must not also send a static bearer token. The exchange
	// transport is what adds Authorization, and only after the token call.
	token := testIDToken(time.Now().Add(time.Hour).Unix())
	var sawStatic bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v2/oauth/token-exchange" && r.Header.Get("Authorization") == "Bearer tskey-test" {
			sawStatic = true
		}
		if r.URL.Path == "/api/v2/oauth/token-exchange" {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"access_token": "access",
				"token_type":   "Bearer",
				"expires_in":   3600,
			})
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"users":[]}`))
	}))
	defer server.Close()
	service := NewTailscaleService(&config.Config{
		TailscaleAPIURL:      server.URL,
		TailscaleTailnet:     "example.com",
		TailscaleAPIKey:      "tskey-test",
		TailscaleAuth:        config.TailscaleAuthWIF,
		TailscaleWIFClientID: "client",
		TailscaleWIFIDToken:  token,
	})
	if _, err := service.GetUsers(); err != nil {
		t.Fatal(err)
	}
	if sawStatic {
		t.Fatal("workload identity client sent the API key")
	}
	parsed, err := url.Parse(server.URL)
	if err != nil || service.tsClient == nil || service.tsClient.BaseURL.Host != parsed.Host {
		t.Fatalf("client base = %v", service.tsClient)
	}
}
