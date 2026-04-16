# Flat Storage Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the 13-table tiered aggregation scheme with 5 flat tables, retaining full per-minute fidelity and applying coarser bucketing at query time.

**Architecture:** Drop all `_hourly` and `_daily` tables plus `flow_logs_current`. Rename `*_minutely` tables to their base names. All queries hit exactly one table; `GetBandwidth`/`GetNodeBandwidth`/`GetTrafficStats` bucket with `(bucket/N)*N` in SQL when the requested window is wide. Single `TSFLOW_RETENTION` env var (default 30 days) replaces three separate retention vars.

**Tech Stack:** Go, SQLite (modernc.org/sqlite), Gin

---

### Task 1: Simplify the Store interface

**Files:**
- Modify: `backend/internal/database/models.go`
- Modify: `backend/internal/database/schema_test.go`

- [ ] **Step 1: Update `TestCleanup_EmptyDB` to use the new single-arg signature**

In `schema_test.go`, change:

```go
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
```

- [ ] **Step 2: Run the test to confirm it fails to compile**

```bash
cd backend && go test ./internal/database/ 2>&1 | head -20
```

Expected: compile error about wrong number of arguments to `Cleanup`.

- [ ] **Step 3: Update the `Store` interface in `models.go`**

Replace the raw-log section and the affected signatures:

```go
// Store defines the interface for flow log storage
type Store interface {
	Init(ctx context.Context) error
	Close() error

	// Pre-aggregated data operations
	UpsertNodePairAggregates(ctx context.Context, aggregates []NodePairAggregate) error
	GetNodePairAggregates(ctx context.Context, start, end time.Time, bucketSize int64) ([]NodePairAggregate, error)

	// Bandwidth operations
	UpsertBandwidth(ctx context.Context, buckets []BandwidthBucket) error
	UpsertNodeBandwidth(ctx context.Context, buckets []NodeBandwidth) error
	GetBandwidth(ctx context.Context, start, end time.Time) ([]BandwidthBucket, error)
	GetNodeBandwidth(ctx context.Context, start, end time.Time, nodeID string) ([]BandwidthBucket, error)

	// Traffic stats operations
	UpsertTrafficStats(ctx context.Context, stats []TrafficStats) error
	GetTrafficStats(ctx context.Context, start, end time.Time) ([]TrafficStats, error)
	GetTrafficStatsFromNodePairs(ctx context.Context, start, end time.Time) ([]TrafficStats, error)
	GetTopTalkers(ctx context.Context, start, end time.Time, limit int) ([]TopTalker, error)
	GetTopPairs(ctx context.Context, start, end time.Time, limit int) ([]TopPair, error)
	GetNodeStats(ctx context.Context, nodeID string, start, end time.Time) (*NodeDetailStats, error)

	// Atomic poll commit
	CommitPollResults(ctx context.Context, results PollResults) error

	// State operations
	GetPollState(ctx context.Context) (*PollState, error)
	UpdatePollState(ctx context.Context, lastPollEnd time.Time) error
	GetDataRange(ctx context.Context) (*DataRange, error)

	// Maintenance
	Cleanup(ctx context.Context, retention time.Duration) (int64, error)
	GetStats(ctx context.Context) (map[string]any, error)
}
```

Also remove the `FlowLog` type and `InsertFlowLogs`/`GetRecentFlowLogs`/`GetFlowLogsInRange` from the interface (keep `FlowLog` struct if it appears elsewhere; remove it from `Store`).

- [ ] **Step 4: Run the test again**

```bash
cd backend && go test ./internal/database/ 2>&1 | head -20
```

Expected: compile errors in `aggregate_queries.go` and `maintenance.go` about missing methods — that's expected. The interface change itself is correct.

- [ ] **Step 5: Commit the interface change**

```bash
git add backend/internal/database/models.go backend/internal/database/schema_test.go
git commit -m "refactor: simplify Store interface - flat tables, single retention"
```

---

### Task 2: New flat schema with migration

**Files:**
- Modify: `backend/internal/database/schema.go`
- Modify: `backend/internal/database/schema_test.go`

- [ ] **Step 1: Write failing migration tests**

Append to `schema_test.go`:

```go
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
```

- [ ] **Step 2: Run tests to confirm they fail**

```bash
cd backend && go test ./internal/database/ -run TestInit_Migration -v 2>&1
```

