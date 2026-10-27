package metrics

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus/testutil"
	dto "github.com/prometheus/client_model/go"
	"github.com/rajsinghtech/tsflow/backend/internal/config"
	"github.com/rajsinghtech/tsflow/backend/internal/database"
	"github.com/rajsinghtech/tsflow/backend/internal/services"
)

func init() { gin.SetMode(gin.TestMode) }

// fakeAPI answers the Tailscale API with one flow log for tailnet
// example.com and a 500 for every other tailnet.
func fakeAPI(logged time.Time) http.Handler {
	stamp := logged.Format(time.RFC3339)
	logs := `{"logs":[{"logged":"` + stamp + `","start":"` + stamp + `","end":"` + stamp +
		`","nodeId":"node-a","virtualTraffic":[{"proto":6,"src":"100.64.0.1:1","dst":"100.64.0.2:443","txBytes":125,"rxBytes":4}]}]}`
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/tailnet/example.com/") {
			http.Error(w, "boom", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(r.URL.Path, "/logging/network") {
			_, _ = io.WriteString(w, logs)
			return
		}
		_, _ = io.WriteString(w, `{"devices":[]}`)
	})
}

func newStore(t *testing.T) *database.SQLiteStore {
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

func gather(t *testing.T, m *Metrics) map[string]*dto.MetricFamily {
	t.Helper()
	families, err := m.Registry().Gather()
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]*dto.MetricFamily{}
	for _, f := range families {
		out[f.GetName()] = f
	}
	return out
}

// value finds the sample whose labels include every pair in want.
func value(t *testing.T, fams map[string]*dto.MetricFamily, name string, want ...string) (*dto.Metric, bool) {
	t.Helper()
	f, ok := fams[name]
	if !ok {
		return nil, false
	}
next:
	for _, m := range f.GetMetric() {
		labels := map[string]string{}
		for _, l := range m.GetLabel() {
			labels[l.GetName()] = l.GetValue()
		}
		for i := 0; i+1 < len(want); i += 2 {
			if labels[want[i]] != want[i+1] {
				continue next
			}
		}
		return m, true
	}
	return nil, false
}

func TestHealthMetricsScrape(t *testing.T) {
	logged := time.Now().Add(-5 * time.Minute).Truncate(time.Second)
	api := httptest.NewServer(fakeAPI(logged))
	defer api.Close()

	store := newStore(t)
	base := services.DefaultPollerConfig()
	base.PollInterval = time.Hour
	base.InitialBackfill = 20 * time.Minute
	base.CleanupInterval = time.Hour
	base.DeviceCacheRefresh = time.Hour
	registry, err := services.NewRegistry(context.Background(), []config.TailnetSpec{
		{ID: "default", Name: "example.com", APIURL: api.URL, APIKey: "k"},
		{ID: "broken", Name: "broken.example.com", APIURL: api.URL, APIKey: "k"},
	}, store, base)
	if err != nil {
		t.Fatal(err)
	}
	m := New(Options{Version: "v-test", Registry: registry, Store: SQLiteStore(store)})
	m.SetDBInitDuration(1500 * time.Millisecond)
	m.Instrument(registry)

	// Nothing has polled yet: the cursor and newest-bucket series are absent
	// rather than reporting a fake zero time.
	fams := gather(t, m)
	if _, ok := value(t, fams, "tsflow_poller_cursor_timestamp_seconds"); ok {
		t.Fatal("cursor reported before the first poll")
	}
	if _, ok := value(t, fams, "tsflow_flow_newest_timestamp_seconds"); ok {
		t.Fatal("newest bucket reported with no data")
	}
	if c, _ := value(t, fams, "tsflow_pair_row_counters_ready"); c.GetGauge().GetValue() != 0 {
		t.Fatal("counters ready before the build finished")
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := registry.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(registry.Stop)

	deadline := time.Now().Add(10 * time.Second)
	for {
		fams = gather(t, m)
		ok, _ := value(t, fams, "tsflow_poller_last_success_timestamp_seconds", "tailnet", "default")
		bad, _ := value(t, fams, "tsflow_poller_errors_total", "tailnet", "broken")
		if ok.GetGauge().GetValue() > 0 && bad.GetCounter().GetValue() > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("polls did not finish: default=%v broken=%v", ok, bad)
		}
		time.Sleep(20 * time.Millisecond)
	}
	m.PairRowCountersDone(250*time.Millisecond, nil)
	fams = gather(t, m)

	checks := []struct {
		name   string
		labels []string
		want   float64
	}{
		{"tsflow_poller_up", []string{"tailnet", "default", "backend", "api"}, 1},
		{"tsflow_poller_ingested_flow_logs_total", []string{"tailnet", "default"}, 1},
		{"tsflow_poller_last_poll_flow_logs", []string{"tailnet", "default"}, 1},
		{"tsflow_poller_errors_total", []string{"tailnet", "default"}, 0},
		{"tsflow_poller_last_success_timestamp_seconds", []string{"tailnet", "broken"}, 0},
		{"tsflow_flow_newest_timestamp_seconds", []string{"tailnet", "default"}, float64(logged.Truncate(time.Minute).Unix())},
		{"tsflow_db_init_duration_seconds", nil, 1.5},
		{"tsflow_pair_row_counters_ready", nil, 1},
		{"tsflow_pair_row_counters_build_duration_seconds", nil, 0.25},
		{"tsflow_build_info", []string{"version", "v-test"}, 1},
	}
	for _, c := range checks {
		got, ok := value(t, fams, c.name, c.labels...)
		if !ok {
			t.Errorf("%s%v missing", c.name, c.labels)
			continue
		}
		v := got.GetGauge().GetValue() + got.GetCounter().GetValue()
		if v != c.want {
			t.Errorf("%s%v = %v, want %v", c.name, c.labels, v, c.want)
		}
	}
	if got, _ := value(t, fams, "tsflow_poller_cursor_timestamp_seconds", "tailnet", "default"); got.GetGauge().GetValue() < float64(logged.Unix()) {
		t.Errorf("cursor = %v, want at or after the logged time", got)
	}
	if _, ok := value(t, fams, "tsflow_flow_newest_timestamp_seconds", "tailnet", "broken"); ok {
		t.Error("newest bucket reported for a tailnet with no rows")
	}
	if got, _ := value(t, fams, "tsflow_db_size_bytes"); got.GetGauge().GetValue() <= 0 {
		t.Error("db size is not positive")
	}
	if got, _ := value(t, fams, "tsflow_poll_duration_seconds", "tailnet", "default", "result", "success"); got.GetHistogram().GetSampleCount() != 1 {
		t.Errorf("default success polls = %v, want 1", got)
	}
	if got, _ := value(t, fams, "tsflow_poll_duration_seconds", "tailnet", "broken", "result", "error"); got.GetHistogram().GetSampleCount() < 1 {
		t.Errorf("broken failed polls = %v, want at least 1", got)
	}
	// Traffic metrics stay off even after real polls when not asked for.
	for _, name := range trafficFamilies {
		if _, ok := fams[name]; ok {
			t.Errorf("%s served without TSFLOW_METRICS_TRAFFIC", name)
		}
	}
	for _, name := range []string{"go_goroutines", "process_resident_memory_bytes", "process_cpu_seconds_total"} {
		if _, ok := fams[name]; !ok {
			t.Errorf("%s missing; the runtime and process collectors are not registered", name)
		}
	}
	if n, err := testutil.GatherAndCount(m.Registry(), "tsflow_metrics_collect_errors_total"); err != nil || n != 1 {
		t.Errorf("collect error counter count=%d err=%v", n, err)
	}
	if got, _ := value(t, fams, "tsflow_metrics_collect_errors_total"); got.GetCounter().GetValue() != 0 {
		t.Errorf("collect errors = %v", got)
	}
}

