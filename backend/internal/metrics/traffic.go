package metrics

import (
	"encoding/json"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/rajsinghtech/tsflow/backend/internal/database"
	"github.com/rajsinghtech/tsflow/backend/internal/services"
)

// Traffic metrics come in two kinds.
//
// Totals by traffic type and protocol are counters, added once per committed
// poll from the aggregates the poller already built. That is a loop over the
// poll's node pairs and nothing else.
//
// Breakdowns by tag, user, port and device are gauges over the last
// trafficWindow of ingested data. They are computed on scrape from the
// in-memory rolling cache, at most once per trafficCacheTTL, and folded to
// the top trafficTopN values plus "other" so the series count stays the same
// at 20 devices or 20,000.
const (
	trafficWindow   = 15 * time.Minute
	trafficCacheTTL = 30 * time.Second
	trafficTopN     = 20
)

var trafficTypes = []string{"virtual", "subnet", "exit", "physical"}

func trafficTypeIndex(t string) int {
	for i, tt := range trafficTypes {
		if t == tt {
			return i
		}
	}
	return -1
}

// trafficCounters holds one tailnet's pre-resolved counter children, so the
// ingest path does no label lookups.
type trafficCounters struct {
	bytes, packets, flows [4]prometheus.Counter
	tcp, udp, other       prometheus.Counter
}

func (m *Metrics) newTrafficCounters() {
	m.trafficBytes = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace, Name: "traffic_bytes_total",
		Help: "Bytes in ingested flow logs by tailnet and traffic type, counted when ingested. Each byte is counted once, by its sender.",
	}, []string{"tailnet", "traffic_type"})
	m.trafficPackets = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace, Name: "traffic_packets_total",
		Help: "Packets in ingested flow logs by tailnet and traffic type, counted when ingested.",
	}, []string{"tailnet", "traffic_type"})
	m.trafficFlows = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace, Name: "traffic_flows_total",
		Help: "Flow records in ingested flow logs by tailnet and traffic type.",
	}, []string{"tailnet", "traffic_type"})
	m.trafficProtocol = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace, Name: "traffic_protocol_bytes_total",
		Help: "Bytes by tailnet and IP protocol (tcp, udp, other), not counting physical transport.",
	}, []string{"tailnet", "protocol"})
	m.reg.MustRegister(m.trafficBytes, m.trafficPackets, m.trafficFlows, m.trafficProtocol)
}

func (m *Metrics) trafficObserver(tailnet string) func([]database.NodePairAggregate, []database.TrafficStats) {
	var c trafficCounters
	for i, tt := range trafficTypes {
		c.bytes[i] = m.trafficBytes.WithLabelValues(tailnet, tt)
		c.packets[i] = m.trafficPackets.WithLabelValues(tailnet, tt)
		c.flows[i] = m.trafficFlows.WithLabelValues(tailnet, tt)
	}
	c.tcp = m.trafficProtocol.WithLabelValues(tailnet, "tcp")
	c.udp = m.trafficProtocol.WithLabelValues(tailnet, "udp")
	c.other = m.trafficProtocol.WithLabelValues(tailnet, "other")
	return c.observe
}

func (c *trafficCounters) observe(pairs []database.NodePairAggregate, stats []database.TrafficStats) {
	var bytes, packets, flows [4]int64
	for i := range pairs {
		np := &pairs[i]
		t := trafficTypeIndex(np.TrafficType)
		if t < 0 {
			continue
		}
		bytes[t] += np.TxBytes + np.RxBytes
		packets[t] += np.TxPkts + np.RxPkts
		flows[t] += np.FlowCount
	}
	for i := range trafficTypes {
		addNonNegative(c.bytes[i], bytes[i])
		addNonNegative(c.packets[i], packets[i])
		addNonNegative(c.flows[i], flows[i])
	}
	var tcp, udp, other int64
	for i := range stats {
		tcp += stats[i].TCPBytes
		udp += stats[i].UDPBytes
		other += stats[i].OtherProtoBytes
	}
	addNonNegative(c.tcp, tcp)
	addNonNegative(c.udp, udp)
	addNonNegative(c.other, other)
}

func addNonNegative(c prometheus.Counter, v int64) {
	if v > 0 {
		c.Add(float64(v))
	}
}