Expected: FAIL (node_pairs table not found, Init doesn't do migration yet).

- [ ] **Step 3: Rewrite `Init()` in `schema.go`**

Replace the entire body of `Init()`:

```go
func (s *SQLiteStore) Init(ctx context.Context) error {
	// Step 1: Migrate minutely tables to flat names (idempotent).
	// ALTER TABLE fails if source is absent or target already exists — both are OK.
	for _, m := range [][2]string{
		{"node_pairs_minutely", "node_pairs"},
		{"bandwidth_minutely", "bandwidth"},
		{"bandwidth_by_node_minutely", "bandwidth_by_node"},
		{"traffic_stats_minutely", "traffic_stats"},
	} {
		_, _ = s.db.ExecContext(ctx, fmt.Sprintf("ALTER TABLE %s RENAME TO %s", m[0], m[1]))
	}

	// Step 2: Drop old tier tables and the ephemeral raw-log table.
	for _, table := range []string{
		"node_pairs_hourly", "node_pairs_daily",
		"bandwidth_hourly", "bandwidth_daily",
		"bandwidth_by_node_hourly", "bandwidth_by_node_daily",
		"traffic_stats_hourly", "traffic_stats_daily",
		"flow_logs_current",
	} {
		_, _ = s.db.ExecContext(ctx, fmt.Sprintf("DROP TABLE IF EXISTS %s", table))
	}

	// Step 3: Create flat tables (IF NOT EXISTS handles fresh installs and post-migration runs).
	schema := `
	CREATE TABLE IF NOT EXISTS node_pairs (
		bucket       INTEGER NOT NULL,
		src_node_id  TEXT    NOT NULL,
		dst_node_id  TEXT    NOT NULL,
		traffic_type TEXT    NOT NULL,
		tx_bytes     INTEGER DEFAULT 0,
		rx_bytes     INTEGER DEFAULT 0,
		tx_pkts      INTEGER DEFAULT 0,
		rx_pkts      INTEGER DEFAULT 0,
		flow_count   INTEGER DEFAULT 0,
		protocols    TEXT    DEFAULT '[]',
		ports        TEXT    DEFAULT '[]',
		PRIMARY KEY (bucket, src_node_id, dst_node_id, traffic_type)
	);
	CREATE INDEX IF NOT EXISTS idx_node_pairs_bucket ON node_pairs(bucket);
	CREATE INDEX IF NOT EXISTS idx_node_pairs_src    ON node_pairs(src_node_id, bucket);
	CREATE INDEX IF NOT EXISTS idx_node_pairs_dst    ON node_pairs(dst_node_id, bucket);

	CREATE TABLE IF NOT EXISTS bandwidth (
		bucket   INTEGER PRIMARY KEY,
		tx_bytes INTEGER DEFAULT 0,
		rx_bytes INTEGER DEFAULT 0
	);

	CREATE TABLE IF NOT EXISTS bandwidth_by_node (
		bucket   INTEGER NOT NULL,
		node_id  TEXT    NOT NULL,
		tx_bytes INTEGER DEFAULT 0,
		rx_bytes INTEGER DEFAULT 0,
		PRIMARY KEY (bucket, node_id)
	);
	CREATE INDEX IF NOT EXISTS idx_bandwidth_by_node ON bandwidth_by_node(node_id, bucket);

	CREATE TABLE IF NOT EXISTS traffic_stats (
		bucket           INTEGER PRIMARY KEY,
		tcp_bytes        INTEGER DEFAULT 0,
		udp_bytes        INTEGER DEFAULT 0,
		other_proto_bytes INTEGER DEFAULT 0,
		virtual_bytes    INTEGER DEFAULT 0,
		subnet_bytes     INTEGER DEFAULT 0,
		physical_bytes   INTEGER DEFAULT 0,
		total_flows      INTEGER DEFAULT 0,
		unique_pairs     INTEGER DEFAULT 0,
		top_ports        TEXT    DEFAULT '[]'
	);

	CREATE TABLE IF NOT EXISTS poll_state (
		id           INTEGER PRIMARY KEY CHECK (id = 1),
		last_poll_end DATETIME,
		updated_at   DATETIME DEFAULT CURRENT_TIMESTAMP
	);
	INSERT OR IGNORE INTO poll_state (id, last_poll_end, updated_at) VALUES (1, NULL, CURRENT_TIMESTAMP);
	`

	if _, err := s.db.ExecContext(ctx, schema); err != nil {
		return fmt.Errorf("failed to create schema: %w", err)
	}

	log.Printf("Database initialized at %s", s.dbPath)
	return nil
}
```

- [ ] **Step 4: Run migration tests**

```bash
cd backend && go test ./internal/database/ -run "TestInit|TestNewSQLiteStore|TestPollState|TestGetDataRange|TestCleanup" -v 2>&1
```

Expected: all pass (compile errors in aggregate_queries.go are expected — those get fixed in Task 3).

- [ ] **Step 5: Commit**

```bash
git add backend/internal/database/schema.go backend/internal/database/schema_test.go
git commit -m "feat: flat schema with migration from tiered tables"
```

---

### Task 3: Rewrite aggregate_queries.go

**Files:**
- Modify: `backend/internal/database/aggregate_queries.go`
- Modify: `backend/internal/database/schema_test.go`

- [ ] **Step 1: Write failing tests for `resolveBucketSize` and bucketed `GetBandwidth`**

Append to `schema_test.go`:

```go
func TestResolveBucketSize(t *testing.T) {
	cases := []struct {
		rangeSeconds int64
		want         int64
	}{
		{30 * 60, 60},           // 30 min → 1-min buckets
		{2 * 3600, 60},          // exactly 2h → 1-min buckets
		{2*3600 + 1, 3600},      // just over 2h → 1-hour buckets
		{48 * 3600, 3600},       // exactly 48h → 1-hour buckets
		{48*3600 + 1, 86400},    // just over 48h → 1-day buckets
		{30 * 24 * 3600, 86400}, // 30 days → 1-day buckets
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

	// Insert three 1-minute bandwidth rows spanning 3 minutes
	base := int64(1000000) // arbitrary unix timestamp aligned to minute
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

	// Verify bytes are not double-counted
	total := int64(0)
	for _, b := range buckets {
		total += b.TxBytes
	}
	if total != 300 {
		t.Errorf("expected total tx=300, got %d", total)
	}
}
```

- [ ] **Step 2: Run to confirm failures**

```bash
cd backend && go test ./internal/database/ -run "TestResolveBucketSize|TestGetBandwidth_Bucketing" -v 2>&1
```

Expected: compile error (resolveBucketSize not defined yet).

- [ ] **Step 3: Rewrite `aggregate_queries.go`**

Replace the entire file:

```go
package database

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"time"
)

// resolveBucketSize returns the SQL grouping interval in seconds for a query window.
//
//	≤ 2 hours  → 60 s  (1-minute buckets, raw)
//	≤ 48 hours → 3600 s (1-hour buckets)
//	otherwise  → 86400 s (1-day buckets)
func resolveBucketSize(rangeSeconds int64) int64 {
	if rangeSeconds <= 2*3600 {
		return 60
	}
	if rangeSeconds <= 48*3600 {
		return 3600
	}
	return 86400
}

// CommitPollResults atomically writes all aggregates and updates poll state.
func (s *SQLiteStore) CommitPollResults(ctx context.Context, results PollResults) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	if err := upsertNodePairsTx(ctx, tx, results.NodePairs); err != nil {
		return err
	}
	if err := upsertBandwidthTx(ctx, tx, results.Bandwidth); err != nil {
		return err
	}
	if err := upsertNodeBandwidthTx(ctx, tx, results.NodeBandwidth); err != nil {
		return err
	}
	if err := upsertTrafficStatsTx(ctx, tx, results.TrafficStats); err != nil {
		return err
	}

	const sqliteFormat = "2006-01-02 15:04:05"
	_, err = tx.ExecContext(ctx,
		"UPDATE poll_state SET last_poll_end = ?, updated_at = CURRENT_TIMESTAMP WHERE id = 1",
		results.PollEnd.UTC().Format(sqliteFormat),
	)
	if err != nil {
		return fmt.Errorf("failed to update poll state: %w", err)
	}

	return tx.Commit()
}

func upsertNodePairsTx(ctx context.Context, tx *sql.Tx, aggregates []NodePairAggregate) error {
	if len(aggregates) == 0 {
		return nil
	}

	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO node_pairs (bucket, src_node_id, dst_node_id, traffic_type,
		                        tx_bytes, rx_bytes, tx_pkts, rx_pkts, flow_count, protocols, ports)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(bucket, src_node_id, dst_node_id, traffic_type) DO UPDATE SET
			tx_bytes   = tx_bytes   + excluded.tx_bytes,
			rx_bytes   = rx_bytes   + excluded.rx_bytes,
			tx_pkts    = tx_pkts    + excluded.tx_pkts,
			rx_pkts    = rx_pkts    + excluded.rx_pkts,
			flow_count = flow_count + excluded.flow_count,
			protocols  = (SELECT json_group_array(value) FROM (
			                 SELECT value FROM json_each(node_pairs.protocols)
			                 UNION
			                 SELECT value FROM json_each(excluded.protocols)
			              )),
			ports = excluded.ports
	`)
	if err != nil {
		return fmt.Errorf("failed to prepare node_pairs upsert: %w", err)
	}
	defer stmt.Close()

	const bucketSize = int64(60)
	for _, agg := range aggregates {
		bucket := (agg.Bucket / bucketSize) * bucketSize
		if _, err := stmt.ExecContext(ctx,
			bucket, agg.SrcNodeID, agg.DstNodeID, agg.TrafficType,
			agg.TxBytes, agg.RxBytes, agg.TxPkts, agg.RxPkts,
			agg.FlowCount, agg.Protocols, agg.Ports,
		); err != nil {
			return fmt.Errorf("failed to upsert node pair: %w", err)
		}
	}
	return nil
}

