package database

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

// SQLiteStore implements Store using SQLite
type SQLiteStore struct {
	db     *sql.DB
	dbPath string
	mu     sync.RWMutex
}

// NewSQLiteStore creates a new SQLite store
func NewSQLiteStore(dbPath string) (*SQLiteStore, error) {
	dsn := fmt.Sprintf("file:%s?_journal_mode=WAL&_busy_timeout=5000&_synchronous=NORMAL&_cache_size=10000", dbPath)

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(time.Hour)

	// Ensure PRAGMAs are applied — DSN params aren't always honored by all drivers
	for _, pragma := range []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA busy_timeout=5000",
		"PRAGMA synchronous=NORMAL",
		"PRAGMA cache_size=10000",
	} {
		if _, err := db.Exec(pragma); err != nil {
			db.Close()
			return nil, fmt.Errorf("failed to set %s: %w", pragma, err)
		}
	}

	return &SQLiteStore{db: db, dbPath: dbPath}, nil
}

// Init creates the database schema
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
		bucket            INTEGER PRIMARY KEY,
		tcp_bytes         INTEGER DEFAULT 0,
		udp_bytes         INTEGER DEFAULT 0,
		other_proto_bytes INTEGER DEFAULT 0,
		virtual_bytes     INTEGER DEFAULT 0,
		subnet_bytes      INTEGER DEFAULT 0,
		physical_bytes    INTEGER DEFAULT 0,
		total_flows       INTEGER DEFAULT 0,
		unique_pairs      INTEGER DEFAULT 0,
		top_ports         TEXT    DEFAULT '[]'
	);

	CREATE TABLE IF NOT EXISTS poll_state (
		id            INTEGER PRIMARY KEY CHECK (id = 1),
		last_poll_end DATETIME,
		updated_at    DATETIME DEFAULT CURRENT_TIMESTAMP
	);
	INSERT OR IGNORE INTO poll_state (id, last_poll_end, updated_at) VALUES (1, NULL, CURRENT_TIMESTAMP);
	`

	if _, err := s.db.ExecContext(ctx, schema); err != nil {
		return fmt.Errorf("failed to create schema: %w", err)
	}

	log.Printf("Database initialized at %s", s.dbPath)
	return nil
}

// Close closes the database connection
func (s *SQLiteStore) Close() error {
	return s.db.Close()
}

// parseTime parses a time string from SQLite
func parseTime(s string) time.Time {
	formats := []string{
		"2006-01-02 15:04:05.999999999 -0700 MST",
		"2006-01-02 15:04:05.999999999 +0000 UTC",
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02 15:04:05.999999999-07:00",
		"2006-01-02T15:04:05.999999999-07:00",
		"2006-01-02 15:04:05",
		"2006-01-02T15:04:05",
	}
	for _, format := range formats {
		if t, err := time.Parse(format, s); err == nil {
			return t
		}
	}
	return time.Time{}
}
