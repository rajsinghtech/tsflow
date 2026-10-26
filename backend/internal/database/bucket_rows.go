package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"time"
)

// node_pair_bucket_rows holds the exact number of node_pairs rows for each
// tailnet and minute bucket. An insert trigger keeps it current inside the
// same statement that adds a pair row, so every writer is covered, including
// an older binary that predates the table.
//
// Rows only leave node_pairs through retention, which deletes every bucket
// before a cutoff. Cleanup drops the matching counters in the same
// transaction. Readers also ignore counters below the oldest stored bucket,
// so counters an older binary's cleanup left behind never reach a total.
// A delete trigger would cover that too, but it turns retention's range
// delete into one counter update per row.
//
// The data range and the poller status report how many pair rows are
// stored. Counting node_pairs scans a whole index on every request, which is
// several seconds cold on a large tailnet. Summing this table reads one small
// row per minute instead.
const bucketRowsTableSQL = `
	CREATE TABLE IF NOT EXISTS node_pair_bucket_rows (
		tailnet_id TEXT NOT NULL,
		bucket INTEGER NOT NULL,
		rows INTEGER NOT NULL,
		PRIMARY KEY (tailnet_id, bucket)
	) WITHOUT ROWID`

// ready is 1 once every bucket that existed when the trigger was created has
// been counted. Until then readers keep counting node_pairs.
const bucketRowsStateSQL = `
	CREATE TABLE IF NOT EXISTS node_pair_bucket_rows_state (
		id INTEGER PRIMARY KEY CHECK (id = 1),
		ready INTEGER NOT NULL DEFAULT 0
	)`

const bucketRowsTrigger = "node_pair_bucket_rows_insert"

// An upsert that merges into an existing pair takes the UPDATE path, which
// does not fire an INSERT trigger, so a merged minute is not counted twice.
const bucketRowsTriggerSQL = `
	CREATE TRIGGER ` + bucketRowsTrigger + ` AFTER INSERT ON node_pairs
	BEGIN
		INSERT INTO node_pair_bucket_rows (tailnet_id, bucket, rows)
		VALUES (NEW.tailnet_id, NEW.bucket, 1)
		ON CONFLICT(tailnet_id, bucket) DO UPDATE SET rows = rows + 1;
	END`

const bucketRowsTriggerExistsSQL = `
	SELECT COUNT(*) FROM sqlite_master
	WHERE type = 'trigger' AND tbl_name = 'node_pairs' AND name = '` + bucketRowsTrigger + `'`

// bucketRowsChunkSeconds is how much of one tailnet a single backfill
// transaction counts. An hour keeps each write short enough that a poll
// commit waiting on the writer is not held up noticeably.
const bucketRowsChunkSeconds int64 = 3600

// bucketRowsReady reports whether the counters can answer for node_pairs.
// Rebuilding node_pairs drops its triggers, which sends readers back to
// counting until the next Init and backfill.
func bucketRowsReady(ctx context.Context, q rowQuerier) (bool, error) {
	var n int
	if err := q.QueryRowContext(ctx, bucketRowsTriggerExistsSQL).Scan(&n); err != nil {
		return false, fmt.Errorf("failed to inspect pair row counters: %w", err)
	}
	if n != 1 {
		return false, nil
	}
	var ready int
	err := q.QueryRowContext(ctx, "SELECT ready FROM node_pair_bucket_rows_state WHERE id = 1").Scan(&ready)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("failed to read pair row counter state: %w", err)
	}
	return ready == 1, nil
}

