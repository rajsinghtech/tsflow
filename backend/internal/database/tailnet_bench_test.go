package database

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

// BenchmarkTailnetQueries measures the common read paths at about 20k nodes
// per tailnet. It is skipped under -short so the default test run stays small.
// `go test` does not execute benchmarks unless -bench is set.
func BenchmarkTailnetQueries(b *testing.B) {
	if testing.Short() {
		b.Skip("skipping 20k-node tailnet benchmark in short mode")
	}

	const (
		nodes   = 20000
		minutes = 4
		peers   = 1
	)
	base := int64(1700000000)
	ctx := context.Background()
	tailnetStore := setupBenchDB(b)
	singleStore := setupBenchDB(b)
	legacyDB := openLegacyBenchDB(b)

	insertStart := time.Now()
	insertTailnetBenchRows(b, tailnetStore, []string{DefaultTailnetID, "other"}, nodes, minutes, peers, base)
	insertTailnetBenchRows(b, singleStore, []string{DefaultTailnetID}, nodes, minutes, peers, base)
	insertLegacyBenchRows(b, legacyDB, nodes, minutes, peers, base)
	b.Logf("loaded %d nodes x %d minutes x %d peers into a 2-tailnet database, a 1-tailnet database, and a legacy database in %s",
		nodes, minutes, peers, time.Since(insertStart).Round(time.Millisecond))
	for _, store := range []*SQLiteStore{tailnetStore, singleStore} {
		if _, err := store.db.ExecContext(ctx, "ANALYZE"); err != nil {
			b.Fatal(err)
		}
	}
	if _, err := legacyDB.ExecContext(ctx, "ANALYZE"); err != nil {
		b.Fatal(err)
	}

	start := time.Unix(base, 0).UTC()
	end := time.Unix(base+int64(minutes)*60, 0).UTC()
	nodeID := "n00000"

	b.Run("legacy-range-sum", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			var total int64
			if err := legacyDB.QueryRowContext(ctx, `
				SELECT COALESCE(SUM(tx_bytes + rx_bytes), 0) FROM node_pairs
				WHERE bucket >= ? AND bucket < ?
			`, base, base+int64(minutes)*60).Scan(&total); err != nil {
				b.Fatal(err)
			}
			if total == 0 {
				b.Fatal("legacy range sum was empty")
			}
		}
	})
	b.Run("tailnet-range-sum", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			var total int64
			if err := tailnetStore.db.QueryRowContext(ctx, `
				SELECT COALESCE(SUM(tx_bytes + rx_bytes), 0) FROM node_pairs
				WHERE tailnet_id = ? AND bucket >= ? AND bucket < ?
			`, DefaultTailnetID, base, base+int64(minutes)*60).Scan(&total); err != nil {
				b.Fatal(err)
			}
			if total == 0 {
				b.Fatal("tailnet range sum was empty")
			}
		}
	})
	b.Run("single-tailnet-range-sum", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			var total int64
			if err := singleStore.db.QueryRowContext(ctx, `
				SELECT COALESCE(SUM(tx_bytes + rx_bytes), 0) FROM node_pairs
				WHERE tailnet_id = ? AND bucket >= ? AND bucket < ?
			`, DefaultTailnetID, base, base+int64(minutes)*60).Scan(&total); err != nil {
				b.Fatal(err)
			}
			if total == 0 {
				b.Fatal("single tailnet range sum was empty")
			}
		}
	})
	b.Run("legacy-node-sum", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			var total int64
			if err := legacyDB.QueryRowContext(ctx, `
				SELECT COALESCE(SUM(tx_bytes + rx_bytes), 0) FROM node_pairs
				WHERE src_node_id = ? AND bucket >= ? AND bucket < ?
			`, nodeID, base, base+int64(minutes)*60).Scan(&total); err != nil {
				b.Fatal(err)
			}
			if total == 0 {
				b.Fatal("legacy node sum was empty")
			}
		}
	})
	b.Run("tailnet-node-sum", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			var total int64
			if err := tailnetStore.db.QueryRowContext(ctx, `
				SELECT COALESCE(SUM(tx_bytes + rx_bytes), 0) FROM node_pairs
				WHERE tailnet_id = ? AND src_node_id = ? AND bucket >= ? AND bucket < ?
			`, DefaultTailnetID, nodeID, base, base+int64(minutes)*60).Scan(&total); err != nil {
				b.Fatal(err)
			}
			if total == 0 {
				b.Fatal("tailnet node sum was empty")
			}
		}
	})
	b.Run("single-tailnet-node-sum", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			var total int64
			if err := singleStore.db.QueryRowContext(ctx, `
				SELECT COALESCE(SUM(tx_bytes + rx_bytes), 0) FROM node_pairs
				WHERE tailnet_id = ? AND src_node_id = ? AND bucket >= ? AND bucket < ?
			`, DefaultTailnetID, nodeID, base, base+int64(minutes)*60).Scan(&total); err != nil {
				b.Fatal(err)
			}
			if total == 0 {
				b.Fatal("single tailnet node sum was empty")
			}
		}
	})
	b.Run("tailnet-node-bandwidth", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			buckets, err := tailnetStore.GetNodeBandwidth(ctx, DefaultTailnetID, start, end, nodeID)
			if err != nil {
				b.Fatal(err)
			}
			if len(buckets) == 0 {
				b.Fatal("node bandwidth was empty")
			}
		}
	})
	b.Run("tailnet-top-talkers", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			talkers, err := tailnetStore.GetTopTalkers(ctx, DefaultTailnetID, start, end, 20)
			if err != nil {
				b.Fatal(err)
			}
			if len(talkers) == 0 {
				b.Fatal("top talkers was empty")
			}
		}
	})
	b.Run("tailnet-node-pairs", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			pairs, err := tailnetStore.GetNodePairAggregates(ctx, DefaultTailnetID, start, end)
			if err != nil {
				b.Fatal(err)
			}
			if len(pairs) == 0 {
				b.Fatal("node pairs was empty")
			}
		}
	})
}