func TestHTTPMiddlewareUsesRouteTemplates(t *testing.T) {
	m := New(Options{})
	router := gin.New()
	router.Use(m.Middleware())
	router.GET("/api/devices/:deviceId/flows", func(c *gin.Context) { c.Status(http.StatusOK) })
	router.GET("/api/fail", func(c *gin.Context) { c.Status(http.StatusBadGateway) })
	router.NoRoute(func(c *gin.Context) { c.Status(http.StatusOK) })

	for i := 0; i < 50; i++ {
		req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/devices/node-%d/flows", i), nil)
		router.ServeHTTP(httptest.NewRecorder(), req)
	}
	for _, path := range []string{"/", "/analytics", "/some/spa/page"} {
		router.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, path, nil))
	}
	router.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/api/fail", nil))
	router.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("BREW", "/api/fail", nil))

	fams := gather(t, m)
	if got, _ := value(t, fams, "tsflow_http_request_duration_seconds", "route", "/api/devices/:deviceId/flows", "method", "GET", "code", "200"); got.GetHistogram().GetSampleCount() != 50 {
		t.Errorf("templated route samples = %v, want 50", got)
	}
	if got, _ := value(t, fams, "tsflow_http_request_duration_seconds", "route", "other", "code", "200"); got.GetHistogram().GetSampleCount() != 3 {
		t.Errorf("unmatched samples = %v, want 3", got)
	}
	if _, ok := value(t, fams, "tsflow_http_request_duration_seconds", "route", "/api/fail", "code", "502"); !ok {
		t.Error("502 not recorded")
	}
	if _, ok := value(t, fams, "tsflow_http_request_duration_seconds", "method", "OTHER"); !ok {
		t.Error("unknown method not folded into OTHER")
	}
	// 50 device ids, 3 UI paths and two methods collapse to four series.
	if n := len(fams["tsflow_http_request_duration_seconds"].GetMetric()); n != 4 {
		t.Errorf("http series = %d, want 4", n)
	}
}

func TestMCPToolMetrics(t *testing.T) {
	m := New(Options{})
	m.ObserveMCPTool("top_talkers", 20*time.Millisecond, false)
	m.ObserveMCPTool("top_talkers", 30*time.Millisecond, true)
	m.ObserveMCPTool("unknown", time.Millisecond, true)
	want := `
# HELP tsflow_mcp_tool_calls_total MCP tool calls by tool and result.
# TYPE tsflow_mcp_tool_calls_total counter
tsflow_mcp_tool_calls_total{result="error",tool="top_talkers"} 1
tsflow_mcp_tool_calls_total{result="error",tool="unknown"} 1
tsflow_mcp_tool_calls_total{result="success",tool="top_talkers"} 1
`
	if err := testutil.GatherAndCompare(m.Registry(), strings.NewReader(want), "tsflow_mcp_tool_calls_total"); err != nil {
		t.Fatal(err)
	}
}

func TestHandlerServesTextFormat(t *testing.T) {
	m := New(Options{Version: "v-test"})
	rec := httptest.NewRecorder()
	m.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `tsflow_build_info{goversion=`) {
		t.Fatalf("scrape = %d %s", rec.Code, rec.Body.String())
	}
}

func TestFailedCounterBuildStaysNotReady(t *testing.T) {
	m := New(Options{})
	m.PairRowCountersDone(time.Second, fmt.Errorf("disk full"))
	if v := testutil.ToFloat64(m.countersReady); v != 0 {
		t.Fatalf("ready = %v after a failed build", v)
	}
}
