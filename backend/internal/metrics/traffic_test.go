package metrics

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"github.com/rajsinghtech/tsflow/backend/internal/config"
	"github.com/rajsinghtech/tsflow/backend/internal/database"
	"github.com/rajsinghtech/tsflow/backend/internal/services"
)

var trafficFamilies = []string{
	"tsflow_traffic_bytes_total", "tsflow_traffic_packets_total", "tsflow_traffic_flows_total",
	"tsflow_traffic_protocol_bytes_total", "tsflow_traffic_window_seconds", "tsflow_traffic_window_end_timestamp_seconds",
	"tsflow_tag_traffic_bytes", "tsflow_tag_traffic_packets", "tsflow_user_traffic_bytes", "tsflow_user_traffic_packets",
	"tsflow_port_traffic_bytes", "tsflow_top_talker_bytes", "tsflow_traffic_window_compute_seconds",
}

func TestTrafficMetricsOffByDefault(t *testing.T) {
	m := New(Options{})
	if m.traffic || m.trafficBytes != nil {
		t.Fatal("traffic counters built without Options.Traffic")
	}
	fams := gather(t, m)
	for _, name := range trafficFamilies {
		if _, ok := fams[name]; ok {
			t.Errorf("%s served without TSFLOW_METRICS_TRAFFIC", name)
		}
	}
}

// testIdentities is three devices and an address no device owns:
// A has tag:web, B has tag:web and tag:db, C has no tags.
func testIdentities() *services.DeviceCache {
	dc := services.NewDeviceCache()
	dc.Update([]services.Device{
		{ID: "nA", Name: "a.example.ts.net", User: "alice@example.com", Addresses: []string{"100.64.0.1"}, Tags: []string{"tag:web"}},
		{ID: "nB", Name: "b.example.ts.net", User: "tagged-devices", Addresses: []string{"100.64.0.2"}, Tags: []string{"tag:web", "tag:db"}},
		{ID: "nC", Name: "c.example.ts.net", User: "bob@example.com", Addresses: []string{"100.64.0.3"}},
	})
	return dc
}

func pair(bucket int64, src, dst, typ string, tx, rx int64) database.NodePairAggregate {
	return database.NodePairAggregate{Bucket: bucket, SrcNodeID: src, DstNodeID: dst, TrafficType: typ,
		TxBytes: tx, RxBytes: rx, TxPkts: tx / 10, RxPkts: rx / 10, FlowCount: 1,
		Protocols: "[6]", ProtocolBytes: "{}", Ports: "[]"}
}

func testWindow(now time.Time) ([]database.NodePairAggregate, []database.TrafficStats) {
	b1 := now.Add(-5*time.Minute).Unix() / 60 * 60
	b2 := b1 + 60
	newest := now.Unix() / 60 * 60
	pairs := []database.NodePairAggregate{
		pair(b1, "nA", "nB", "virtual", 100, 50),
		pair(b2, "nA", "nC", "virtual", 10, 0),
		pair(b2, "10.0.0.9", "nC", "subnet", 0, 7),
		pair(b2, "nA", "nB", "physical", 1000, 1000),
		// The newest minute is still filling and stays out of the window.
		pair(newest, "nA", "nB", "virtual", 5000, 5000),
	}
	stats := []database.TrafficStats{
		{Bucket: b1, TCPBytes: 140, UDPBytes: 10, TopPorts: `[{"port":443,"proto":6,"bytes":100},{"port":53,"proto":17,"bytes":10}]`},
		{Bucket: b2, TCPBytes: 10, OtherProtoBytes: 7, TopPorts: `[{"port":443,"proto":6,"bytes":10}]`},
		{Bucket: newest, TCPBytes: 10000, TopPorts: `[{"port":22,"proto":6,"bytes":10000}]`},
	}
	return pairs, stats
}

type sample struct {
	labels map[string]string
	value  float64
}

func collectSamples(t *testing.T, metrics []prometheus.Metric) map[string][]sample {
	t.Helper()
	out := map[string][]sample{}
	for _, m := range metrics {
		var pb dto.Metric
		if err := m.Write(&pb); err != nil {
			t.Fatal(err)
		}
		desc := m.Desc().String()
		name := desc[strings.Index(desc, `fqName: "`)+9:]
		name = name[:strings.Index(name, `"`)]
		labels := map[string]string{}
		for _, l := range pb.GetLabel() {
			labels[l.GetName()] = l.GetValue()
		}
		out[name] = append(out[name], sample{labels, pb.GetGauge().GetValue() + pb.GetCounter().GetValue()})
	}
	return out
}

func find(samples []sample, kv ...string) (float64, bool) {
next:
	for _, s := range samples {
		for i := 0; i+1 < len(kv); i += 2 {
			if s.labels[kv[i]] != kv[i+1] {
				continue next
			}
		}
		return s.value, true
	}
	return 0, false
}

