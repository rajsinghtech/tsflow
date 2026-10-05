package services

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/rajsinghtech/tsflow/backend/internal/config"
	"github.com/rajsinghtech/tsflow/backend/internal/database"
)

func TestEnvRegistryMatchesDirectPoller(t *testing.T) {
	logged := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	server := httptest.NewServer(tailscaleAPI(t, "example.com", flowLogBody(logged, "node-env", 125), ""))
	defer server.Close()

	t.Setenv("TSFLOW_TAILNETS_FILE", "")
	t.Setenv("TAILSCALE_TAILNET", "example.com")
	t.Setenv("TAILSCALE_API_KEY", "env-key")
	t.Setenv("TAILSCALE_API_URL", server.URL)
	t.Setenv("TAILSCALE_OAUTH_CLIENT_ID", "")
	t.Setenv("TAILSCALE_OAUTH_CLIENT_SECRET", "")
	t.Setenv("VITE_TAILSCALE_API_KEY", "")
	t.Setenv("VITE_TAILSCALE_OAUTH_CLIENT_ID", "")
	t.Setenv("VITE_TAILSCALE_OAUTH_CLIENT_SECRET", "")
	t.Setenv("TSFLOW_POLL_INTERVAL", "1h")
	t.Setenv("TSFLOW_INITIAL_BACKFILL", "20m")
	t.Setenv("TSFLOW_RETENTION", "24h")
	t.Setenv("TSFLOW_FLOW_BACKEND", "api")
	t.Setenv("TSFLOW_S3_PREFIX", "network/")

	cfg := config.Load()
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	specs, err := cfg.ResolveTailnets()
	if err != nil {
		t.Fatal(err)
	}
	if len(specs) != 1 || specs[0].ID != database.DefaultTailnetID {
		t.Fatalf("env specs = %+v", specs)
	}
	base, err := PollerConfigFrom(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if base.TailnetID != "" || base.PollInterval != time.Hour || base.InitialBackfill != 20*time.Minute || base.Retention != 24*time.Hour {
		t.Fatalf("process poller config = %+v", base)
	}
	if base.FlowBackend != "api" || base.ObjectStore.Prefix != "network/" {
		t.Fatalf("backend/prefix = %s %s", base.FlowBackend, base.ObjectStore.Prefix)
	}

	registryStore := newRegistryStore(t)
	directStore := newRegistryStore(t)
	registry, err := NewRegistry(context.Background(), specs, registryStore, base)
	if err != nil {
		t.Fatal(err)
	}
	entry, ok := registry.Default()
	if !ok || entry.Poller.tailnetID != database.DefaultTailnetID {
		t.Fatalf("default runtime = %+v", entry)
	}
	if entry.Service.tailnet != "example.com" || entry.Service.apiKey != "env-key" {
		t.Fatalf("registry service does not match env credentials")
	}

	direct := NewPoller(NewTailscaleService(cfg), directStore, base)
	ctx := context.Background()
	if err := entry.Poller.poll(ctx); err != nil {
		t.Fatal(err)
	}
	if err := direct.poll(ctx); err != nil {
		t.Fatal(err)
	}

	start := logged.Add(-time.Minute)
	end := logged.Add(time.Minute)
	registryPairs, err := registryStore.GetNodePairAggregates(ctx, database.DefaultTailnetID, start, end)
	if err != nil {
		t.Fatal(err)
	}
	directPairs, err := directStore.GetNodePairAggregates(ctx, database.DefaultTailnetID, start, end)
	if err != nil {
		t.Fatal(err)
	}
	if len(registryPairs) != 1 || len(directPairs) != 1 {
		t.Fatalf("pairs registry=%+v direct=%+v", registryPairs, directPairs)
	}
	if registryPairs[0].SrcNodeID != directPairs[0].SrcNodeID || registryPairs[0].TxBytes != directPairs[0].TxBytes || registryPairs[0].TxBytes != 125 {
		t.Fatalf("pair mismatch registry=%+v direct=%+v", registryPairs[0], directPairs[0])
	}
	other, err := registryStore.GetNodePairAggregates(ctx, "other", start, end)
	if err != nil {
		t.Fatal(err)
	}
	if len(other) != 0 {
		t.Fatalf("default poll wrote another tailnet: %+v", other)
	}
}

func TestRegistryPollersIsolateFailureAndDelay(t *testing.T) {
	logged := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	release := make(chan struct{})
	slowHit := make(chan struct{}, 1)
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/tailnet/slow.example/") {
			t.Errorf("slow server saw unexpected path %s", r.URL.Path)
		}
		select {
		case slowHit <- struct{}{}:
		default:
		}
		select {
		case <-release:
		case <-r.Context().Done():
		}
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	}))
	defer slow.Close()

	fast := httptest.NewServer(tailscaleAPI(t, "fast.example", flowLogBody(logged, "node-fast", 80), ""))
	defer fast.Close()

	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/tailnet/bad.example/") {
			t.Errorf("bad server saw unexpected path %s", r.URL.Path)
		}
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	}))
	defer bad.Close()

	store := newRegistryStore(t)
	base := DefaultPollerConfig()
	base.PollInterval = time.Hour
	base.InitialBackfill = 20 * time.Minute
	base.CleanupInterval = time.Hour
	base.Retention = 0
	base.FlowBackend = "api"
	base.DeviceCacheRefresh = time.Hour

	registry, err := NewRegistry(context.Background(), []config.TailnetSpec{
		{ID: "slow", Name: "slow.example", APIURL: slow.URL, APIKey: "slow"},
		{ID: "fast", Name: "fast.example", APIURL: fast.URL, APIKey: "fast"},
		{ID: "bad", Name: "bad.example", APIURL: bad.URL, APIKey: "bad"},
	}, store, base)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := registry.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		close(release)
		cancel()
		registry.Stop()
	})

	select {
	case <-slowHit:
	case <-time.After(2 * time.Second):
		t.Fatal("slow tailnet did not start its request")
	}

	deadline := time.Now().Add(2 * time.Second)
	var fastPairs []database.NodePairAggregate
	for time.Now().Before(deadline) {
		fastPairs, err = store.GetNodePairAggregates(ctx, "fast", logged.Add(-time.Minute), logged.Add(time.Minute))
		if err != nil {
			t.Fatal(err)
		}
		if len(fastPairs) == 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if len(fastPairs) != 1 || fastPairs[0].TxBytes != 80 {
		t.Fatalf("fast tailnet was stalled by the others: %+v", fastPairs)
	}
	slowPairs, err := store.GetNodePairAggregates(ctx, "slow", logged.Add(-time.Hour), logged.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(slowPairs) != 0 {
		t.Fatalf("slow tailnet wrote data while its API was blocked: %+v", slowPairs)
	}

	badEntry, _ := registry.Get("bad")
	authDeadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(authDeadline) {
		if badEntry.Poller.Stats()["pollErrors"].(int64) > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if badEntry.Poller.Stats()["pollErrors"].(int64) == 0 {
		t.Fatal("unauthorized tailnet did not record a poll error")
	}
	badPairs, err := store.GetNodePairAggregates(ctx, "bad", logged.Add(-time.Hour), logged.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(badPairs) != 0 {
		t.Fatalf("unauthorized tailnet wrote rows: %+v", badPairs)
	}
}

func TestRegistryUsesPerTailnetObjectPrefix(t *testing.T) {
	base := DefaultPollerConfig()
	base.FlowBackend = "api"
	base.ObjectStore.Prefix = "network/"
	registry, err := NewRegistry(context.Background(), []config.TailnetSpec{
		{ID: database.DefaultTailnetID, Name: "example.com", APIURL: "https://api.tailscale.com", APIKey: "a"},
		{ID: "lab", Name: "lab.example.com", APIURL: "https://api.tailscale.com", APIKey: "b", S3Prefix: "lab/network/"},
	}, nil, base)
	if err != nil {
		t.Fatal(err)
	}
	home, _ := registry.Default()
	lab, _ := registry.Get("lab")
	if home.Poller.config.ObjectStore.Prefix != "network/" || lab.Poller.config.ObjectStore.Prefix != "lab/network/" {
		t.Fatalf("prefixes home=%q lab=%q", home.Poller.config.ObjectStore.Prefix, lab.Poller.config.ObjectStore.Prefix)
	}
	if home.Poller.tailnetID != database.DefaultTailnetID || lab.Poller.tailnetID != "lab" {
		t.Fatalf("ids home=%s lab=%s", home.Poller.tailnetID, lab.Poller.tailnetID)
	}
}

func tailscaleAPI(t *testing.T, tailnet, logsBody, devicesBody string) http.Handler {
	t.Helper()
	if devicesBody == "" {
		devicesBody = `{"devices":[]}`
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/tailnet/"+tailnet+"/") {
			http.Error(w, "unexpected tailnet", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(r.URL.Path, "/logging/network") {
			_, _ = w.Write([]byte(logsBody))
			return
		}
		_, _ = w.Write([]byte(devicesBody))
	})
}

func flowLogBody(logged time.Time, nodeID string, txBytes int) string {
	stamp := logged.Format(time.RFC3339)
	return `{"logs":[{"logged":"` + stamp + `","start":"` + stamp + `","end":"` + stamp + `","nodeId":"` + nodeID + `","virtualTraffic":[{"proto":6,"src":"100.64.0.1:1","dst":"100.64.0.2:443","txBytes":` + itoa(txBytes) + `,"rxBytes":4}]}]}`
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var digits [16]byte
	i := len(digits)
	for n > 0 {
		i--
		digits[i] = byte('0' + n%10)
		n /= 10
	}
	return string(digits[i:])
}

func newRegistryStore(t *testing.T) *database.SQLiteStore {
	t.Helper()
	store, err := database.NewSQLiteStore(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	if err := store.Init(context.Background()); err != nil {
		t.Fatal(err)
	}
	return store
}
