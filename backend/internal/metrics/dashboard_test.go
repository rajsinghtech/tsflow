package metrics

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	dto "github.com/prometheus/client_model/go"
	"github.com/rajsinghtech/tsflow/backend/internal/config"
	"github.com/rajsinghtech/tsflow/backend/internal/services"
)

const (
	dashboardPath = "../../../k8s/monitoring/tsflow-dashboard.json"
	rulesPath     = "../../../k8s/monitoring/prometheusrule.yaml"
)

// everyFamily turns on every metric tsflow can serve, runs one poll and one
// request of each kind, and returns the label names each family carries.
func everyFamily(t *testing.T) map[string]map[string]bool {
	t.Helper()
	now := time.Now()
	entry := func(at time.Time) string {
		s := at.Truncate(time.Minute).Add(10 * time.Second).Format(time.RFC3339)
		return `{"logged":"` + s + `","start":"` + s + `","end":"` + s +
			`","nodeId":"nA","virtualTraffic":[{"proto":6,"src":"100.64.0.1:50000","dst":"100.64.0.2:443","txBytes":125,"txPkts":3}]}`
	}
	// Two minutes: the window gauges leave the newest one out.
	logs := `{"logs":[` + entry(now.Add(-4*time.Minute)) + "," + entry(now.Add(-time.Minute)) + `]}`
	devices := `{"devices":[
		{"id":"1","nodeId":"nA","name":"a.example.ts.net","user":"alice@example.com","addresses":["100.64.0.1"],"tags":["tag:web"]},
		{"id":"2","nodeId":"nB","name":"b.example.ts.net","user":"tagged-devices","addresses":["100.64.0.2"],"tags":["tag:db"]}]}`
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(r.URL.Path, "/logging/network") {
			_, _ = io.WriteString(w, logs)
			return
		}
		_, _ = io.WriteString(w, devices)
	}))
	t.Cleanup(api.Close)

	store := newStore(t)
	base := services.DefaultPollerConfig()
	base.PollInterval = time.Hour
	base.InitialBackfill = 20 * time.Minute
	base.CleanupInterval = time.Hour
	base.DeviceCacheRefresh = time.Hour
	registry, err := services.NewRegistry(context.Background(), []config.TailnetSpec{
		{ID: "default", Name: "example.com", APIURL: api.URL, APIKey: "k"},
	}, store, base)
	if err != nil {
		t.Fatal(err)
	}
	m := New(Options{Version: "v-test", Registry: registry, Store: SQLiteStore(store), Traffic: true, TopTalkers: 5})
	m.SetDBInitDuration(time.Second)
	m.PairRowCountersDone(time.Second, nil)
	m.Instrument(registry)
	m.ObserveMCPTool("whoami", time.Millisecond, false)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	if err := registry.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(registry.Stop)

	router := gin.New()
	router.Use(m.Middleware())
	router.GET("/api/health", func(c *gin.Context) { c.Status(http.StatusOK) })
	router.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/api/health", nil))

	deadline := time.Now().Add(10 * time.Second)
	for {
		fams := gather(t, m)
		if v, ok := value(t, fams, "tsflow_traffic_flows_total", "traffic_type", "virtual"); ok && v.GetCounter().GetValue() > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("poll did not finish")
		}
		time.Sleep(20 * time.Millisecond)
	}
	m.trafficWindow.mu.Lock()
	m.trafficWindow.cached = nil
	m.trafficWindow.mu.Unlock()

	out := map[string]map[string]bool{}
	add := func(name string, labels []*dto.LabelPair, extra ...string) {
		if out[name] == nil {
			out[name] = map[string]bool{}
		}
		for _, l := range labels {
			out[name][l.GetName()] = true
		}
		for _, e := range extra {
			out[name][e] = true
		}
	}
	for name, f := range gather(t, m) {
		for _, s := range f.GetMetric() {
			add(name, s.GetLabel())
			switch f.GetType() {
			case dto.MetricType_HISTOGRAM:
				add(name+"_bucket", s.GetLabel(), "le")
				add(name+"_sum", s.GetLabel())
				add(name+"_count", s.GetLabel())
			case dto.MetricType_SUMMARY:
				add(name+"_sum", s.GetLabel())
				add(name+"_count", s.GetLabel())
			}
		}
	}
	return out
}

var (
	metricRef = regexp.MustCompile(`\b((?:tsflow|go|process)_[a-z0-9_]+)\b(\{[^}]*\})?`)
	matcher   = regexp.MustCompile(`([a-zA-Z_][a-zA-Z0-9_]*)\s*(?:=~|!~|!=|=)`)
	byClause  = regexp.MustCompile(`\b(?:by|without)\s*\(([^)]*)\)`)
)

// Prometheus adds these to every series; le belongs to histogram buckets.
var scrapeLabels = map[string]bool{"job": true, "instance": true}

// checkExpr reports metric names, selector labels and by() labels in expr
// that tsflow does not serve.
func checkExpr(families map[string]map[string]bool, expr string) []string {
	var problems []string
	used := map[string]bool{}
	for _, m := range metricRef.FindAllStringSubmatch(expr, -1) {
		labels, ok := families[m[1]]
		if !ok {
			problems = append(problems, "unknown metric "+m[1])
			continue
		}
		for k := range labels {
			used[k] = true
		}
		for _, l := range matcher.FindAllStringSubmatch(m[2], -1) {
			if !labels[l[1]] && !scrapeLabels[l[1]] {
				problems = append(problems, fmt.Sprintf("%s has no label %q", m[1], l[1]))
			}
		}
	}
	for _, b := range byClause.FindAllStringSubmatch(expr, -1) {
		for _, l := range strings.Split(b[1], ",") {
			if l = strings.TrimSpace(l); l != "" && !used[l] && !scrapeLabels[l] {
				problems = append(problems, fmt.Sprintf("by (%s): no metric in the query has it", l))
			}
		}
	}
	return problems
}