func upsertBandwidthTx(ctx context.Context, tx *sql.Tx, buckets []BandwidthBucket) error {
	if len(buckets) == 0 {
		return nil
	}

	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO bandwidth (bucket, tx_bytes, rx_bytes) VALUES (?, ?, ?)
		ON CONFLICT(bucket) DO UPDATE SET
			tx_bytes = tx_bytes + excluded.tx_bytes,
			rx_bytes = rx_bytes + excluded.rx_bytes
	`)
	if err != nil {
		return fmt.Errorf("failed to prepare bandwidth upsert: %w", err)
	}
	defer stmt.Close()

	const bucketSize = int64(60)
	for _, b := range buckets {
		bucket := (b.Time.UTC().Unix() / bucketSize) * bucketSize
		if _, err := stmt.ExecContext(ctx, bucket, b.TxBytes, b.RxBytes); err != nil {
			return fmt.Errorf("failed to upsert bandwidth: %w", err)
		}
	}
	return nil
}

func upsertNodeBandwidthTx(ctx context.Context, tx *sql.Tx, buckets []NodeBandwidth) error {
	if len(buckets) == 0 {
		return nil
	}

	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO bandwidth_by_node (bucket, node_id, tx_bytes, rx_bytes) VALUES (?, ?, ?, ?)
		ON CONFLICT(bucket, node_id) DO UPDATE SET
			tx_bytes = tx_bytes + excluded.tx_bytes,
			rx_bytes = rx_bytes + excluded.rx_bytes
	`)
	if err != nil {
		return fmt.Errorf("failed to prepare bandwidth_by_node upsert: %w", err)
	}
	defer stmt.Close()

	const bucketSize = int64(60)
	for _, b := range buckets {
		bucket := (b.Bucket / bucketSize) * bucketSize
		if _, err := stmt.ExecContext(ctx, bucket, b.NodeID, b.TxBytes, b.RxBytes); err != nil {
			return fmt.Errorf("failed to upsert node bandwidth: %w", err)
		}
	}
	return nil
}

