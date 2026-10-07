package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/rajsinghtech/tsflow/backend/internal/config"
	"github.com/rajsinghtech/tsflow/backend/internal/database"
	"github.com/rajsinghtech/tsflow/backend/internal/services"
)

func TestRankedAnalyticsSingleTailnetAndEmpty(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := setupHandlerTestDB(t)
	h := &Handlers{store: store}
	start := time.Now().UTC().Add(-30 * time.Minute).Truncate(time.Minute)
	end := start.Add(20 * time.Minute)
	window := rankWindow(start, end)

	code, body := serve(dataRouter(h), http.MethodGet, "/api/analytics/talkers"+window)
	if code != http.StatusOK || !strings.Contains(string(body), `"talkers":[]`) {
		t.Fatalf("empty talkers: %d %s", code, body)
	}
	var emptyTalkers struct {
		Talkers  []database.RankedTalker `json:"talkers"`
		Metadata struct {
			Tailnet string `json:"tailnet"`
			Limit   int    `json:"limit"`
			Offset  int    `json:"offset"`
			Count   int    `json:"count"`
			HasMore bool   `json:"hasMore"`
			Sort    string `json:"sort"`
		} `json:"metadata"`
	}
	if err := json.Unmarshal(body, &emptyTalkers); err != nil {
		t.Fatal(err)
	}
	if emptyTalkers.Metadata.Tailnet != database.DefaultTailnetID || emptyTalkers.Metadata.Limit != database.RankDefaultLimit ||
		emptyTalkers.Metadata.Offset != 0 || emptyTalkers.Metadata.Count != 0 || emptyTalkers.Metadata.HasMore ||
		emptyTalkers.Metadata.Sort != database.RankSortBytes || len(emptyTalkers.Talkers) != 0 {
		t.Fatalf("empty talker metadata = %#v", emptyTalkers.Metadata)
	}

	code, body = serve(dataRouter(h), http.MethodGet, "/api/analytics/pairs"+window)
	if code != http.StatusOK || !strings.Contains(string(body), `"pairs":[]`) {
		t.Fatalf("empty pairs: %d %s", code, body)
	}

	base := start.Unix()
	if err := store.UpsertNodePairAggregates(context.Background(), database.DefaultTailnetID, []database.NodePairAggregate{
		{Bucket: base, SrcNodeID: "a", DstNodeID: "b", TrafficType: "virtual", TxBytes: 30, RxBytes: 5, FlowCount: 1, Protocols: "[6]", ProtocolBytes: `{"6":35}`, Ports: "[]"},
		{Bucket: base, SrcNodeID: "c", DstNodeID: "a", TrafficType: "virtual", TxBytes: 4, RxBytes: 1, FlowCount: 8, Protocols: "[6]", ProtocolBytes: `{"6":5}`, Ports: "[]"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertNodePairAggregates(context.Background(), "other", []database.NodePairAggregate{
		{Bucket: base, SrcNodeID: "z", DstNodeID: "y", TrafficType: "virtual", TxBytes: 800, RxBytes: 1, FlowCount: 1, Protocols: "[6]", ProtocolBytes: `{"6":801}`, Ports: "[]"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertNodeMetadata(context.Background(), database.DefaultTailnetID, []database.NodeMetadata{
		{NodeID: "a", Hostname: "laptop"},
		{NodeID: "b", Name: "bob-device"},
	}); err != nil {
		t.Fatal(err)
	}

	plainCode, plainBody := serve(dataRouter(h), http.MethodGet, "/api/analytics/talkers"+window)
	namedCode, namedBody := serve(dataRouter(h), http.MethodGet, "/api/analytics/talkers"+window+"&tailnet=default")
	if plainCode != http.StatusOK || namedCode != http.StatusOK || string(plainBody) != string(namedBody) {
		t.Fatalf("single-tailnet default\nplain: %d %s\nnamed: %d %s", plainCode, plainBody, namedCode, namedBody)
	}
	var ranked struct {
		Talkers []database.RankedTalker `json:"talkers"`
	}
	if err := json.Unmarshal(plainBody, &ranked); err != nil {
		t.Fatal(err)
	}
	if len(ranked.Talkers) != 3 || ranked.Talkers[0].NodeID != "a" || ranked.Talkers[0].Hostname != "laptop" ||
		ranked.Talkers[0].TotalBytes != 40 || ranked.Talkers[0].FlowCount != 9 ||
		ranked.Talkers[1].NodeID != "b" || ranked.Talkers[1].Hostname != "bob-device" || ranked.Talkers[1].TotalBytes != 35 {
		t.Fatalf("talkers = %#v", ranked.Talkers)
	}
	for _, talker := range ranked.Talkers {
		if talker.NodeID == "z" || talker.TotalBytes == 801 {
			t.Fatalf("default response included another tailnet: %#v", ranked.Talkers)
		}
	}

	pageCode, pageBody := serve(dataRouter(h), http.MethodGet, "/api/analytics/pairs"+window+"&limit=1&offset=1")
	if pageCode != http.StatusOK {
		t.Fatalf("pair page: %d %s", pageCode, pageBody)
	}
	var pairPage struct {
		Pairs    []database.RankedPair `json:"pairs"`
		Metadata struct {
			Count   int  `json:"count"`
			HasMore bool `json:"hasMore"`
			Offset  int  `json:"offset"`
		} `json:"metadata"`
	}
	if err := json.Unmarshal(pageBody, &pairPage); err != nil {
		t.Fatal(err)
	}
	if pairPage.Metadata.Offset != 1 || pairPage.Metadata.Count != 1 || pairPage.Metadata.HasMore ||
		len(pairPage.Pairs) != 1 || pairPage.Pairs[0].SrcNodeID != "c" || pairPage.Pairs[0].DstNodeID != "a" ||
		pairPage.Pairs[0].TotalBytes != 5 || pairPage.Pairs[0].FlowCount != 8 || pairPage.Pairs[0].DstHostname != "laptop" {
		t.Fatalf("pair page = %#v meta %#v", pairPage.Pairs, pairPage.Metadata)
	}

	flowCode, flowBody := serve(dataRouter(h), http.MethodGet, "/api/analytics/talkers"+window+"&sort=flows&limit=1")
	if flowCode != http.StatusOK || !strings.Contains(string(flowBody), `"nodeId":"a"`) || !strings.Contains(string(flowBody), `"sort":"flows"`) {
		t.Fatalf("flow sort: %d %s", flowCode, flowBody)
	}

	unknownCode, _ := serve(dataRouter(h), http.MethodGet, "/api/analytics/talkers"+window+"&tailnet=other")
	if unknownCode != http.StatusNotFound {
		t.Fatalf("unknown tailnet status=%d", unknownCode)
	}
	badSort, _ := serve(dataRouter(h), http.MethodGet, "/api/analytics/talkers"+window+"&sort=packets")
	if badSort != http.StatusBadRequest {
		t.Fatalf("bad sort status=%d", badSort)
	}
	badOffset, _ := serve(dataRouter(h), http.MethodGet, "/api/analytics/pairs"+window+"&offset=-1")
	if badOffset != http.StatusBadRequest {
		t.Fatalf("bad offset status=%d", badOffset)
	}
	badType, _ := serve(dataRouter(h), http.MethodGet, "/api/analytics/talkers"+window+"&trafficTypes=nope")
	if badType != http.StatusBadRequest {
		t.Fatalf("bad traffic type status=%d", badType)
	}

	missing := &Handlers{}
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/analytics/talkers"+window, nil)
	missing.GetRankedTalkers(c)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("missing store status=%d", w.Code)
	}
}

func TestRankedAnalyticsSelectsTailnet(t *testing.T) {
	gin.SetMode(gin.TestMode)
	server := httptest.NewServer(tailnetHTTP(t, map[string]string{
		"alpha.example": "alpha-device",
		"beta.example":  "beta-device",
	}))
	defer server.Close()

	store := newAPIStore(t)
	seedTailnet(t, store, "alpha", 111)
	seedTailnet(t, store, "beta", 222)
	router := dataRouter(registryHandlers(t, store, []config.TailnetSpec{
		{ID: "alpha", Name: "alpha.example", APIURL: server.URL, APIKey: "alpha-secret"},
		{ID: "beta", Name: "beta.example", APIURL: server.URL, APIKey: "beta-secret"},
	}))
	start := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	window := rankWindow(start, start.Add(30*time.Minute))

	missingCode, missingBody := serve(router, http.MethodGet, "/api/analytics/talkers"+window)
	if missingCode != http.StatusBadRequest || !strings.Contains(string(missingBody), "tailnet query parameter is required") {
		t.Fatalf("missing tailnet: %d %s", missingCode, missingBody)
	}
	alphaCode, alphaBody := serve(router, http.MethodGet, "/api/analytics/talkers"+window+"&tailnet=alpha")
	if alphaCode != http.StatusOK || !strings.Contains(string(alphaBody), `"totalBytes":115`) || strings.Contains(string(alphaBody), `"totalBytes":226`) {
		t.Fatalf("alpha talkers: %d %s", alphaCode, alphaBody)
	}
	betaCode, betaBody := serve(router, http.MethodGet, "/api/analytics/pairs"+window+"&tailnet=beta")
	if betaCode != http.StatusOK || !strings.Contains(string(betaBody), `"totalBytes":226`) || strings.Contains(string(betaBody), `"totalBytes":115`) ||
		!strings.Contains(string(betaBody), `"tailnet":"beta"`) {
		t.Fatalf("beta pairs: %d %s", betaCode, betaBody)
	}
}

func TestRankedAnalyticsSearchByTagAndLogin(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := setupHandlerTestDB(t)
	h := &Handlers{store: store}
	start := time.Now().UTC().Add(-30 * time.Minute).Truncate(time.Minute)
	end := start.Add(20 * time.Minute)
	window := rankWindow(start, end)
	base := start.Unix()
	if err := store.UpsertNodeMetadata(context.Background(), database.DefaultTailnetID, []database.NodeMetadata{
		{NodeID: "nBuild001CNTRL", Hostname: "build", Tags: []string{"tag:prod"}, IPs: []string{"100.64.0.21"}},
		{NodeID: "424242", Owner: "ada@example.com", Tags: []string{"tag:prod"}, IPs: []string{"100.64.0.21"}},
		{NodeID: "nOther01CNTRL", Hostname: "laptop", Owner: "bob@example.com", Tags: []string{"tag:ops"}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertNodePairAggregates(context.Background(), database.DefaultTailnetID, []database.NodePairAggregate{
		{Bucket: base, SrcNodeID: "nBuild001CNTRL", DstNodeID: "peer", TrafficType: "virtual", TxBytes: 30, RxBytes: 4, FlowCount: 1, Protocols: "[6]", ProtocolBytes: `{"6":34}`, Ports: "[]"},
		{Bucket: base, SrcNodeID: "424242", DstNodeID: "peer", TrafficType: "virtual", TxBytes: 10, RxBytes: 1, FlowCount: 2, Protocols: "[6]", ProtocolBytes: `{"6":11}`, Ports: "[]"},
		{Bucket: base, SrcNodeID: "nOther01CNTRL", DstNodeID: "peer", TrafficType: "virtual", TxBytes: 80, RxBytes: 1, FlowCount: 1, Protocols: "[6]", ProtocolBytes: `{"6":81}`, Ports: "[]"},
		{Bucket: base, SrcNodeID: "nBuild001CNTRL", DstNodeID: "127.3.3.40", TrafficType: "physical", TxBytes: 900, FlowCount: 1, Protocols: "[0]", ProtocolBytes: `{"0":900}`, Ports: "[]"},
	}); err != nil {
		t.Fatal(err)
	}

	// The cache sees the API device (tagged, no user) and is hydrated from the
	// stored metadata, as the poller does on each device refresh.
	poller := services.NewPoller(nil, store, services.DefaultPollerConfig())
	poller.GetDeviceCache().Update([]services.Device{
		{ID: "nBuild001CNTRL", NodeID: "nBuild001CNTRL", Hostname: "build", Addresses: []string{"100.64.0.21"}, Tags: []string{"tag:prod"}},
		{ID: "nOther01CNTRL", NodeID: "nOther01CNTRL", Hostname: "laptop", User: "bob@example.com", Tags: []string{"tag:ops"}},
	})
	metadata, err := store.GetNodeMetadata(context.Background(), database.DefaultTailnetID)
	if err != nil {
		t.Fatal(err)
	}
	poller.GetDeviceCache().UpsertNodeMetadata(metadata)
	h.poller = poller

	code, body := serve(dataRouter(h), http.MethodGet, "/api/analytics/talkers"+window+"&q="+url.QueryEscape("ada@example.com"))
	if code != http.StatusOK || !strings.Contains(string(body), `"nodeId":"nBuild001CNTRL"`) || !strings.Contains(string(body), `"owner":"ada@example.com"`) ||
		!strings.Contains(string(body), `"totalBytes":45`) || strings.Contains(string(body), `"nodeId":"nOther01CNTRL"`) || strings.Contains(string(body), `"totalBytes":900`) {
		t.Fatalf("login search: %d %s", code, body)
	}
	code, body = serve(dataRouter(h), http.MethodGet, "/api/analytics/talkers"+window+"&q="+url.QueryEscape("user@ada"))
	if code != http.StatusOK || !strings.Contains(string(body), `"nodeId":"nBuild001CNTRL"`) || strings.Contains(string(body), `"nodeId":"nOther01CNTRL"`) {
		t.Fatalf("user@ search: %d %s", code, body)
	}
	code, body = serve(dataRouter(h), http.MethodGet, "/api/analytics/pairs"+window+"&q="+url.QueryEscape("tag:prod"))
	if code != http.StatusOK || !strings.Contains(string(body), `"srcNodeId":"nBuild001CNTRL"`) || !strings.Contains(string(body), `"totalBytes":45`) ||
		strings.Contains(string(body), "127.3.3.40") || strings.Contains(string(body), `"srcNodeId":"nOther01CNTRL"`) || !strings.Contains(string(body), `"q":"tag:prod"`) {
		t.Fatalf("tag search: %d %s", code, body)
	}
	code, body = serve(dataRouter(h), http.MethodGet, "/api/analytics/pairs"+window+"&q=tag:prod&trafficTypes=physical")
	if code != http.StatusOK || !strings.Contains(string(body), `"dstNodeId":"127.3.3.40"`) || !strings.Contains(string(body), `"dstHostname":"DERP relay"`) ||
		strings.Contains(string(body), `"dstNodeId":"peer"`) {
		t.Fatalf("physical tag search: %d %s", code, body)
	}
	code, _ = serve(dataRouter(h), http.MethodGet, "/api/analytics/talkers"+window+"&q="+url.QueryEscape("tag:"+strings.Repeat("p", 400)))
	if code != http.StatusBadRequest {
		t.Fatalf("long search status=%d", code)
	}
	// The separate tag and user parameters are not part of the API; q is.
	code, body = serve(dataRouter(h), http.MethodGet, "/api/analytics/talkers"+window+"&tag=ops")
	if code != http.StatusOK || !strings.Contains(string(body), `"nodeId":"nOther01CNTRL"`) || !strings.Contains(string(body), `"nodeId":"nBuild001CNTRL"`) {
		t.Fatalf("tag param should be ignored: %d %s", code, body)
	}

	plainCode, plainBody := serve(dataRouter(h), http.MethodGet, "/api/analytics/talkers"+window)
	if plainCode != http.StatusOK || strings.Contains(string(plainBody), `"q":`) || strings.Contains(string(plainBody), `"tag"`) {
		t.Fatalf("unfiltered talkers changed: %d %s", plainCode, plainBody)
	}
}

func rankWindow(start, end time.Time) string {
	return "?start=" + url.QueryEscape(start.Format(time.RFC3339)) + "&end=" + url.QueryEscape(end.Format(time.RFC3339))
}
