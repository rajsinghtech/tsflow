package database

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

func TestProtocolBackfillFreshDB(t *testing.T) {
	store := setupTestDB(t)
	if store.protocolBackfillScans != 0 {
		t.Fatalf("fresh database scanned node_pairs %d times", store.protocolBackfillScans)
	}
	assertBackfillMark(t, store, DefaultTailnetID, -1)

	if err := store.Init(context.Background()); err != nil {
		t.Fatal(err)
	}
	if store.protocolBackfillScans != 0 {
		t.Fatalf("second open scanned node_pairs %d times", store.protocolBackfillScans)
	}
	assertBackfillMark(t, store, DefaultTailnetID, -1)
}

func TestProtocolBackfillSkipsFilledDatabase(t *testing.T) {
	store := setupTestDB(t)
	ctx := context.Background()
	const base int64 = 1800000000
	insertProtocolRow(t, store, DefaultTailnetID, base, "a", "b", 10, 0, "[6]", `{"6":10}`)
	insertProtocolRow(t, store, "other", base, "a", "b", 20, 0, "[17]", `{"17":20}`)
	before := protocolByteMap(t, store)

	// The fresh mark is -1, so these rows are past it and get checked once.
	if err := store.Init(ctx); err != nil {
		t.Fatal(err)
	}
	if store.protocolBackfillScans != 2 {
		t.Fatalf("first check of new rows scanned %d times (%v)", store.protocolBackfillScans, store.protocolBackfillTailnets)
	}
	assertProtocolMapsEqual(t, before, protocolByteMap(t, store))
	assertBackfillMark(t, store, DefaultTailnetID, base)
	assertBackfillMark(t, store, "other", base)

	scans := store.protocolBackfillScans
	if err := store.Init(ctx); err != nil {
		t.Fatal(err)
	}
	if store.protocolBackfillScans != scans {
		t.Fatalf("filled database scanned node_pairs: before %d after %d, tailnets %v", scans, store.protocolBackfillScans, store.protocolBackfillTailnets)
	}
	assertProtocolMapsEqual(t, before, protocolByteMap(t, store))
}

func TestProtocolBackfillFillsGap(t *testing.T) {
	store := setupTestDB(t)
	ctx := context.Background()
	if _, err := store.db.ExecContext(ctx, `DELETE FROM backfill_state`); err != nil {
		t.Fatal(err)
	}
	const base int64 = 1800000000
	insertProtocolRow(t, store, DefaultTailnetID, base, "keep", "peer", 10, 0, "[6]", `{"6":10}`)
	insertProtocolRow(t, store, DefaultTailnetID, base+60, "empty", "peer", 40, 0, "[17]", `{}`)
	insertProtocolRow(t, store, DefaultTailnetID, base+120, "split", "peer", 100, 51, "[6,17]", "")
	if _, err := store.db.ExecContext(ctx, `
		UPDATE node_pairs SET protocol_bytes = NULL
		WHERE tailnet_id = ? AND src_node_id = 'split'
	`, DefaultTailnetID); err != nil {
		t.Fatal(err)
	}
	insertProtocolRow(t, store, "other", base, "gap", "peer", 9, 0, "[6]", `{}`)
	insertProtocolRow(t, store, "other", base+60, "keep", "peer", 7, 0, "[17]", `{"17":7}`)

	store.protocolBackfillScans = 0
	store.protocolBackfillTailnets = nil
	if err := store.Init(ctx); err != nil {
		t.Fatal(err)
	}
	if store.protocolBackfillScans != 2 {
		t.Fatalf("scan count = %d, want 2 (%v)", store.protocolBackfillScans, store.protocolBackfillTailnets)
	}
	if len(store.protocolBackfillTailnets) != 2 || store.protocolBackfillTailnets[0] != DefaultTailnetID || store.protocolBackfillTailnets[1] != "other" {
		t.Fatalf("scanned tailnets = %v", store.protocolBackfillTailnets)
	}
	assertProtocolByteMap(t, protocolBytes(t, store, DefaultTailnetID, "keep"), map[string]int64{"6": 10})
	assertProtocolByteMap(t, protocolBytes(t, store, DefaultTailnetID, "empty"), map[string]int64{"17": 40})
	assertProtocolByteMap(t, protocolBytes(t, store, DefaultTailnetID, "split"), map[string]int64{"6": 76, "17": 75})
	assertProtocolByteMap(t, protocolBytes(t, store, "other", "gap"), map[string]int64{"6": 9})
	assertProtocolByteMap(t, protocolBytes(t, store, "other", "keep"), map[string]int64{"17": 7})
	assertBackfillMark(t, store, DefaultTailnetID, base+120)
	assertBackfillMark(t, store, "other", base+60)

	filled := protocolByteMap(t, store)
	if err := store.Init(ctx); err != nil {
		t.Fatal(err)
	}
	if store.protocolBackfillScans != 2 {
		t.Fatalf("second open scanned again: %d (%v)", store.protocolBackfillScans, store.protocolBackfillTailnets)
	}
	assertProtocolMapsEqual(t, filled, protocolByteMap(t, store))
}