var (
	windowSecondsDesc = prometheus.NewDesc(namespace+"_traffic_window_seconds",
		"Seconds of data behind the traffic window gauges. Lower than the window after a restart until the cache fills.",
		[]string{"tailnet"}, nil)
	windowEndDesc = prometheus.NewDesc(namespace+"_traffic_window_end_timestamp_seconds",
		"Unix time the traffic window ends: the start of the newest minute, which is still filling and not included.",
		[]string{"tailnet"}, nil)
	tagBytesDesc = prometheus.NewDesc(namespace+"_tag_traffic_bytes",
		"Bytes devices with a tag sent (tx) or received (rx) in the traffic window. Top 20 tags, then other. A device with two tags counts in both.",
		[]string{"tailnet", "tag", "direction"}, nil)
	tagPacketsDesc = prometheus.NewDesc(namespace+"_tag_traffic_packets",
		"Packets devices with a tag sent or received in the traffic window.",
		[]string{"tailnet", "tag", "direction"}, nil)
	userBytesDesc = prometheus.NewDesc(namespace+"_user_traffic_bytes",
		"Bytes a user's devices sent (tx) or received (rx) in the traffic window. Top 20 users, then other.",
		[]string{"tailnet", "user", "direction"}, nil)
	userPacketsDesc = prometheus.NewDesc(namespace+"_user_traffic_packets",
		"Packets a user's devices sent or received in the traffic window.",
		[]string{"tailnet", "user", "direction"}, nil)
	portBytesDesc = prometheus.NewDesc(namespace+"_port_traffic_bytes",
		"Bytes by service port in the traffic window. Top 20 ports, then port=\"other\".",
		[]string{"tailnet", "protocol", "port"}, nil)
	talkerBytesDesc = prometheus.NewDesc(namespace+"_top_talker_bytes",
		"Bytes the top devices sent (tx) or received (rx) in the traffic window. Only with TSFLOW_METRICS_TOP_TALKERS.",
		[]string{"tailnet", "node", "node_id", "direction"}, nil)
	trafficComputeDesc = prometheus.NewDesc(namespace+"_traffic_window_compute_seconds",
		"Time the last traffic window computation took.", nil, nil)
)

// trafficCollector computes the window gauges from the rolling caches and
// keeps the result for trafficCacheTTL, so frequent or parallel scrapes do
// the work once.
type trafficCollector struct {
	sources    func() []trafficSource
	topTalkers int
	breakdowns bool
	now        func() time.Time

	mu       sync.Mutex
	computed time.Time
	cached   []prometheus.Metric
}

func (c *trafficCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- trafficComputeDesc
	if c.breakdowns {
		for _, d := range []*prometheus.Desc{windowSecondsDesc, windowEndDesc, tagBytesDesc, tagPacketsDesc,
			userBytesDesc, userPacketsDesc, portBytesDesc} {
			ch <- d
		}
	}
	if c.topTalkers > 0 {
		ch <- talkerBytesDesc
		if !c.breakdowns {
			ch <- windowSecondsDesc
			ch <- windowEndDesc
		}
	}
}

func (c *trafficCollector) Collect(ch chan<- prometheus.Metric) {
	c.mu.Lock()
	now := c.now()
	if c.cached == nil || now.Sub(c.computed) >= trafficCacheTTL {
		started := time.Now()
		out := c.compute()
		out = append(out, prometheus.MustNewConstMetric(trafficComputeDesc, prometheus.GaugeValue,
			time.Since(started).Seconds()))
		c.cached, c.computed = out, now
	}
	cached := c.cached
	c.mu.Unlock()
	for _, metric := range cached {
		ch <- metric
	}
}

// nodeTotals is one device's sent and received traffic in the window.
type nodeTotals struct {
	txBytes, rxBytes, txPkts, rxPkts int64
}

func (n *nodeTotals) add(o nodeTotals) {
	n.txBytes += o.txBytes
	n.rxBytes += o.rxBytes
	n.txPkts += o.txPkts
	n.rxPkts += o.rxPkts
}

// trafficSource is one tailnet's caches.
type trafficSource struct {
	tailnet string
	rolling *services.RollingWindowCache
	devices *services.DeviceCache
}

func registrySources(reg *services.Registry) func() []trafficSource {
	return func() []trafficSource {
		var out []trafficSource
		for _, entry := range reg.List() {
			if entry == nil || entry.Poller == nil {
				continue
			}
			out = append(out, trafficSource{entry.ID, entry.Poller.GetRollingCache(), entry.Poller.GetDeviceCache()})
		}
		return out
	}
}