func TestTrafficWindowBreakdowns(t *testing.T) {
	now := time.Now()
	rc := services.NewRollingWindowCache(time.Hour)
	pairs, stats := testWindow(now)
	rc.Update(pairs, nil, nil, stats)
	c := &trafficCollector{breakdowns: true, topTalkers: 2, now: time.Now}
	got := collectSamples(t, c.computeTailnet(nil, "default", rc, testIdentities()))

	want := []struct {
		name string
		kv   []string
		v    float64
	}{
		{"tsflow_tag_traffic_bytes", []string{"tag", "tag:web", "direction", "tx"}, 160},
		{"tsflow_tag_traffic_bytes", []string{"tag", "tag:web", "direction", "rx"}, 150},
		{"tsflow_tag_traffic_bytes", []string{"tag", "tag:db", "direction", "tx"}, 50},
		{"tsflow_tag_traffic_bytes", []string{"tag", "tag:db", "direction", "rx"}, 100},
		{"tsflow_tag_traffic_bytes", []string{"tag", "untagged", "direction", "tx"}, 7},
		{"tsflow_tag_traffic_bytes", []string{"tag", "untagged", "direction", "rx"}, 10},
		{"tsflow_tag_traffic_bytes", []string{"tag", "unknown", "direction", "tx"}, 0},
		{"tsflow_tag_traffic_bytes", []string{"tag", "unknown", "direction", "rx"}, 7},
		{"tsflow_tag_traffic_packets", []string{"tag", "tag:web", "direction", "tx"}, 16},
		{"tsflow_user_traffic_bytes", []string{"user", "alice@example.com", "direction", "tx"}, 110},
		{"tsflow_user_traffic_bytes", []string{"user", "alice@example.com", "direction", "rx"}, 50},
		{"tsflow_user_traffic_bytes", []string{"user", "tagged-devices", "direction", "rx"}, 100},
		{"tsflow_user_traffic_bytes", []string{"user", "bob@example.com", "direction", "tx"}, 7},
		{"tsflow_user_traffic_bytes", []string{"user", "unknown", "direction", "rx"}, 7},
		{"tsflow_port_traffic_bytes", []string{"protocol", "tcp", "port", "443"}, 110},
		{"tsflow_port_traffic_bytes", []string{"protocol", "udp", "port", "53"}, 10},
		{"tsflow_port_traffic_bytes", []string{"protocol", "other", "port", "other"}, 47},
		{"tsflow_top_talker_bytes", []string{"node", "a.example.ts.net", "node_id", "nA", "direction", "tx"}, 110},
		{"tsflow_top_talker_bytes", []string{"node", "b.example.ts.net", "node_id", "nB", "direction", "rx"}, 100},
		{"tsflow_traffic_window_seconds", []string{"tailnet", "default"}, 120},
		{"tsflow_traffic_window_end_timestamp_seconds", []string{"tailnet", "default"}, float64(now.Unix() / 60 * 60)},
	}
	for _, w := range want {
		v, ok := find(got[w.name], append([]string{"tailnet", "default"}, w.kv...)...)
		if !ok {
			t.Errorf("%s%v missing; have %v", w.name, w.kv, got[w.name])
			continue
		}
		if v != w.v {
			t.Errorf("%s%v = %v, want %v", w.name, w.kv, v, w.v)
		}
	}
	if n := len(got["tsflow_top_talker_bytes"]); n != 4 {
		t.Errorf("top talker series = %d, want 2 devices x 2 directions", n)
	}
	if _, ok := find(got["tsflow_port_traffic_bytes"], "port", "22"); ok {
		t.Error("the newest, still-filling minute leaked into the window")
	}
}

func TestTrafficCounters(t *testing.T) {
	m := New(Options{Traffic: true})
	pairs, stats := testWindow(time.Now())
	observe := m.trafficObserver("default")
	observe(pairs, stats)
	fams := gather(t, m)
	checks := []struct {
		name string
		kv   []string
		want float64
	}{
		{"tsflow_traffic_bytes_total", []string{"traffic_type", "virtual"}, 100 + 50 + 10 + 10000},
		{"tsflow_traffic_bytes_total", []string{"traffic_type", "subnet"}, 7},
		{"tsflow_traffic_bytes_total", []string{"traffic_type", "physical"}, 2000},
		{"tsflow_traffic_bytes_total", []string{"traffic_type", "exit"}, 0},
		{"tsflow_traffic_packets_total", []string{"traffic_type", "virtual"}, 10 + 5 + 1 + 1000},
		{"tsflow_traffic_flows_total", []string{"traffic_type", "virtual"}, 3},
		{"tsflow_traffic_protocol_bytes_total", []string{"protocol", "tcp"}, 10150},
		{"tsflow_traffic_protocol_bytes_total", []string{"protocol", "udp"}, 10},
		{"tsflow_traffic_protocol_bytes_total", []string{"protocol", "other"}, 7},
	}
	for _, c := range checks {
		got, ok := value(t, fams, c.name, append([]string{"tailnet", "default"}, c.kv...)...)
		if !ok {
			t.Errorf("%s%v missing", c.name, c.kv)
			continue
		}
		if v := got.GetCounter().GetValue(); v != c.want {
			t.Errorf("%s%v = %v, want %v", c.name, c.kv, v, c.want)
		}
	}
}

