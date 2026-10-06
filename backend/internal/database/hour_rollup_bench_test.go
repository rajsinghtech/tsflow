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
// flow per minute. The 1 hour and 24 hour windows are a median of three
// runs. The 7 day window is one run: it is the same 20,000 nodes and one
// row per minute (10,080 minutes), loaded with one INSERT SELECT per hour
// instead of a statement per row. It is skipped under -short.
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

func TestBulkScaleInsertMatchesFormula(t *testing.T) {
	store := setupBenchDB(t)
	const nodes = 5
	const minutes = 61
	const base int64 = 1_699_999_200
	if got := insertScalePairs(t, store, nodes, minutes, base); got != nodes*minutes {
		t.Fatalf("inserted %d", got)
	}
	ctx := context.Background()
	for minute := 0; minute < minutes; minute++ {
		for node := 0; node < nodes; node++ {
			wantTx := int64(node%50 + (minute % 60) + 1)
			wantRx := wantTx / 2
			var gotTx, gotRx int64
			var dst string
			err := store.db.QueryRowContext(ctx, `
				SELECT tx_bytes, rx_bytes, dst_node_id FROM node_pairs
				WHERE tailnet_id = ? AND bucket = ? AND src_node_id = ?
			`, DefaultTailnetID, base+int64(minute)*60, fmt.Sprintf("n%05d", node)).Scan(&gotTx, &gotRx, &dst)
			if err != nil {
				t.Fatalf("minute %d node %d: %v", minute, node, err)
			}
			if gotTx != wantTx || gotRx != wantRx || dst != fmt.Sprintf("n%05d", (node+1)%nodes) {
				t.Fatalf("minute %d node %d: tx %d rx %d dst %s", minute, node, gotTx, gotRx, dst)
			}
		}
	}
	var tcp, flows, pairs int64
	// Minute 60 is the first minute of the next hour, so off is 0.
	err := store.db.QueryRowContext(ctx, `
		SELECT tcp_bytes, total_flows, unique_pairs FROM traffic_stats
		WHERE tailnet_id = ? AND bucket = ?
	`, DefaultTailnetID, base+60*60).Scan(&tcp, &flows, &pairs)
	if err != nil {
		t.Fatal(err)
	}
	var wantTCP int64
	for node := 0; node < nodes; node++ {
		wantTCP += int64(node%50 + 0 + 1)
	}
	if tcp != wantTCP || flows != int64(nodes) || pairs != int64(nodes) {
		t.Fatalf("stats tcp %d flows %d pairs %d, want tcp %d", tcp, flows, pairs, wantTCP)
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

func insertScalePairs(tb testing.TB, store *SQLiteStore, nodes, minutes int, base int64) int {
	tb.Helper()
	ctx := context.Background()
	// One connection owns the temp tables. Loading is not part of the timed
	// query: synchronous is off, the secondary indexes are rebuilt after the
	// rows exist, and the production cache size is restored before return.
	conn, err := store.db.Conn(ctx)
	if err != nil {
		tb.Fatal(err)
	}
	defer conn.Close()
	for _, pragma := range []string{
		"PRAGMA synchronous=OFF",
		"PRAGMA cache_size=-262144",
		"DROP INDEX IF EXISTS idx_node_pairs_src",
		"DROP INDEX IF EXISTS idx_node_pairs_dst",
		"DROP INDEX IF EXISTS idx_node_pairs_endpoints",
		`CREATE TEMP TABLE scale_nodes (i INTEGER PRIMARY KEY, name TEXT NOT NULL)`,
		`CREATE TEMP TABLE scale_minutes (i INTEGER PRIMARY KEY, bucket INTEGER NOT NULL, off INTEGER NOT NULL)`,
	} {
		if _, err := conn.ExecContext(ctx, pragma); err != nil {
			tb.Fatal(err)
		}
	}
	nodeStmt, err := conn.PrepareContext(ctx, `INSERT INTO scale_nodes (i, name) VALUES (?, ?)`)
	if err != nil {
		tb.Fatal(err)
	}
	for i := 0; i < nodes; i++ {
		if _, err := nodeStmt.ExecContext(ctx, i, fmt.Sprintf("n%05d", i)); err != nil {
			nodeStmt.Close()
			tb.Fatal(err)
		}
	}
	nodeStmt.Close()
	minuteStmt, err := conn.PrepareContext(ctx, `INSERT INTO scale_minutes (i, bucket, off) VALUES (?, ?, ?)`)
	if err != nil {
		tb.Fatal(err)
	}
	for i := 0; i < minutes; i++ {
		if _, err := minuteStmt.ExecContext(ctx, i, base+int64(i)*60, i%60); err != nil {
			minuteStmt.Close()
			tb.Fatal(err)
		}
	}
	minuteStmt.Close()

	const pairInsert = `
		INSERT INTO node_pairs (
			tailnet_id, bucket, src_node_id, dst_node_id, traffic_type,
			tx_bytes, rx_bytes, tx_pkts, rx_pkts, flow_count,
			protocols, protocol_bytes, ports,
			tx_ports, rx_ports, tx_protocol_bytes, rx_protocol_bytes,
			directional_ports
		)
		SELECT ?, m.bucket, s.name, d.name, 'virtual',
			(s.i % 50) + m.off + 1,
			((s.i % 50) + m.off + 1) / 2,
			1, 1, 1,
			'[6,17]', '{"6":100,"17":40}',
			'[{"port":443,"proto":6,"bytes":100},{"port":53,"proto":17,"bytes":40}]',
			'[{"port":443,"proto":6,"bytes":100}]',
			'[{"port":53,"proto":17,"bytes":40}]',
			'{"6":100}', '{"17":40}', 1
		FROM scale_minutes m
		JOIN scale_nodes s
		JOIN scale_nodes d ON d.i = (s.i + 1) % ?
		WHERE m.i >= ? AND m.i < ?
	`
	const statsInsert = `
		INSERT INTO traffic_stats (
			tailnet_id, bucket, tcp_bytes, udp_bytes, virtual_bytes, total_flows, unique_pairs, top_ports
		)
		SELECT ?, m.bucket,
			SUM((s.i % 50) + m.off + 1),
			SUM(((s.i % 50) + m.off + 1) / 2),
			SUM((s.i % 50) + m.off + 1) + SUM(((s.i % 50) + m.off + 1) / 2),
			?, ?,
			'[{"port":443,"proto":6,"bytes":100}]'
		FROM scale_minutes m
		JOIN scale_nodes s
		WHERE m.i >= ? AND m.i < ?
		GROUP BY m.bucket
	`
	for startMin := 0; startMin < minutes; startMin += 60 {
		endMin := startMin + 60
		if endMin > minutes {
			endMin = minutes
		}
		tx, err := conn.BeginTx(ctx, nil)
		if err != nil {
			tb.Fatal(err)
		}
		if _, err := tx.ExecContext(ctx, pairInsert, DefaultTailnetID, nodes, startMin, endMin); err != nil {
			tx.Rollback()
			tb.Fatal(err)
		}
		if _, err := tx.ExecContext(ctx, statsInsert, DefaultTailnetID, nodes, nodes, startMin, endMin); err != nil {
			tx.Rollback()
			tb.Fatal(err)
		}
		if err := tx.Commit(); err != nil {
			tb.Fatal(err)
		}
		if startMin == 0 || (startMin/60)%24 == 0 {
			if b, ok := tb.(*testing.B); ok {
				b.Logf("inserted through minute %d/%d", endMin, minutes)
			}
		}
	}
	indexStart := time.Now()
	for _, stmt := range []string{
		`CREATE INDEX idx_node_pairs_src ON node_pairs(tailnet_id, src_node_id, bucket)`,
		`CREATE INDEX idx_node_pairs_dst ON node_pairs(tailnet_id, dst_node_id, bucket)`,
		`CREATE INDEX idx_node_pairs_endpoints ON node_pairs(tailnet_id, src_node_id, dst_node_id, traffic_type, bucket)`,
		"PRAGMA synchronous=NORMAL",
		"PRAGMA cache_size=10000",
		"PRAGMA wal_checkpoint(TRUNCATE)",
	} {
		if _, err := conn.ExecContext(ctx, stmt); err != nil {
			tb.Fatal(err)
		}
	}
	if b, ok := tb.(*testing.B); ok {
		b.Logf("rebuilt secondary indexes in %s", time.Since(indexStart).Round(time.Millisecond))
	}
	return nodes * minutes
}