func (c *trafficCollector) compute() []prometheus.Metric {
	var out []prometheus.Metric
	if c.sources == nil {
		return out
	}
	for _, src := range c.sources() {
		out = c.computeTailnet(out, src.tailnet, src.rolling, src.devices)
	}
	return out
}

func (c *trafficCollector) computeTailnet(out []prometheus.Metric, tailnet string, rc *services.RollingWindowCache, dc *services.DeviceCache) []prometheus.Metric {
	if rc == nil {
		return out
	}
	newest, ok := rc.NewestBucket()
	if !ok {
		return out
	}
	// The newest minute is usually still being written by later polls.
	end := newest
	start := end - int64(trafficWindow/time.Second)

	nodes := make(map[string]*nodeTotals)
	node := func(id string) *nodeTotals {
		n := nodes[id]
		if n == nil {
			n = &nodeTotals{}
			nodes[id] = n
		}
		return n
	}
	ports := make(map[portKey]int64)
	var protoTotal int64
	visitor := services.WindowVisitor{
		NodePair: func(np *database.NodePairAggregate) {
			// Physical rows carry the same bytes as the tunneled traffic.
			if np.TrafficType == "physical" {
				return
			}
			src, dst := node(np.SrcNodeID), node(np.DstNodeID)
			src.txBytes += np.TxBytes
			src.txPkts += np.TxPkts
			dst.rxBytes += np.TxBytes
			dst.rxPkts += np.TxPkts
			dst.txBytes += np.RxBytes
			dst.txPkts += np.RxPkts
			src.rxBytes += np.RxBytes
			src.rxPkts += np.RxPkts
		},
	}
	if c.breakdowns {
		visitor.TrafficStats = func(ts *database.TrafficStats) {
			protoTotal += ts.TCPBytes + ts.UDPBytes + ts.OtherProtoBytes
			addTopPorts(ports, ts.TopPorts)
		}
	}
	buckets := rc.VisitWindow(start, end, visitor)

	out = append(out,
		prometheus.MustNewConstMetric(windowSecondsDesc, prometheus.GaugeValue, float64(buckets*60), tailnet),
		prometheus.MustNewConstMetric(windowEndDesc, prometheus.GaugeValue, float64(end), tailnet),
	)

	type identity struct {
		name, user string
		tags       []string
		known      bool
	}
	ident := func(id string) identity {
		if dc == nil {
			return identity{name: id}
		}
		d := dc.GetDevice(id)
		if d == nil {
			d = dc.GetDeviceByIP(id)
		}
		if d == nil {
			return identity{name: id}
		}
		name := d.Name
		if name == "" {
			name = d.Hostname
		}
		if name == "" {
			name = id
		}
		return identity{name: name, user: d.Owner, tags: d.Tags, known: true}
	}

	if c.breakdowns {
		tags := make(map[string]*nodeTotals)
		users := make(map[string]*nodeTotals)
		bump := func(m map[string]*nodeTotals, key string, n *nodeTotals) {
			t := m[key]
			if t == nil {
				t = &nodeTotals{}
				m[key] = t
			}
			t.add(*n)
		}
		for id, n := range nodes {
			who := ident(id)
			switch {
			case !who.known:
				bump(tags, "unknown", n)
				bump(users, "unknown", n)
				continue
			case len(who.tags) == 0:
				bump(tags, "untagged", n)
			default:
				for _, tag := range who.tags {
					bump(tags, tag, n)
				}
			}
			user := who.user
			if user == "" {
				user = "unknown"
			}
			bump(users, user, n)
		}
		out = appendDirectional(out, tagBytesDesc, tagPacketsDesc, tailnet, foldTop(tags, "untagged", "unknown"))
		out = appendDirectional(out, userBytesDesc, userPacketsDesc, tailnet, foldTop(users, "unknown"))
		out = appendPorts(out, tailnet, ports, protoTotal)
	}

	if c.topTalkers > 0 {
		ranked := make([]rankedTotals, 0, len(nodes))
		for id, n := range nodes {
			ranked = append(ranked, rankedTotals{key: id, totals: *n})
		}
		sortRanked(ranked)
		if len(ranked) > c.topTalkers {
			ranked = ranked[:c.topTalkers]
		}
		for _, r := range ranked {
			who := ident(r.key)
			out = append(out,
				prometheus.MustNewConstMetric(talkerBytesDesc, prometheus.GaugeValue, float64(r.totals.txBytes), tailnet, who.name, r.key, "tx"),
				prometheus.MustNewConstMetric(talkerBytesDesc, prometheus.GaugeValue, float64(r.totals.rxBytes), tailnet, who.name, r.key, "rx"),
			)
		}
	}
	return out
}

