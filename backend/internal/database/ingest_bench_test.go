package database

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"strconv"
	"testing"
	"time"
)

// Ingest benchmark at production scale, using synthetic data only.
//
// It builds object-sized batches (one per five minutes) of node pair rows
// with realistic port and protocol columns, commits them through
// CommitObjectIngest the way the object-store poller does, then moves the
// cursor with UpdatePollState so the hourly rollup runs, and finally runs
// retention. Each phase is timed. It is skipped unless TSFLOW_INGEST_BENCH=1.
//
// Settings (environment, all optional):
//
//	TSFLOW_INGEST_BENCH_NODES     distinct nodes             (default 5000)
//	TSFLOW_INGEST_BENCH_PAIRS     pair rows per minute       (default 40000)
//	TSFLOW_INGEST_BENCH_FILES     five-minute objects        (default 24, two hours)
//	TSFLOW_INGEST_BENCH_OVERLAP   share of rows whose minute and pair
//	                              already exist from the previous object,
//	                              0 to 1                      (default 0.15)
//	TSFLOW_INGEST_BENCH_DIR       directory for the database (default temp)
//
// Run, with CPU and heap profiles:
//
//	TSFLOW_INGEST_BENCH=1 go test ./internal/database -run TestIngestProdScale \
//	  -count=1 -v -timeout 60m -cpuprofile cpu.out -memprofile mem.out
//
// A default run holds 200,000 pair rows per object, about the size of one
// 1.6 million flow object after aggregation.
func TestIngestProdScale(t *testing.T) {
	if os.Getenv("TSFLOW_INGEST_BENCH") != "1" {
		t.Skip("set TSFLOW_INGEST_BENCH=1 to run the ingest scale benchmark")
	}
	nodes := benchEnvInt("TSFLOW_INGEST_BENCH_NODES", 5000)
	pairsPerMinute := benchEnvInt("TSFLOW_INGEST_BENCH_PAIRS", 40000)
	files := benchEnvInt("TSFLOW_INGEST_BENCH_FILES", 24)
	overlap := benchEnvFloat("TSFLOW_INGEST_BENCH_OVERLAP", 0.15)

	dir := os.Getenv("TSFLOW_INGEST_BENCH_DIR")
	if dir == "" {
		dir = t.TempDir()
	}
	dbPath := dir + "/ingest-bench.db"
	os.Remove(dbPath)
	store, err := NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := store.Init(ctx); err != nil {
		t.Fatal(err)
	}

	var began time.Time
	var sinceBegin, longestWrite time.Duration
	store.writeStarted = func(string) { began = time.Now() }
	store.beforeCommit = func(string) {
		sinceBegin = time.Since(began)
		// The longest a single write transaction held the writer before its
		// commit: while it runs, every other write waits.
		if sinceBegin > longestWrite {
			longestWrite = sinceBegin
		}
	}

	gen := newIngestGenerator(1, nodes)
	// Start in the past so retention cleanup has something to remove.
	const tailnet = "bench"
	base := (time.Now().UTC().Add(-time.Duration(files)*5*time.Minute).Unix() / 300) * 300

	var totalRows, totalFlows int64
	var upsertSum, commitSum, objectSum time.Duration
	var worstObject time.Duration
	var rollupSum time.Duration
	prev := []NodePairAggregate(nil)
	wallStart := time.Now()
	for f := 0; f < files; f++ {
		start := base + int64(f)*300
		rows := gen.objectRows(start, pairsPerMinute, overlap, prev)
		prev = rows
		var flows int
		for _, r := range rows {
			flows += int(r.FlowCount)
		}
		result := ObjectIngestResult{
			Key:           fmt.Sprintf("bench/%d.json.gz", start),
			LastModified:  time.Unix(start+300, 0).UTC(),
			Size:          int64(len(rows)) * 40,
			FlowCount:     flows,
			NodeMetadata:  gen.metadata(),
			NodePairs:     rows,
			Bandwidth:     gen.bandwidth(start),
			NodeBandwidth: gen.nodeBandwidth(start),
			TrafficStats:  gen.trafficStats(start),
		}
		t0 := time.Now()
		if err := store.CommitObjectIngest(ctx, tailnet, result); err != nil {
			t.Fatal(err)
		}
		d := time.Since(t0)
		objectSum += d
		upsertSum += sinceBegin
		commitSum += d - sinceBegin
		if d > worstObject {
			worstObject = d
		}
		totalRows += int64(len(rows))
		totalFlows += int64(flows)

		// The poller moves the cursor once per batch. Do it every three
		// objects, a typical catch-up batch.
		if f%3 == 2 || f == files-1 {
			t1 := time.Now()
			if err := store.UpdatePollState(ctx, tailnet, time.Unix(start+300, 0).UTC()); err != nil {
				t.Fatal(err)
			}
			rollupSum += time.Since(t1)
		}
		if f%6 == 5 {
			t.Logf("object %d/%d: last=%s rows=%d", f+1, files, d.Round(time.Millisecond), len(rows))
		}
	}
	wall := time.Since(wallStart)

	// Retention: keep the newest hour, drop the rest.
	t2 := time.Now()
	deleted, err := store.Cleanup(ctx, tailnet, 55*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	cleanup := time.Since(t2)

	fi, _ := os.Stat(dbPath)
	wal, _ := os.Stat(dbPath + "-wal")
	var walSize int64
	if wal != nil {
		walSize = wal.Size()
	}
	res := map[string]any{
		"nodes": nodes, "pairs_per_minute": pairsPerMinute, "objects": files, "overlap": overlap,
		"pair_rows": totalRows, "flows_represented": totalFlows,
		"object_commit_total_s":   objectSum.Seconds(),
		"object_commit_avg_s":     objectSum.Seconds() / float64(files),
		"object_commit_worst_s":   worstObject.Seconds(),
		"upsert_avg_s":            upsertSum.Seconds() / float64(files),
		"commit_avg_s":            commitSum.Seconds() / float64(files),
		"update_poll_state_total": rollupSum.Seconds(),
		"longest_write_tx_s":      longestWrite.Seconds(),
		"cleanup_s":               cleanup.Seconds(),
		"cleanup_deleted_rows":    deleted,
		"pair_rows_per_s":         float64(totalRows) / objectSum.Seconds(),
		"flows_per_s":             float64(totalFlows) / objectSum.Seconds(),
		"wall_s":                  wall.Seconds(),
		"db_mb":                   float64(fi.Size()) / 1e6,
		"wal_mb":                  float64(walSize) / 1e6,
	}
	out, _ := json.MarshalIndent(res, "", "  ")
	t.Logf("RESULT\n%s", out)
	if p := os.Getenv("TSFLOW_INGEST_BENCH_OUT"); p != "" {
		os.WriteFile(p, out, 0o644)
	}
}

func benchEnvInt(name string, def int) int {
	if v, err := strconv.Atoi(os.Getenv(name)); err == nil && v > 0 {
		return v
	}
	return def
}

func benchEnvFloat(name string, def float64) float64 {
	if v, err := strconv.ParseFloat(os.Getenv(name), 64); err == nil && v >= 0 {
		return v
	}
	return def
}

type ingestGenerator struct {
	rng   *rand.Rand
	nodes []string
	ports []int
}

func newIngestGenerator(seed int64, nodeCount int) *ingestGenerator {
	g := &ingestGenerator{rng: rand.New(rand.NewSource(seed))}
	for i := 0; i < nodeCount; i++ {
		g.nodes = append(g.nodes, fmt.Sprintf("n%06dCNTRL", i))
	}
	// Common service ports plus a long tail of ephemeral-looking ports.
	g.ports = []int{22, 53, 80, 123, 443, 3306, 5432, 6379, 8080, 8443, 9090, 9100, 50051}
	for i := 0; i < 400; i++ {
		g.ports = append(g.ports, 10000+i*37)
	}
	return g
}

func (g *ingestGenerator) pickNode() string {
	// Skewed: a few busy nodes, many quiet ones.
	idx := int(float64(len(g.nodes)) * g.rng.Float64() * g.rng.Float64())
	return g.nodes[idx]
}

type benchPort struct {
	Port  int   `json:"port"`
	Proto int   `json:"proto"`
	Bytes int64 `json:"bytes"`
}

func (g *ingestGenerator) portList(maxN int) (string, map[int]int64) {
	n := 1 + g.rng.Intn(maxN)
	list := make([]benchPort, 0, n)
	seen := map[int]bool{}
	protoBytes := map[int]int64{}
	for len(list) < n {
		p := g.ports[g.rng.Intn(len(g.ports))]
		if seen[p] {
			continue
		}
		seen[p] = true
		proto := 6
		if g.rng.Intn(5) == 0 {
			proto = 17
		}
		b := int64(1000 + g.rng.Intn(5_000_000))
		list = append(list, benchPort{Port: p, Proto: proto, Bytes: b})
		protoBytes[proto] += b
	}
	// Largest first, like the real aggregator.
	for i := 1; i < len(list); i++ {
		for j := i; j > 0 && list[j].Bytes > list[j-1].Bytes; j-- {
			list[j], list[j-1] = list[j-1], list[j]
		}
	}
	b, _ := json.Marshal(list)
	return string(b), protoBytes
}

func protoMapJSON(m map[int]int64) string {
	b, _ := json.Marshal(m)
	return string(b)
}

func protoListJSON(m map[int]int64) string {
	out := []int{}
	for p := range m {
		out = append(out, p)
	}
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j] < out[j-1]; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	b, _ := json.Marshal(out)
	return string(b)
}

