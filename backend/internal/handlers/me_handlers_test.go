package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/rajsinghtech/tsflow/backend/internal/access"
	"github.com/rajsinghtech/tsflow/backend/internal/database"
	"github.com/rajsinghtech/tsflow/backend/internal/services"
)

func TestViewerScopeRequiresIdentityAndIgnoresClientLogin(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := setupHandlerTestDB(t)
	h := &Handlers{store: store}
	start := time.Date(2026, 3, 2, 12, 0, 0, 0, time.UTC)
	end := start.Add(time.Hour)
	window := rankWindow(start, end)
	base := start.Unix()
	if err := store.UpsertNodeMetadata(context.Background(), database.DefaultTailnetID, []database.NodeMetadata{
		{NodeID: "nBuild001CNTRL", Hostname: "build", Tags: []string{"tag:prod"}, IPs: []string{"100.64.0.21"}},
		{NodeID: "424242", Owner: "ada@example.com", Tags: []string{"tag:prod"}, IPs: []string{"100.64.0.21"}},
		{NodeID: "nBob00001CNTRL", Hostname: "laptop", Owner: "bob@example.com", Tags: []string{"tag:eng"}, IPs: []string{"100.64.0.8"}},
		{NodeID: "nExtra001CNTRL", Hostname: "extra", Owner: "ada@example.com.extra", IPs: []string{"100.64.0.22"}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertNodePairAggregates(context.Background(), database.DefaultTailnetID, []database.NodePairAggregate{
		{Bucket: base + 60, SrcNodeID: "nBuild001CNTRL", DstNodeID: "peer", TrafficType: "virtual", TxBytes: 30, RxBytes: 4, FlowCount: 1, Protocols: "[6]", ProtocolBytes: `{"6":34}`, Ports: "[]"},
		{Bucket: base + 120, SrcNodeID: "424242", DstNodeID: "peer", TrafficType: "virtual", TxBytes: 10, RxBytes: 1, FlowCount: 1, Protocols: "[6]", ProtocolBytes: `{"6":11}`, Ports: "[]"},
		{Bucket: base + 180, SrcNodeID: "nBob00001CNTRL", DstNodeID: "peer", TrafficType: "virtual", TxBytes: 80, RxBytes: 1, FlowCount: 1, Protocols: "[6]", ProtocolBytes: `{"6":81}`, Ports: "[]"},
		{Bucket: base + 240, SrcNodeID: "nExtra001CNTRL", DstNodeID: "peer", TrafficType: "virtual", TxBytes: 70, RxBytes: 1, FlowCount: 1, Protocols: "[6]", ProtocolBytes: `{"6":71}`, Ports: "[]"},
	}); err != nil {
		t.Fatal(err)
	}

	code, body := serve(dataRouter(h), http.MethodGet, "/api/analytics/me"+window)
	if code != http.StatusUnauthorized {
		t.Fatalf("anonymous me: %d %s", code, body)
	}
	code, body = serve(dataRouter(h), http.MethodGet, "/api/analytics/talkers"+window+"&me=1&user="+url.QueryEscape("bob@example.com"))
	if code != http.StatusUnauthorized {
		t.Fatalf("anonymous me filter: %d %s", code, body)
	}

	ada := dataRouter(h, asViewer("Ada@Example.com"))
	code, body = serve(ada, http.MethodGet, "/api/analytics/me"+window+"&user="+url.QueryEscape("bob@example.com"))
	if code != http.StatusOK || !strings.Contains(string(body), `"login":"Ada@Example.com"`) ||
		!strings.Contains(string(body), `"nodeId":"nBuild001CNTRL"`) || !strings.Contains(string(body), `"totalBytes":45`) ||
		strings.Contains(string(body), "nBob00001CNTRL") || strings.Contains(string(body), "nExtra001CNTRL") || strings.Contains(string(body), `"totalBytes":80`) {
		t.Fatalf("viewer summary: %d %s", code, body)
	}

	code, body = serve(ada, http.MethodGet, "/api/analytics/talkers"+window+"&me=1&user="+url.QueryEscape("bob@example.com"))
	if code != http.StatusOK || !strings.Contains(string(body), `"nodeId":"nBuild001CNTRL"`) || strings.Contains(string(body), "nBob00001CNTRL") {
		t.Fatalf("me talkers: %d %s", code, body)
	}
	code, body = serve(ada, http.MethodGet, "/api/analytics/new-pairs"+window+"&me=1&lookback=24h")
	if code != http.StatusOK || !strings.Contains(string(body), `"srcNodeId":"nBuild001CNTRL"`) || strings.Contains(string(body), "nBob00001CNTRL") {
		t.Fatalf("me new pairs: %d %s", code, body)
	}
	code, body = serve(ada, http.MethodGet, "/api/analytics/device-timeline"+window+"&me=1&node=nBob00001CNTRL")
	if code != http.StatusNotFound {
		t.Fatalf("other device timeline: %d %s", code, body)
	}
	code, body = serve(ada, http.MethodGet, "/api/analytics/device-timeline"+window+"&me=1&node=nBuild001CNTRL")
	if code != http.StatusOK || !strings.Contains(string(body), `"hostname":"build"`) {
		t.Fatalf("own timeline: %d %s", code, body)
	}
}

func asViewer(login string) gin.HandlerFunc {
	return func(c *gin.Context) {
		access.SetIdentity(c, access.Identity{Login: login, Allow: access.Allow{All: true}})
		c.Next()
	}
}

func TestViewerScopeNarrowsBySearch(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := setupHandlerTestDB(t)
	h := &Handlers{store: store}
	start := time.Date(2026, 3, 2, 12, 0, 0, 0, time.UTC)
	end := start.Add(time.Hour)
	window := rankWindow(start, end)
	base := start.Unix()
	ctx := context.Background()
	if err := store.UpsertNodeMetadata(ctx, database.DefaultTailnetID, []database.NodeMetadata{
		{NodeID: "nBuild001CNTRL", Hostname: "build", Owner: "ada@example.com", Tags: []string{"tag:prod"}, IPs: []string{"100.64.0.21"}},
		{NodeID: "nAdaLap01CNTRL", Hostname: "ada-laptop", Owner: "ada@example.com", IPs: []string{"100.64.0.30"}},
		{NodeID: "nBob00001CNTRL", Hostname: "bob-prod", Owner: "bob@example.com", Tags: []string{"tag:prod"}, IPs: []string{"100.64.0.8"}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertNodePairAggregates(ctx, database.DefaultTailnetID, []database.NodePairAggregate{
		{Bucket: base + 60, SrcNodeID: "nBuild001CNTRL", DstNodeID: "peer", TrafficType: "virtual", TxBytes: 30, RxBytes: 4, FlowCount: 1, Protocols: "[6]", ProtocolBytes: `{"6":34}`, Ports: "[]"},
		{Bucket: base + 120, SrcNodeID: "nAdaLap01CNTRL", DstNodeID: "peer", TrafficType: "virtual", TxBytes: 50, RxBytes: 5, FlowCount: 1, Protocols: "[6]", ProtocolBytes: `{"6":55}`, Ports: "[]"},
		{Bucket: base + 180, SrcNodeID: "nBob00001CNTRL", DstNodeID: "peer", TrafficType: "virtual", TxBytes: 80, RxBytes: 1, FlowCount: 1, Protocols: "[6]", ProtocolBytes: `{"6":81}`, Ports: "[]"},
	}); err != nil {
		t.Fatal(err)
	}
	poller := services.NewPoller(nil, store, services.DefaultPollerConfig())
	metadata, err := store.GetNodeMetadata(ctx, database.DefaultTailnetID)
	if err != nil {
		t.Fatal(err)
	}
	poller.GetDeviceCache().UpsertNodeMetadata(metadata)
	h.poller = poller
	ada := dataRouter(h, asViewer("ada@example.com"))

	code, body := serve(ada, http.MethodGet, "/api/analytics/talkers"+window+"&me=1")
	if code != http.StatusOK || !strings.Contains(string(body), "nBuild001CNTRL") || !strings.Contains(string(body), "nAdaLap01CNTRL") || strings.Contains(string(body), "nBob00001CNTRL") {
		t.Fatalf("me talkers: %d %s", code, body)
	}
	// q narrows the viewer's own devices. Another owner's match stays out.
	code, body = serve(ada, http.MethodGet, "/api/analytics/talkers"+window+"&me=1&q="+url.QueryEscape("tag:prod"))
	if code != http.StatusOK || !strings.Contains(string(body), "nBuild001CNTRL") || strings.Contains(string(body), "nAdaLap01CNTRL") ||
		strings.Contains(string(body), "nBob00001CNTRL") || !strings.Contains(string(body), `"q":"tag:prod"`) {
		t.Fatalf("me talkers with q: %d %s", code, body)
	}
	code, body = serve(ada, http.MethodGet, "/api/analytics/pairs"+window+"&me=1&q="+url.QueryEscape("ada-laptop"))
	if code != http.StatusOK || !strings.Contains(string(body), "nAdaLap01CNTRL") || strings.Contains(string(body), "nBuild001CNTRL") || strings.Contains(string(body), "nBob00001CNTRL") {
		t.Fatalf("me pairs with q: %d %s", code, body)
	}
	code, body = serve(ada, http.MethodGet, "/api/analytics/talkers"+window+"&me=1&q="+url.QueryEscape("bob-prod"))
	if code != http.StatusOK || strings.Contains(string(body), "nBob00001CNTRL") || strings.Contains(string(body), "nBuild001CNTRL") {
		t.Fatalf("me talkers with another owner's device: %d %s", code, body)
	}
}

func TestViewerSummaryOrdersDevicesByTraffic(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := setupHandlerTestDB(t)
	h := &Handlers{store: store}
	start := time.Date(2026, 3, 2, 12, 0, 0, 0, time.UTC)
	end := start.Add(time.Hour)
	window := rankWindow(start, end)
	base := start.Unix()
	ctx := context.Background()
	// Thirteen quiet devices sort ahead of the busy one by id. The summary
	// builds timelines and peers for twelve devices, so it must pick by bytes.
	var metadata []database.NodeMetadata
	var rows []database.NodePairAggregate
	for i := 0; i < 13; i++ {
		id := fmt.Sprintf("nAda%05dCNTRL", i)
		metadata = append(metadata, database.NodeMetadata{NodeID: id, Hostname: fmt.Sprintf("quiet-%02d", i), Owner: "ada@example.com"})
		rows = append(rows, database.NodePairAggregate{Bucket: base + 60, SrcNodeID: id, DstNodeID: fmt.Sprintf("peer-%02d", i), TrafficType: "virtual", TxBytes: int64(10 + i), FlowCount: 1, Protocols: "[6]", ProtocolBytes: `{"6":10}`, Ports: "[]"})
	}
	metadata = append(metadata, database.NodeMetadata{NodeID: "nAdaZZZZZCNTRL", Hostname: "busy", Owner: "ada@example.com"})
	rows = append(rows, database.NodePairAggregate{Bucket: base + 120, SrcNodeID: "nAdaZZZZZCNTRL", DstNodeID: "big-peer", TrafficType: "virtual", TxBytes: 5000, RxBytes: 100, FlowCount: 3, Protocols: "[6]", ProtocolBytes: `{"6":5100}`, Ports: "[]"})
	if err := store.UpsertNodeMetadata(ctx, database.DefaultTailnetID, metadata); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertNodePairAggregates(ctx, database.DefaultTailnetID, rows); err != nil {
		t.Fatal(err)
	}
	code, body := serve(dataRouter(h, asViewer("ada@example.com")), http.MethodGet, "/api/analytics/me"+window)
	if code != http.StatusOK {
		t.Fatalf("viewer summary: %d %s", code, body)
	}
	var summary struct {
		Devices []struct {
			NodeID     string `json:"nodeId"`
			TotalBytes int64  `json:"totalBytes"`
		} `json:"devices"`
		Peers []struct {
			PeerID string `json:"peerId"`
		} `json:"peers"`
	}
	if err := json.Unmarshal(body, &summary); err != nil {
		t.Fatal(err)
	}
	if len(summary.Devices) != 14 || summary.Devices[0].NodeID != "nAdaZZZZZCNTRL" || summary.Devices[0].TotalBytes != 5100 || summary.Devices[1].NodeID != "nAda00012CNTRL" {
		t.Fatalf("device order: %s", body)
	}
	if len(summary.Peers) == 0 || summary.Peers[0].PeerID != "big-peer" {
		t.Fatalf("top peers miss the busiest device: %s", body)
	}
	// Stored data starts inside the default 7d lookback, as on /new.
	if !strings.Contains(string(body), `"lookbackComplete":false`) || !strings.Contains(string(body), `"dataStart":"2026-03-02T12:01:00Z"`) {
		t.Fatalf("new-pair coverage: %s", body)
	}
}

func TestViewerSummaryLeavesOutOwnDevicesAndSelfPairs(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := setupHandlerTestDB(t)
	h := &Handlers{store: store}
	start := time.Date(2026, 3, 2, 12, 0, 0, 0, time.UTC)
	end := start.Add(time.Hour)
	base := start.Unix()
	ctx := context.Background()
	if err := store.UpsertNodeMetadata(ctx, database.DefaultTailnetID, []database.NodeMetadata{
		{NodeID: "nAdaLaptopCNTRL", Hostname: "ada-laptop", Owner: "ada@example.com"},
		{NodeID: "nAdaServerCNTRL", Hostname: "ada-server", Owner: "ada@example.com"},
		{NodeID: "nShared001CNTRL", Hostname: "shared-db", Owner: "bob@example.com"},
	}); err != nil {
		t.Fatal(err)
	}
	row := func(at int64, src, dst string, tx int64) database.NodePairAggregate {
		return database.NodePairAggregate{Bucket: base + at, SrcNodeID: src, DstNodeID: dst, TrafficType: "virtual", TxBytes: tx, FlowCount: 1, Protocols: "[6]", ProtocolBytes: `{"6":1}`, Ports: "[]"}
	}
	if err := store.UpsertNodePairAggregates(ctx, database.DefaultTailnetID, []database.NodePairAggregate{
		row(60, "nAdaLaptopCNTRL", "nAdaLaptopCNTRL", 700),  // talks to itself
		row(120, "nAdaLaptopCNTRL", "nAdaServerCNTRL", 900), // between the viewer's own devices
		row(180, "nAdaLaptopCNTRL", "nShared001CNTRL", 300),
		row(240, "nAdaServerCNTRL", "quiet-peer", 0),
	}); err != nil {
		t.Fatal(err)
	}
	code, body := serve(dataRouter(h, asViewer("ada@example.com")), http.MethodGet, "/api/analytics/me"+rankWindow(start, end))
	if code != http.StatusOK {
		t.Fatalf("viewer summary: %d %s", code, body)
	}
	var summary struct {
		Peers []struct {
			PeerID string `json:"peerId"`
		} `json:"peers"`
		NewPairs []struct {
			Src string `json:"srcNodeId"`
			Dst string `json:"dstNodeId"`
		} `json:"newPairs"`
	}
	if err := json.Unmarshal(body, &summary); err != nil {
		t.Fatal(err)
	}
	// Own devices and 0 B peers stay out of top peers.
	if len(summary.Peers) != 1 || summary.Peers[0].PeerID != "nShared001CNTRL" {
		t.Fatalf("top peers: %s", body)
	}
	// A connection between two own devices is still new. A device talking to itself is not.
	for _, pair := range summary.NewPairs {
		if pair.Src == pair.Dst {
			t.Fatalf("self pair in new connections: %s", body)
		}
	}
	if len(summary.NewPairs) != 3 {
		t.Fatalf("new connections: %s", body)
	}
}