func upsertTrafficStatsTx(ctx context.Context, tx *sql.Tx, stats []TrafficStats) error {
	if len(stats) == 0 {
		return nil
	}

	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO traffic_stats (bucket, tcp_bytes, udp_bytes, other_proto_bytes,
		                           virtual_bytes, subnet_bytes, physical_bytes,
		                           total_flows, unique_pairs, top_ports)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(bucket) DO UPDATE SET
			tcp_bytes         = tcp_bytes         + excluded.tcp_bytes,
			udp_bytes         = udp_bytes         + excluded.udp_bytes,
			other_proto_bytes = other_proto_bytes + excluded.other_proto_bytes,
			virtual_bytes     = virtual_bytes     + excluded.virtual_bytes,
			subnet_bytes      = subnet_bytes      + excluded.subnet_bytes,
			physical_bytes    = physical_bytes    + excluded.physical_bytes,
			total_flows       = total_flows       + excluded.total_flows,
			unique_pairs      = MAX(unique_pairs, excluded.unique_pairs),
			top_ports         = excluded.top_ports
	`)
	if err != nil {
		return fmt.Errorf("failed to prepare traffic_stats upsert: %w", err)
	}
	defer stmt.Close()

	const bucketSize = int64(60)
	for _, st := range stats {
		bucket := (st.Bucket / bucketSize) * bucketSize
		if _, err := stmt.ExecContext(ctx,
			bucket, st.TCPBytes, st.UDPBytes, st.OtherProtoBytes,
			st.VirtualBytes, st.SubnetBytes, st.PhysicalBytes,
			st.TotalFlows, st.UniquePairs, st.TopPorts,
		); err != nil {
			return fmt.Errorf("failed to upsert traffic stats: %w", err)
		}
	}
	return nil
}

// UpsertNodePairAggregates upserts node-pair aggregates into node_pairs.
func (s *SQLiteStore) UpsertNodePairAggregates(ctx context.Context, aggregates []NodePairAggregate) error {
	if len(aggregates) == 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()
	if err := upsertNodePairsTx(ctx, tx, aggregates); err != nil {
		return err
	}
	return tx.Commit()
}

// GetNodePairAggregates retrieves node-pair aggregates for a time range.
func (s *SQLiteStore) GetNodePairAggregates(ctx context.Context, start, end time.Time, bucketSize int64) ([]NodePairAggregate, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	startUnix := start.UTC().Unix()
	endUnix := end.UTC().Unix()
	if startUnix >= endUnix {
		return nil, fmt.Errorf("invalid time range: start (%v) must be before end (%v)", start, end)
	}

	query := `
		SELECT MIN(bucket), src_node_id, dst_node_id, traffic_type,
		       SUM(tx_bytes), SUM(rx_bytes), SUM(tx_pkts), SUM(rx_pkts),
		       SUM(flow_count),
		       COALESCE((SELECT protocols FROM node_pairs sub
		                 WHERE sub.src_node_id = main.src_node_id
		                   AND sub.dst_node_id = main.dst_node_id
		                   AND sub.traffic_type = main.traffic_type
		                   AND sub.bucket >= ? AND sub.bucket <= ?
		                 ORDER BY sub.bucket DESC LIMIT 1), '[]'),
		       COALESCE((SELECT ports FROM node_pairs sub
		                 WHERE sub.src_node_id = main.src_node_id
		                   AND sub.dst_node_id = main.dst_node_id
		                   AND sub.traffic_type = main.traffic_type
		                   AND sub.bucket >= ? AND sub.bucket <= ?
		                 ORDER BY sub.bucket DESC LIMIT 1), '[]')
		FROM node_pairs main
		WHERE bucket >= ? AND bucket <= ?
		GROUP BY src_node_id, dst_node_id, traffic_type
		ORDER BY SUM(tx_bytes) + SUM(rx_bytes) DESC
	`
	rows, err := s.db.QueryContext(ctx, query,
		startUnix, endUnix,
		startUnix, endUnix,
		startUnix, endUnix,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to query node pairs: %w", err)
	}
	defer rows.Close()

	var aggregates []NodePairAggregate
	for rows.Next() {
		var agg NodePairAggregate
		if err := rows.Scan(
			&agg.Bucket, &agg.SrcNodeID, &agg.DstNodeID, &agg.TrafficType,
			&agg.TxBytes, &agg.RxBytes, &agg.TxPkts, &agg.RxPkts,
			&agg.FlowCount, &agg.Protocols, &agg.Ports,
		); err != nil {
			return nil, fmt.Errorf("failed to scan node pair: %w", err)
		}
		aggregates = append(aggregates, agg)
	}
	return aggregates, rows.Err()
}

// UpsertBandwidth upserts total bandwidth into bandwidth.
func (s *SQLiteStore) UpsertBandwidth(ctx context.Context, buckets []BandwidthBucket) error {
	if len(buckets) == 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()
	if err := upsertBandwidthTx(ctx, tx, buckets); err != nil {
		return err
	}
	return tx.Commit()
}

// UpsertNodeBandwidth upserts per-node bandwidth into bandwidth_by_node.
func (s *SQLiteStore) UpsertNodeBandwidth(ctx context.Context, buckets []NodeBandwidth) error {
	if len(buckets) == 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()
	if err := upsertNodeBandwidthTx(ctx, tx, buckets); err != nil {
		return err
	}
	return tx.Commit()
}

// GetBandwidth retrieves total bandwidth for a time range, bucketed by window size.
func (s *SQLiteStore) GetBandwidth(ctx context.Context, start, end time.Time) ([]BandwidthBucket, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	startUnix := start.UTC().Unix()
	endUnix := end.UTC().Unix()
	if startUnix >= endUnix {
		return nil, fmt.Errorf("invalid time range: start (%v) must be before end (%v)", start, end)
	}

	bs := resolveBucketSize(endUnix - startUnix)
	query := fmt.Sprintf(`
		SELECT (bucket / %d) * %d AS b, SUM(tx_bytes), SUM(rx_bytes)
		FROM bandwidth
		WHERE bucket >= ? AND bucket <= ?
		GROUP BY b
		ORDER BY b ASC
	`, bs, bs)

	rows, err := s.db.QueryContext(ctx, query, startUnix, endUnix)
	if err != nil {
		return nil, fmt.Errorf("failed to query bandwidth: %w", err)
	}
	defer rows.Close()

	var result []BandwidthBucket
	for rows.Next() {
		var bucket int64
		var b BandwidthBucket
		if err := rows.Scan(&bucket, &b.TxBytes, &b.RxBytes); err != nil {
			return nil, fmt.Errorf("failed to scan bandwidth bucket: %w", err)
		}
		b.Time = time.Unix(bucket, 0).UTC()
		result = append(result, b)
	}
	return result, rows.Err()
}

// GetNodeBandwidth retrieves bandwidth for a specific node, bucketed by window size.
func (s *SQLiteStore) GetNodeBandwidth(ctx context.Context, start, end time.Time, nodeID string) ([]BandwidthBucket, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	startUnix := start.UTC().Unix()
	endUnix := end.UTC().Unix()
	if startUnix >= endUnix {
		return nil, fmt.Errorf("invalid time range: start (%v) must be before end (%v)", start, end)
	}

	bs := resolveBucketSize(endUnix - startUnix)
	query := fmt.Sprintf(`
		SELECT (bucket / %d) * %d AS b, SUM(tx_bytes), SUM(rx_bytes)
		FROM bandwidth_by_node
		WHERE bucket >= ? AND bucket <= ? AND node_id = ?
		GROUP BY b
		ORDER BY b ASC
	`, bs, bs)

	rows, err := s.db.QueryContext(ctx, query, startUnix, endUnix, nodeID)
	if err != nil {
		return nil, fmt.Errorf("failed to query node bandwidth: %w", err)
	}
	defer rows.Close()

	var result []BandwidthBucket
	for rows.Next() {
		var bucket int64
		var b BandwidthBucket
		if err := rows.Scan(&bucket, &b.TxBytes, &b.RxBytes); err != nil {
			return nil, fmt.Errorf("failed to scan node bandwidth bucket: %w", err)
		}
		b.Time = time.Unix(bucket, 0).UTC()
		result = append(result, b)
	}
	return result, rows.Err()
}

// UpsertTrafficStats upserts network-wide traffic statistics into traffic_stats.
func (s *SQLiteStore) UpsertTrafficStats(ctx context.Context, stats []TrafficStats) error {
	if len(stats) == 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()
	if err := upsertTrafficStatsTx(ctx, tx, stats); err != nil {
		return err
	}
	return tx.Commit()
}

// GetTrafficStats retrieves network-wide traffic statistics for a time range, bucketed by window size.
func (s *SQLiteStore) GetTrafficStats(ctx context.Context, start, end time.Time) ([]TrafficStats, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	startUnix := start.UTC().Unix()
	endUnix := end.UTC().Unix()
	if startUnix >= endUnix {
		return nil, fmt.Errorf("invalid time range: start (%v) must be before end (%v)", start, end)
	}

	bs := resolveBucketSize(endUnix - startUnix)
	// top_ports: SQLite picks an arbitrary row's value within each group — acceptable for coarse buckets.
	query := fmt.Sprintf(`
		SELECT (bucket / %d) * %d AS b,
		       SUM(tcp_bytes), SUM(udp_bytes), SUM(other_proto_bytes),
		       SUM(virtual_bytes), SUM(subnet_bytes), SUM(physical_bytes),
		       SUM(total_flows), MAX(unique_pairs), top_ports
		FROM traffic_stats
		WHERE bucket >= ? AND bucket <= ?
		GROUP BY b
		ORDER BY b ASC
	`, bs, bs)

	rows, err := s.db.QueryContext(ctx, query, startUnix, endUnix)
	if err != nil {
		return nil, fmt.Errorf("failed to query traffic stats: %w", err)
	}
	defer rows.Close()

	var results []TrafficStats
	for rows.Next() {
		var st TrafficStats
		if err := rows.Scan(
			&st.Bucket, &st.TCPBytes, &st.UDPBytes, &st.OtherProtoBytes,
			&st.VirtualBytes, &st.SubnetBytes, &st.PhysicalBytes,
			&st.TotalFlows, &st.UniquePairs, &st.TopPorts,
		); err != nil {
			return nil, fmt.Errorf("failed to scan traffic stats: %w", err)
		}
		results = append(results, st)
	}
	return results, rows.Err()
}

// GetTrafficStatsFromNodePairs synthesizes traffic stats from node_pairs (fallback for old data).
func (s *SQLiteStore) GetTrafficStatsFromNodePairs(ctx context.Context, start, end time.Time) ([]TrafficStats, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	startUnix := start.UTC().Unix()
	endUnix := end.UTC().Unix()
	if startUnix >= endUnix {
		return nil, fmt.Errorf("invalid time range: start (%v) must be before end (%v)", start, end)
	}

	bs := resolveBucketSize(endUnix - startUnix)
	query := fmt.Sprintf(`
		SELECT (bucket / %d) * %d AS b, traffic_type,
		       SUM(tx_bytes + rx_bytes) AS total_bytes,
		       SUM(flow_count) AS total_flows,
		       COUNT(DISTINCT src_node_id || '|' || dst_node_id) AS unique_pairs
		FROM node_pairs
		WHERE bucket >= ? AND bucket <= ?
		GROUP BY b, traffic_type
		ORDER BY b ASC
	`, bs, bs)

	rows, err := s.db.QueryContext(ctx, query, startUnix, endUnix)
	if err != nil {
		return nil, fmt.Errorf("failed to query node pairs for traffic stats: %w", err)
	}
	defer rows.Close()

	bucketMap := make(map[int64]*TrafficStats)
	for rows.Next() {
		var bucket int64
		var trafficType string
		var totalBytes, totalFlows, uniquePairs int64
		if err := rows.Scan(&bucket, &trafficType, &totalBytes, &totalFlows, &uniquePairs); err != nil {
			return nil, fmt.Errorf("failed to scan: %w", err)
		}
		st, ok := bucketMap[bucket]
		if !ok {
			st = &TrafficStats{Bucket: bucket, TopPorts: "[]"}
			bucketMap[bucket] = st
		}
		switch trafficType {
		case "virtual", "exit":
			st.VirtualBytes += totalBytes
		case "subnet":
			st.SubnetBytes += totalBytes
		case "physical":
			st.PhysicalBytes += totalBytes
		}
		st.TotalFlows += totalFlows
		if uniquePairs > st.UniquePairs {
			st.UniquePairs = uniquePairs
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Derive protocol breakdown from protocols JSON column
	protoQuery := fmt.Sprintf(`
		SELECT (bucket / %d) * %d AS b, protocols, SUM(tx_bytes + rx_bytes)
		FROM node_pairs
		WHERE bucket >= ? AND bucket <= ? AND traffic_type != 'physical'
		GROUP BY b, protocols
	`, bs, bs)
	protoRows, err := s.db.QueryContext(ctx, protoQuery, startUnix, endUnix)
	if err == nil {
		for protoRows.Next() {
			var b int64
			var protosJSON string
			var totalBytes int64
			if err := protoRows.Scan(&b, &protosJSON, &totalBytes); err != nil {
				continue
			}
			st, ok := bucketMap[b]
			if !ok {
				continue
			}
			var protos []int
			if err := json.Unmarshal([]byte(protosJSON), &protos); err != nil || len(protos) == 0 {
				continue
			}
			perProto := totalBytes / int64(len(protos))
			rem := totalBytes - perProto*int64(len(protos))
			for i, p := range protos {
				share := perProto
				if i == 0 {
					share += rem
				}
				switch p {
				case 6:
					st.TCPBytes += share
				case 17:
					st.UDPBytes += share
				default:
					st.OtherProtoBytes += share
				}
			}
		}
		protoRows.Close()
	}

	results := make([]TrafficStats, 0, len(bucketMap))
	for _, st := range bucketMap {
		results = append(results, *st)
	}
	sort.Slice(results, func(i, j int) bool { return results[i].Bucket < results[j].Bucket })
	return results, nil
}

// GetTopTalkers returns nodes ranked by total traffic volume.
func (s *SQLiteStore) GetTopTalkers(ctx context.Context, start, end time.Time, limit int) ([]TopTalker, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	startUnix := start.UTC().Unix()
	endUnix := end.UTC().Unix()
	if startUnix >= endUnix {
		return nil, fmt.Errorf("invalid time range: start (%v) must be before end (%v)", start, end)
	}
	if limit <= 0 {
		limit = 10
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT node_id, SUM(tx_bytes), SUM(rx_bytes), SUM(tx_bytes + rx_bytes) AS total
		FROM bandwidth_by_node
		WHERE bucket >= ? AND bucket <= ?
		GROUP BY node_id
		ORDER BY total DESC
		LIMIT ?
	`, startUnix, endUnix, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to query top talkers: %w", err)
	}
	defer rows.Close()

	var results []TopTalker
	for rows.Next() {
		var t TopTalker
		if err := rows.Scan(&t.NodeID, &t.TxBytes, &t.RxBytes, &t.TotalBytes); err != nil {
			return nil, fmt.Errorf("failed to scan top talker: %w", err)
		}
		results = append(results, t)
	}
	return results, rows.Err()
}

