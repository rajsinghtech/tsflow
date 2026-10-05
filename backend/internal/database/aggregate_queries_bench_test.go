package database

import (
	"context"
	"fmt"
	"reflect"
	"testing"
	"time"
)

// BenchmarkNodePairAggregates compares the old correlated graph query with the
// single-pass read. The fixture is one peer per node across the default
// two-hour window, with protocol and port JSON on every minute row.
// It is skipped under -short. go test does not run benchmarks unless -bench is set.
func BenchmarkNodePairAggregates(b *testing.B) {
	if testing.Short() {
		b.Skip("skipping node-pair aggregate benchmark in short mode")
	}
	for _, nodes := range []int{1000, 20000} {
		nodes := nodes
		b.Run(fmt.Sprintf("%d-nodes", nodes), func(b *testing.B) {
			const minutes = 120
			const base int64 = 1_700_000_040
			ctx := context.Background()
			store := setupBenchDB(b)
			insertStart := time.Now()
			inserted := insertBenchNodePairs(b, store, nodes, minutes, base)
			b.Logf("loaded %d node-pair rows (%d nodes x %d minutes) in %s", inserted, nodes, minutes, time.Since(insertStart).Round(time.Millisecond))
			if _, err := store.db.ExecContext(ctx, "ANALYZE"); err != nil {
				b.Fatal(err)
			}
			start := time.Unix(base, 0).UTC()
			end := time.Unix(base+int64(minutes)*60, 0).UTC()

			legacy, err := store.legacyNodePairAggregates(ctx, DefaultTailnetID, start, end)
			if err != nil {
				b.Fatal(err)
			}
			current, err := store.GetNodePairAggregates(ctx, DefaultTailnetID, start, end)
			if err != nil {
				b.Fatal(err)
			}
			if len(legacy) != nodes || len(current) != nodes {
				b.Fatalf("pair count legacy=%d current=%d want %d", len(legacy), len(current), nodes)
			}
			if !reflect.DeepEqual(legacy, current) {
				b.Fatal("legacy and single-pass aggregates differ")
			}

			b.Run("legacy", func(b *testing.B) {
				b.ReportAllocs()
				for i := 0; i < b.N; i++ {
					rows, err := store.legacyNodePairAggregates(ctx, DefaultTailnetID, start, end)
					if err != nil {
						b.Fatal(err)
					}
					if len(rows) != nodes {
						b.Fatalf("legacy pair count = %d", len(rows))
					}
				}
			})
			b.Run("single-pass", func(b *testing.B) {
				b.ReportAllocs()
				for i := 0; i < b.N; i++ {
					rows, err := store.GetNodePairAggregates(ctx, DefaultTailnetID, start, end)
					if err != nil {
						b.Fatal(err)
					}
					if len(rows) != nodes {
						b.Fatalf("single-pass pair count = %d", len(rows))
					}
				}
			})
		})
	}
}

func insertBenchNodePairs(b *testing.B, store *SQLiteStore, nodes, minutes int, base int64) int {
	b.Helper()
	ctx := context.Background()
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		b.Fatal(err)
	}
	defer tx.Rollback()
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
		b.Fatal(err)
	}
	defer stmt.Close()
	inserted := 0
	for minute := 0; minute < minutes; minute++ {
		bucket := base + int64(minute)*60
		for node := 0; node < nodes; node++ {
			src := fmt.Sprintf("n%05d", node)
			dst := fmt.Sprintf("n%05d", (node+1)%nodes)
			bytes := int64(node%50 + minute + 1)
			if _, err := stmt.ExecContext(ctx, DefaultTailnetID, bucket, src, dst, bytes, bytes/2); err != nil {
				b.Fatal(err)
			}
			inserted++
		}
	}
	if err := tx.Commit(); err != nil {
		b.Fatal(err)
	}
	return inserted
}