type rankedTotals struct {
	key    string
	totals nodeTotals
}

func sortRanked(r []rankedTotals) {
	sort.Slice(r, func(i, j int) bool {
		a := r[i].totals.txBytes + r[i].totals.rxBytes
		b := r[j].totals.txBytes + r[j].totals.rxBytes
		if a != b {
			return a > b
		}
		return r[i].key < r[j].key
	})
}

// foldTop keeps the trafficTopN largest keys and sums the rest into
// "other". Keys in always are kept outside the ranking, so "unknown" does
// not push out a real tag or disappear into other.
func foldTop(m map[string]*nodeTotals, always ...string) []rankedTotals {
	var kept, ranked []rankedTotals
	for key, t := range m {
		r := rankedTotals{key: key, totals: *t}
		if contains(always, key) {
			kept = append(kept, r)
			continue
		}
		ranked = append(ranked, r)
	}
	sortRanked(ranked)
	if len(ranked) > trafficTopN {
		var other nodeTotals
		for _, r := range ranked[trafficTopN:] {
			other.add(r.totals)
		}
		ranked = append(ranked[:trafficTopN], rankedTotals{key: "other", totals: other})
	}
	sortRanked(kept)
	return append(ranked, kept...)
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func appendDirectional(out []prometheus.Metric, bytesDesc, pktsDesc *prometheus.Desc, tailnet string, rows []rankedTotals) []prometheus.Metric {
	for _, r := range rows {
		out = append(out,
			prometheus.MustNewConstMetric(bytesDesc, prometheus.GaugeValue, float64(r.totals.txBytes), tailnet, r.key, "tx"),
			prometheus.MustNewConstMetric(bytesDesc, prometheus.GaugeValue, float64(r.totals.rxBytes), tailnet, r.key, "rx"),
			prometheus.MustNewConstMetric(pktsDesc, prometheus.GaugeValue, float64(r.totals.txPkts), tailnet, r.key, "tx"),
			prometheus.MustNewConstMetric(pktsDesc, prometheus.GaugeValue, float64(r.totals.rxPkts), tailnet, r.key, "rx"),
		)
	}
	return out
}

type portKey struct {
	proto, port int
}

// addTopPorts adds one minute's network-wide top ports, stored as
// [{"port":443,"proto":6,"bytes":123}].
func addTopPorts(ports map[portKey]int64, raw string) {
	if raw == "" || raw == "[]" {
		return
	}
	var entries []struct {
		Port  int   `json:"port"`
		Proto int   `json:"proto"`
		Bytes int64 `json:"bytes"`
	}
	if json.Unmarshal([]byte(raw), &entries) != nil {
		return
	}
	for _, e := range entries {
		if e.Port > 0 && e.Bytes > 0 {
			ports[portKey{proto: e.Proto, port: e.Port}] += e.Bytes
		}
	}
}

func appendPorts(out []prometheus.Metric, tailnet string, ports map[portKey]int64, total int64) []prometheus.Metric {
	type row struct {
		key   portKey
		bytes int64
	}
	rows := make([]row, 0, len(ports))
	for k, b := range ports {
		rows = append(rows, row{k, b})
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].bytes != rows[j].bytes {
			return rows[i].bytes > rows[j].bytes
		}
		if rows[i].key.proto != rows[j].key.proto {
			return rows[i].key.proto < rows[j].key.proto
		}
		return rows[i].key.port < rows[j].key.port
	})
	if len(rows) > trafficTopN {
		rows = rows[:trafficTopN]
	}
	var listed int64
	for _, r := range rows {
		listed += r.bytes
		out = append(out, prometheus.MustNewConstMetric(portBytesDesc, prometheus.GaugeValue, float64(r.bytes),
			tailnet, protocolName(r.key.proto), strconv.Itoa(r.key.port)))
	}
	// Each minute stores only its top ports, so the rest of the bytes, ICMP
	// included, are known only as a remainder.
	if rest := total - listed; rest > 0 {
		out = append(out, prometheus.MustNewConstMetric(portBytesDesc, prometheus.GaugeValue, float64(rest),
			tailnet, "other", "other"))
	}
	return out
}

func protocolName(p int) string {
	switch p {
	case 6:
		return "tcp"
	case 17:
		return "udp"
	case 1:
		return "icmp"
	case 58:
		return "ipv6-icmp"
	case 132:
		return "sctp"
	}
	return strconv.Itoa(p)
}