func TestProtocolBackfillFillsRowsWrittenAfterHighWater(t *testing.T) {
	store := setupTestDB(t)
	ctx := context.Background()
	const base int64 = 1800000000
	if _, err := store.db.ExecContext(ctx, `DELETE FROM backfill_state`); err != nil {
		t.Fatal(err)
	}
	insertProtocolRow(t, store, DefaultTailnetID, base, "keep", "peer", 10, 0, "[6]", `{"6":10}`)
	insertProtocolRow(t, store, DefaultTailnetID, base+60, "keep-next", "peer", 8, 0, "[17]", `{"17":8}`)
	insertProtocolRow(t, store, "other", base, "keep", "peer", 3, 0, "[6]", `{"6":3}`)
	if err := store.Init(ctx); err != nil {
		t.Fatal(err)
	}
	assertBackfillMark(t, store, DefaultTailnetID, base+60)
	assertBackfillMark(t, store, "other", base)
	scans := store.protocolBackfillScans

	// An older release writes empty protocol bytes, then this build starts again.
	// Rows past the mark are filled. A row in a bucket the mark already covers stays.
	insertProtocolRow(t, store, DefaultTailnetID, base, "old-minute", "peer", 40, 0, "[17]", `{}`)
	insertProtocolRow(t, store, DefaultTailnetID, base+120, "downgrade", "peer", 15, 0, "[6]", `{}`)
	insertProtocolRow(t, store, DefaultTailnetID, base+180, "valid-new", "peer", 4, 0, "[6]", `{"6":4}`)
	insertProtocolRow(t, store, "other", base+60, "downgrade", "peer", 9, 0, "[17]", `{}`)

	if err := store.Init(ctx); err != nil {
		t.Fatal(err)
	}
	if store.protocolBackfillScans != scans+2 {
		t.Fatalf("scans = %d, want %d (%v)", store.protocolBackfillScans, scans+2, store.protocolBackfillTailnets)
	}
	if got := protocolBytes(t, store, DefaultTailnetID, "old-minute"); got != "{}" {
		t.Fatalf("bucket at the covered minute was rewritten: %s", got)
	}
	assertProtocolByteMap(t, protocolBytes(t, store, DefaultTailnetID, "keep"), map[string]int64{"6": 10})
	assertProtocolByteMap(t, protocolBytes(t, store, DefaultTailnetID, "downgrade"), map[string]int64{"6": 15})
	assertProtocolByteMap(t, protocolBytes(t, store, DefaultTailnetID, "valid-new"), map[string]int64{"6": 4})
	assertProtocolByteMap(t, protocolBytes(t, store, "other", "downgrade"), map[string]int64{"17": 9})
	assertBackfillMark(t, store, DefaultTailnetID, base+180)
	assertBackfillMark(t, store, "other", base+60)

	scans = store.protocolBackfillScans
	if err := store.Init(ctx); err != nil {
		t.Fatal(err)
	}
	if store.protocolBackfillScans != scans {
		t.Fatalf("startup after the new mark scanned again: %d (%v)", store.protocolBackfillScans, store.protocolBackfillTailnets)
	}
	assertProtocolByteMap(t, protocolBytes(t, store, DefaultTailnetID, "downgrade"), map[string]int64{"6": 15})
	if got := protocolBytes(t, store, DefaultTailnetID, "old-minute"); got != "{}" {
		t.Fatalf("covered bucket changed on the following start: %s", got)
	}
}

