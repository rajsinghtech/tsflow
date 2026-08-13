package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/rajsinghtech/tsflow/backend/internal/database"
)

func TestSampleLogsNeverExceedsCap(t *testing.T) {
	logs := make([]any, 99999)
	sampled, interval := sampleLogs(logs, 50000)
	if len(sampled) > 50000 {
		t.Fatalf("sampled %d logs, cap is 50000", len(sampled))
	}
	if interval != 2 || len(sampled) != 50000 {
		t.Fatalf("interval=%d sampled=%d, want interval=2 sampled=50000", interval, len(sampled))
	}
}

func TestDeprecatedFlowEndpointsReturnGone(t *testing.T) {
	h := &Handlers{}
	for name, handler := range map[string]gin.HandlerFunc{
		"stored": h.GetStoredFlowLogs,
		"device": h.GetDeviceFlows,
	} {
		t.Run(name, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest(http.MethodGet, "/api/flow-logs", nil)
			handler(c)
			if w.Code != http.StatusGone {
				t.Fatalf("status=%d, want 410", w.Code)
			}
		})
	}
}

func TestAggregatedFlowMergesPortsAcrossBuckets(t *testing.T) {
	store := setupHandlerTestDB(t)
	base := time.Now().UTC().Truncate(time.Minute).Add(-2 * time.Minute)
	if err := store.UpsertNodePairAggregates(context.Background(), []database.NodePairAggregate{
		{Bucket: base.Unix(), SrcNodeID: "a", DstNodeID: "b", TrafficType: "virtual", TxBytes: 100, Protocols: "[6]", ProtocolBytes: `{"6":100}`, Ports: `[{"port":443,"proto":6,"bytes":100}]`},
		{Bucket: base.Add(time.Minute).Unix(), SrcNodeID: "a", DstNodeID: "b", TrafficType: "virtual", TxBytes: 300, Protocols: "[17]", ProtocolBytes: `{"17":300}`, Ports: `[{"port":53,"proto":17,"bytes":300}]`},
	}); err != nil {
		t.Fatal(err)
	}

	h := &Handlers{store: store}
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/flow-logs/aggregated?start="+base.Format(time.RFC3339)+"&end="+base.Add(2*time.Minute).Format(time.RFC3339), nil)
	h.GetAggregatedFlowLogs(c)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var response struct {
		Flows []struct {
			Protocol    int                 `json:"protocol"`
			Ports       []database.PortStat `json:"ports"`
			Directional bool                `json:"directional"`
		} `json:"flows"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Flows) != 1 || response.Flows[0].Protocol != 17 {
		t.Fatalf("unexpected flow response: %+v", response.Flows)
	}
	if len(response.Flows[0].Ports) != 2 || response.Flows[0].Ports[0].Port != 53 || response.Flows[0].Ports[1].Port != 443 {
		t.Fatalf("ports were not merged: %+v", response.Flows[0].Ports)
	}
	if response.Flows[0].Directional {
		t.Fatalf("legacy aggregate unexpectedly marked directional: %+v", response.Flows[0])
	}
}

func TestAggregatedFlowExposesDirectionalMetadata(t *testing.T) {
	store := setupHandlerTestDB(t)
	base := time.Now().UTC().Truncate(time.Minute).Add(-2 * time.Minute)
	if err := store.UpsertNodePairAggregates(context.Background(), []database.NodePairAggregate{
		{
			Bucket: base.Unix(), SrcNodeID: "a", DstNodeID: "b", TrafficType: "virtual",
			TxBytes: 100, RxBytes: 200, Protocols: "[17,6]", ProtocolBytes: `{"6":100,"17":200}`,
			Ports:           `[ {"port":53,"proto":17,"bytes":200}, {"port":443,"proto":6,"bytes":100} ]`,
			TxProtocolBytes: `{"6":100}`, RxProtocolBytes: `{"17":200}`,
			TxPorts: `[ {"port":443,"proto":6,"bytes":100} ]`, RxPorts: `[ {"port":53,"proto":17,"bytes":200} ]`,
			DirectionalPorts: true,
		},
		{
			Bucket: base.Add(time.Minute).Unix(), SrcNodeID: "a", DstNodeID: "b", TrafficType: "virtual",
			TxBytes: 300, RxBytes: 400, Protocols: "[17,6]", ProtocolBytes: `{"6":300,"17":400}`,
			Ports:           `[ {"port":53,"proto":17,"bytes":400}, {"port":443,"proto":6,"bytes":300} ]`,
			TxProtocolBytes: `{"6":300}`, RxProtocolBytes: `{"17":400}`,
			TxPorts: `[ {"port":443,"proto":6,"bytes":300} ]`, RxPorts: `[ {"port":53,"proto":17,"bytes":400} ]`,
			DirectionalPorts: true,
		},
	}); err != nil {
		t.Fatal(err)
	}

	h := &Handlers{store: store}
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/flow-logs/aggregated?start="+base.Format(time.RFC3339)+"&end="+base.Add(2*time.Minute).Format(time.RFC3339), nil)
	h.GetAggregatedFlowLogs(c)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}

	var response struct {
		Flows []struct {
			TotalTxBytes    int64               `json:"totalTxBytes"`
			TotalRxBytes    int64               `json:"totalRxBytes"`
			Protocol        int                 `json:"protocol"`
			Directional     bool                `json:"directional"`
			TxProtocolBytes map[int]int64       `json:"txProtocolBytes"`
			RxProtocolBytes map[int]int64       `json:"rxProtocolBytes"`
			TxPorts         []database.PortStat `json:"txPorts"`
			RxPorts         []database.PortStat `json:"rxPorts"`
		} `json:"flows"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Flows) != 1 {
		t.Fatalf("unexpected flow response: %+v", response)
	}
	flow := response.Flows[0]
	if flow.TotalTxBytes != 400 || flow.TotalRxBytes != 600 || flow.Protocol != 17 {
		t.Fatalf("merged legacy fields = %+v", flow)
	}
	if !flow.Directional {
		t.Fatalf("directional metadata was not exposed: %+v", flow)
	}
	if flow.TxProtocolBytes[6] != 400 || len(flow.TxProtocolBytes) != 1 {
		t.Fatalf("tx protocol bytes = %+v", flow.TxProtocolBytes)
	}
	if flow.RxProtocolBytes[17] != 600 || len(flow.RxProtocolBytes) != 1 {
		t.Fatalf("rx protocol bytes = %+v", flow.RxProtocolBytes)
	}
	if len(flow.TxPorts) != 1 || flow.TxPorts[0].Port != 443 || flow.TxPorts[0].Bytes != 400 {
		t.Fatalf("tx ports = %+v", flow.TxPorts)
	}
	if len(flow.RxPorts) != 1 || flow.RxPorts[0].Port != 53 || flow.RxPorts[0].Bytes != 600 {
		t.Fatalf("rx ports = %+v", flow.RxPorts)
	}
}