// GetTopPairs returns node pairs ranked by total traffic volume.
func (s *SQLiteStore) GetTopPairs(ctx context.Context, start, end time.Time, limit int) ([]TopPair, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	startUnix := start.UTC().Unix()
	endUnix := end.UTC().Unix()
	if startUnix >= endUnix {
		return nil, fmt.Errorf("invalid time range: start (%v) must be before end (%v)", start, end)
	}
	if limit <= 0 {
		limit = 10
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT src_node_id, dst_node_id,
		       SUM(tx_bytes), SUM(rx_bytes),
		       SUM(tx_bytes + rx_bytes) AS total, SUM(flow_count)
		FROM node_pairs
		WHERE bucket >= ? AND bucket <= ?
		GROUP BY src_node_id, dst_node_id
		ORDER BY total DESC
		LIMIT ?
	`, startUnix, endUnix, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to query top pairs: %w", err)
	}
	defer rows.Close()

	var results []TopPair
	for rows.Next() {
		var p TopPair
		if err := rows.Scan(&p.SrcNodeID, &p.DstNodeID, &p.TxBytes, &p.RxBytes, &p.TotalBytes, &p.FlowCount); err != nil {
			return nil, fmt.Errorf("failed to scan top pair: %w", err)
		}
		results = append(results, p)
	}
	return results, rows.Err()
}

// GetNodeStats returns detailed traffic statistics for a single node.
func (s *SQLiteStore) GetNodeStats(ctx context.Context, nodeID string, start, end time.Time) (*NodeDetailStats, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	startUnix := start.UTC().Unix()
	endUnix := end.UTC().Unix()
	if startUnix >= endUnix {
		return nil, fmt.Errorf("invalid time range: start (%v) must be before end (%v)", start, end)
	}

	result := &NodeDetailStats{
		NodeID:   nodeID,
		TopPeers: make([]TopPair, 0),
		TopPorts: make([]PortStat, 0),
	}

	if err := s.db.QueryRowContext(ctx, `
		SELECT COALESCE(SUM(tx_bytes), 0), COALESCE(SUM(rx_bytes), 0)
		FROM bandwidth_by_node
		WHERE node_id = ? AND bucket >= ? AND bucket <= ?
	`, nodeID, startUnix, endUnix).Scan(&result.TotalTx, &result.TotalRx); err != nil {
		return nil, fmt.Errorf("failed to query node bandwidth: %w", err)
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT peer_id, SUM(tx), SUM(rx), SUM(tx+rx) AS total, SUM(fc)
		FROM (
			SELECT dst_node_id AS peer_id, SUM(tx_bytes) AS tx, SUM(rx_bytes) AS rx, SUM(flow_count) AS fc
			FROM node_pairs
			WHERE src_node_id = ? AND bucket >= ? AND bucket <= ?
			GROUP BY dst_node_id
			UNION ALL
			SELECT src_node_id AS peer_id, SUM(rx_bytes) AS tx, SUM(tx_bytes) AS rx, SUM(flow_count) AS fc
			FROM node_pairs
			WHERE dst_node_id = ? AND bucket >= ? AND bucket <= ?
			GROUP BY src_node_id
		)
		GROUP BY peer_id
		ORDER BY total DESC
		LIMIT 10
	`, nodeID, startUnix, endUnix, nodeID, startUnix, endUnix)
	if err != nil {
		return nil, fmt.Errorf("failed to query node peers: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var p TopPair
		if err := rows.Scan(&p.DstNodeID, &p.TxBytes, &p.RxBytes, &p.TotalBytes, &p.FlowCount); err != nil {
			return nil, fmt.Errorf("failed to scan peer: %w", err)
		}
		p.SrcNodeID = nodeID
		result.TopPeers = append(result.TopPeers, p)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	portRows, err := s.db.QueryContext(ctx, `
		SELECT ports FROM node_pairs
		WHERE (src_node_id = ? OR dst_node_id = ?)
		  AND bucket >= ? AND bucket <= ?
		  AND ports != '[]'
	`, nodeID, nodeID, startUnix, endUnix)
	if err != nil {
		return nil, fmt.Errorf("failed to query node ports: %w", err)
	}
	defer portRows.Close()

	type protoPortKey struct{ proto, port int }
	portAgg := make(map[protoPortKey]int64)
	for portRows.Next() {
		var portsJSON string
		if err := portRows.Scan(&portsJSON); err != nil {
			continue
		}
		var entries []PortStat
		if err := json.Unmarshal([]byte(portsJSON), &entries); err != nil {
			continue
		}
		for _, e := range entries {
			portAgg[protoPortKey{e.Proto, e.Port}] += e.Bytes
		}
	}
	for ppk, bytes := range portAgg {
		switch ppk.proto {
		case 6:
			result.TCPBytes += bytes
		case 17:
			result.UDPBytes += bytes
		default:
			result.OtherBytes += bytes
		}
		result.TopPorts = append(result.TopPorts, PortStat{Port: ppk.port, Proto: ppk.proto, Bytes: bytes})
	}
	sort.Slice(result.TopPorts, func(i, j int) bool { return result.TopPorts[i].Bytes > result.TopPorts[j].Bytes })
	if len(result.TopPorts) > 15 {
		result.TopPorts = result.TopPorts[:15]
	}

	return result, nil
}
```

- [ ] **Step 4: Run tests**

```bash
cd backend && go test ./internal/database/ -v 2>&1
```

Expected: `TestResolveBucketSize`, `TestGetBandwidth_Bucketing`, and the schema tests all pass. Compile errors may remain in `maintenance.go` (fixed next task) and `services/` (fixed in Task 6).

- [ ] **Step 5: Commit**

```bash
git add backend/internal/database/aggregate_queries.go backend/internal/database/schema_test.go
git commit -m "feat: rewrite aggregate_queries with flat tables and query-time bucketing"
```

---

### Task 4: Simplify maintenance.go

**Files:**
- Modify: `backend/internal/database/maintenance.go`

No new tests needed — `TestCleanup_EmptyDB` (updated in Task 1) and `TestGetDataRange_Empty` already cover the contracts.

- [ ] **Step 1: Rewrite `maintenance.go`**

Replace the entire file:

```go
package database

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"time"
)

func (s *SQLiteStore) GetPollState(ctx context.Context) (*PollState, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var state PollState
	var lastPollEnd, updatedAt sql.NullString
	err := s.db.QueryRowContext(ctx,
		"SELECT last_poll_end, updated_at FROM poll_state WHERE id = 1",
	).Scan(&lastPollEnd, &updatedAt)
	if err != nil {
		return nil, fmt.Errorf("failed to get poll state: %w", err)
	}
	if lastPollEnd.Valid && lastPollEnd.String != "" {
		state.LastPollEnd = parseTime(lastPollEnd.String)
	}
	if updatedAt.Valid && updatedAt.String != "" {
		state.UpdatedAt = parseTime(updatedAt.String)
	}
	return &state, nil
}

func (s *SQLiteStore) UpdatePollState(ctx context.Context, lastPollEnd time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	const sqliteFormat = "2006-01-02 15:04:05"
	_, err := s.db.ExecContext(ctx,
		"UPDATE poll_state SET last_poll_end = ?, updated_at = CURRENT_TIMESTAMP WHERE id = 1",
		lastPollEnd.UTC().Format(sqliteFormat),
	)
	if err != nil {
		return fmt.Errorf("failed to update poll state: %w", err)
	}
	return nil
}

// GetDataRange returns the time range of data stored in node_pairs.
func (s *SQLiteStore) GetDataRange(ctx context.Context) (*DataRange, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var minBucket, maxBucket sql.NullInt64
	var count int64
	err := s.db.QueryRowContext(ctx,
		"SELECT MIN(bucket), MAX(bucket), COUNT(*) FROM node_pairs",
	).Scan(&minBucket, &maxBucket, &count)
	if err != nil {
		return nil, fmt.Errorf("failed to get data range: %w", err)
	}
	if count == 0 || !minBucket.Valid {
		return &DataRange{}, nil
	}
	return &DataRange{
		Earliest: time.Unix(minBucket.Int64, 0).UTC(),
		Latest:   time.Unix(maxBucket.Int64, 0).UTC(),
		Count:    count,
	}, nil
}

// Cleanup deletes rows older than retention from all four data tables.
func (s *SQLiteStore) Cleanup(ctx context.Context, retention time.Duration) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	cutoff := time.Now().UTC().Unix() - int64(retention.Seconds())
	var total int64
	for _, table := range []string{"node_pairs", "bandwidth", "bandwidth_by_node", "traffic_stats"} {
		result, err := s.db.ExecContext(ctx,
			fmt.Sprintf("DELETE FROM %s WHERE bucket < ?", table), cutoff,
		)
		if err != nil {
			log.Printf("Warning: failed to cleanup %s: %v", table, err)
			continue
		}
		if n, _ := result.RowsAffected(); n > 0 {
			total += n
		}
	}
	return total, nil
}