// TestTrafficSeriesStayBounded runs the window at 20,000 devices with a
// tag, user and port count far past the top N, and checks the series count
// does not grow with them.
func TestTrafficSeriesStayBounded(t *testing.T) {
	now := time.Now()
	rc, dc := syntheticTailnet(now, 20000, 500, 3000, 2000, 15)
	c := &trafficCollector{breakdowns: true, now: time.Now}
	got := collectSamples(t, c.computeTailnet(nil, "default", rc, dc))
	limits := map[string]int{
		// top 20 + other + untagged + unknown, two directions
		"tsflow_tag_traffic_bytes":   (trafficTopN + 3) * 2,
		"tsflow_tag_traffic_packets": (trafficTopN + 3) * 2,
		"tsflow_user_traffic_bytes":  (trafficTopN + 2) * 2,
		"tsflow_port_traffic_bytes":  trafficTopN + 1,
	}
	for name, limit := range limits {
		if n := len(got[name]); n == 0 || n > limit {
			t.Errorf("%s has %d series, want 1..%d", name, n, limit)
		}
	}
	if _, ok := find(got["tsflow_tag_traffic_bytes"], "tag", "other"); !ok {
		t.Error("tags past the top N were not folded into other")
	}
	if _, ok := got["tsflow_top_talker_bytes"]; ok {
		t.Error("top talkers listed without TSFLOW_METRICS_TOP_TALKERS")
	}
}

func TestTrafficWindowIsCachedForTTL(t *testing.T) {
	now := time.Now()
	clock := now
	rc := services.NewRollingWindowCache(time.Hour)
	pairs, stats := testWindow(now)
	rc.Update(pairs[:1], nil, nil, stats[:1])
	rc.Update(pairs[4:], nil, nil, stats[2:])
	c := &trafficCollector{
		sources:    func() []trafficSource { return []trafficSource{{"default", rc, testIdentities()}} },
		breakdowns: true,
		now:        func() time.Time { return clock },
	}
	read := func() float64 {
		ch := make(chan prometheus.Metric, 1000)
		c.Collect(ch)
		close(ch)
		var ms []prometheus.Metric
		for m := range ch {
			ms = append(ms, m)
		}
		v, _ := find(collectSamples(t, ms)["tsflow_port_traffic_bytes"], "port", "443")
		return v
	}
	if v := read(); v != 100 {
		t.Fatalf("first read = %v, want 100", v)
	}
	rc.Update(pairs[1:2], nil, nil, stats[1:2])
	clock = clock.Add(trafficCacheTTL - time.Second)
	if v := read(); v != 100 {
		t.Fatalf("read inside the TTL = %v, want the cached 100", v)
	}
	clock = clock.Add(2 * time.Second)
	if v := read(); v != 110 {
		t.Fatalf("read after the TTL = %v, want 110", v)
	}
}

// syntheticTailnet builds a rolling cache and device cache shaped like the
// 20k bench: about half as many active pairs per minute as devices.
func syntheticTailnet(now time.Time, devices, tags, users, ports, minutes int) (*services.RollingWindowCache, *services.DeviceCache) {
	dc := services.NewDeviceCache()
	list := make([]services.Device, devices)
	for i := range list {
		d := services.Device{
			ID:        fmt.Sprintf("n%07dCNTRL", i),
			Name:      fmt.Sprintf("host-%d.example.ts.net", i),
			User:      fmt.Sprintf("user%d@example.com", i%users),
			Addresses: []string{fmt.Sprintf("100.%d.%d.%d", 64+i/65536, (i/256)%256, i%256)},
		}
		if i%3 != 0 {
			d.User = "tagged-devices"
			d.Tags = []string{fmt.Sprintf("tag:svc%d", i%tags)}
			if i%7 == 0 {
				d.Tags = append(d.Tags, "tag:prod")
			}
		}
		list[i] = d
	}
	dc.Update(list)

	rc := services.NewRollingWindowCache(time.Hour)
	newest := now.Unix() / 60 * 60
	perMinute := devices / 2
	for m := minutes; m >= 0; m-- {
		bucket := newest - int64(m*60)
		pairs := make([]database.NodePairAggregate, 0, perMinute)
		for j := 0; j < perMinute; j++ {
			src := (j*7919 + m*31) % devices
			dst := (src + 1 + j%997) % devices
			typ := "virtual"
			if j%7 == 0 {
				typ = "subnet"
			}
			pairs = append(pairs, pair(bucket, list[src].ID, list[dst].ID, typ, int64(1000+j), int64(500+j)))
		}
		var b strings.Builder
		b.WriteString("[")
		for p := 0; p < 20; p++ {
			if p > 0 {
				b.WriteString(",")
			}
			fmt.Fprintf(&b, `{"port":%d,"proto":6,"bytes":%d}`, 1024+(p*37+m)%ports, 100000-p)
		}
		b.WriteString("]")
		rc.Update(pairs, nil, nil, []database.TrafficStats{{Bucket: bucket, TCPBytes: 5_000_000, TopPorts: b.String()}})
	}
	return rc, dc
}

