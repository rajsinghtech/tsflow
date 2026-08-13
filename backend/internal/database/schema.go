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
	for _, m := range [][2]string{
		{"node_pairs_minutely", "node_pairs"},
		{"bandwidth_minutely", "bandwidth"},
		{"bandwidth_by_node_minutely", "bandwidth_by_node"},
		{"traffic_stats_minutely", "traffic_stats"},
	} {
		sourceExists, err := s.tableExists(ctx, m[0])
		if err != nil {
			return fmt.Errorf("failed to inspect migration table %s: %w", m[0], err)
		}
		if !sourceExists {
			continue
		}
		targetExists, err := s.tableExists(ctx, m[1])
		if err != nil {
			return fmt.Errorf("failed to inspect migration table %s: %w", m[1], err)
		}
		if targetExists {
			return fmt.Errorf("cannot migrate %s to %s: both tables exist", m[0], m[1])
		}
		if _, err := s.db.ExecContext(ctx, fmt.Sprintf("ALTER TABLE %s RENAME TO %s", m[0], m[1])); err != nil {
			return fmt.Errorf("failed to migrate %s to %s: %w", m[0], m[1], err)
		}
	}

	// Step 2: Drop old tier tables and the ephemeral raw-log table.
	for _, table := range []string{
		"node_pairs_hourly", "node_pairs_daily",
		"bandwidth_hourly", "bandwidth_daily",
		"bandwidth_by_node_hourly", "bandwidth_by_node_daily",
		"traffic_stats_hourly", "traffic_stats_daily",
		"flow_logs_current",
	} {
		if _, err := s.db.ExecContext(ctx, fmt.Sprintf("DROP TABLE IF EXISTS %s", table)); err != nil {
			return fmt.Errorf("failed to drop obsolete table %s: %w", table, err)
		}
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
		protocol_bytes TEXT  DEFAULT '{}',
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
		exit_bytes        INTEGER DEFAULT 0,
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

	CREATE TABLE IF NOT EXISTS ingested_objects (
		object_key          TEXT PRIMARY KEY,
		last_modified       DATETIME,
		size_bytes          INTEGER DEFAULT 0,
		flow_count          INTEGER DEFAULT 0,
		ingested_at         DATETIME DEFAULT CURRENT_TIMESTAMP,
		metadata_hydrated   INTEGER NOT NULL DEFAULT 0
	);
	CREATE INDEX IF NOT EXISTS idx_ingested_objects_ingested_at ON ingested_objects(ingested_at);

	CREATE TABLE IF NOT EXISTS node_metadata (
		node_id    TEXT PRIMARY KEY,
		name       TEXT DEFAULT '',
		hostname   TEXT DEFAULT '',
		owner      TEXT DEFAULT '',
		ips        TEXT DEFAULT '[]',
		tags       TEXT DEFAULT '[]',
		updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);

	CREATE TABLE IF NOT EXISTS object_metadata_nodes (
		object_key TEXT NOT NULL,
		node_id    TEXT NOT NULL,
		PRIMARY KEY (object_key, node_id)
	);
	CREATE INDEX IF NOT EXISTS idx_object_metadata_nodes_node ON object_metadata_nodes(node_id);
	`

	if _, err := s.db.ExecContext(ctx, schema); err != nil {
		return fmt.Errorf("failed to create schema: %w", err)
	}

	// Add columns introduced after the original flat-table migration. SQLite
	// has no IF NOT EXISTS form for ADD COLUMN, so inspect the schema before
	// executing the migration and surface real ALTER TABLE failures.
	protocolBytesExists, err := s.columnExists(ctx, "node_pairs", "protocol_bytes")
	if err != nil {
		return fmt.Errorf("failed to inspect node_pairs columns: %w", err)
	}
	if !protocolBytesExists {
		if _, err := s.db.ExecContext(ctx, `ALTER TABLE node_pairs ADD COLUMN protocol_bytes TEXT DEFAULT '{}'`); err != nil {
			return fmt.Errorf("failed to add node_pairs.protocol_bytes: %w", err)
		}
	}
	metadataHydratedExists, err := s.columnExists(ctx, "ingested_objects", "metadata_hydrated")
	if err != nil {
		return fmt.Errorf("failed to inspect ingested_objects columns: %w", err)
	}
	if !metadataHydratedExists {
		// Existing ingestion rows were written before the per-object metadata
		// index existed, so schedule them for bounded hydration on upgrade.
		if _, err := s.db.ExecContext(ctx, `ALTER TABLE ingested_objects ADD COLUMN metadata_hydrated INTEGER NOT NULL DEFAULT 0`); err != nil {
			return fmt.Errorf("failed to add ingested_objects.metadata_hydrated: %w", err)
		}
	}
	exitBytesExists, err := s.columnExists(ctx, "traffic_stats", "exit_bytes")
	if err != nil {
		return fmt.Errorf("failed to inspect traffic_stats columns: %w", err)
	}
	if !exitBytesExists {
		if _, err := s.db.ExecContext(ctx, `ALTER TABLE traffic_stats ADD COLUMN exit_bytes INTEGER DEFAULT 0`); err != nil {
			return fmt.Errorf("failed to add traffic_stats.exit_bytes: %w", err)
		}
	}
	if err := s.backfillProtocolBytes(ctx); err != nil {
		return fmt.Errorf("failed to backfill protocol byte totals: %w", err)
	}

	log.Printf("Database initialized at %s", s.dbPath)
	return nil
}

func (s *SQLiteStore) backfillProtocolBytes(ctx context.Context) error {
	rows, err := s.db.QueryContext(ctx, `
		SELECT rowid, protocols, tx_bytes + rx_bytes, protocol_bytes
		FROM node_pairs
		WHERE protocol_bytes IS NULL OR protocol_bytes = '' OR protocol_bytes = '{}'
		   OR NOT json_valid(protocol_bytes)
	`)
	if err != nil {
		return err
	}

	type row struct {
		id        int64
		protocols string
		total     int64
	}
	var pending []row
	for rows.Next() {
		var item row
		var protocolBytes string
		if err := rows.Scan(&item.id, &item.protocols, &item.total, &protocolBytes); err != nil {
			rows.Close()
			return err
		}
		pending = append(pending, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}

	for _, item := range pending {
		protocolBytes := normalizeProtocolBytes("", item.protocols, item.total)
		if _, err := s.db.ExecContext(ctx,
			`UPDATE node_pairs SET protocol_bytes = ? WHERE rowid = ?`, protocolBytes, item.id,
		); err != nil {
			return err
		}
	}
	return nil
}

func (s *SQLiteStore) tableExists(ctx context.Context, table string) (bool, error) {
	var exists int
	err := s.db.QueryRowContext(ctx,
		"SELECT EXISTS (SELECT 1 FROM sqlite_master WHERE type = 'table' AND name = ?)", table,
	).Scan(&exists)
	return exists != 0, err
}

func (s *SQLiteStore) columnExists(ctx context.Context, table, column string) (bool, error) {
	rows, err := s.db.QueryContext(ctx, fmt.Sprintf("PRAGMA table_info(%s)", table))
	if err != nil {
		return false, err
	}
	defer rows.Close()

	for rows.Next() {
		var cid int
		var name, columnType string
		var notNull, primaryKey int
		var defaultValue sql.NullString
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			return false, err
		}
		if name == column {
			return true, nil
		}
	}
	return false, rows.Err()
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