func setupBenchDB(tb testing.TB) *SQLiteStore {
	tb.Helper()
	dir := tb.TempDir()
	store, err := NewSQLiteStore(dir + "/bench.db")
	if err != nil {
		tb.Fatal(err)
	}
	tb.Cleanup(func() { store.Close() })
	if err := store.Init(context.Background()); err != nil {
		tb.Fatal(err)
	}
	return store
}

func openLegacyBenchDB(b *testing.B) *sql.DB {
	b.Helper()
	db, err := sql.Open("sqlite", b.TempDir()+"/legacy.db")
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { db.Close() })
	if _, err := db.Exec(`
		PRAGMA journal_mode=WAL;
		PRAGMA synchronous=NORMAL;
		CREATE TABLE node_pairs (
			bucket INTEGER NOT NULL,
			src_node_id TEXT NOT NULL,
			dst_node_id TEXT NOT NULL,
			traffic_type TEXT NOT NULL,
			tx_bytes INTEGER DEFAULT 0,
			rx_bytes INTEGER DEFAULT 0,
			tx_pkts INTEGER DEFAULT 0,
			rx_pkts INTEGER DEFAULT 0,
			flow_count INTEGER DEFAULT 0,
			protocols TEXT DEFAULT '[]',
			protocol_bytes TEXT DEFAULT '{}',
			ports TEXT DEFAULT '[]',
			tx_ports TEXT DEFAULT '[]',
			rx_ports TEXT DEFAULT '[]',
			tx_protocol_bytes TEXT DEFAULT '{}',
			rx_protocol_bytes TEXT DEFAULT '{}',
			directional_ports INTEGER NOT NULL DEFAULT 0,
			PRIMARY KEY (bucket, src_node_id, dst_node_id, traffic_type)
		);
		CREATE INDEX idx_node_pairs_bucket ON node_pairs(bucket);
		CREATE INDEX idx_node_pairs_src ON node_pairs(src_node_id, bucket);
		CREATE INDEX idx_node_pairs_dst ON node_pairs(dst_node_id, bucket);
	`); err != nil {
		b.Fatal(err)
	}
	return db
}

func insertTailnetBenchRows(b *testing.B, store *SQLiteStore, tailnets []string, nodes, minutes, peers int, base int64) {
	b.Helper()
	tx, err := store.db.BeginTx(context.Background(), nil)
	if err != nil {
		b.Fatal(err)
	}
	defer tx.Rollback()
	stmt, err := tx.PrepareContext(context.Background(), `
		INSERT INTO node_pairs (
			tailnet_id, bucket, src_node_id, dst_node_id, traffic_type, tx_bytes, rx_bytes, flow_count, protocols, ports
		) VALUES (?, ?, ?, ?, 'virtual', ?, ?, 1, '[6]', '[]')
	`)
	if err != nil {
		b.Fatal(err)
	}
	defer stmt.Close()
	for _, tailnetID := range tailnets {
		for minute := 0; minute < minutes; minute++ {
			bucket := base + int64(minute)*60
			for node := 0; node < nodes; node++ {
				src := fmt.Sprintf("n%05d", node)
				for peer := 1; peer <= peers; peer++ {
					dst := fmt.Sprintf("n%05d", (node+peer)%nodes)
					bytes := int64(node%50 + minute + 1)
					if _, err := stmt.ExecContext(context.Background(), tailnetID, bucket, src, dst, bytes, bytes/2); err != nil {
						b.Fatal(err)
					}
				}
			}
		}
	}
	if err := tx.Commit(); err != nil {
		b.Fatal(err)
	}
}

func insertLegacyBenchRows(b *testing.B, db *sql.DB, nodes, minutes, peers int, base int64) {
	b.Helper()
	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		b.Fatal(err)
	}
	defer tx.Rollback()
	stmt, err := tx.PrepareContext(context.Background(), `
		INSERT INTO node_pairs (
			bucket, src_node_id, dst_node_id, traffic_type, tx_bytes, rx_bytes, flow_count,
			protocols, protocol_bytes, ports
		) VALUES (?, ?, ?, 'virtual', ?, ?, 1, '[6]', '{"6":1}', '[]')
	`)
	if err != nil {
		b.Fatal(err)
	}
	defer stmt.Close()
	for minute := 0; minute < minutes; minute++ {
		bucket := base + int64(minute)*60
		for node := 0; node < nodes; node++ {
			src := fmt.Sprintf("n%05d", node)
			for peer := 1; peer <= peers; peer++ {
				dst := fmt.Sprintf("n%05d", (node+peer)%nodes)
				bytes := int64(node%50 + minute + 1)
				if _, err := stmt.ExecContext(context.Background(), bucket, src, dst, bytes, bytes/2); err != nil {
					b.Fatal(err)
				}
			}
		}
	}
	if err := tx.Commit(); err != nil {
		b.Fatal(err)
	}
}