func (g *ingestGenerator) row(bucket int64, src, dst string) NodePairAggregate {
	ports, pb := g.portList(8)
	txPorts, txp := g.portList(5)
	rxPorts, rxp := g.portList(5)
	var total int64
	for _, v := range pb {
		total += v
	}
	traffic := "virtual"
	if r := g.rng.Intn(20); r == 0 {
		traffic = "physical"
	} else if r == 1 {
		traffic = "subnet"
	}
	if src > dst {
		src, dst = dst, src
	}
	return NodePairAggregate{
		Bucket: bucket, SrcNodeID: src, DstNodeID: dst, TrafficType: traffic,
		TxBytes: total / 2, RxBytes: total - total/2,
		TxPkts: total / 1400, RxPkts: total / 1500,
		FlowCount: int64(1 + g.rng.Intn(15)),
		Protocols: protoListJSON(pb), ProtocolBytes: protoMapJSON(pb),
		Ports: ports, TxPorts: txPorts, RxPorts: rxPorts,
		TxProtocolBytes: protoMapJSON(txp), RxProtocolBytes: protoMapJSON(rxp),
		DirectionalPorts: true,
	}
}

// objectRows builds five minutes of pair rows. A share of the first minute
// repeats pairs from the end of the previous object, which is what a pair
// that spans two objects looks like to the store.
func (g *ingestGenerator) objectRows(start int64, perMinute int, overlap float64, prev []NodePairAggregate) []NodePairAggregate {
	rows := make([]NodePairAggregate, 0, perMinute*5)
	seen := make(map[[4]string]bool, perMinute*5)
	add := func(r NodePairAggregate) {
		k := [4]string{strconv.FormatInt(r.Bucket, 10), r.SrcNodeID, r.DstNodeID, r.TrafficType}
		if seen[k] {
			return
		}
		seen[k] = true
		rows = append(rows, r)
	}
	for m := 0; m < 5; m++ {
		bucket := start + int64(m)*60
		for len(rows) < (m+1)*perMinute {
			if m == 0 && len(prev) > 0 && g.rng.Float64() < overlap {
				// Re-hit a row from the previous object's last minute.
				p := prev[len(prev)-1-g.rng.Intn(perMinute)]
				r := g.row(bucket, p.SrcNodeID, p.DstNodeID)
				r.TrafficType = p.TrafficType
				add(r)
				continue
			}
			add(g.row(bucket, g.pickNode(), g.pickNode()))
		}
	}
	return rows
}

