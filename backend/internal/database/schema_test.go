package database

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestNewSQLiteStore(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	store, err := NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	ctx := context.Background()
	if err := store.Init(ctx); err != nil {
		t.Fatal(err)
	}

	// Verify file was created
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		t.Error("database file was not created")
	}
}


func TestPollState(t *testing.T) {
	store := setupTestDB(t)
	ctx := context.Background()

	// Initial state should have zero time
	state, err := store.GetPollState(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !state.LastPollEnd.IsZero() {
		t.Errorf("expected zero last poll end, got %v", state.LastPollEnd)
	}

	// Update and verify
	now := time.Now().UTC().Truncate(time.Second)
	if err := store.UpdatePollState(ctx, now); err != nil {
		t.Fatal(err)
	}

	state, err = store.GetPollState(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if state.LastPollEnd.Truncate(time.Second) != now {
		t.Errorf("expected %v, got %v", now, state.LastPollEnd)
	}
}

func TestGetDataRange_Empty(t *testing.T) {
	store := setupTestDB(t)
	ctx := context.Background()

	dr, err := store.GetDataRange(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if dr.Count != 0 {
		t.Errorf("expected 0 count, got %d", dr.Count)
	}
}

func TestCleanup_EmptyDB(t *testing.T) {
	store := setupTestDB(t)
	ctx := context.Background()

	deleted, err := store.Cleanup(ctx, 24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if deleted != 0 {
		t.Errorf("expected 0 deleted on empty db, got %d", deleted)
	}
}

func setupTestDB(t *testing.T) *SQLiteStore {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	store, err := NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })

	if err := store.Init(context.Background()); err != nil {
		t.Fatal(err)
	}
	return store
}

func TestInit_Migration(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	store, err := NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	ctx := context.Background()

	// Manually create old-style minutely tables to simulate a pre-migration DB
	oldSchema := `
		CREATE TABLE node_pairs_minutely (
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
		INSERT INTO node_pairs_minutely VALUES (1000, 'a', 'b', 'virtual', 100, 50, 1, 1, 1, '[]', '[]');
		CREATE TABLE bandwidth_minutely (bucket INTEGER PRIMARY KEY, tx_bytes INTEGER DEFAULT 0, rx_bytes INTEGER DEFAULT 0);
		CREATE TABLE bandwidth_by_node_minutely (bucket INTEGER NOT NULL, node_id TEXT NOT NULL, tx_bytes INTEGER DEFAULT 0, rx_bytes INTEGER DEFAULT 0, PRIMARY KEY (bucket, node_id));
		CREATE TABLE traffic_stats_minutely (bucket INTEGER PRIMARY KEY, tcp_bytes INTEGER DEFAULT 0, udp_bytes INTEGER DEFAULT 0, other_proto_bytes INTEGER DEFAULT 0, virtual_bytes INTEGER DEFAULT 0, subnet_bytes INTEGER DEFAULT 0, physical_bytes INTEGER DEFAULT 0, total_flows INTEGER DEFAULT 0, unique_pairs INTEGER DEFAULT 0, top_ports TEXT DEFAULT '[]');
		CREATE TABLE poll_state (id INTEGER PRIMARY KEY CHECK (id = 1), last_poll_end DATETIME, updated_at DATETIME DEFAULT CURRENT_TIMESTAMP);
		INSERT OR IGNORE INTO poll_state VALUES (1, NULL, CURRENT_TIMESTAMP);
		CREATE TABLE node_pairs_hourly (bucket INTEGER, src_node_id TEXT, dst_node_id TEXT, traffic_type TEXT, tx_bytes INTEGER DEFAULT 0, rx_bytes INTEGER DEFAULT 0, tx_pkts INTEGER DEFAULT 0, rx_pkts INTEGER DEFAULT 0, flow_count INTEGER DEFAULT 0, protocols TEXT DEFAULT '[]', ports TEXT DEFAULT '[]', PRIMARY KEY (bucket, src_node_id, dst_node_id, traffic_type));
		CREATE TABLE node_pairs_daily  (bucket INTEGER, src_node_id TEXT, dst_node_id TEXT, traffic_type TEXT, tx_bytes INTEGER DEFAULT 0, rx_bytes INTEGER DEFAULT 0, tx_pkts INTEGER DEFAULT 0, rx_pkts INTEGER DEFAULT 0, flow_count INTEGER DEFAULT 0, protocols TEXT DEFAULT '[]', ports TEXT DEFAULT '[]', PRIMARY KEY (bucket, src_node_id, dst_node_id, traffic_type));
		CREATE TABLE flow_logs_current (id INTEGER PRIMARY KEY AUTOINCREMENT, logged_at DATETIME NOT NULL, node_id TEXT NOT NULL, traffic_type TEXT NOT NULL, protocol INTEGER DEFAULT 0, src_ip TEXT NOT NULL, src_port INTEGER DEFAULT 0, dst_ip TEXT NOT NULL, dst_port INTEGER DEFAULT 0, tx_bytes INTEGER DEFAULT 0, rx_bytes INTEGER DEFAULT 0, tx_pkts INTEGER DEFAULT 0, rx_pkts INTEGER DEFAULT 0);
	`
	if _, err := store.db.ExecContext(ctx, oldSchema); err != nil {
		t.Fatalf("failed to create old schema: %v", err)
	}

	// Run Init — this should migrate
	if err := store.Init(ctx); err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	// Verify new table exists and old data was preserved
	var count int64
	if err := store.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM node_pairs").Scan(&count); err != nil {
		t.Fatalf("node_pairs table missing after migration: %v", err)
	}
	if count != 1 {
		t.Errorf("expected 1 row in node_pairs after migration, got %d", count)
	}

	// Verify old tables were dropped
	for _, table := range []string{"node_pairs_minutely", "node_pairs_hourly", "node_pairs_daily", "flow_logs_current"} {
		var name string
		err := store.db.QueryRowContext(ctx,
			"SELECT name FROM sqlite_master WHERE type='table' AND name=?", table,
		).Scan(&name)
		if err == nil {
			t.Errorf("old table %q still exists after migration", table)
		}
	}
}

func TestInit_Idempotent(t *testing.T) {
	store := setupTestDB(t)
	ctx := context.Background()

	// Running Init a second time should not fail
	if err := store.Init(ctx); err != nil {
		t.Fatalf("second Init() failed: %v", err)
	}
}

func TestResolveBucketSize(t *testing.T) {
	cases := []struct {
		rangeSeconds int64
		want         int64
	}{
		{30 * 60, 60},
		{2 * 3600, 60},
		{2*3600 + 1, 3600},
		{48 * 3600, 3600},
		{48*3600 + 1, 86400},
		{30 * 24 * 3600, 86400},
	}
	for _, tc := range cases {
		got := resolveBucketSize(tc.rangeSeconds)
		if got != tc.want {
			t.Errorf("resolveBucketSize(%d) = %d, want %d", tc.rangeSeconds, got, tc.want)
		}
	}
}

func TestGetBandwidth_Bucketing(t *testing.T) {
	store := setupTestDB(t)
	ctx := context.Background()

	// Insert three 1-minute bandwidth rows
	base := int64(1000000)
	base = (base / 60) * 60
	for i := int64(0); i < 3; i++ {
		_, err := store.db.ExecContext(ctx,
			"INSERT INTO bandwidth (bucket, tx_bytes, rx_bytes) VALUES (?, ?, ?)",
			base+i*60, 100, 50,
		)
		if err != nil {
			t.Fatal(err)
		}
	}

	start := time.Unix(base, 0)
	end := time.Unix(base+3*60, 0)

	// Small range (≤2h) → should return 3 individual 1-min buckets
	buckets, err := store.GetBandwidth(ctx, start, end)
	if err != nil {
		t.Fatal(err)
	}
	if len(buckets) != 3 {
		t.Errorf("expected 3 buckets for small range, got %d", len(buckets))
	}

	total := int64(0)
	for _, b := range buckets {
		total += b.TxBytes
	}
	if total != 300 {
		t.Errorf("expected total tx=300, got %d", total)
	}
}