// TestTrafficEndToEnd polls a fake API through a real registry and checks
// the counters and window gauges come out of the poll path.
func TestTrafficEndToEnd(t *testing.T) {
	now := time.Now()
	earlier := now.Add(-4 * time.Minute).Truncate(time.Minute).Add(10 * time.Second)
	later := now.Add(-1 * time.Minute).Truncate(time.Minute).Add(10 * time.Second)
	entry := func(at time.Time, node, src, dst string, tx int) string {
		s := at.Format(time.RFC3339)
		return fmt.Sprintf(`{"logged":"%s","start":"%s","end":"%s","nodeId":"%s","virtualTraffic":[{"proto":6,"src":"%s:50000","dst":"%s:443","txBytes":%d,"txPkts":3}]}`,
			s, s, s, node, src, dst, tx)
	}
	logs := `{"logs":[` + entry(earlier, "nA", "100.64.0.1", "100.64.0.2", 125) + "," +
		entry(later, "nA", "100.64.0.1", "100.64.0.2", 1) + `]}`
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
	defer api.Close()

	base := services.DefaultPollerConfig()
	base.PollInterval = time.Hour
	base.InitialBackfill = 20 * time.Minute
	base.CleanupInterval = time.Hour
	base.DeviceCacheRefresh = time.Hour
	registry, err := services.NewRegistry(context.Background(), []config.TailnetSpec{
		{ID: "default", Name: "example.com", APIURL: api.URL, APIKey: "k"},
	}, newStore(t), base)
	if err != nil {
		t.Fatal(err)
	}
	m := New(Options{Registry: registry, Traffic: true, TopTalkers: 1})
	m.Instrument(registry)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := registry.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(registry.Stop)

	deadline := time.Now().Add(10 * time.Second)
	var fams map[string]*dto.MetricFamily
	for {
		fams = gather(t, m)
		if got, ok := value(t, fams, "tsflow_traffic_flows_total", "tailnet", "default", "traffic_type", "virtual"); ok && got.GetCounter().GetValue() == 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("poll did not reach the traffic counters: %v", fams["tsflow_traffic_flows_total"])
		}
		time.Sleep(20 * time.Millisecond)
	}
	if got, _ := value(t, fams, "tsflow_traffic_bytes_total", "tailnet", "default", "traffic_type", "virtual"); got.GetCounter().GetValue() != 126 {
		t.Errorf("virtual bytes = %v, want 126", got.GetCounter().GetValue())
	}
	if got, _ := value(t, fams, "tsflow_traffic_packets_total", "tailnet", "default", "traffic_type", "virtual"); got.GetCounter().GetValue() != 6 {
		t.Errorf("virtual packets = %v, want 6", got.GetCounter().GetValue())
	}
	// The first scrape may have run before the poll; read past the cache.
	m.trafficWindow.mu.Lock()
	m.trafficWindow.cached = nil
	m.trafficWindow.mu.Unlock()
	fams = gather(t, m)
	if got, ok := value(t, fams, "tsflow_tag_traffic_bytes", "tailnet", "default", "tag", "tag:web", "direction", "tx"); !ok || got.GetGauge().GetValue() != 125 {
		t.Errorf("tag:web tx = %v, want 125 (the later minute is the newest and stays out)", got)
	}
	if got, ok := value(t, fams, "tsflow_tag_traffic_bytes", "tailnet", "default", "tag", "tag:db", "direction", "rx"); !ok || got.GetGauge().GetValue() != 125 {
		t.Errorf("tag:db rx = %v, want 125", got)
	}
	if got, ok := value(t, fams, "tsflow_top_talker_bytes", "tailnet", "default", "direction", "tx"); !ok || got.GetGauge().GetValue() != 125 {
		t.Errorf("top talker tx = %v, want 125", got)
	}
	if n := len(fams["tsflow_top_talker_bytes"].GetMetric()); n != 2 {
		t.Errorf("top talker series = %d, want 1 device x 2 directions", n)
	}
}