func (g *ingestGenerator) metadata() []NodeMetadata {
	out := make([]NodeMetadata, 0, len(g.nodes))
	now := time.Now().UTC()
	for i, n := range g.nodes {
		out = append(out, NodeMetadata{
			NodeID: n, Name: fmt.Sprintf("host-%d.example.ts.net", i), Hostname: fmt.Sprintf("host-%d", i),
			Owner: fmt.Sprintf("user%d@example.com", i%300), IPs: []string{fmt.Sprintf("100.64.%d.%d", i/250, i%250+1)},
			Tags: []string{"tag:bench"}, Updated: now,
		})
	}
	return out
}

func (g *ingestGenerator) bandwidth(start int64) []BandwidthBucket {
	out := make([]BandwidthBucket, 0, 5)
	for m := 0; m < 5; m++ {
		out = append(out, BandwidthBucket{Time: time.Unix(start+int64(m)*60, 0).UTC(), TxBytes: 1 << 30, RxBytes: 1 << 30})
	}
	return out
}

func (g *ingestGenerator) nodeBandwidth(start int64) []NodeBandwidth {
	out := make([]NodeBandwidth, 0, len(g.nodes)*5)
	for m := 0; m < 5; m++ {
		for _, n := range g.nodes {
			out = append(out, NodeBandwidth{Bucket: start + int64(m)*60, NodeID: n, TxBytes: 1 << 20, RxBytes: 1 << 20})
		}
	}
	return out
}

func (g *ingestGenerator) trafficStats(start int64) []TrafficStats {
	out := make([]TrafficStats, 0, 5)
	ports, _ := g.portList(20)
	for m := 0; m < 5; m++ {
		out = append(out, TrafficStats{Bucket: start + int64(m)*60, TCPBytes: 1 << 30, UDPBytes: 1 << 28, VirtualBytes: 1 << 30,
			TotalFlows: 300000, UniquePairs: 40000, TopPorts: ports})
	}
	return out
}