// ensureBucketRowCounts creates the counter tables and trigger. It does not
// count existing rows; BuildPairRowCounts does that in the background so a
// large database does not hold up startup.
func (s *SQLiteStore) ensureBucketRowCounts(ctx context.Context) error {
	for _, stmt := range []string{bucketRowsTableSQL, bucketRowsStateSQL} {
		if _, err := s.writer.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("failed to create pair row counters: %w", err)
		}
	}
	var n, state int
	if err := s.writer.QueryRowContext(ctx, bucketRowsTriggerExistsSQL).Scan(&n); err != nil {
		return fmt.Errorf("failed to inspect pair row counters: %w", err)
	}
	if err := s.writer.QueryRowContext(ctx, "SELECT COUNT(*) FROM node_pair_bucket_rows_state WHERE id = 1").Scan(&state); err != nil {
		return fmt.Errorf("failed to read pair row counter state: %w", err)
	}
	if n == 1 && state == 1 {
		// Drop counters an older binary's retention left below the oldest
		// stored minute. Readers already skip them; this keeps the table small.
		if _, err := s.writer.ExecContext(ctx, `
			DELETE FROM node_pair_bucket_rows
			WHERE bucket < COALESCE(
				(SELECT MIN(p.bucket) FROM node_pairs p WHERE p.tailnet_id = node_pair_bucket_rows.tailnet_id),
				9223372036854775807)`); err != nil {
			return fmt.Errorf("failed to prune node_pair_bucket_rows: %w", err)
		}
		return nil
	}

	// No trigger: either a database from before the counters or one whose
	// node_pairs was rebuilt. A trigger without its state row is treated the
	// same way. Start over, with the trigger counting every
	// insert from here on and ready cleared until the backfill finishes.
	tx, err := s.writer.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin pair row counter setup: %w", err)
	}
	defer tx.Rollback()
	for _, stmt := range []string{
		"DROP TRIGGER IF EXISTS " + bucketRowsTrigger,
		"DELETE FROM node_pair_bucket_rows",
		"INSERT INTO node_pair_bucket_rows_state (id, ready) VALUES (1, 0) ON CONFLICT(id) DO UPDATE SET ready = 0",
		bucketRowsTriggerSQL,
		// With nothing stored yet there is nothing to backfill.
		"UPDATE node_pair_bucket_rows_state SET ready = 1 WHERE id = 1 AND NOT EXISTS (SELECT 1 FROM node_pairs)",
	} {
		if _, err := tx.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("failed to set up pair row counters: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit pair row counter setup: %w", err)
	}
	return nil
}

// BuildPairRowCounts counts the node_pairs rows that were stored before the
// insert trigger existed, then marks the counters ready. It returns at once
// when they already are. Each hour of each tailnet is recounted in its own
// short write transaction, under that tailnet's write lock, and replaces
// whatever the trigger added for those minutes so far. The trigger keeps
// adding to a minute after its recount, so the total stays exact while polls
// keep landing. A restart part way through starts the recount again.
func (s *SQLiteStore) BuildPairRowCounts(ctx context.Context) error {
	ready, err := bucketRowsReady(ctx, s.db)
	if err != nil || ready {
		return err
	}
	started := time.Now()
	tailnets, err := s.pairTailnets(ctx)
	if err != nil {
		return err
	}
	for _, tailnetID := range tailnets {
		if err := s.buildTailnetPairRowCounts(ctx, tailnetID); err != nil {
			return err
		}
	}

	if _, err := s.writer.ExecContext(ctx, "UPDATE node_pair_bucket_rows_state SET ready = 1 WHERE id = 1"); err != nil {
		return fmt.Errorf("failed to mark pair row counters ready: %w", err)
	}
	log.Printf("Pair row counters ready after %s", time.Since(started).Round(time.Millisecond))
	return nil
}

// pairTailnets lists the tailnets that have pair rows. Each step is a seek on
// the primary key, so this does not scan node_pairs.
func (s *SQLiteStore) pairTailnets(ctx context.Context) ([]string, error) {
	var out []string
	last := ""
	for {
		var next sql.NullString
		if err := s.db.QueryRowContext(ctx,
			"SELECT MIN(tailnet_id) FROM node_pairs WHERE tailnet_id > ?", last,
		).Scan(&next); err != nil {
			return nil, fmt.Errorf("failed to list pair tailnets: %w", err)
		}
		if !next.Valid {
			return out, nil
		}
		out = append(out, next.String)
		last = next.String
	}
}

