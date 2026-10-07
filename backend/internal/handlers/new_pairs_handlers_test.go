package handlers

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/rajsinghtech/tsflow/backend/internal/database"
)

func TestNewPairsEndpoint(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := setupHandlerTestDB(t)
	h := &Handlers{store: store}
	start := time.Date(2026, 3, 2, 12, 0, 0, 0, time.UTC)
	end := start.Add(time.Hour)
	window := rankWindow(start, end)
	base := start.Unix()
	if err := store.UpsertNodeMetadata(context.Background(), database.DefaultTailnetID, []database.NodeMetadata{
		{NodeID: "a", Hostname: "alpha"},
		{NodeID: "b", Hostname: "bravo"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertNodePairAggregates(context.Background(), database.DefaultTailnetID, []database.NodePairAggregate{
		{Bucket: base - int64((3 * 24 * time.Hour).Seconds()), SrcNodeID: "a", DstNodeID: "b", TrafficType: "virtual", TxBytes: 10, RxBytes: 1, FlowCount: 1, Protocols: "[6]", ProtocolBytes: `{"6":11}`, Ports: "[]"},
		{Bucket: base + 60, SrcNodeID: "a", DstNodeID: "b", TrafficType: "virtual", TxBytes: 40, RxBytes: 2, FlowCount: 2, Protocols: "[6]", ProtocolBytes: `{"6":42}`, Ports: "[]"},
		{Bucket: base + 120, SrcNodeID: "b", DstNodeID: "a", TrafficType: "virtual", TxBytes: 8, RxBytes: 1, FlowCount: 1, Protocols: "[6]", ProtocolBytes: `{"6":9}`, Ports: "[]"},
		{Bucket: base + 180, SrcNodeID: "a", DstNodeID: "127.3.3.40", TrafficType: "physical", TxBytes: 900, FlowCount: 1, Protocols: "[0]", ProtocolBytes: `{"0":900}`, Ports: "[]"},
	}); err != nil {
		t.Fatal(err)
	}

	code, body := serve(dataRouter(h), http.MethodGet, "/api/analytics/new-pairs"+window)
	if code != http.StatusOK || !strings.Contains(string(body), `"srcNodeId":"b"`) || !strings.Contains(string(body), `"dstHostname":"alpha"`) ||
		!strings.Contains(string(body), `"lookback":"7d"`) || strings.Contains(string(body), `"srcNodeId":"a"`) || strings.Contains(string(body), "127.3.3.40") {
		t.Fatalf("default new pairs: %d %s", code, body)
	}

	code, body = serve(dataRouter(h), http.MethodGet, "/api/analytics/new-pairs"+window+"&lookback=24h&limit=1&offset=1")
	if code != http.StatusOK || !strings.Contains(string(body), `"hasMore":false`) || !strings.Contains(string(body), `"lookback":"1d"`) ||
		!strings.Contains(string(body), `"srcNodeId":"a"`) || !strings.Contains(string(body), `"dstNodeId":"b"`) {
		t.Fatalf("24h page: %d %s", code, body)
	}

	code, body = serve(dataRouter(h), http.MethodGet, "/api/analytics/new-pairs"+window+"&trafficTypes=physical")
	if code != http.StatusOK || !strings.Contains(string(body), `"dstHostname":"DERP relay"`) || strings.Contains(string(body), `"dstNodeId":"b"`) {
		t.Fatalf("physical: %d %s", code, body)
	}

	code, _ = serve(dataRouter(h), http.MethodGet, "/api/analytics/new-pairs"+window+"&lookback=1m")
	if code != http.StatusBadRequest {
		t.Fatalf("short lookback status=%d", code)
	}
	code, _ = serve(dataRouter(h), http.MethodGet, "/api/analytics/new-pairs"+window+"&lookback="+url.QueryEscape("nope"))
	if code != http.StatusBadRequest {
		t.Fatalf("bad lookback status=%d", code)
	}

	missing := &Handlers{}
	wCode, _ := serve(dataRouter(missing), http.MethodGet, "/api/analytics/new-pairs"+window)
	if wCode != http.StatusServiceUnavailable {
		t.Fatalf("missing store status=%d", wCode)
	}
}