// GetStats returns row counts, database size, and data range.
func (s *SQLiteStore) GetStats(ctx context.Context) (map[string]any, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	tableCounts := make(map[string]int64)
	for _, table := range []string{"node_pairs", "bandwidth", "bandwidth_by_node", "traffic_stats"} {
		var count int64
		_ = s.db.QueryRowContext(ctx, fmt.Sprintf("SELECT COUNT(*) FROM %s", table)).Scan(&count)
		tableCounts[table] = count
	}

	var pageCount, pageSize int64
	_ = s.db.QueryRowContext(ctx, "PRAGMA page_count").Scan(&pageCount)
	_ = s.db.QueryRowContext(ctx, "PRAGMA page_size").Scan(&pageSize)

	var minB, maxB sql.NullInt64
	var cnt int64
	_ = s.db.QueryRowContext(ctx,
		"SELECT MIN(bucket), MAX(bucket), COUNT(*) FROM node_pairs",
	).Scan(&minB, &maxB, &cnt)
	dr := &DataRange{}
	if cnt > 0 && minB.Valid {
		dr.Earliest = time.Unix(minB.Int64, 0).UTC()
		dr.Latest = time.Unix(maxB.Int64, 0).UTC()
		dr.Count = cnt
	}

	return map[string]any{
		"tableCounts": tableCounts,
		"dbSizeBytes": pageCount * pageSize,
		"dataRange":   dr,
	}, nil
}
```

- [ ] **Step 2: Run database tests**

```bash
cd backend && go test ./internal/database/ -v 2>&1
```

Expected: all database tests pass.

- [ ] **Step 3: Commit**

```bash
git add backend/internal/database/maintenance.go
git commit -m "refactor: simplify maintenance - single retention, 4 tables"
```

---

### Task 5: Remove flow_queries.go and fix the handler

**Files:**
- Delete: `backend/internal/database/flow_queries.go`
- Modify: `backend/internal/database/schema_test.go`
- Modify: `backend/internal/handlers/flow_handlers.go`

- [ ] **Step 1: Remove `TestInsertAndGetFlowLogs` from `schema_test.go`**

Delete the entire `TestInsertAndGetFlowLogs` function (lines 32–83 in the original file). It tests `InsertFlowLogs` and `GetRecentFlowLogs` which no longer exist.

- [ ] **Step 2: Run tests to confirm they still compile**

```bash
cd backend && go test ./internal/database/ -v 2>&1
```

Expected: all pass (no reference to the deleted test).

- [ ] **Step 3: Delete `flow_queries.go`**

```bash
rm backend/internal/database/flow_queries.go
```

- [ ] **Step 4: Update `GetStoredFlowLogs` in `flow_handlers.go`**

Replace the body of `GetStoredFlowLogs` (starting at line 173) with a stub that returns empty:

```go
func (h *Handlers) GetStoredFlowLogs(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"logs": []any{}})
}
```

Remove any imports in `flow_handlers.go` that become unused after this change (likely `"log"`, `"time"`, and references to `MaxLogsInResponse`, `parseLimitParam` if only used here). Run `goimports` or `go build` to identify them.

- [ ] **Step 5: Build to confirm no compile errors**

```bash
cd backend && go build ./... 2>&1
```

Expected: clean build (frontend embed error is fine, it's not a compile error).

- [ ] **Step 6: Run all backend tests**

```bash
cd backend && go test ./internal/... 2>&1
```

Expected: all pass.

- [ ] **Step 7: Commit**

```bash
git add backend/internal/database/flow_queries.go backend/internal/database/schema_test.go backend/internal/handlers/flow_handlers.go
git commit -m "feat: remove flow_logs_current table and raw log storage"
```

---

### Task 6: Update the poller

**Files:**
- Modify: `backend/internal/services/poller.go`

- [ ] **Step 1: Update `PollerConfig` and `DefaultPollerConfig`**

Replace the config struct and default function:

```go
type PollerConfig struct {
	// PollInterval is how often to poll for new logs
	PollInterval time.Duration
	// InitialBackfill is how far back to fetch on first run
	InitialBackfill time.Duration
	// Retention is how long to keep flow data (default 30 days)
	Retention time.Duration
	// CleanupInterval is how often to run cleanup
	CleanupInterval time.Duration
	// DeviceCacheRefresh is how often to refresh device cache
	DeviceCacheRefresh time.Duration
}