func TestProtocolBackfillHighWaterUsesPrimaryKey(t *testing.T) {
	store := setupTestDB(t)
	const base int64 = 1800000000
	insertProtocolRow(t, store, DefaultTailnetID, base, "a", "b", 10, 0, "[6]", `{"6":10}`)
	insertProtocolRow(t, store, "other", base+60, "a", "b", 4, 0, "[6]", `{"6":4}`)
	if _, err := store.db.ExecContext(context.Background(), "ANALYZE"); err != nil {
		t.Fatal(err)
	}

	maxPlan := explain(t, store, "EXPLAIN QUERY PLAN "+protocolBackfillNewerMaxSQL, DefaultTailnetID, base)
	if !strings.Contains(maxPlan, "PRIMARY KEY") || !strings.Contains(maxPlan, "tailnet_id") || strings.Contains(maxPlan, "SCAN") {
		t.Fatalf("high-water probe plan = %q", maxPlan)
	}
	rowPlan := explain(t, store, "EXPLAIN QUERY PLAN "+protocolBackfillNewerRowsSQL, DefaultTailnetID, base)
	if !strings.Contains(rowPlan, "PRIMARY KEY") || !strings.Contains(rowPlan, "tailnet_id") || strings.Contains(rowPlan, "SCAN") {
		t.Fatalf("rows-past-mark plan = %q", rowPlan)
	}
}