func (s *SQLiteStore) buildTailnetPairRowCounts(ctx context.Context, tailnetID string) error {
	var minBucket, maxBucket sql.NullInt64
	if err := s.db.QueryRowContext(ctx, `
		SELECT (SELECT MIN(bucket) FROM node_pairs WHERE tailnet_id = ?),
		       (SELECT MAX(bucket) FROM node_pairs WHERE tailnet_id = ?)`,
		tailnetID, tailnetID,
	).Scan(&minBucket, &maxBucket); err != nil {
		return fmt.Errorf("failed to read pair bucket range: %w", err)
	}
	if !minBucket.Valid || !maxBucket.Valid {
		return nil
	}
	from := (minBucket.Int64 / bucketRowsChunkSeconds) * bucketRowsChunkSeconds
	for ; from <= maxBucket.Int64; from += bucketRowsChunkSeconds {
		if err := s.recountPairRows(ctx, tailnetID, from, from+bucketRowsChunkSeconds); err != nil {
			return err
		}
	}
	return nil
}

func (s *SQLiteStore) recountPairRows(ctx context.Context, tailnetID string, from, to int64) error {
	unlock := s.lockTailnet(tailnetID)
	defer unlock()
	tx, err := s.beginWrite(ctx, tailnetID)
	if err != nil {
		return fmt.Errorf("failed to begin pair row recount: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx,
		"DELETE FROM node_pair_bucket_rows WHERE tailnet_id = ? AND bucket >= ? AND bucket < ?",
		tailnetID, from, to,
	); err != nil {
		return fmt.Errorf("failed to clear pair row counters: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO node_pair_bucket_rows (tailnet_id, bucket, rows)
		SELECT tailnet_id, bucket, COUNT(*) FROM node_pairs
		WHERE tailnet_id = ? AND bucket >= ? AND bucket < ?
		GROUP BY tailnet_id, bucket`,
		tailnetID, from, to,
	); err != nil {
		return fmt.Errorf("failed to recount pair rows: %w", err)
	}
	if err := s.commitWrite(tx, tailnetID); err != nil {
		return fmt.Errorf("failed to commit pair row recount: %w", err)
	}
	return nil
}

// countNodePairs returns the pair row count for a tailnet at or after
// fromBucket, which must be at or after the oldest stored bucket. It sums the
// per-bucket counts when they are ready and counts node_pairs otherwise.
func countNodePairs(ctx context.Context, q rowQuerier, tailnetID string, fromBucket int64, counted bool) (int64, error) {
	query := "SELECT COUNT(*) FROM node_pairs WHERE tailnet_id = ? AND bucket >= ?"
	if counted {
		query = "SELECT COALESCE(SUM(rows), 0) FROM node_pair_bucket_rows WHERE tailnet_id = ? AND bucket >= ?"
	}
	var n int64
	if err := q.QueryRowContext(ctx, query, tailnetID, fromBucket).Scan(&n); err != nil {
		return 0, fmt.Errorf("failed to count node pairs: %w", err)
	}
	return n, nil
}

// countAllNodePairs is every stored pair row for a tailnet.
func countAllNodePairs(ctx context.Context, q rowQuerier, tailnetID string, counted bool) (int64, error) {
	if !counted {
		var n int64
		if err := q.QueryRowContext(ctx, "SELECT COUNT(*) FROM node_pairs WHERE tailnet_id = ?", tailnetID).Scan(&n); err != nil {
			return 0, fmt.Errorf("failed to count node pairs: %w", err)
		}
		return n, nil
	}
	var minBucket sql.NullInt64
	if err := q.QueryRowContext(ctx, "SELECT MIN(bucket) FROM node_pairs WHERE tailnet_id = ?", tailnetID).Scan(&minBucket); err != nil {
		return 0, fmt.Errorf("failed to read oldest pair bucket: %w", err)
	}
	if !minBucket.Valid {
		return 0, nil
	}
	return countNodePairs(ctx, q, tailnetID, minBucket.Int64, true)
}

// pruneBucketRows drops the counters retention removed from node_pairs.
func pruneBucketRows(ctx context.Context, exec sqlExecer, tailnetID string, cutoff int64) error {
	if _, err := exec.ExecContext(ctx,
		"DELETE FROM node_pair_bucket_rows WHERE tailnet_id = ? AND bucket < ?", tailnetID, cutoff,
	); err != nil {
		return fmt.Errorf("failed to cleanup node_pair_bucket_rows: %w", err)
	}
	return nil
}
