package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/rajsinghtech/tsflow/backend/internal/config"
	"github.com/rajsinghtech/tsflow/backend/internal/database"
	"github.com/rajsinghtech/tsflow/backend/internal/services"
)

func TestSingleTailnetEnvRegistryMatchesDirectAPI(t *testing.T) {
	logged := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(r.URL.Path, "/logging/network") {
			_, _ = w.Write([]byte(flowBody(logged)))
			return
		}
		_, _ = w.Write([]byte(`{"devices":[{"id":"device-1","name":"node.example.com","hostname":"node","addresses":["100.64.0.1"],"authorized":true,"lastSeen":"2026-03-01T12:00:00Z","created":"2026-03-01T12:00:00Z"}]}`))
	}))
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

	cfg := config.Load()
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	specs, err := cfg.ResolveTailnets()
	if err != nil {
		t.Fatal(err)
	}
	base, err := services.PollerConfigFrom(cfg)
	if err != nil {
		t.Fatal(err)
	}
	registryStore := newAPIStore(t)
	directStore := newAPIStore(t)
	registry, err := services.NewRegistry(context.Background(), specs, registryStore, base)
	if err != nil {
		t.Fatal(err)
	}
	directService := services.NewTailscaleService(cfg)
	directPoller := services.NewPoller(directService, directStore, base)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := registry.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if err := directPoller.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cancel()
		registry.Stop()
		directPoller.Stop()
	})

	waitForPair(t, registryStore)
	waitForPair(t, directStore)

	entry, ok := registry.Default()
	if !ok {
		t.Fatal("missing default tailnet")
	}
	start := logged.Add(-time.Minute).Format(time.RFC3339)
	end := logged.Add(time.Minute).Format(time.RFC3339)
	flowURL := "/api/flow-logs/aggregated?start=" + start + "&end=" + end
	registryFlows := callHandler(t, entry.Service, entry.Poller, registryStore, (*Handlers).GetAggregatedFlowLogs, flowURL)
	directFlows := callHandler(t, directService, directPoller, directStore, (*Handlers).GetAggregatedFlowLogs, flowURL)
	if registryFlows != directFlows {
		t.Fatalf("aggregated flows differ\nregistry: %s\ndirect:   %s", registryFlows, directFlows)
	}
	if !strings.Contains(registryFlows, "125") {
		t.Fatalf("aggregated flows missing expected bytes: %s", registryFlows)
	}

	registryDevices := callHandler(t, entry.Service, entry.Poller, registryStore, (*Handlers).GetDevices, "/api/devices")
	directDevices := callHandler(t, directService, directPoller, directStore, (*Handlers).GetDevices, "/api/devices")
	if registryDevices != directDevices {
		t.Fatalf("devices differ\nregistry: %s\ndirect:   %s", registryDevices, directDevices)
	}
	if !strings.Contains(registryDevices, "device-1") {
		t.Fatalf("devices response = %s", registryDevices)
	}
}

func newAPIStore(t *testing.T) *database.SQLiteStore {
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

func waitForPair(t *testing.T, store *database.SQLiteStore) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		pairs, err := store.GetNodePairAggregates(context.Background(), database.DefaultTailnetID, time.Unix(0, 0), time.Now().Add(time.Hour))
		if err != nil {
			t.Fatal(err)
		}
		if len(pairs) > 0 {
			return
		}
		time.Sleep(15 * time.Millisecond)
	}
	t.Fatal("poller did not write node pairs")
}

func callHandler(t *testing.T, service *services.TailscaleService, poller *services.Poller, store *database.SQLiteStore, method func(*Handlers, *gin.Context), target string) string {
	t.Helper()
	h := &Handlers{tailscaleService: service, poller: poller, store: store}
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, target, nil)
	method(h, c)
	if w.Code != http.StatusOK {
		t.Fatalf("%s status=%d body=%s", target, w.Code, w.Body.String())
	}
	return w.Body.String()
}

func flowBody(logged time.Time) string {
	stamp := logged.Format(time.RFC3339)
	return `{"logs":[{"logged":"` + stamp + `","start":"` + stamp + `","end":"` + stamp + `","nodeId":"node-env","virtualTraffic":[{"proto":6,"src":"100.64.0.1:1","dst":"100.64.0.2:443","txBytes":125,"rxBytes":4}]}]}`
}