func TestProtocolBackfillUpgradesBooleanCompletionTable(t *testing.T) {
	store := setupTestDB(t)
	ctx := context.Background()
	const base int64 = 1800000000
	if _, err := store.db.ExecContext(ctx, `
		DROP TABLE backfill_state;
		CREATE TABLE backfill_state (
			tailnet_id TEXT NOT NULL PRIMARY KEY,
			completed_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		) WITHOUT ROWID;
		INSERT INTO backfill_state (tailnet_id, completed_at) VALUES ('', '2026-03-01 00:00:00');
		INSERT INTO backfill_state (tailnet_id, completed_at) VALUES ('default', '2026-03-01 00:00:00');
	`); err != nil {
		t.Fatal(err)
	}
	insertProtocolRow(t, store, DefaultTailnetID, base, "gap", "peer", 15, 0, "[6]", `{}`)
	if err := store.Init(ctx); err != nil {
		t.Fatal(err)
	}
	assertProtocolByteMap(t, protocolBytes(t, store, DefaultTailnetID, "gap"), map[string]int64{"6": 15})
	assertBackfillMark(t, store, DefaultTailnetID, base)
	var sentinel int
	if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM backfill_state WHERE tailnet_id = ''`).Scan(&sentinel); err != nil {
		t.Fatal(err)
	}
	if sentinel != 0 {
		t.Fatal("empty tailnet id is still stored as a completion mark")
	}
}

func TestProtocolBackfillUpgradesPreChangeDB(t *testing.T) {
	t.Run("tailnet schema", func(t *testing.T) {
		store := setupTestDB(t)
		ctx := context.Background()
		if _, err := store.db.ExecContext(ctx, `DROP TABLE backfill_state`); err != nil {
			t.Fatal(err)
		}
		const base int64 = 1800000000
		insertProtocolRow(t, store, DefaultTailnetID, base, "keep", "peer", 10, 0, "[6]", `{"6":10}`)
		insertProtocolRow(t, store, DefaultTailnetID, base+60, "fill", "peer", 40, 0, "[17]", `{}`)
		insertProtocolRow(t, store, "other", base, "fill", "peer", 9, 0, "[6]", `not-json`)
		upgraded := finishBackfillUpgrade(t, store)
		if upgraded.protocolBackfillScans != 2 {
			t.Fatalf("upgrade scans = %d %v", upgraded.protocolBackfillScans, upgraded.protocolBackfillTailnets)
		}
		assertProtocolByteMap(t, protocolBytes(t, upgraded, DefaultTailnetID, "keep"), map[string]int64{"6": 10})
		assertProtocolByteMap(t, protocolBytes(t, upgraded, DefaultTailnetID, "fill"), map[string]int64{"17": 40})
		assertProtocolByteMap(t, protocolBytes(t, upgraded, "other", "fill"), map[string]int64{"6": 9})
		assertBackfillMark(t, upgraded, DefaultTailnetID, base+60)
		assertBackfillMark(t, upgraded, "other", base)
		restart := finishBackfillUpgrade(t, upgraded)
		if restart.protocolBackfillScans != 0 {
			t.Fatalf("reopen scanned %d times", restart.protocolBackfillScans)
		}
		assertProtocolByteMap(t, protocolBytes(t, restart, DefaultTailnetID, "keep"), map[string]int64{"6": 10})
		assertProtocolByteMap(t, protocolBytes(t, restart, "other", "fill"), map[string]int64{"6": 9})
	})

	t.Run("pre tailnet schema", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "test.db")
		store, err := NewSQLiteStore(path)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { store.Close() })
		ctx := context.Background()
		if _, err := store.db.ExecContext(ctx, `
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
				PRIMARY KEY (bucket, src_node_id, dst_node_id, traffic_type)
			);
			INSERT INTO node_pairs (
				bucket, src_node_id, dst_node_id, traffic_type, tx_bytes, rx_bytes,
				flow_count, protocols, protocol_bytes, ports
			) VALUES
				(1800000000, 'keep', 'peer', 'virtual', 10, 0, 1, '[6]', '{"6":10}', '[]'),
				(1800000060, 'fill', 'peer', 'virtual', 40, 0, 1, '[17]', '{}', '[]'),
				(1800000120, 'bad', 'peer', 'virtual', 100, 51, 1, '[6,17]', 'not-json', '[]');
			CREATE TABLE poll_state (
				id INTEGER PRIMARY KEY CHECK (id = 1),
				last_poll_end DATETIME,
				updated_at DATETIME
			);
			INSERT INTO poll_state (id, last_poll_end, updated_at)
			VALUES (1, '2026-03-01 12:00:00', '2026-03-01 12:00:01');
		`); err != nil {
			t.Fatal(err)
		}
		if err := store.Init(ctx); err != nil {
			t.Fatal(err)
		}
		if store.protocolBackfillScans != 1 || len(store.protocolBackfillTailnets) != 1 || store.protocolBackfillTailnets[0] != DefaultTailnetID {
			t.Fatalf("upgrade scans = %d %v", store.protocolBackfillScans, store.protocolBackfillTailnets)
		}
		var tx int64
		if err := store.db.QueryRowContext(ctx, `
			SELECT tx_bytes FROM node_pairs WHERE tailnet_id = ? AND src_node_id = 'keep'
		`, DefaultTailnetID).Scan(&tx); err != nil {
			t.Fatal(err)
		}
		if tx != 10 {
			t.Fatalf("tx_bytes = %d, want 10", tx)
		}
		assertProtocolByteMap(t, protocolBytes(t, store, DefaultTailnetID, "keep"), map[string]int64{"6": 10})
		assertProtocolByteMap(t, protocolBytes(t, store, DefaultTailnetID, "fill"), map[string]int64{"17": 40})
		assertProtocolByteMap(t, protocolBytes(t, store, DefaultTailnetID, "bad"), map[string]int64{"6": 76, "17": 75})
		assertBackfillMark(t, store, DefaultTailnetID, 1800000120)

		reopen := finishBackfillUpgrade(t, store)
		assertProtocolByteMap(t, protocolBytes(t, reopen, DefaultTailnetID, "keep"), map[string]int64{"6": 10})
		assertProtocolByteMap(t, protocolBytes(t, reopen, DefaultTailnetID, "fill"), map[string]int64{"17": 40})
		assertProtocolByteMap(t, protocolBytes(t, reopen, DefaultTailnetID, "bad"), map[string]int64{"6": 76, "17": 75})
		if reopen.protocolBackfillScans != 0 {
			t.Fatalf("reopen scanned %d times", reopen.protocolBackfillScans)
		}
	})
}

// finishBackfillUpgrade closes store and opens the same file the way a process
// restart does. The returned store is the second open.
func finishBackfillUpgrade(t *testing.T, store *SQLiteStore) *SQLiteStore {
	t.Helper()
	path := store.dbPath
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	reopen, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { reopen.Close() })
	if err := reopen.Init(context.Background()); err != nil {
		t.Fatal(err)
	}
	return reopen
}

func insertProtocolRow(t *testing.T, store *SQLiteStore, tailnetID string, bucket int64, src, dst string, tx, rx int64, protocols, protocolBytes string) {
	t.Helper()
	if protocolBytes == "" {
		protocolBytes = "{}"
	}
	if _, err := store.db.ExecContext(context.Background(), `
		INSERT INTO node_pairs (
			tailnet_id, bucket, src_node_id, dst_node_id, traffic_type,
			tx_bytes, rx_bytes, flow_count, protocols, protocol_bytes, ports
		) VALUES (?, ?, ?, ?, 'virtual', ?, ?, 1, ?, ?, '[]')
	`, tailnetID, bucket, src, dst, tx, rx, protocols, protocolBytes); err != nil {
		t.Fatal(err)
	}
}

func protocolBytes(t *testing.T, store *SQLiteStore, tailnetID, src string) string {
	t.Helper()
	var raw string
	err := store.db.QueryRowContext(context.Background(), `
		SELECT protocol_bytes FROM node_pairs WHERE tailnet_id = ? AND src_node_id = ?
	`, tailnetID, src).Scan(&raw)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func protocolByteMap(t *testing.T, store *SQLiteStore) map[string]string {
	t.Helper()
	rows, err := store.db.QueryContext(context.Background(), `
		SELECT tailnet_id, src_node_id, protocol_bytes FROM node_pairs ORDER BY tailnet_id, src_node_id
	`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var tailnetID, src, raw string
		if err := rows.Scan(&tailnetID, &src, &raw); err != nil {
			t.Fatal(err)
		}
		out[tailnetID+"/"+src] = raw
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func assertProtocolMapsEqual(t *testing.T, before, after map[string]string) {
	t.Helper()
	if len(before) != len(after) {
		t.Fatalf("protocol row count %d -> %d", len(before), len(after))
	}
	for key, want := range before {
		if after[key] != want {
			t.Fatalf("%s protocol_bytes = %s, want %s", key, after[key], want)
		}
	}
}

func assertBackfillMark(t *testing.T, store *SQLiteStore, tailnetID string, bucket int64) {
	t.Helper()
	var got int64
	var completed string
	err := store.db.QueryRowContext(context.Background(), `
		SELECT protocol_bytes_bucket, completed_at FROM backfill_state WHERE tailnet_id = ?
	`, tailnetID).Scan(&got, &completed)
	if err != nil {
		t.Fatalf("backfill mark for %s: %v", tailnetID, err)
	}
	if got != bucket {
		t.Fatalf("backfill mark for %s = %d, want %d", tailnetID, got, bucket)
	}
	if parseTime(completed).IsZero() {
		t.Fatalf("tailnet %q completion time %q is not a timestamp", tailnetID, completed)
	}
}
