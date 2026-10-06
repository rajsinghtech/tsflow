package database

import (
	"context"
	"fmt"
	"testing"
	"time"
)

// BenchmarkCoveredStatsOverview compares a full node_pairs derivation with the
// overview path that skips it when traffic_stats already has every minute.
// BenchmarkProtocolBackfill compares a repair scan with a startup that finds
// the completion row and does not read node_pairs.
//
// Both are skipped under -short. Sizes are large enough to show the scan and
// small enough to load in a test run. `go test` does not run benchmarks unless
// -bench is set.
func BenchmarkCoveredStatsOverview(b *testing.B) {
	if testing.Short() {
		b.Skip("skipping stats overview benchmark in short mode")
	}
	const (
		nodes   = 20000
		minutes = 20
	)
	store := setupBenchDB(b)
	base := int64(1800000000)
	ctx := context.Background()
	loadStart := time.Now()
	insertBenchPairsAndStats(b, store, []string{DefaultTailnetID, "other"}, nodes, minutes, base)
	b.Logf("loaded %d nodes x %d minutes x 2 tailnets in %s", nodes, minutes, time.Since(loadStart).Round(time.Millisecond))
	if _, err := store.db.ExecContext(ctx, "ANALYZE"); err != nil {
		b.Fatal(err)
	}
	start := time.Unix(base, 0).UTC()
	end := time.Unix(base+int64(minutes)*60, 0).UTC()

	b.Run("full-derived", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			primary, err := store.GetTrafficStats(ctx, DefaultTailnetID, start, end)
			if err != nil {
				b.Fatal(err)
			}
			derived, err := store.GetTrafficStatsFromNodePairs(ctx, DefaultTailnetID, start, end)
			if err != nil {
				b.Fatal(err)
			}
			if len(primary) != minutes || len(derived) != minutes {
				b.Fatalf("primary %d derived %d, want %d", len(primary), len(derived), minutes)
			}
		}
	})
	b.Run("covered-skip", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			primary, err := store.GetTrafficStats(ctx, DefaultTailnetID, start, end)
			if err != nil {
				b.Fatal(err)
			}
			before := store.DerivedStatsScanCount()
			derived, err := store.FillMissingTrafficStats(ctx, DefaultTailnetID, start, end, primary)
			if err != nil {
				b.Fatal(err)
			}
			if store.DerivedStatsScanCount() != before {
				b.Fatal("covered window read node_pairs")
			}
			if len(primary) != minutes || len(derived) != 0 {
				b.Fatalf("primary %d derived %d", len(primary), len(derived))
			}
		}
	})

	// One minute of traffic_stats is removed so the gap path has to read pairs.
	gap := base + int64(minutes/2)*60
	if _, err := store.db.ExecContext(ctx, `DELETE FROM traffic_stats WHERE tailnet_id = ? AND bucket = ?`, DefaultTailnetID, gap); err != nil {
		b.Fatal(err)
	}
	b.Run("one-minute-gap", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			primary, err := store.GetTrafficStats(ctx, DefaultTailnetID, start, end)
			if err != nil {
				b.Fatal(err)
			}
			derived, err := store.FillMissingTrafficStats(ctx, DefaultTailnetID, start, end, primary)
			if err != nil {
				b.Fatal(err)
			}
			if len(derived) != 1 {
				b.Fatalf("gap buckets = %d, want 1", len(derived))
			}
		}
	})
}

func BenchmarkProtocolBackfill(b *testing.B) {
	if testing.Short() {
		b.Skip("skipping protocol backfill benchmark in short mode")
	}
	const (
		nodes   = 20000
		minutes = 20
	)
	store := setupBenchDB(b)
	base := int64(1800000000)
	ctx := context.Background()
	loadStart := time.Now()
	insertBenchPairsAndStats(b, store, []string{DefaultTailnetID, "other"}, nodes, minutes, base)
	b.Logf("loaded %d nodes x %d minutes x 2 tailnets in %s", nodes, minutes, time.Since(loadStart).Round(time.Millisecond))
	if _, err := store.db.ExecContext(ctx, `DELETE FROM backfill_state`); err != nil {
		b.Fatal(err)
	}

	b.Run("filled-scan", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			b.StopTimer()
			if _, err := store.db.ExecContext(ctx, `DELETE FROM backfill_state`); err != nil {
				b.Fatal(err)
			}
			b.StartTimer()
			if err := store.backfillProtocolBytes(ctx); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("filled-skip", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			if err := store.backfillProtocolBytes(ctx); err != nil {
				b.Fatal(err)
			}
		}
	})
}

func insertBenchPairsAndStats(b *testing.B, store *SQLiteStore, tailnets []string, nodes, minutes int, base int64) {
	b.Helper()
	ctx := context.Background()
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		b.Fatal(err)
	}
	defer tx.Rollback()
	pairStmt, err := tx.PrepareContext(ctx, `
		INSERT INTO node_pairs (
			tailnet_id, bucket, src_node_id, dst_node_id, traffic_type,
			tx_bytes, rx_bytes, flow_count, protocols, protocol_bytes, ports
		) VALUES (?, ?, ?, ?, 'virtual', ?, ?, 1, '[6]', ?, ?)
	`)
	if err != nil {
		b.Fatal(err)
	}
	defer pairStmt.Close()
	statStmt, err := tx.PrepareContext(ctx, `
		INSERT INTO traffic_stats (
			tailnet_id, bucket, tcp_bytes, virtual_bytes, total_flows, unique_pairs, top_ports
		) VALUES (?, ?, ?, ?, ?, ?, '[{"port":443,"proto":6,"bytes":1}]')
	`)
	if err != nil {
		b.Fatal(err)
	}
	defer statStmt.Close()

	for _, tailnetID := range tailnets {
		for minute := 0; minute < minutes; minute++ {
			bucket := base + int64(minute)*60
			var flows int64
			for node := 0; node < nodes; node++ {
				src := fmt.Sprintf("n%05d", node)
				dst := fmt.Sprintf("n%05d", (node+1)%nodes)
				bytes := int64(node%50 + minute + 1)
				proto := fmt.Sprintf(`{"6":%d}`, bytes)
				ports := fmt.Sprintf(`[{"port":443,"proto":6,"bytes":%d}]`, bytes)
				if _, err := pairStmt.ExecContext(ctx, tailnetID, bucket, src, dst, bytes, bytes/2, proto, ports); err != nil {
					b.Fatal(err)
				}
				flows++
			}
			if _, err := statStmt.ExecContext(ctx, tailnetID, bucket, flows, flows, flows, int64(nodes)); err != nil {
				b.Fatal(err)
			}
		}
	}
	if err := tx.Commit(); err != nil {
		b.Fatal(err)
	}
}
