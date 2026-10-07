package handlers

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/rajsinghtech/tsflow/backend/internal/access"
	"github.com/rajsinghtech/tsflow/backend/internal/config"
	"github.com/rajsinghtech/tsflow/backend/internal/database"
	"github.com/rajsinghtech/tsflow/backend/internal/services"
)

func TestSingleTailnetResponsesMatchDirectHandlers(t *testing.T) {
	const secret = "golden-api-key-should-not-matter"
	server := httptest.NewServer(tailnetHTTP(t, map[string]string{
		"example.com": "device-1",
	}))
	defer server.Close()

	store := newAPIStore(t)
	seedTailnet(t, store, database.DefaultTailnetID, 1000)
	base := quietPollerConfig()
	spec := config.TailnetSpec{
		ID:     database.DefaultTailnetID,
		Name:   "example.com",
		APIURL: server.URL,
		APIKey: secret,
	}
	registry, err := services.NewRegistry(context.Background(), []config.TailnetSpec{spec}, store, base)
	if err != nil {
		t.Fatal(err)
	}
	directService := services.NewTailscaleService(spec.ServiceConfig(nil))
	directPoller := services.NewPoller(directService, store, base)
	direct := NewHandlers(directService, store, directPoller, "test")
	routed := NewHandlers(directService, store, directPoller, "test")
	routed.UseRegistry(registry)

	start := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	end := start.Add(30 * time.Minute)
	window := fmt.Sprintf("?start=%s&end=%s", url.QueryEscape(start.Format(time.RFC3339)), url.QueryEscape(end.Format(time.RFC3339)))
	endpoints := []struct {
		method string
		path   string
		status int
	}{
		{http.MethodGet, "/api/devices", http.StatusOK},
		{http.MethodGet, "/api/services-records", http.StatusOK},
		{http.MethodGet, "/api/network-logs" + window, http.StatusOK},
		{http.MethodGet, "/api/network-map", http.StatusOK},
		{http.MethodGet, "/api/devices/device-1/flows", http.StatusGone},
		{http.MethodGet, "/api/dns/nameservers", http.StatusOK},
		{http.MethodGet, "/api/flow-logs", http.StatusGone},
		{http.MethodGet, "/api/flow-logs/aggregated" + window, http.StatusOK},
		{http.MethodGet, "/api/flow-logs/range", http.StatusOK},
		{http.MethodGet, "/api/bandwidth" + window, http.StatusOK},
		{http.MethodGet, "/api/stats/overview" + window, http.StatusOK},
		{http.MethodGet, "/api/stats/top-talkers" + window, http.StatusOK},
		{http.MethodGet, "/api/stats/top-pairs" + window, http.StatusOK},
		{http.MethodGet, "/api/analytics/talkers" + window, http.StatusOK},
		{http.MethodGet, "/api/analytics/pairs" + window, http.StatusOK},
		{http.MethodGet, "/api/stats/node/node-a" + window, http.StatusOK},
		{http.MethodGet, "/api/policy", http.StatusOK},
		{http.MethodGet, "/api/users", http.StatusOK},
		{http.MethodGet, "/api/poller/status", http.StatusOK},
		{http.MethodPost, "/api/poller/trigger", http.StatusOK},
	}

	directRouter := dataRouter(direct)
	routedRouter := dataRouter(routed)
	openRouter := dataRouter(direct, access.Middleware(config.Access{}, nil))
	for _, ep := range endpoints {
		t.Run(ep.method+" "+ep.path, func(t *testing.T) {
			directCode, directBody := serve(directRouter, ep.method, ep.path)
			routedCode, routedBody := serve(routedRouter, ep.method, ep.path)
			selectedCode, selectedBody := serve(routedRouter, ep.method, withTailnet(ep.path, database.DefaultTailnetID))
			plainCode, plainBody := serve(directRouter, ep.method, withTailnet(ep.path, database.DefaultTailnetID))
			openCode, openBody := serve(openRouter, ep.method, ep.path)
			if directCode != ep.status {
				t.Fatalf("direct status=%d body=%s", directCode, directBody)
			}
			if routedCode != directCode || !bytes.Equal(routedBody, directBody) {
				t.Fatalf("registry response differs\ndirect: %d %s\nrouted: %d %s", directCode, directBody, routedCode, routedBody)
			}
			if selectedCode != directCode || !bytes.Equal(selectedBody, directBody) {
				t.Fatalf("?tailnet=default differs\ndirect: %d %s\nselected: %d %s", directCode, directBody, selectedCode, selectedBody)
			}
			if plainCode != directCode || !bytes.Equal(plainBody, directBody) {
				t.Fatalf("direct ?tailnet=default differs\nplain: %d %s\ntagged: %d %s", directCode, directBody, plainCode, plainBody)
			}
			if openCode != directCode || !bytes.Equal(openBody, directBody) {
				t.Fatalf("access control off differs\ndirect: %d %s\nopen: %d %s", directCode, directBody, openCode, openBody)
			}
		})
	}

	_, flows := serve(directRouter, http.MethodGet, "/api/flow-logs/aggregated"+window)
	if !bytes.Contains(flows, []byte(`"totalTxBytes":1000`)) {
		t.Fatalf("aggregated flows missing seeded bytes: %s", flows)
	}
	_, devices := serve(directRouter, http.MethodGet, "/api/devices")
	if !bytes.Contains(devices, []byte("device-1")) {
		t.Fatalf("devices missing fixture: %s", devices)
	}
}

