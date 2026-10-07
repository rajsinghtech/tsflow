package handlers

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/rajsinghtech/tsflow/backend/internal/access"
	"github.com/rajsinghtech/tsflow/backend/internal/database"
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