func DefaultPollerConfig() PollerConfig {
	return PollerConfig{
		PollInterval:       5 * time.Minute,
		InitialBackfill:    6 * time.Hour,
		Retention:          30 * 24 * time.Hour,
		CleanupInterval:    1 * time.Hour,
		DeviceCacheRefresh: 5 * time.Minute,
	}
}
```

- [ ] **Step 2: Update `Start()` log line**

Change:

```go
log.Printf("Starting background poller (interval: %v, retention minutely: %v, hourly: %v)",
    p.config.PollInterval, p.config.RetentionMinutely, p.config.RetentionHourly)
```

To:

```go
log.Printf("Starting background poller (interval: %v, retention: %v)",
    p.config.PollInterval, p.config.Retention)
```

- [ ] **Step 3: Update `pollRange` — remove `InsertFlowLogs` call**

Remove the `isRecent` block and replace with a direct count:

```go
// Pre-aggregate at poll time: node pairs, bandwidth, and traffic stats
nodePairs, totalBandwidth, nodeBandwidth, trafficStats := p.aggregate(flowLogs)

if err := p.store.CommitPollResults(ctx, database.PollResults{
    NodePairs:     nodePairs,
    Bandwidth:     totalBandwidth,
    NodeBandwidth: nodeBandwidth,
    TrafficStats:  trafficStats,
    PollEnd:       end,
}); err != nil {
    return fmt.Errorf("failed to commit poll results: %w", err)
}