func TestHealthIgnoresTailnetQuery(t *testing.T) {
	h := NewHandlers(nil, nil, nil, "test")
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/health?tailnet=missing", nil)
	h.HealthCheck(c)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestMultiTailnetRouting(t *testing.T) {
	server := httptest.NewServer(tailnetHTTP(t, map[string]string{
		"alpha.example":   "alpha-device",
		"beta.example":    "beta-device",
		"default.example": "default-device",
	}))
	defer server.Close()

	store := newAPIStore(t)
	seedTailnet(t, store, "alpha", 111)
	seedTailnet(t, store, "beta", 222)
	seedTailnet(t, store, database.DefaultTailnetID, 333)

	windowStart := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	window := fmt.Sprintf("?start=%s&end=%s", url.QueryEscape(windowStart.Format(time.RFC3339)), url.QueryEscape(windowStart.Add(30*time.Minute).Format(time.RFC3339)))

	withoutDefault := dataRouter(registryHandlers(t, store, []config.TailnetSpec{
		{ID: "alpha", Name: "alpha.example", APIURL: server.URL, APIKey: "alpha-secret"},
		{ID: "beta", Name: "beta.example", APIURL: server.URL, APIKey: "beta-secret"},
	}))

	alphaCode, alphaBody := serve(withoutDefault, http.MethodGet, "/api/flow-logs/aggregated"+window+"&tailnet=alpha")
	if alphaCode != http.StatusOK || !bytes.Contains(alphaBody, []byte(`"totalTxBytes":111`)) || bytes.Contains(alphaBody, []byte(`"totalTxBytes":222`)) {
		t.Fatalf("alpha flows: %d %s", alphaCode, alphaBody)
	}
	betaCode, betaBody := serve(withoutDefault, http.MethodGet, "/api/flow-logs/aggregated"+window+"&tailnet=beta")
	if betaCode != http.StatusOK || !bytes.Contains(betaBody, []byte(`"totalTxBytes":222`)) || bytes.Contains(betaBody, []byte(`"totalTxBytes":111`)) {
		t.Fatalf("beta flows: %d %s", betaCode, betaBody)
	}
	alphaDevices, alphaDeviceBody := serve(withoutDefault, http.MethodGet, "/api/devices?tailnet=alpha")
	if alphaDevices != http.StatusOK || !bytes.Contains(alphaDeviceBody, []byte("alpha-device")) || bytes.Contains(alphaDeviceBody, []byte("beta-device")) {
		t.Fatalf("alpha devices: %d %s", alphaDevices, alphaDeviceBody)
	}
	betaDevices, betaDeviceBody := serve(withoutDefault, http.MethodGet, "/api/devices?tailnet=beta")
	if betaDevices != http.StatusOK || !bytes.Contains(betaDeviceBody, []byte("beta-device")) || bytes.Contains(betaDeviceBody, []byte("alpha-device")) {
		t.Fatalf("beta devices: %d %s", betaDevices, betaDeviceBody)
	}

	missingCode, missingBody := serve(withoutDefault, http.MethodGet, "/api/flow-logs/aggregated"+window)
	if missingCode != http.StatusBadRequest || !bytes.Contains(missingBody, []byte("alpha")) || !bytes.Contains(missingBody, []byte("beta")) || !bytes.Contains(missingBody, []byte("tailnet query parameter is required")) {
		t.Fatalf("missing tailnet: %d %s", missingCode, missingBody)
	}
	emptyCode, emptyBody := serve(withoutDefault, http.MethodGet, "/api/devices?tailnet=")
	if emptyCode != http.StatusBadRequest || !bytes.Contains(emptyBody, []byte(`"tailnets":["alpha","beta"]`)) {
		t.Fatalf("empty tailnet: %d %s", emptyCode, emptyBody)
	}
	unknownCode, unknownBody := serve(withoutDefault, http.MethodGet, "/api/devices?tailnet=missing")
	if unknownCode != http.StatusNotFound || !bytes.Contains(unknownBody, []byte(`unknown tailnet \"missing\"`)) {
		t.Fatalf("unknown tailnet: %d %s", unknownCode, unknownBody)
	}
	defaultMissingCode, _ := serve(withoutDefault, http.MethodGet, "/api/stats/overview"+window+"&tailnet=default")
	if defaultMissingCode != http.StatusNotFound {
		t.Fatalf("unset default status=%d", defaultMissingCode)
	}

	withDefault := dataRouter(registryHandlers(t, store, []config.TailnetSpec{
		{ID: database.DefaultTailnetID, Name: "default.example", APIURL: server.URL, APIKey: "default-secret"},
		{ID: "beta", Name: "beta.example", APIURL: server.URL, APIKey: "beta-secret"},
	}))
	implicitCode, implicitBody := serve(withDefault, http.MethodGet, "/api/flow-logs/aggregated"+window)
	explicitCode, explicitBody := serve(withDefault, http.MethodGet, "/api/flow-logs/aggregated"+window+"&tailnet=default")
	if implicitCode != http.StatusOK || !bytes.Equal(implicitBody, explicitBody) || !bytes.Contains(implicitBody, []byte(`"totalTxBytes":333`)) || bytes.Contains(implicitBody, []byte(`"totalTxBytes":222`)) {
		t.Fatalf("default selection\nimplicit: %d %s\nexplicit: %d %s", implicitCode, implicitBody, explicitCode, explicitBody)
	}
	otherCode, otherBody := serve(withDefault, http.MethodGet, "/api/devices?tailnet=beta")
	if otherCode != http.StatusOK || !bytes.Contains(otherBody, []byte("beta-device")) || bytes.Contains(otherBody, []byte("default-device")) {
		t.Fatalf("beta devices with default configured: %d %s", otherCode, otherBody)
	}
}

func TestSingleConfiguredTailnetDoesNotRequireParam(t *testing.T) {
	server := httptest.NewServer(tailnetHTTP(t, map[string]string{
		"lab.example": "lab-device",
	}))
	defer server.Close()
	store := newAPIStore(t)
	h := registryHandlers(t, store, []config.TailnetSpec{{
		ID: "lab", Name: "lab.example", APIURL: server.URL, APIKey: "lab-secret",
	}})
	router := dataRouter(h)
	plainCode, plainBody := serve(router, http.MethodGet, "/api/devices")
	namedCode, namedBody := serve(router, http.MethodGet, "/api/devices?tailnet=lab")
	if plainCode != http.StatusOK || !bytes.Equal(plainBody, namedBody) || !bytes.Contains(plainBody, []byte("lab-device")) {
		t.Fatalf("single tailnet\nplain: %d %s\nnamed: %d %s", plainCode, plainBody, namedCode, namedBody)
	}
	unknownCode, unknownBody := serve(router, http.MethodGet, "/api/devices?tailnet=default")
	if unknownCode != http.StatusNotFound || !bytes.Contains(unknownBody, []byte(`unknown tailnet \"default\"`)) {
		t.Fatalf("unknown default: %d %s", unknownCode, unknownBody)
	}
}

func TestTailnetRequestsDoNotBlockEachOther(t *testing.T) {
	release := make(chan struct{})
	started := make(chan struct{})
	var once sync.Once
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		once.Do(func() { close(started) })
		<-release
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(deviceJSON("slow-device")))
	}))
	defer slow.Close()
	fast := httptest.NewServer(tailnetHTTP(t, map[string]string{
		"fast.example": "fast-device",
	}))
	defer fast.Close()

	router := dataRouter(registryHandlers(t, newAPIStore(t), []config.TailnetSpec{
		{ID: "slow", Name: "slow.example", APIURL: slow.URL, APIKey: "slow-secret"},
		{ID: "fast", Name: "fast.example", APIURL: fast.URL, APIKey: "fast-secret"},
	}))

	errCh := make(chan error, 1)
	go func() {
		code, body := serve(router, http.MethodGet, "/api/devices?tailnet=slow")
		if code != http.StatusOK || !bytes.Contains(body, []byte("slow-device")) {
			errCh <- fmt.Errorf("slow status=%d body=%s", code, body)
			return
		}
		errCh <- nil
	}()

	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("slow tailnet request did not reach its API")
	}

	began := time.Now()
	code, body := serve(router, http.MethodGet, "/api/devices?tailnet=fast")
	if time.Since(began) > time.Second {
		t.Fatalf("fast tailnet waited %s for the slow tailnet", time.Since(began))
	}
	if code != http.StatusOK || !bytes.Contains(body, []byte("fast-device")) {
		t.Fatalf("fast status=%d body=%s", code, body)
	}
	close(release)
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("slow tailnet request did not finish")
	}
}

