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
	// migrateFailAfter aborts tailnet migration after the named table is
	// dropped and before the transaction commits. Tests use it to prove a
	// failed upgrade leaves the previous rows in place.
	migrateFailAfter string
	// nodePairReadHook, when set, runs immediately before the graph query.
	// Tests use it to show the store mutex is not held across that read.
	nodePairReadHook func()
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

	// Add columns that predate tailnet ids. A fresh database has no tables yet,
	// so this is a no-op and ensureTailnetSchema creates the current shape
	// directly. Existing tables are altered in place, then rebuilt with a
	// tailnet id inside one transaction.
	if err := s.ensureLegacyColumns(ctx); err != nil {
		return err
	}
	if err := s.backfillProtocolBytes(ctx); err != nil {
		return fmt.Errorf("failed to backfill protocol byte totals: %w", err)
	}
	if err := s.migrateTailnetSchema(ctx); err != nil {
		return err
	}
	if err := s.ensureTailnetSchema(ctx); err != nil {
		return err
	}

	log.Printf("Database initialized at %s", s.dbPath)
	return nil
}

// ensureLegacyColumns adds columns introduced after the original flat-table
// migration. SQLite has no IF NOT EXISTS form for ADD COLUMN, so inspect the
// schema before executing each statement. Tables that do not exist yet are
// created later with those columns already present.
func (s *SQLiteStore) ensureLegacyColumns(ctx context.Context) error {
	nodePairsExist, err := s.tableExists(ctx, "node_pairs")
	if err != nil {
		return fmt.Errorf("failed to inspect node_pairs: %w", err)
	}
	if nodePairsExist {
		protocolBytesExists, err := s.columnExists(ctx, "node_pairs", "protocol_bytes")
		if err != nil {
			return fmt.Errorf("failed to inspect node_pairs columns: %w", err)
		}
		if !protocolBytesExists {
			if _, err := s.db.ExecContext(ctx, `ALTER TABLE node_pairs ADD COLUMN protocol_bytes TEXT DEFAULT '{}'`); err != nil {
				return fmt.Errorf("failed to add node_pairs.protocol_bytes: %w", err)
			}
		}
		for _, column := range []struct {
			name string
			ddl  string
		}{
			{name: "tx_ports", ddl: `ALTER TABLE node_pairs ADD COLUMN tx_ports TEXT DEFAULT '[]'`},
			{name: "rx_ports", ddl: `ALTER TABLE node_pairs ADD COLUMN rx_ports TEXT DEFAULT '[]'`},
			{name: "tx_protocol_bytes", ddl: `ALTER TABLE node_pairs ADD COLUMN tx_protocol_bytes TEXT DEFAULT '{}'`},
			{name: "rx_protocol_bytes", ddl: `ALTER TABLE node_pairs ADD COLUMN rx_protocol_bytes TEXT DEFAULT '{}'`},
			{name: "directional_ports", ddl: `ALTER TABLE node_pairs ADD COLUMN directional_ports INTEGER NOT NULL DEFAULT 0`},
		} {
			exists, err := s.columnExists(ctx, "node_pairs", column.name)
			if err != nil {
				return fmt.Errorf("failed to inspect node_pairs.%s: %w", column.name, err)
			}
			if !exists {
				if _, err := s.db.ExecContext(ctx, column.ddl); err != nil {
					return fmt.Errorf("failed to add node_pairs.%s: %w", column.name, err)
				}
			}
		}
	}

	ingestedExist, err := s.tableExists(ctx, "ingested_objects")
	if err != nil {
		return fmt.Errorf("failed to inspect ingested_objects: %w", err)
	}
	if ingestedExist {
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
	}

	statsExist, err := s.tableExists(ctx, "traffic_stats")
	if err != nil {
		return fmt.Errorf("failed to inspect traffic_stats: %w", err)
	}
	if statsExist {
		exitBytesExists, err := s.columnExists(ctx, "traffic_stats", "exit_bytes")
		if err != nil {
			return fmt.Errorf("failed to inspect traffic_stats columns: %w", err)
		}
		if !exitBytesExists {
			if _, err := s.db.ExecContext(ctx, `ALTER TABLE traffic_stats ADD COLUMN exit_bytes INTEGER DEFAULT 0`); err != nil {
				return fmt.Errorf("failed to add traffic_stats.exit_bytes: %w", err)
			}
		}
	}
	return nil
}

func (s *SQLiteStore) backfillProtocolBytes(ctx context.Context) error {
	exists, err := s.tableExists(ctx, "node_pairs")
	if err != nil || !exists {
		return err
	}
	hasProtocolBytes, err := s.columnExists(ctx, "node_pairs", "protocol_bytes")
	if err != nil || !hasProtocolBytes {
		return err
	}
	hasTailnet, err := s.columnExists(ctx, "node_pairs", "tailnet_id")
	if err != nil {
		return err
	}

	// Key updates by the primary key rather than rowid. These tables are stored
	// WITHOUT ROWID, and two tailnets can share the same pair key.
	query := `
		SELECT '', bucket, src_node_id, dst_node_id, traffic_type, protocols, tx_bytes + rx_bytes
		FROM node_pairs
		WHERE protocol_bytes IS NULL OR protocol_bytes = '' OR protocol_bytes = '{}'
		   OR NOT json_valid(protocol_bytes)
	`
	update := `UPDATE node_pairs SET protocol_bytes = ?
		WHERE bucket = ? AND src_node_id = ? AND dst_node_id = ? AND traffic_type = ?`
	if hasTailnet {
		query = `
			SELECT tailnet_id, bucket, src_node_id, dst_node_id, traffic_type, protocols, tx_bytes + rx_bytes
			FROM node_pairs
			WHERE protocol_bytes IS NULL OR protocol_bytes = '' OR protocol_bytes = '{}'
			   OR NOT json_valid(protocol_bytes)
		`
		update = `UPDATE node_pairs SET protocol_bytes = ?
			WHERE tailnet_id = ? AND bucket = ? AND src_node_id = ? AND dst_node_id = ? AND traffic_type = ?`
	}

	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return err
	}

	type row struct {
		tailnetID   string
		bucket      int64
		srcNodeID   string
		dstNodeID   string
		trafficType string
		protocols   string
		total       int64
	}
	var pending []row
	for rows.Next() {
		var item row
		if err := rows.Scan(&item.tailnetID, &item.bucket, &item.srcNodeID, &item.dstNodeID, &item.trafficType, &item.protocols, &item.total); err != nil {
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
		args := []any{protocolBytes}
		if hasTailnet {
			args = append(args, item.tailnetID)
		}
		args = append(args, item.bucket, item.srcNodeID, item.dstNodeID, item.trafficType)
		if _, err := s.db.ExecContext(ctx, update, args...); err != nil {
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
