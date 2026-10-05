package database

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"sort"
	"sync"
	"sync/atomic"
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

	// derivedStatsScans counts node_pairs reads that build traffic stats.
	derivedStatsScans atomic.Int64
	// derivedStatsHook, when set, receives each derived read's bucket ranges.
	// Tests use it to prove a covered minute is not part of that read.
	derivedStatsHook func(ranges [][2]int64)

	// protocolBackfillScans counts passes that read node_pairs past a tailnet's
	// high-water bucket. A startup with nothing newer does not increment it.
	// protocolBackfillTailnets is the order those passes ran.
	protocolBackfillScans    int
	protocolBackfillTailnets []string
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
	if err := s.migrateTailnetSchema(ctx); err != nil {
		return err
	}
	if err := s.ensureTailnetSchema(ctx); err != nil {
		return err
	}
	if err := s.ensureBackfillState(ctx); err != nil {
		return err
	}
	// Repair legacy protocol totals after the tailnet rewrite so every row
	// already has a tailnet id. Databases that finished this pass skip it.
	if err := s.backfillProtocolBytes(ctx); err != nil {
		return fmt.Errorf("failed to backfill protocol byte totals: %w", err)
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

// protocolBackfillNewerMaxSQL is the startup probe for one tailnet. The primary
// key starts with (tailnet_id, bucket), so this is a seek to the newest bucket
// after the high-water mark. An empty range does not read older rows.
const protocolBackfillNewerMaxSQL = `
	SELECT MAX(bucket) FROM node_pairs
	WHERE tailnet_id = ? AND bucket > ?
`

// protocolBackfillNewerRowsSQL reads only the rows past the high-water mark
// that still need protocol byte totals. The same primary key bounds the read.
const protocolBackfillNewerRowsSQL = `
	SELECT bucket, src_node_id, dst_node_id, traffic_type, protocols, tx_bytes + rx_bytes
	FROM node_pairs
	WHERE tailnet_id = ? AND bucket > ?
	  AND (protocol_bytes IS NULL OR protocol_bytes = '' OR protocol_bytes = '{}'
	       OR NOT json_valid(protocol_bytes))
`

// ensureBackfillState creates the high-water table. It is not part of the
// tailnet rewrite: there is no pre-tailnet copy of it, and a failed tailnet
// migration should not have to rebuild it. A table created by an earlier
// build of this change, which only stored a completion time, gains the bucket
// column. Those rows start at -1, so the next start checks every bucket once
// and then records the real high-water mark.
func (s *SQLiteStore) ensureBackfillState(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS backfill_state (
			tailnet_id TEXT NOT NULL PRIMARY KEY,
			protocol_bytes_bucket INTEGER NOT NULL DEFAULT -1,
			completed_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		) WITHOUT ROWID
	`)
	if err != nil {
		return fmt.Errorf("failed to create backfill_state: %w", err)
	}
	hasBucket, err := s.columnExists(ctx, "backfill_state", "protocol_bytes_bucket")
	if err != nil {
		return fmt.Errorf("failed to inspect backfill_state: %w", err)
	}
	if !hasBucket {
		if _, err := s.db.ExecContext(ctx, `
			ALTER TABLE backfill_state
			ADD COLUMN protocol_bytes_bucket INTEGER NOT NULL DEFAULT -1
		`); err != nil {
			return fmt.Errorf("failed to add backfill_state.protocol_bytes_bucket: %w", err)
		}
	}
	// The first cut of this table used an empty tailnet id as a pass-wide
	// "done" flag. High-water marks are per tailnet, so that row is not a mark.
	if _, err := s.db.ExecContext(ctx, `DELETE FROM backfill_state WHERE tailnet_id = ''`); err != nil {
		return fmt.Errorf("failed to drop the protocol backfill sentinel: %w", err)
	}
	return nil
}

// backfillProtocolBytes fills protocol_bytes on rows written before that
// column carried totals. Each tailnet stores the newest bucket the pass has
// covered. Later startups read only rows past that bucket, which is how a
// downgrade that writes empty protocol bytes gets repaired without reading
// the rows already checked. The mark moves forward in the same transaction
// as the updates, so a crash resumes from the previous bucket.
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
	if !hasTailnet {
		return fmt.Errorf("node_pairs is missing tailnet_id")
	}

	tailnets, err := s.protocolBackfillTailnetIDs(ctx)
	if err != nil {
		return err
	}
	if len(tailnets) == 0 {
		// Fresh database. Record default so the tailnet this process serves
		// has a mark, without a json_valid read.
		return s.markProtocolBackfill(ctx, s.db, DefaultTailnetID, -1)
	}
	for _, tailnetID := range tailnets {
		if err := s.backfillProtocolBytesTailnet(ctx, tailnetID); err != nil {
			return err
		}
	}
	return nil
}

func (s *SQLiteStore) backfillProtocolBytesTailnet(ctx context.Context, tailnetID string) error {
	mark, hasMark, err := s.protocolBackfillMark(ctx, tailnetID)
	if err != nil {
		return err
	}

	var maxBucket sql.NullInt64
	if hasMark {
		err = s.db.QueryRowContext(ctx, protocolBackfillNewerMaxSQL, tailnetID, mark).Scan(&maxBucket)
	} else {
		err = s.db.QueryRowContext(ctx, `
			SELECT MAX(bucket) FROM node_pairs WHERE tailnet_id = ?
		`, tailnetID).Scan(&maxBucket)
	}
	if err != nil {
		return err
	}
	if !maxBucket.Valid {
		if !hasMark {
			return s.markProtocolBackfill(ctx, s.db, tailnetID, -1)
		}
		return nil
	}

	// Key updates by the primary key rather than rowid. These tables are stored
	// WITHOUT ROWID, and two tailnets can share the same pair key.
	s.protocolBackfillScans++
	s.protocolBackfillTailnets = append(s.protocolBackfillTailnets, tailnetID)

	var rows *sql.Rows
	if hasMark {
		rows, err = s.db.QueryContext(ctx, protocolBackfillNewerRowsSQL, tailnetID, mark)
	} else {
		rows, err = s.db.QueryContext(ctx, `
			SELECT bucket, src_node_id, dst_node_id, traffic_type, protocols, tx_bytes + rx_bytes
			FROM node_pairs
			WHERE tailnet_id = ?
			  AND (protocol_bytes IS NULL OR protocol_bytes = '' OR protocol_bytes = '{}'
			       OR NOT json_valid(protocol_bytes))
		`, tailnetID)
	}
	if err != nil {
		return err
	}

	type row struct {
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
		if err := rows.Scan(&item.bucket, &item.srcNodeID, &item.dstNodeID, &item.trafficType, &item.protocols, &item.total); err != nil {
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

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin protocol byte backfill: %w", err)
	}
	defer tx.Rollback()

	for _, item := range pending {
		protocolBytes := normalizeProtocolBytes("", item.protocols, item.total)
		if _, err := tx.ExecContext(ctx, `
			UPDATE node_pairs SET protocol_bytes = ?
			WHERE tailnet_id = ? AND bucket = ? AND src_node_id = ? AND dst_node_id = ? AND traffic_type = ?
		`, protocolBytes, tailnetID, item.bucket, item.srcNodeID, item.dstNodeID, item.trafficType); err != nil {
			return err
		}
	}
	if err := s.markProtocolBackfill(ctx, tx, tailnetID, maxBucket.Int64); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit protocol byte backfill: %w", err)
	}
	if len(pending) > 0 {
		log.Printf("Backfilled %d protocol byte rows for tailnet %s", len(pending), tailnetID)
	}
	return nil
}

func (s *SQLiteStore) protocolBackfillMark(ctx context.Context, tailnetID string) (int64, bool, error) {
	var bucket int64
	err := s.db.QueryRowContext(ctx, `
		SELECT protocol_bytes_bucket FROM backfill_state WHERE tailnet_id = ?
	`, tailnetID).Scan(&bucket)
	if err == sql.ErrNoRows {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	return bucket, true, nil
}

func (s *SQLiteStore) markProtocolBackfill(ctx context.Context, exec sqlExecer, tailnetID string, bucket int64) error {
	_, err := exec.ExecContext(ctx, `
		INSERT INTO backfill_state (tailnet_id, protocol_bytes_bucket, completed_at)
		VALUES (?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(tailnet_id) DO UPDATE SET
			protocol_bytes_bucket = excluded.protocol_bytes_bucket,
			completed_at = CURRENT_TIMESTAMP
		WHERE excluded.protocol_bytes_bucket > backfill_state.protocol_bytes_bucket
	`, tailnetID, bucket)
	if err != nil {
		return fmt.Errorf("failed to record protocol byte backfill for %q: %w", tailnetID, err)
	}
	return nil
}

// protocolBackfillTailnetIDs returns every tailnet that already has a high-water
// mark, plus any tailnet present in node_pairs. The node_pairs walk seeks the
// next tailnet id on the primary key instead of reading every pair.
func (s *SQLiteStore) protocolBackfillTailnetIDs(ctx context.Context) ([]string, error) {
	seen := map[string]struct{}{}
	var tailnets []string
	rows, err := s.db.QueryContext(ctx, `
		SELECT tailnet_id FROM backfill_state WHERE tailnet_id != '' ORDER BY tailnet_id
	`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var tailnetID string
		if err := rows.Scan(&tailnetID); err != nil {
			rows.Close()
			return nil, err
		}
		if _, ok := seen[tailnetID]; ok {
			continue
		}
		seen[tailnetID] = struct{}{}
		tailnets = append(tailnets, tailnetID)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}

	var after string
	for {
		var next string
		err := s.db.QueryRowContext(ctx, `
			SELECT tailnet_id FROM node_pairs
			WHERE tailnet_id > ?
			ORDER BY tailnet_id
			LIMIT 1
		`, after).Scan(&next)
		if err == sql.ErrNoRows {
			break
		}
		if err != nil {
			return nil, err
		}
		if next <= after {
			return nil, fmt.Errorf("protocol backfill tailnet walk did not advance past %q", after)
		}
		if _, ok := seen[next]; !ok {
			seen[next] = struct{}{}
			tailnets = append(tailnets, next)
		}
		after = next
	}
	sort.Strings(tailnets)
	return tailnets, nil
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
