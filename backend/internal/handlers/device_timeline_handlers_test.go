package handlers

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/rajsinghtech/tsflow/backend/internal/database"
)

func TestDeviceTimelineEndpoint(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := setupHandlerTestDB(t)
	h := &Handlers{store: store}
	start := time.Date(2026, 3, 2, 12, 0, 0, 0, time.UTC)
	end := start.Add(3 * time.Hour)
	window := rankWindow(start, end)
	base := start.Unix()
	if err := store.UpsertNodeMetadata(context.Background(), database.DefaultTailnetID, []database.NodeMetadata{
		{NodeID: "bravo", Hostname: "bravo"},
		{NodeID: "alpha", Hostname: "alpha"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertNodePairAggregates(context.Background(), database.DefaultTailnetID, []database.NodePairAggregate{
		{Bucket: base + 60, SrcNodeID: "bravo", DstNodeID: "alpha", TrafficType: "virtual", TxBytes: 100, RxBytes: 10, FlowCount: 2, Protocols: "[6]", ProtocolBytes: `{"6":110}`, Ports: "[]"},
		{Bucket: base + 120, SrcNodeID: "bravo", DstNodeID: "127.3.3.40", TrafficType: "physical", TxBytes: 5000, FlowCount: 1, Protocols: "[0]", ProtocolBytes: `{"0":5000}`, Ports: "[]"},
	}); err != nil {
		t.Fatal(err)
	}

	code, body := serve(dataRouter(h), http.MethodGet, "/api/analytics/device-timeline"+window+"&node=bravo&limit=10")
	if code != http.StatusOK || !strings.Contains(string(body), `"hostname":"bravo"`) || !strings.Contains(string(body), `"peerId":"alpha"`) ||
		strings.Contains(string(body), "127.3.3.40") || !strings.Contains(string(body), `"seconds":`) {
		t.Fatalf("timeline: %d %s", code, body)
	}
	code, body = serve(dataRouter(h), http.MethodGet, "/api/analytics/device-timeline"+window+"&node=bravo&trafficTypes=physical")
	if code != http.StatusOK || !strings.Contains(string(body), `"hostname":"DERP relay"`) || strings.Contains(string(body), `"peerId":"alpha"`) {
		t.Fatalf("physical timeline: %d %s", code, body)
	}
	code, _ = serve(dataRouter(h), http.MethodGet, "/api/analytics/device-timeline"+window)
	if code != http.StatusBadRequest {
		t.Fatalf("missing node status=%d", code)
	}
	missing := &Handlers{}
	code, _ = serve(dataRouter(missing), http.MethodGet, "/api/analytics/device-timeline"+window+"&node=bravo")
	if code != http.StatusServiceUnavailable {
		t.Fatalf("missing store status=%d", code)
	}
}