func TestListTailnetsOmitsCredentials(t *testing.T) {
	const apiSecret = "api-key-must-stay-hidden"
	const secondSecret = "second-api-key-must-stay-hidden"
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	}))
	defer bad.Close()
	okServer := httptest.NewServer(tailnetHTTP(t, map[string]string{
		"ok.example": "ok-device",
	}))
	defer okServer.Close()

	store := newAPIStore(t)
	base := quietPollerConfig()
	registry, err := services.NewRegistry(context.Background(), []config.TailnetSpec{
		{ID: "bad", Name: "Bad Network", APIURL: bad.URL, APIKey: apiSecret},
		{ID: "ok", Name: "ok.example", APIURL: okServer.URL, APIKey: secondSecret},
	}, store, base)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := registry.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cancel()
		registry.Stop()
	})

	badEntry, found := registry.Get("bad")
	if !found {
		t.Fatal("missing bad tailnet")
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if badEntry.Poller.Stats()["pollErrors"].(int64) > 0 {
			break
		}
		time.Sleep(15 * time.Millisecond)
	}
	if badEntry.Poller.Stats()["pollErrors"].(int64) == 0 {
		t.Fatal("unauthorized tailnet did not record a poll error")
	}

	h := NewHandlers(nil, store, nil, "test")
	h.UseRegistry(registry)
	code, body := serve(dataRouter(h), http.MethodGet, "/api/tailnets")
	if code != http.StatusOK {
		t.Fatalf("status=%d body=%s", code, body)
	}
	text := string(body)
	if strings.Contains(text, apiSecret) || strings.Contains(text, secondSecret) {
		t.Fatalf("tailnet list leaked credentials: %s", text)
	}
	if !strings.Contains(text, `"id":"bad"`) || !strings.Contains(text, `"displayName":"Bad Network"`) {
		t.Fatalf("missing id or display name: %s", text)
	}
	if !strings.Contains(text, `"id":"ok"`) || !strings.Contains(text, `"displayName":"ok.example"`) {
		t.Fatalf("missing second tailnet: %s", text)
	}
	if !strings.Contains(text, `"lastError"`) {
		t.Fatalf("missing poller last error: %s", text)
	}
	badAt := strings.Index(text, `"id":"bad"`)
	okAt := strings.Index(text, `"id":"ok"`)
	if badAt < 0 || okAt < 0 || badAt > okAt {
		t.Fatalf("tailnets not in configured order: %s", text)
	}
}