func TestAggregatedFlowClearsDirectionalMetadataWhenLegacyBucketIsMerged(t *testing.T) {
	store := setupHandlerTestDB(t)
	base := time.Now().UTC().Truncate(time.Minute).Add(-2 * time.Minute)
	if err := store.UpsertNodePairAggregates(context.Background(), []database.NodePairAggregate{
		{
			Bucket: base.Unix(), SrcNodeID: "a", DstNodeID: "b", TrafficType: "virtual",
			TxBytes: 100, Protocols: "[6]", ProtocolBytes: `{"6":100}`,
			Ports:           `[ {"port":443,"proto":6,"bytes":100} ]`,
			TxProtocolBytes: `{"6":100}`, TxPorts: `[ {"port":443,"proto":6,"bytes":100} ]`,
			DirectionalPorts: true,
		},
		{
			Bucket: base.Add(time.Minute).Unix(), SrcNodeID: "a", DstNodeID: "b", TrafficType: "virtual",
			TxBytes: 200, Protocols: "[17]", ProtocolBytes: `{"17":200}`,
			Ports: `[ {"port":53,"proto":17,"bytes":200} ]`,
		},
	}); err != nil {
		t.Fatal(err)
	}

	h := &Handlers{store: store}
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/flow-logs/aggregated?start="+base.Format(time.RFC3339)+"&end="+base.Add(2*time.Minute).Format(time.RFC3339), nil)
	h.GetAggregatedFlowLogs(c)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}

	var response struct {
		Flows []struct {
			Directional     bool                `json:"directional"`
			TxProtocolBytes map[int]int64       `json:"txProtocolBytes"`
			RxProtocolBytes map[int]int64       `json:"rxProtocolBytes"`
			TxPorts         []database.PortStat `json:"txPorts"`
			RxPorts         []database.PortStat `json:"rxPorts"`
		} `json:"flows"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Flows) != 1 {
		t.Fatalf("unexpected flow response: %+v", response)
	}
	flow := response.Flows[0]
	if flow.Directional {
		t.Fatalf("legacy contribution did not clear directional metadata: %+v", flow)
	}
	if flow.TxProtocolBytes != nil || flow.RxProtocolBytes != nil || flow.TxPorts != nil || flow.RxPorts != nil {
		t.Fatalf("directional fields survived legacy merge: %+v", flow)
	}
}

func TestBandwidthByIPsRejectsEmptyEntries(t *testing.T) {
	store := setupHandlerTestDB(t)
	h := &Handlers{store: store}
	start := time.Now().UTC().Truncate(time.Minute).Add(-time.Minute)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet,
		"/api/bandwidth?start="+start.Format(time.RFC3339)+"&end="+start.Add(time.Minute).Format(time.RFC3339)+"&ips=100.64.0.1,,100.64.0.2", nil)
	h.GetBandwidthByIPs(c)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s, want 400", w.Code, w.Body.String())
	}
}

func TestAggregatedFlowsRejectInvalidTrafficType(t *testing.T) {
	store := setupHandlerTestDB(t)
	h := &Handlers{store: store}
	start := time.Now().UTC().Truncate(time.Minute).Add(-time.Minute)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet,
		"/api/flow-logs/aggregated?start="+start.Format(time.RFC3339)+"&end="+start.Add(time.Minute).Format(time.RFC3339)+"&trafficTypes=bogus", nil)
	h.GetAggregatedFlowLogs(c)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s, want 400", w.Code, w.Body.String())
	}
}

func TestStatsOverviewMergesMissingDerivedBucketsAndKeepsPrimaryOverlap(t *testing.T) {
	store := setupHandlerTestDB(t)
	base := time.Now().UTC().Truncate(time.Minute).Add(-3 * time.Minute)
	ctx := context.Background()
	if err := store.UpsertTrafficStats(ctx, []database.TrafficStats{
		{Bucket: base.Unix(), TCPBytes: 100, VirtualBytes: 100, TotalFlows: 1, UniquePairs: 1},
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertNodePairAggregates(ctx, []database.NodePairAggregate{
		// This overlaps the authoritative traffic_stats bucket and must not be added to it.
		{Bucket: base.Unix(), SrcNodeID: "a", DstNodeID: "b", TrafficType: "virtual", TxBytes: 900, FlowCount: 9, Protocols: "[17]", ProtocolBytes: `{"17":900}`},
		// This bucket exists only in node_pairs and must be included.
		{Bucket: base.Add(time.Minute).Unix(), SrcNodeID: "c", DstNodeID: "d", TrafficType: "virtual", TxBytes: 200, FlowCount: 2, Protocols: "[17]", ProtocolBytes: `{"17":200}`},
	}); err != nil {
		t.Fatal(err)
	}

	response := getStatsOverviewResponse(t, store, base, base.Add(2*time.Minute))
	if len(response.Buckets) != 2 {
		t.Fatalf("bucket count = %d, want 2: %+v", len(response.Buckets), response.Buckets)
	}
	if response.Buckets[0].Bucket != base.Unix() || response.Buckets[1].Bucket != base.Add(time.Minute).Unix() {
		t.Fatalf("bucket order = %+v", response.Buckets)
	}
	if response.Buckets[0].TCPBytes != 100 || response.Buckets[0].UDPBytes != 0 || response.Buckets[0].TotalFlows != 1 {
		t.Fatalf("overlapping primary bucket was changed: %+v", response.Buckets[0])
	}
	if response.Buckets[1].UDPBytes != 200 || response.Buckets[1].VirtualBytes != 200 || response.Buckets[1].TotalFlows != 2 {
		t.Fatalf("derived bucket = %+v", response.Buckets[1])
	}
	if response.Summary.TCPBytes != 100 || response.Summary.UDPBytes != 200 || response.Summary.VirtualBytes != 300 || response.Summary.TotalFlows != 3 {
		t.Fatalf("summary = %+v", response.Summary)
	}
	if response.Metadata.Source != "database" {
		t.Fatalf("source = %q, want database", response.Metadata.Source)
	}
}

func TestStatsOverviewFallsBackToDerivedBucketsWhenTrafficStatsIsEmpty(t *testing.T) {
	store := setupHandlerTestDB(t)
	base := time.Now().UTC().Truncate(time.Minute).Add(-3 * time.Minute)
	if err := store.UpsertNodePairAggregates(context.Background(), []database.NodePairAggregate{
		{Bucket: base.Unix(), SrcNodeID: "a", DstNodeID: "b", TrafficType: "exit", TxBytes: 75, FlowCount: 1, Protocols: "[6]", ProtocolBytes: `{"6":75}`},
	}); err != nil {
		t.Fatal(err)
	}

	response := getStatsOverviewResponse(t, store, base, base.Add(time.Minute))
	if len(response.Buckets) != 1 || response.Buckets[0].Bucket != base.Unix() {
		t.Fatalf("fallback buckets = %+v", response.Buckets)
	}
	if response.Buckets[0].TCPBytes != 75 || response.Buckets[0].ExitBytes != 75 || response.Buckets[0].TotalFlows != 1 {
		t.Fatalf("fallback bucket = %+v", response.Buckets[0])
	}
	if response.Summary.TCPBytes != 75 || response.Summary.ExitBytes != 75 || response.Summary.TotalFlows != 1 {
		t.Fatalf("fallback summary = %+v", response.Summary)
	}
	if response.Metadata.Source != "database (derived)" {
		t.Fatalf("source = %q, want database (derived)", response.Metadata.Source)
	}
}

type statsOverviewResponse struct {
	Summary struct {
		TCPBytes        int64 `json:"tcpBytes"`
		UDPBytes        int64 `json:"udpBytes"`
		OtherProtoBytes int64 `json:"otherProtoBytes"`
		VirtualBytes    int64 `json:"virtualBytes"`
		ExitBytes       int64 `json:"exitBytes"`
		SubnetBytes     int64 `json:"subnetBytes"`
		PhysicalBytes   int64 `json:"physicalBytes"`
		TotalFlows      int64 `json:"totalFlows"`
		UniquePairs     int64 `json:"uniquePairs"`
	} `json:"summary"`
	Buckets []struct {
		Bucket       int64 `json:"bucket"`
		TCPBytes     int64 `json:"tcpBytes"`
		UDPBytes     int64 `json:"udpBytes"`
		VirtualBytes int64 `json:"virtualBytes"`
		ExitBytes    int64 `json:"exitBytes"`
		TotalFlows   int64 `json:"totalFlows"`
	} `json:"buckets"`
	Metadata struct {
		Source string `json:"source"`
	} `json:"metadata"`
}

func getStatsOverviewResponse(t *testing.T, store *database.SQLiteStore, start, end time.Time) statsOverviewResponse {
	t.Helper()
	h := &Handlers{store: store}
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/stats/overview?start="+start.Format(time.RFC3339)+"&end="+end.Format(time.RFC3339), nil)
	h.GetStatsOverview(c)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var response statsOverviewResponse
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	return response
}

func setupHandlerTestDB(t *testing.T) *database.SQLiteStore {
	t.Helper()
	store, err := database.NewSQLiteStore(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Init(context.Background()); err != nil {
		store.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}
