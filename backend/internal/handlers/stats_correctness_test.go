package handlers

import (
	"context"
	"encoding/json"
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

func TestStatsOverviewUniquePairsSpanTheWindow(t *testing.T) {
	store := setupHandlerTestDB(t)
	base := time.Now().UTC().Truncate(time.Minute).Add(-time.Hour).Unix()
	ctx := context.Background()
	if err := store.UpsertNodePairAggregates(ctx, database.DefaultTailnetID, []database.NodePairAggregate{
		{Bucket: base, SrcNodeID: "a", DstNodeID: "b", TrafficType: "virtual", TxBytes: 10, FlowCount: 1, Protocols: "[6]", ProtocolBytes: `{"6":10}`},
		{Bucket: base + 60, SrcNodeID: "c", DstNodeID: "d", TrafficType: "subnet", TxBytes: 20, FlowCount: 1, Protocols: "[17]", ProtocolBytes: `{"17":20}`},
	}); err != nil {
		t.Fatal(err)
	}

	body := readOverview(t, store, time.Unix(base, 0).UTC(), time.Unix(base+120, 0).UTC(), "")
	if len(body.Buckets) != 2 || body.Buckets[0].UniquePairs != 1 || body.Buckets[1].UniquePairs != 1 {
		t.Fatalf("buckets = %+v, want one pair in each minute", body.Buckets)
	}
	if body.Summary.UniquePairs != 2 {
		t.Fatalf("summary uniquePairs = %d, want 2 distinct pairs across the window", body.Summary.UniquePairs)
	}
}

func TestStatsOverviewOmitsPhysicalFromProtocolTotals(t *testing.T) {
	store := setupHandlerTestDB(t)
	base := time.Now().UTC().Truncate(time.Minute).Add(-time.Hour).Unix()
	ctx := context.Background()
	if err := store.UpsertNodePairAggregates(ctx, database.DefaultTailnetID, []database.NodePairAggregate{
		{Bucket: base, SrcNodeID: "a", DstNodeID: "b", TrafficType: "virtual", TxBytes: 80, FlowCount: 1, Protocols: "[6]", ProtocolBytes: `{"6":80}`, Ports: `[{"port":443,"proto":6,"bytes":80}]`},
		{Bucket: base, SrcNodeID: "a", DstNodeID: "127.3.3.40", TrafficType: "physical", TxBytes: 200, FlowCount: 1, Protocols: "[0]", ProtocolBytes: `{"0":200}`, Ports: `[{"port":27,"proto":0,"bytes":200}]`},
	}); err != nil {
		t.Fatal(err)
	}
	start := time.Unix(base, 0).UTC()
	end := start.Add(time.Minute)

	body := readOverview(t, store, start, end, "")
	if body.Summary.TCPBytes != 80 || body.Summary.OtherProtoBytes != 0 || body.Summary.PhysicalBytes != 200 || body.Summary.VirtualBytes != 80 {
		t.Fatalf("summary = %+v, want physical bytes kept separate", body.Summary)
	}
	if strings.Contains(body.Buckets[0].TopPorts, `"port":27`) {
		t.Fatalf("top ports include a DERP region: %s", body.Buckets[0].TopPorts)
	}

	included := readOverview(t, store, start, end, "virtual,physical")
	if included.Summary.TCPBytes != 80 || included.Summary.OtherProtoBytes != 200 || included.Summary.PhysicalBytes != 200 {
		t.Fatalf("included summary = %+v, want physical protocol bytes when requested", included.Summary)
	}
}

func TestTopTalkersLabelDERPAndOmitItByDefault(t *testing.T) {
	store := setupHandlerTestDB(t)
	base := time.Now().UTC().Truncate(time.Minute).Add(-time.Hour)
	ctx := context.Background()
	if err := store.UpsertNodePairAggregates(ctx, database.DefaultTailnetID, []database.NodePairAggregate{
		{Bucket: base.Unix(), SrcNodeID: "laptop", DstNodeID: "peer", TrafficType: "virtual", TxBytes: 15, FlowCount: 1, Protocols: "[6]", ProtocolBytes: `{"6":15}`},
		{Bucket: base.Unix(), SrcNodeID: "127.3.3.40", DstNodeID: "relay", TrafficType: "physical", TxBytes: 900, FlowCount: 2, Protocols: "[0]", ProtocolBytes: `{"0":900}`},
	}); err != nil {
		t.Fatal(err)
	}
	h := &Handlers{store: store}
	window := "?start=" + base.Format(time.RFC3339) + "&end=" + base.Add(time.Minute).Format(time.RFC3339)

	defaultBody := handlerJSON(t, h, func(c *gin.Context) { h.GetTopTalkers(c) }, "/api/stats/top-talkers"+window)
	if strings.Contains(defaultBody, "127.3.3.40") {
		t.Fatalf("default talkers include DERP: %s", defaultBody)
	}

	physicalBody := handlerJSON(t, h, func(c *gin.Context) { h.GetRankedTalkers(c) }, "/api/analytics/talkers"+window+"&trafficTypes=physical")
	if !strings.Contains(physicalBody, `"nodeId":"127.3.3.40"`) || !strings.Contains(physicalBody, `"hostname":"DERP relay"`) {
		t.Fatalf("physical ranking = %s, want the DERP relay labeled", physicalBody)
	}
}

func TestResolveNodeNameLabelsDERPRelay(t *testing.T) {
	h := &Handlers{}
	if got := h.resolveNodeName(nil, "127.3.3.40"); got != "DERP relay" {
		t.Fatalf("name = %q", got)
	}
	if got := h.resolveNodeName(nil, "127.3.3.40:27"); got != "DERP relay" {
		t.Fatalf("region address name = %q", got)
	}
	if got := h.resolveNodeName(nil, "100.64.0.1"); got != "" {
		t.Fatalf("ordinary IP name = %q", got)
	}
}

func TestPartialBucketCoverageUsesActualOverlap(t *testing.T) {
	start := time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC)
	end := start.Add(2 * time.Hour)
	day := 24 * time.Hour
	buckets := []database.BandwidthBucket{
		{Time: time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC), TxBytes: 1000},
		{Time: time.Date(2026, 3, 8, 0, 0, 0, 0, time.UTC), TxBytes: 1000},
	}
	applyBucketCoverage(buckets[:1], start, end, int64(day.Seconds()))
	if buckets[0].Seconds != 2*3600 {
		t.Fatalf("first bucket seconds = %d, want 7200", buckets[0].Seconds)
	}

	weekEnd := time.Date(2026, 3, 8, 2, 0, 0, 0, time.UTC)
	applyBucketCoverage(buckets[1:], time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC), weekEnd, int64(day.Seconds()))
	if buckets[1].Seconds != 2*3600 {
		t.Fatalf("last bucket seconds = %d, want 7200", buckets[1].Seconds)
	}
}

