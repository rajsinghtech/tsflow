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
			Protocol int                 `json:"protocol"`
			Ports    []database.PortStat `json:"ports"`
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