p.rollingCache.Update(nodePairs, totalBandwidth, nodeBandwidth, trafficStats)

p.mu.Lock()
p.lastPollTime = time.Now()
p.lastPollCount = len(flowLogs)
p.totalPolled += int64(len(flowLogs))
p.mu.Unlock()

if len(flowLogs) > 0 {
    log.Printf("Polled %d flow logs, aggregated %d node pairs, %d bandwidth buckets (%v to %v)",
        len(flowLogs), len(nodePairs), len(totalBandwidth),
        start.Format(time.RFC3339), end.Format(time.RFC3339))
}
return nil
```

The full `pollRange` after the aggregate call should look exactly as above. Remove the old block that starts `insertedCount := len(flowLogs)`.

- [ ] **Step 4: Update `cleanup()` method**

```go
func (p *Poller) cleanup(ctx context.Context) error {
	deleted, err := p.store.Cleanup(ctx, p.config.Retention)
	if err != nil {
		return err
	}
	if deleted > 0 {
		log.Printf("Cleaned up %d old records", deleted)
	}
	return nil
}
```

- [ ] **Step 5: Build and test**

```bash
cd backend && go build ./... 2>&1
cd backend && go test ./internal/... 2>&1
```

Expected: clean build and all tests pass.

- [ ] **Step 6: Commit**

```bash
git add backend/internal/services/poller.go
git commit -m "refactor: poller uses single Retention, no raw log insertion"
```

---

### Task 7: Update config and main

**Files:**
- Modify: `backend/main.go`

- [ ] **Step 1: Replace the three retention env var reads in `main.go`**

Find and remove:

```go
if retention := os.Getenv("TSFLOW_RETENTION_MINUTELY"); retention != "" {
    if d, err := time.ParseDuration(retention); err == nil {
        pollerConfig.RetentionMinutely = d
    }
}
if retention := os.Getenv("TSFLOW_RETENTION_HOURLY"); retention != "" {
    if d, err := time.ParseDuration(retention); err == nil {
        pollerConfig.RetentionHourly = d
    }
}
if retention := os.Getenv("TSFLOW_RETENTION_DAILY"); retention != "" {
    if d, err := time.ParseDuration(retention); err == nil {
        pollerConfig.RetentionDaily = d
    }
}
```

Replace with:

```go
if retention := os.Getenv("TSFLOW_RETENTION"); retention != "" {
    if d, err := time.ParseDuration(retention); err == nil {
        pollerConfig.Retention = d
    }
}
```

- [ ] **Step 2: Update the startup log line**

Change:

```go
log.Printf("Retention: minutely=%s, hourly=%s, daily=%s",
    pollerConfig.RetentionMinutely, pollerConfig.RetentionHourly, pollerConfig.RetentionDaily)
```

To:

```go
log.Printf("Retention: %s", pollerConfig.Retention)
```

- [ ] **Step 3: Build the full backend**

```bash
cd backend && go build ./... 2>&1
```

Expected: clean.

- [ ] **Step 4: Run all tests**

```bash
cd backend && go test ./internal/... 2>&1
```

Expected: all pass.

- [ ] **Step 5: Commit**

```bash
git add backend/main.go
git commit -m "feat: TSFLOW_RETENTION replaces three tiered retention env vars"
```

---

### Task 8: Update README

**Files:**
- Modify: `README.md`

- [ ] **Step 1: Update the Data Storage & Polling table**

Replace the three tiered retention rows:

```markdown
| `TSFLOW_RETENTION_MINUTELY` | How long to keep per-minute flow data | `24h` |
| `TSFLOW_RETENTION_HOURLY` | How long to keep per-hour aggregated data | `168h` (7 days) |
| `TSFLOW_RETENTION_DAILY` | How long to keep per-day aggregated data | forever |
```

With:

```markdown
| `TSFLOW_RETENTION` | How long to keep flow data | `720h` (30 days) |
```

- [ ] **Step 2: Update the Data Storage prose section**

Replace:

```markdown
TSFlow stores flow logs in SQLite using a tiered aggregation scheme:
- **Minutely data** — retained 24 hours (configurable via `TSFLOW_RETENTION_MINUTELY`)
- **Hourly data** — retained 7 days (configurable via `TSFLOW_RETENTION_HOURLY`)
- **Daily data** — kept forever by default (configurable via `TSFLOW_RETENTION_DAILY`)
```

With:

```markdown
TSFlow stores per-minute flow aggregates in SQLite with a rolling retention window (default 30 days). Charts over wider windows use query-time bucketing — no data loss from pre-aggregation.
```

- [ ] **Step 3: Commit**

```bash
git add README.md
git commit -m "docs: update README for flat storage and single TSFLOW_RETENTION"
```
