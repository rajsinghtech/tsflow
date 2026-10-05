package database

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPreTailnetBackupRestoresOldWriters(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "o'brien")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(dir, "tsflow.db")
	store, err := NewSQLiteStore(dbPath)
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
			ports TEXT DEFAULT '[]',
			PRIMARY KEY (bucket, src_node_id, dst_node_id, traffic_type)
		);
		INSERT INTO node_pairs (
			bucket, src_node_id, dst_node_id, traffic_type, tx_bytes, rx_bytes, flow_count, protocols, ports
		) VALUES (1700000000, 'node-a', 'node-b', 'virtual', 100, 40, 1, '[6]', '[]');
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
	backupPath := migrationBackupPath(dbPath)
	backupSum := fileSum(t, backupPath)

	if err := store.Init(ctx); err != nil {
		t.Fatalf("second init: %v", err)
	}
	if got := fileSum(t, backupPath); got != backupSum {
		t.Fatal("second init rewrote the pre-migration backup")
	}

	live := store.db
	if _, err := live.ExecContext(ctx, `
		INSERT INTO node_pairs (
			bucket, src_node_id, dst_node_id, traffic_type, tx_bytes, protocols, ports
		) VALUES (1700000060, 'node-c', 'node-d', 'virtual', 1, '[]', '[]')
		ON CONFLICT(bucket, src_node_id, dst_node_id, traffic_type) DO UPDATE SET
			tx_bytes = tx_bytes + excluded.tx_bytes
	`); err == nil {
		t.Fatal("migrated database accepted an old node_pairs upsert")
	}
	if _, err := live.ExecContext(ctx, `
		UPDATE poll_state SET last_poll_end = '2026-04-01 00:00:00' WHERE id = 1
	`); err == nil {
		t.Fatal("migrated database accepted an old poll_state update")
	}

	backup, err := sql.Open("sqlite", backupPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { backup.Close() })

	var txBytes int64
	var pollID int
	var pollEnd string
	if err := backup.QueryRowContext(ctx, `
		SELECT tx_bytes FROM node_pairs WHERE src_node_id = 'node-a'
	`).Scan(&txBytes); err != nil {
		t.Fatalf("backup is missing the pre-migration row: %v", err)
	}
	if txBytes != 100 {
		t.Fatalf("backup tx_bytes = %d, want 100", txBytes)
	}
	if err := backup.QueryRowContext(ctx, `
		SELECT id, last_poll_end FROM poll_state WHERE id = 1
	`).Scan(&pollID, &pollEnd); err != nil {
		t.Fatalf("backup poll_state: %v", err)
	}
	if pollID != 1 || !parseTime(pollEnd).Equal(time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)) {
		t.Fatalf("backup poll cursor = %d %q", pollID, pollEnd)
	}

	if _, err := backup.ExecContext(ctx, `
		INSERT INTO node_pairs (
			bucket, src_node_id, dst_node_id, traffic_type, tx_bytes, rx_bytes, flow_count, protocols, ports
		) VALUES (1700000000, 'node-a', 'node-b', 'virtual', 5, 1, 1, '[6]', '[]')
		ON CONFLICT(bucket, src_node_id, dst_node_id, traffic_type) DO UPDATE SET
			tx_bytes = tx_bytes + excluded.tx_bytes
	`); err != nil {
		t.Fatalf("old node_pairs upsert failed on backup: %v", err)
	}
	if _, err := backup.ExecContext(ctx, `
		UPDATE poll_state
		SET last_poll_end = '2026-03-02 00:00:00', updated_at = '2026-03-02 00:00:00'
		WHERE id = 1
	`); err != nil {
		t.Fatalf("old poll_state update failed on backup: %v", err)
	}
	if err := backup.QueryRowContext(ctx, `SELECT tx_bytes FROM node_pairs WHERE src_node_id = 'node-a'`).Scan(&txBytes); err != nil {
		t.Fatal(err)
	}
	if txBytes != 105 {
		t.Fatalf("backup tx_bytes after old upsert = %d, want 105", txBytes)
	}
	if err := backup.QueryRowContext(ctx, `SELECT last_poll_end FROM poll_state WHERE id = 1`).Scan(&pollEnd); err != nil {
		t.Fatal(err)
	}
	if !parseTime(pollEnd).Equal(time.Date(2026, 3, 2, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("backup poll cursor after old update = %q", pollEnd)
	}

	var liveTx int64
	if err := live.QueryRowContext(ctx, `
		SELECT tx_bytes FROM node_pairs WHERE tailnet_id = ? AND src_node_id = 'node-a'
	`, DefaultTailnetID).Scan(&liveTx); err != nil {
		t.Fatal(err)
	}
	if liveTx != 100 {
		t.Fatalf("live database changed while restoring the backup: tx_bytes=%d", liveTx)
	}
}

func TestTailnetMigrationCanSkipBackup(t *testing.T) {
	t.Setenv(skipMigrationBackupEnv, "1")
	store := openRawStore(t)
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
			ports TEXT DEFAULT '[]',
			PRIMARY KEY (bucket, src_node_id, dst_node_id, traffic_type)
		);
		INSERT INTO node_pairs (
			bucket, src_node_id, dst_node_id, traffic_type, tx_bytes, protocols, ports
		) VALUES (1700000000, 'node-a', 'node-b', 'virtual', 3, '[]', '[]');
	`); err != nil {
		t.Fatal(err)
	}
	if err := store.Init(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(migrationBackupPath(store.dbPath)); !os.IsNotExist(err) {
		t.Fatalf("skip flag still left a backup: %v", err)
	}
	var txBytes int64
	if err := store.db.QueryRowContext(ctx, `
		SELECT tx_bytes FROM node_pairs WHERE tailnet_id = ?
	`, DefaultTailnetID).Scan(&txBytes); err != nil {
		t.Fatal(err)
	}
	if txBytes != 3 {
		t.Fatalf("migrated tx_bytes = %d", txBytes)
	}
}

func TestTailnetMigrationStopsWhenBackupCannotBeWritten(t *testing.T) {
	store := openRawStore(t)
	ctx := context.Background()
	if _, err := store.db.ExecContext(ctx, `
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
	tmp := migrationBackupPath(store.dbPath) + ".tmp"
	if err := os.Mkdir(tmp, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tmp+"/keep", []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	err := store.Init(ctx)
	if err == nil || !strings.Contains(err.Error(), "refusing to migrate") {
		t.Fatalf("Init error = %v, want backup refusal", err)
	}
	hasTailnet, colErr := store.columnExists(ctx, "poll_state", "tailnet_id")
	if colErr != nil {
		t.Fatal(colErr)
	}
	if hasTailnet {
		t.Fatal("schema changed even though the backup could not be written")
	}
	var pollID int
	if err := store.db.QueryRowContext(ctx, `SELECT id FROM poll_state`).Scan(&pollID); err != nil {
		t.Fatalf("original poll_state was lost: %v", err)
	}
	if pollID != 1 {
		t.Fatalf("poll id = %d", pollID)
	}
}

func TestFreshDatabaseDoesNotWriteMigrationBackup(t *testing.T) {
	store := setupTestDB(t)
	if _, err := os.Stat(migrationBackupPath(store.dbPath)); !os.IsNotExist(err) {
		t.Fatalf("fresh database wrote a backup: %v", err)
	}
}

func fileSum(t *testing.T, path string) [32]byte {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(body) == 0 {
		t.Fatalf("backup %s is empty", path)
	}
	return sha256.Sum256(body)
}