func registryHandlers(t *testing.T, store database.Store, specs []config.TailnetSpec) *Handlers {
	t.Helper()
	registry, err := services.NewRegistry(context.Background(), specs, store, quietPollerConfig())
	if err != nil {
		t.Fatal(err)
	}
	var service *services.TailscaleService
	var poller *services.Poller
	if entry, ok := registry.Default(); ok {
		service = entry.Service
		poller = entry.Poller
	}
	h := NewHandlers(service, store, poller, "test")
	h.UseRegistry(registry)
	return h
}

func quietPollerConfig() services.PollerConfig {
	cfg := services.DefaultPollerConfig()
	cfg.PollInterval = time.Hour
	cfg.InitialBackfill = 20 * time.Minute
	cfg.Retention = 24 * time.Hour
	cfg.CleanupInterval = time.Hour
	cfg.DeviceCacheRefresh = time.Hour
	cfg.FlowBackend = "api"
	return cfg
}

func seedTailnet(t *testing.T, store *database.SQLiteStore, id string, tx int64) {
	t.Helper()
	bucket := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	ctx := context.Background()
	if err := store.UpsertNodePairAggregates(ctx, id, []database.NodePairAggregate{{
		Bucket:        bucket.Unix(),
		SrcNodeID:     "node-a",
		DstNodeID:     "node-b",
		TrafficType:   "virtual",
		TxBytes:       tx,
		RxBytes:       4,
		TxPkts:        1,
		RxPkts:        1,
		FlowCount:     1,
		Protocols:     "[6]",
		ProtocolBytes: fmt.Sprintf(`{"6":%d}`, tx+4),
		Ports:         fmt.Sprintf(`[{"port":443,"proto":6,"bytes":%d}]`, tx+4),
	}}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertBandwidth(ctx, id, []database.BandwidthBucket{{
		Time:    bucket,
		TxBytes: tx,
		RxBytes: 4,
	}}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertTrafficStats(ctx, id, []database.TrafficStats{{
		Bucket:       bucket.Unix(),
		TCPBytes:     tx + 4,
		VirtualBytes: tx + 4,
		TotalFlows:   1,
		UniquePairs:  1,
		TopPorts:     "[]",
	}}); err != nil {
		t.Fatal(err)
	}
}

func dataRouter(h *Handlers, extra ...gin.HandlerFunc) http.Handler {
	router := gin.New()
	api := router.Group("/api")
	for _, mw := range extra {
		if mw != nil {
			api.Use(mw)
		}
	}
	api.GET("/devices", h.GetDevices)
	api.GET("/services-records", h.GetServicesAndRecords)
	api.GET("/network-logs", h.GetNetworkLogs)
	api.GET("/network-map", h.GetNetworkMap)
	api.GET("/devices/:deviceId/flows", h.GetDeviceFlows)
	api.GET("/dns/nameservers", h.GetDNSNameservers)
	api.GET("/flow-logs", h.GetStoredFlowLogs)
	api.GET("/flow-logs/aggregated", h.GetAggregatedFlowLogs)
	api.GET("/flow-logs/range", h.GetDataRange)
	api.GET("/bandwidth", h.GetBandwidthAggregated)
	api.GET("/stats/overview", h.GetStatsOverview)
	api.GET("/stats/top-talkers", h.GetTopTalkers)
	api.GET("/stats/top-pairs", h.GetTopPairs)
	api.GET("/analytics/talkers", h.GetRankedTalkers)
	api.GET("/analytics/pairs", h.GetRankedPairs)
	api.GET("/analytics/device-timeline", h.GetDeviceTimeline)
	api.GET("/analytics/new-pairs", h.GetNewPairs)
	api.GET("/stats/node/:id", h.GetNodeDetailStats)
	api.GET("/policy", h.GetPolicy)
	api.GET("/users", h.GetUsers)
	api.GET("/poller/status", h.GetPollerStatus)
	api.POST("/poller/trigger", h.TriggerPoll)
	api.GET("/tailnets", h.ListTailnets)
	return router
}

func serve(handler http.Handler, method, target string) (int, []byte) {
	req := httptest.NewRequest(method, target, nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	return w.Code, w.Body.Bytes()
}

func withTailnet(path, id string) string {
	if strings.Contains(path, "?") {
		return path + "&tailnet=" + url.QueryEscape(id)
	}
	return path + "?tailnet=" + url.QueryEscape(id)
}

func tailnetHTTP(t *testing.T, devicesByTailnet map[string]string) http.Handler {
	t.Helper()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tailnet, deviceID := "", ""
		for name, id := range devicesByTailnet {
			if strings.Contains(r.URL.Path, "/tailnet/"+name+"/") {
				tailnet = name
				deviceID = id
				break
			}
		}
		if tailnet == "" {
			http.Error(w, "unexpected path "+r.URL.Path, http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.Contains(r.URL.Path, "/logging/network"):
			_, _ = w.Write([]byte(flowBody(time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC))))
		case strings.Contains(r.URL.Path, "/dns/nameservers"):
			_, _ = w.Write([]byte(`{"dns":["1.1.1.1"]}`))
		case strings.Contains(r.URL.Path, "/dns/preferences"):
			_, _ = w.Write([]byte(`{"magicDNS":true,"searchDomains":["` + tailnet + `"]}`))
		case strings.Contains(r.URL.Path, "/static-records"):
			_, _ = w.Write([]byte(`{"records":{"app.` + tailnet + `":{"addrs":["100.64.0.10"],"comment":"app"}}}`))
		case strings.Contains(r.URL.Path, "/services"):
			_, _ = w.Write([]byte(`{"vipServices":[{"name":"svc","addrs":["100.64.0.10"]}]}`))
		case strings.Contains(r.URL.Path, "/users"):
			_, _ = w.Write([]byte(`{"users":[{"id":"u1","loginName":"ada@` + tailnet + `"}]}`))
		case strings.Contains(r.URL.Path, "/acl"):
			_, _ = w.Write([]byte(`{"acls":[{"action":"accept","src":["*"],"dst":["*:*"]}]}`))
		case strings.Contains(r.URL.Path, "/devices"):
			_, _ = w.Write([]byte(deviceJSON(deviceID)))
		default:
			http.Error(w, "unhandled "+r.URL.Path, http.StatusNotFound)
		}
	})
}

func deviceJSON(id string) string {
	return `{"devices":[{"id":"` + id + `","name":"` + id + `.example.com","hostname":"` + id + `","addresses":["100.64.0.1"],"authorized":true,"lastSeen":"2026-03-01T12:00:00Z","created":"2026-03-01T12:00:00Z"}]}`
}
