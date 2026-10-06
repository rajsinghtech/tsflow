package database

import (
	"context"
	"fmt"
	"reflect"
	"sort"
	"testing"
	"time"
)

// BenchmarkHourRollupScale compares minute scans with hourly rollups at
// 20,000 nodes. Windows are hour-aligned, one peer per node, one virtual
// flow per minute. It is skipped under -short.
func BenchmarkHourRollupScale(b *testing.B) {
	if testing.Short() {
		b.Skip("skipping hourly rollup scale benchmark in short mode")
	}
	const nodes = 20000
	const base int64 = 1_699_999_200
	for _, window := range []struct {
		name    string
		minutes int
	}{
		{name: "1h", minutes: 60},
		{name: "24h", minutes: 24 * 60},
		{name: "7d", minutes: 7 * 24 * 60},
	} {
		window := window
		b.Run(window.name, func(b *testing.B) {
			ctx := context.Background()
			store := setupBenchDB(b)
			loadStart := time.Now()
			inserted := insertScalePairs(b, store, nodes, window.minutes, base)
			b.Logf("loaded %d minute rows in %s", inserted, time.Since(loadStart).Round(time.Millisecond))
			if _, err := store.db.ExecContext(ctx, "ANALYZE"); err != nil {
				b.Fatal(err)
			}
			start := time.Unix(base, 0).UTC()
			end := time.Unix(base+int64(window.minutes)*60, 0).UTC()
			// A week of minute rows is large enough that three samples would
			// dominate the run. One sample is enough there; shorter windows
			// keep a median of three.
			samples := 3
			if window.minutes > 24*60 {
				samples = 1
			}

			var graphBefore []NodePairAggregate
			beforeGraph := medianQuery(b, samples, "before graph", func() {
				rows, err := store.GetNodePairAggregates(ctx, DefaultTailnetID, start, end)
				if err != nil {
					b.Fatal(err)
				}
				if len(rows) != nodes {
					b.Fatalf("before graph pairs = %d", len(rows))
				}
				graphBefore = rows
			})
			var talkersBefore []TopTalker
			beforeTalkers := medianQuery(b, samples, "before talkers", func() {
				rows, err := store.GetTopTalkers(ctx, DefaultTailnetID, start, end, 20)
				if err != nil {
					b.Fatal(err)
				}
				if len(rows) == 0 {
					b.Fatal("before talkers empty")
				}
				talkersBefore = rows
			})
			var statsBefore []TrafficStats
			beforeStats := medianQuery(b, samples, "before stats", func() {
				rows, err := store.GetTrafficStats(ctx, DefaultTailnetID, start, end)
				if err != nil {
					b.Fatal(err)
				}
				if len(rows) == 0 {
					b.Fatal("before stats empty")
				}
				statsBefore = rows
			})

			rollStart := time.Now()
			if err := store.backfillHourRollups(ctx); err != nil {
				b.Fatal(err)
			}
			b.Logf("rollup build %s", time.Since(rollStart).Round(time.Millisecond))
			if _, err := store.db.ExecContext(ctx, "ANALYZE"); err != nil {
				b.Fatal(err)
			}

			var graphAfter []NodePairAggregate
			afterGraph := medianQuery(b, samples, "after graph", func() {
				rows, err := store.GetNodePairAggregates(ctx, DefaultTailnetID, start, end)
				if err != nil {
					b.Fatal(err)
				}
				if len(rows) != nodes {
					b.Fatalf("after graph pairs = %d", len(rows))
				}
				graphAfter = rows
			})
			var talkersAfter []TopTalker
			afterTalkers := medianQuery(b, samples, "after talkers", func() {
				rows, err := store.GetTopTalkers(ctx, DefaultTailnetID, start, end, 20)
				if err != nil {
					b.Fatal(err)
				}
				if len(rows) == 0 {
					b.Fatal("after talkers empty")
				}
				talkersAfter = rows
			})
			var statsAfter []TrafficStats
			afterStats := medianQuery(b, samples, "after stats", func() {
				rows, err := store.GetTrafficStats(ctx, DefaultTailnetID, start, end)
				if err != nil {
					b.Fatal(err)
				}
				if len(rows) == 0 {
					b.Fatal("after stats empty")
				}
				statsAfter = rows
			})

			if !reflect.DeepEqual(graphBefore, graphAfter) {
				b.Fatal("graph rollup result differs from the minute scan")
			}
			if !reflect.DeepEqual(talkersBefore, talkersAfter) {
				b.Fatalf("talkers differ\nbefore %#v\nafter %#v", talkersBefore, talkersAfter)
			}
			if !reflect.DeepEqual(statsBefore, statsAfter) {
				b.Fatalf("stats differ\nbefore %#v\nafter %#v", statsBefore, statsAfter)
			}

			b.ReportMetric(ms(beforeGraph), "graph-before-ms")
			b.ReportMetric(ms(afterGraph), "graph-after-ms")
			b.ReportMetric(ms(beforeTalkers), "talkers-before-ms")
			b.ReportMetric(ms(afterTalkers), "talkers-after-ms")
			b.ReportMetric(ms(beforeStats), "stats-before-ms")
			b.ReportMetric(ms(afterStats), "stats-after-ms")
			b.Logf("median graph %s -> %s, talkers %s -> %s, stats %s -> %s",
				beforeGraph.Round(time.Millisecond), afterGraph.Round(time.Millisecond),
				beforeTalkers.Round(time.Millisecond), afterTalkers.Round(time.Millisecond),
				beforeStats.Round(time.Millisecond), afterStats.Round(time.Millisecond))

			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := store.GetNodePairAggregates(ctx, DefaultTailnetID, start, end); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func ms(d time.Duration) float64 {
	return float64(d) / float64(time.Millisecond)
}

func medianQuery(b *testing.B, samples int, name string, fn func()) time.Duration {
	b.Helper()
	if samples < 1 {
		samples = 1
	}
	taken := make([]time.Duration, samples)
	for i := range taken {
		start := time.Now()
		fn()
		taken[i] = time.Since(start)
		b.Logf("%s run %d %s", name, i+1, taken[i].Round(time.Millisecond))
	}
	sort.Slice(taken, func(i, j int) bool { return taken[i] < taken[j] })
	return taken[len(taken)/2]
}

func insertScalePairs(b *testing.B, store *SQLiteStore, nodes, minutes int, base int64) int {
	b.Helper()
	ctx := context.Background()
	names := make([]string, nodes)
	for i := range names {
		names[i] = fmt.Sprintf("n%05d", i)
	}
	// Loading is not part of the timed query. Turning synchronous off keeps
	// the insert from waiting on a disk flush after every hour. Queries run
	// after a full checkpoint, with the production synchronous setting.
	if _, err := store.db.ExecContext(ctx, "PRAGMA synchronous=OFF"); err != nil {
		b.Fatal(err)
	}
	inserted := 0
	for startMin := 0; startMin < minutes; startMin += 60 {
		endMin := startMin + 60
		if endMin > minutes {
			endMin = minutes
		}
		tx, err := store.db.BeginTx(ctx, nil)
		if err != nil {
			b.Fatal(err)
		}
		stmt, err := tx.PrepareContext(ctx, `
			INSERT INTO node_pairs (
				tailnet_id, bucket, src_node_id, dst_node_id, traffic_type,
				tx_bytes, rx_bytes, tx_pkts, rx_pkts, flow_count,
				protocols, protocol_bytes, ports,
				tx_ports, rx_ports, tx_protocol_bytes, rx_protocol_bytes,
				directional_ports
			) VALUES (
				?, ?, ?, ?, 'virtual',
				?, ?, 1, 1, 1,
				'[6,17]', '{"6":100,"17":40}', '[{"port":443,"proto":6,"bytes":100},{"port":53,"proto":17,"bytes":40}]',
				'[{"port":443,"proto":6,"bytes":100}]', '[{"port":53,"proto":17,"bytes":40}]',
				'{"6":100}', '{"17":40}', 1
			)
		`)
		if err != nil {
			tx.Rollback()
			b.Fatal(err)
		}
		stats, err := tx.PrepareContext(ctx, `
			INSERT INTO traffic_stats (
				tailnet_id, bucket, tcp_bytes, udp_bytes, virtual_bytes, total_flows, unique_pairs, top_ports
			) VALUES (?, ?, ?, ?, ?, ?, ?, '[{"port":443,"proto":6,"bytes":100}]')
		`)
		if err != nil {
			stmt.Close()
			tx.Rollback()
			b.Fatal(err)
		}
		for minute := startMin; minute < endMin; minute++ {
			bucket := base + int64(minute)*60
			var txBytes, rxBytes int64
			for node := 0; node < nodes; node++ {
				bytes := int64(node%50 + (minute % 60) + 1)
				rx := bytes / 2
				if _, err := stmt.ExecContext(ctx, DefaultTailnetID, bucket, names[node], names[(node+1)%nodes], bytes, rx); err != nil {
					stmt.Close()
					stats.Close()
					tx.Rollback()
					b.Fatal(err)
				}
				txBytes += bytes
				rxBytes += rx
				inserted++
			}
			if _, err := stats.ExecContext(ctx, DefaultTailnetID, bucket, txBytes, rxBytes, txBytes+rxBytes, int64(nodes), int64(nodes)); err != nil {
				stmt.Close()
				stats.Close()
				tx.Rollback()
				b.Fatal(err)
			}
		}
		stmt.Close()
		stats.Close()
		if err := tx.Commit(); err != nil {
			b.Fatal(err)
		}
		if (startMin/60)%24 == 0 {
			b.Logf("inserted through minute %d/%d", endMin, minutes)
		}
	}
	if _, err := store.db.ExecContext(ctx, "PRAGMA synchronous=NORMAL"); err != nil {
		b.Fatal(err)
	}
	if _, err := store.db.ExecContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE)"); err != nil {
		b.Fatal(err)
	}
	return inserted
}