func TestNetworkLogsCapsWindowAndLimit(t *testing.T) {
	var hits int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"logs":[{"id":1},{"id":2},{"id":3}]}`))
	}))
	defer server.Close()
	h := NewHandlers(services.NewTailscaleService(&config.Config{
		TailscaleAPIURL:  server.URL,
		TailscaleTailnet: "example.com",
		TailscaleAPIKey:  "test-key",
	}), nil, nil, "test")

	start := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	small := handlerJSON(t, h, func(c *gin.Context) { h.GetNetworkLogs(c) },
		"/api/network-logs?limit=2&start="+start.Format(time.RFC3339)+"&end="+start.Add(10*time.Minute).Format(time.RFC3339))
	var limited struct {
		Logs []json.RawMessage `json:"logs"`
		Meta struct {
			Limit     int  `json:"limit"`
			Returned  int  `json:"returned"`
			Total     int  `json:"total"`
			Truncated bool `json:"truncated"`
		} `json:"metadata"`
	}
	if err := json.Unmarshal([]byte(small), &limited); err != nil {
		t.Fatal(err)
	}
	if len(limited.Logs) != 2 || limited.Meta.Limit != 2 || limited.Meta.Returned != 2 || limited.Meta.Total != 3 || !limited.Meta.Truncated {
		t.Fatalf("limited logs = %s", small)
	}
	if hits != 1 {
		t.Fatalf("upstream hits = %d, want 1", hits)
	}

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/network-logs?start="+start.Format(time.RFC3339)+"&end="+start.Add(2*time.Hour).Format(time.RFC3339), nil)
	h.GetNetworkLogs(c)
	if w.Code != http.StatusGone {
		t.Fatalf("status=%d body=%s, want 410", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "/api/flow-logs/aggregated") {
		t.Fatalf("gone body = %s", w.Body.String())
	}
	if hits != 1 {
		t.Fatalf("oversized window still called upstream, hits=%d", hits)
	}
}

func TestDNSNameserversSurfacesUpstreamError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"message":"missing scope dns:read"}`, http.StatusForbidden)
	}))
	defer server.Close()
	h := NewHandlers(services.NewTailscaleService(&config.Config{
		TailscaleAPIURL:  server.URL,
		TailscaleTailnet: "example.com",
		TailscaleAPIKey:  "test-key",
	}), nil, nil, "test")

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/dns/nameservers", nil)
	h.GetDNSNameservers(c)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var body struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(body.Error, "status 403") || !strings.Contains(body.Error, "missing scope dns:read") {
		t.Fatalf("error = %q, want the upstream status and body", body.Error)
	}
	if body.Error == "Failed to fetch DNS nameservers" {
		t.Fatal("generic error hid the upstream response")
	}
}

func handlerJSON(t *testing.T, h *Handlers, method func(*gin.Context), target string) string {
	t.Helper()
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, target, nil)
	method(c)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	return w.Body.String()
}

func TestExitInternetEndpointIsLabeled(t *testing.T) {
	h := &Handlers{}
	if got := h.resolveNodeName(nil, services.ExitInternetEndpoint); got != exitInternetName {
		t.Fatalf("resolveNodeName(%q) = %q, want %q", services.ExitInternetEndpoint, got, exitInternetName)
	}
	talkers := []database.RankedTalker{{NodeID: services.ExitInternetEndpoint}}
	labelRankedTalkers(talkers)
	if talkers[0].Hostname != exitInternetName {
		t.Fatalf("ranked talker hostname = %q", talkers[0].Hostname)
	}
}
