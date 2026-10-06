package database

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"testing"
	"time"
)

// BenchmarkQueryDuringIngest measures a query on one tailnet while another
// tailnet is committing 20,000 node pairs.
//
// Run with -benchtime=3x. ns/op is the query. The log line records whether
// that query returned before the ingest finished.
func BenchmarkQueryDuringIngest(b *testing.B) {
	if testing.Short() {
		b.Skip("skipping ingest concurrency benchmark in short mode")
	}
	const (
		nodes    = 20000
		base     = int64(1_700_000_040)
		tailnetA = "alpha"
		tailnetB = "beta"
		sentinel = int64(42)
	)
	ctx := context.Background()
	store := setupBenchDB(b)
	preload := nodesPoll(nodes, base, 10, sentinel)
	for _, id := range []string{tailnetA, tailnetB} {
		started := time.Now()
		if err := store.CommitPollResults(ctx, id, preload); err != nil {
			b.Fatal(err)
		}
		b.Logf("preloaded %s with %d nodes in %s", id, nodes, time.Since(started).Round(time.Millisecond))
	}
	start := time.Unix(base, 0).UTC()
	end := start.Add(time.Minute)
	for _, name := range []string{"node-pairs", "traffic-stats"} {
		b.Run(name, func(b *testing.B) {
			measureQueryDuringIngest(b, store, name, tailnetA, tailnetB, start, end, base, nodes, sentinel)
		})
	}
}

func measureQueryDuringIngest(b *testing.B, store *SQLiteStore, query, tailnetA, tailnetB string, start, end time.Time, base int64, nodes int, sentinel int64) {
	b.Helper()
	ctx := context.Background()
	latencies := make([]time.Duration, 0, b.N)
	ingestLatencies := make([]time.Duration, 0, b.N)
	before := 0

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		bucket := base + int64((i+1)*60)
		// Keep each sub-benchmark on its own minute so the two queries do not
		// ingest the same bucket if a previous sub-benchmark already did.
		if query == "traffic-stats" {
			bucket += 10 * 60
		}
		ingest := nodesPoll(nodes, bucket, 1, 1)
		started := make(chan struct{})
		var once sync.Once
		store.writeStarted = func(id string) {
			if id != tailnetA {
				return
			}
			once.Do(func() { close(started) })
		}
		type done struct {
			err error
			at  time.Time
		}
		ingestDone := make(chan done, 1)
		ingestStart := time.Now()
		go func() {
			err := store.CommitPollResults(ctx, tailnetA, ingest)
			ingestDone <- done{err, time.Now()}
		}()
		select {
		case <-started:
		case res := <-ingestDone:
			b.Fatalf("ingest finished before it signaled start: %v", res.err)
		case <-time.After(2 * time.Minute):
			b.Fatal("timed out waiting for ingest to start")
		}

		b.StartTimer()
		t0 := time.Now()
		var (
			err   error
			pairs int
			tcp   int64
		)
		switch query {
		case "node-pairs":
			rows, qerr := store.GetNodePairAggregates(ctx, tailnetB, start, end)
			err = qerr
			pairs = len(rows)
		case "traffic-stats":
			stats, qerr := store.GetTrafficStats(ctx, tailnetB, start, end)
			err = qerr
			pairs = len(stats)
			if qerr == nil && len(stats) == 1 {
				tcp = stats[0].TCPBytes
			}
		default:
			b.Fatalf("unknown query %s", query)
		}
		latency := time.Since(t0)
		finished := time.Now()
		b.StopTimer()

		res := <-ingestDone
		if res.err != nil {
			b.Fatalf("ingest: %v", res.err)
		}
		if err != nil {
			b.Fatal(err)
		}
		if query == "node-pairs" && pairs != nodes {
			b.Fatalf("node-pairs = %d, want %d", pairs, nodes)
		}
		if query == "traffic-stats" && (pairs != 1 || tcp != sentinel) {
			b.Fatalf("traffic-stats buckets=%d tcp=%d, want 1 and %d", pairs, tcp, sentinel)
		}
		latencies = append(latencies, latency)
		ingestLatencies = append(ingestLatencies, res.at.Sub(ingestStart))
		if finished.Before(res.at) {
			before++
		}
	}
	store.writeStarted = nil

	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
	median := latencies[len(latencies)/2]
	lastIngest := ingestLatencies[len(ingestLatencies)-1]
	b.Logf("median %s over %d runs (%s), finished before ingest %d/%d, last ingest %s",
		median.Round(time.Millisecond), len(latencies), formatDurations(latencies), before, len(latencies), lastIngest.Round(time.Millisecond))
	b.ReportMetric(float64(median)/float64(time.Millisecond), "median-ms")
}

func formatDurations(durations []time.Duration) string {
	parts := make([]string, len(durations))
	for i, d := range durations {
		parts[i] = d.Round(time.Millisecond).String()
	}
	return fmt.Sprint(parts)
}

func nodesPoll(nodes int, bucket, txBytes, tcpBytes int64) PollResults {
	pairs := make([]NodePairAggregate, nodes)
	for i := 0; i < nodes; i++ {
		src := fmt.Sprintf("n%05d", i)
		dst := fmt.Sprintf("n%05d", (i+1)%nodes)
		pairs[i] = NodePairAggregate{
			Bucket:           bucket,
			SrcNodeID:        src,
			DstNodeID:        dst,
			TrafficType:      "virtual",
			TxBytes:          txBytes,
			RxBytes:          1,
			TxPkts:           1,
			RxPkts:           1,
			FlowCount:        1,
			Protocols:        "[6]",
			ProtocolBytes:    `{"6":1}`,
			Ports:            `[{"port":443,"proto":6,"bytes":1}]`,
			TxPorts:          `[{"port":443,"proto":6,"bytes":1}]`,
			RxPorts:          "[]",
			TxProtocolBytes:  `{"6":1}`,
			RxProtocolBytes:  "{}",
			DirectionalPorts: true,
		}
	}
	return PollResults{
		PollEnd:   time.Unix(bucket+60, 0).UTC(),
		NodePairs: pairs,
		TrafficStats: []TrafficStats{{
			Bucket:       bucket,
			TCPBytes:     tcpBytes,
			VirtualBytes: tcpBytes,
			TotalFlows:   int64(nodes),
			UniquePairs:  int64(nodes),
			TopPorts:     `[{"port":443,"proto":6,"bytes":1}]`,
		}},
	}
}