type dashboardPanel struct {
	ID          int    `json:"id"`
	Type        string `json:"type"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Datasource  *struct {
		Type string `json:"type"`
		UID  string `json:"uid"`
	} `json:"datasource"`
	GridPos struct{ X, Y, W, H int } `json:"gridPos"`
	Targets []struct {
		Expr       string `json:"expr"`
		Datasource *struct {
			UID string `json:"uid"`
		} `json:"datasource"`
	} `json:"targets"`
}

type dashboard struct {
	UID           string           `json:"uid"`
	SchemaVersion int              `json:"schemaVersion"`
	Panels        []dashboardPanel `json:"panels"`
	Templating    struct {
		List []struct {
			Name  string `json:"name"`
			Type  string `json:"type"`
			Query any    `json:"query"`
		} `json:"list"`
	} `json:"templating"`
	Annotations struct {
		List []struct {
			Expr string `json:"expr"`
		} `json:"list"`
	} `json:"annotations"`
}

// The shipped Grafana dashboard and alert rules only query metrics and
// labels tsflow actually serves, and stay importable anywhere.
func TestDashboardMatchesMetrics(t *testing.T) {
	raw, err := os.ReadFile(dashboardPath)
	if err != nil {
		t.Fatal(err)
	}
	var d dashboard
	if err := json.Unmarshal(raw, &d); err != nil {
		t.Fatalf("dashboard is not valid JSON: %v", err)
	}
	if strings.Contains(string(raw), "__inputs") || strings.Contains(string(raw), "DS_PROMETHEUS") {
		t.Error("use the datasource variable, not import-time inputs")
	}
	if d.UID == "" || d.SchemaVersion < 36 {
		t.Errorf("uid %q schemaVersion %d", d.UID, d.SchemaVersion)
	}
	vars := map[string]string{}
	for _, v := range d.Templating.List {
		vars[v.Name] = v.Type
	}
	for name, typ := range map[string]string{"datasource": "datasource", "job": "query", "instance": "query", "tailnet": "query", "window": "custom"} {
		if vars[name] != typ {
			t.Errorf("variable %s is %q, want %q", name, vars[name], typ)
		}
	}

	families := everyFamily(t)
	var exprs []string
	seen := map[int]bool{}
	for _, p := range d.Panels {
		if seen[p.ID] {
			t.Errorf("panel id %d used twice", p.ID)
		}
		seen[p.ID] = true
		if p.GridPos.X+p.GridPos.W > 24 || p.GridPos.W <= 0 || p.GridPos.H <= 0 {
			t.Errorf("panel %q gridPos %+v", p.Title, p.GridPos)
		}
		if p.Type == "row" {
			continue
		}
		if p.Description == "" {
			t.Errorf("panel %q has no description", p.Title)
		}
		if p.Datasource == nil || p.Datasource.UID != "${datasource}" {
			t.Errorf("panel %q datasource %+v, want uid ${datasource}", p.Title, p.Datasource)
		}
		if len(p.Targets) == 0 {
			t.Errorf("panel %q has no queries", p.Title)
		}
		for _, q := range p.Targets {
			if q.Datasource == nil || q.Datasource.UID != "${datasource}" {
				t.Errorf("panel %q query datasource %+v, want uid ${datasource}", p.Title, q.Datasource)
			}
			exprs = append(exprs, q.Expr)
		}
	}
	for _, v := range d.Templating.List {
		if q, ok := v.Query.(map[string]any); ok {
			exprs = append(exprs, fmt.Sprint(q["query"]))
		}
	}
	for _, a := range d.Annotations.List {
		if a.Expr != "" {
			exprs = append(exprs, a.Expr)
		}
	}

	rules, err := os.ReadFile(rulesPath)
	if err != nil {
		t.Fatal(err)
	}
	ruleExprs := 0
	lines := strings.Split(string(rules), "\n")
	for i := 0; i < len(lines); i++ {
		line := strings.TrimSpace(lines[i])
		if !strings.HasPrefix(line, "expr:") {
			continue
		}
		expr := strings.TrimSpace(strings.TrimPrefix(line, "expr:"))
		if expr == "|" {
			expr = ""
			indent := len(lines[i]) - len(strings.TrimLeft(lines[i], " "))
			for i+1 < len(lines) && len(lines[i+1])-len(strings.TrimLeft(lines[i+1], " ")) > indent {
				i++
				expr += lines[i] + "\n"
			}
		}
		exprs = append(exprs, expr)
		ruleExprs++
	}
	if ruleExprs == 0 {
		t.Error("no alert expressions found in " + rulesPath)
	}

	var problems []string
	for _, e := range exprs {
		for _, p := range checkExpr(families, e) {
			problems = append(problems, p+" in: "+strings.TrimSpace(e))
		}
	}
	sort.Strings(problems)
	for _, p := range problems {
		t.Error(p)
	}
}

func TestCheckExprCatchesTypos(t *testing.T) {
	families := map[string]map[string]bool{"tsflow_x_total": {"tailnet": true}}
	if p := checkExpr(families, `sum by (tailnet, instance) (rate(tsflow_x_total{tailnet=~"a",job="j"}[5m]))`); len(p) != 0 {
		t.Fatalf("clean expr flagged: %v", p)
	}
	for _, bad := range []string{`tsflow_y_total`, `tsflow_x_total{tailent="a"}`, `sum by (direction) (tsflow_x_total)`} {
		if len(checkExpr(families, bad)) == 0 {
			t.Errorf("%s not flagged", bad)
		}
	}
}
