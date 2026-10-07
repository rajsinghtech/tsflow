package database

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

func (s *SQLiteStore) GetPollState(ctx context.Context, tailnetID string) (*PollState, error) {
	if err := checkTailnetID(tailnetID); err != nil {
		return nil, err
	}
	var state PollState
	var lastPollEnd, updatedAt sql.NullString
	err := s.db.QueryRowContext(ctx,
		"SELECT last_poll_end, updated_at FROM poll_state WHERE tailnet_id = ?", tailnetID,
	).Scan(&lastPollEnd, &updatedAt)
	if err == sql.ErrNoRows {
		return &PollState{}, nil
	}
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

func (s *SQLiteStore) UpdatePollState(ctx context.Context, tailnetID string, lastPollEnd time.Time) error {
	if err := checkTailnetID(tailnetID); err != nil {
		return err
	}
	unlock := s.lockTailnet(tailnetID)
	defer unlock()

	tx, err := s.beginWrite(ctx, tailnetID)
	if err != nil {
		return fmt.Errorf("failed to begin poll cursor update: %w", err)
	}
	defer tx.Rollback()
	if err := upsertPollCursor(ctx, tx, tailnetID, lastPollEnd); err != nil {
		return err
	}
	// Object-store polls commit each object without a poll end, then move the
	// cursor once the batch has been examined. Closing minutes here rolls that
	// tailnet's pairs up to the same cursor the API poll uses.
	if !lastPollEnd.IsZero() {
		if err := rollClosedMinutes(ctx, tx, tailnetID, lastPollEnd.UTC().Unix()); err != nil {
			return err
		}
	}
	return s.commitWrite(tx, tailnetID)
}

type sqlExecer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// upsertPollCursor moves a tailnet's cursor forward and leaves it unchanged
// when the incoming time is missing or older.
func upsertPollCursor(ctx context.Context, exec sqlExecer, tailnetID string, lastPollEnd time.Time) error {
	const sqliteFormat = "2006-01-02 15:04:05"
	stamp := lastPollEnd.UTC().Format(sqliteFormat)
	_, err := exec.ExecContext(ctx, `
		INSERT INTO poll_state (tailnet_id, last_poll_end, updated_at)
		VALUES (?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(tailnet_id) DO UPDATE SET
			last_poll_end = CASE
				WHEN poll_state.last_poll_end IS NULL OR poll_state.last_poll_end = ''
				  OR datetime(poll_state.last_poll_end) IS NULL
				  OR datetime(poll_state.last_poll_end) < datetime(?)
				THEN ? ELSE poll_state.last_poll_end END,
			updated_at = CURRENT_TIMESTAMP
	`, tailnetID, stamp, stamp, stamp)
	if err != nil {
		return fmt.Errorf("failed to update poll state: %w", err)
	}
	return nil
}

func (s *SQLiteStore) IsObjectIngested(ctx context.Context, tailnetID string, key string) (bool, error) {
	if err := checkTailnetID(tailnetID); err != nil {
		return false, err
	}
	var exists int
	err := s.db.QueryRowContext(ctx,
		"SELECT 1 FROM ingested_objects WHERE tailnet_id = ? AND object_key = ? LIMIT 1",
		tailnetID, key,
	).Scan(&exists)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("failed to check ingested object: %w", err)
	}
	return true, nil
}

// GetObjectsNeedingMetadata returns ingested objects whose embedded node
// metadata has not been hydrated, or whose previously recorded nodes are
// missing from node_metadata. The latter makes hydration repair partial
// metadata-table loss without rescanning the entire object store.
func (s *SQLiteStore) GetObjectsNeedingMetadata(ctx context.Context, tailnetID string, limit int) ([]string, error) {
	if err := checkTailnetID(tailnetID); err != nil {
		return nil, err
	}
	if limit <= 0 {
		return []string{}, nil
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT o.object_key
		FROM ingested_objects o
		WHERE o.tailnet_id = ?
		  AND (
			o.metadata_hydrated = 0
			OR EXISTS (
				SELECT 1
				FROM object_metadata_nodes m
				WHERE m.tailnet_id = o.tailnet_id
				  AND m.object_key = o.object_key
				  AND NOT EXISTS (
					SELECT 1 FROM node_metadata n
					WHERE n.tailnet_id = o.tailnet_id AND n.node_id = m.node_id
				  )
			)
		  )
		ORDER BY o.ingested_at ASC, o.object_key ASC
		LIMIT ?
	`, tailnetID, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to query objects needing metadata: %w", err)
	}
	defer rows.Close()

	var keys []string
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			return nil, fmt.Errorf("failed to scan object needing metadata: %w", err)
		}
		keys = append(keys, key)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to read objects needing metadata: %w", err)
	}
	return keys, nil
}

// MarkObjectMetadataHydrated records the node IDs found while rereading an
// already-ingested object. It is transactional so a failed metadata upsert
// never makes the object look complete on the next poll.
func (s *SQLiteStore) MarkObjectMetadataHydrated(ctx context.Context, tailnetID string, key string, nodeIDs []string) error {
	if err := checkTailnetID(tailnetID); err != nil {
		return err
	}
	if key == "" {
		return fmt.Errorf("object key is required")
	}
	unlock := s.lockTailnet(tailnetID)
	defer unlock()

	tx, err := s.beginWrite(ctx, tailnetID)
	if err != nil {
		return fmt.Errorf("failed to begin metadata hydration transaction: %w", err)
	}
	defer tx.Rollback()
	if err := recordObjectMetadataTx(ctx, tx, tailnetID, key, nodeIDs); err != nil {
		return err
	}
	if err := s.commitWrite(tx, tailnetID); err != nil {
		return fmt.Errorf("failed to commit metadata hydration: %w", err)
	}
	return nil
}

func upsertNodeMetadataTx(ctx context.Context, tx *sql.Tx, tailnetID string, nodes []NodeMetadata) error {
	if len(nodes) == 0 {
		return nil
	}
	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO node_metadata (tailnet_id, node_id, name, hostname, owner, ips, tags, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(tailnet_id, node_id) DO UPDATE SET
			name = CASE WHEN excluded.name != '' THEN excluded.name ELSE node_metadata.name END,
			hostname = CASE WHEN excluded.hostname != '' THEN excluded.hostname ELSE node_metadata.hostname END,
			owner = CASE WHEN excluded.owner != '' THEN excluded.owner ELSE node_metadata.owner END,
			ips = CASE WHEN excluded.ips != '[]' THEN excluded.ips ELSE node_metadata.ips END,
			tags = CASE WHEN excluded.tags != '[]' THEN excluded.tags ELSE node_metadata.tags END,
			updated_at = CURRENT_TIMESTAMP
	`)
	if err != nil {
		return fmt.Errorf("failed to prepare node metadata upsert: %w", err)
	}
	defer stmt.Close()

	for _, node := range nodes {
		if node.NodeID == "" {
			continue
		}
		ips, err := json.Marshal(node.IPs)
		if err != nil {
			return fmt.Errorf("failed to marshal node metadata IPs: %w", err)
		}
		tags, err := json.Marshal(node.Tags)
		if err != nil {
			return fmt.Errorf("failed to marshal node metadata tags: %w", err)
		}
		if _, err := stmt.ExecContext(ctx, tailnetID, node.NodeID, node.Name, node.Hostname, node.Owner, string(ips), string(tags)); err != nil {
			return fmt.Errorf("failed to upsert node metadata: %w", err)
		}
	}
	return nil
}

func recordObjectMetadataTx(ctx context.Context, tx *sql.Tx, tailnetID, key string, nodeIDs []string) error {
	if key == "" {
		return fmt.Errorf("object key is required")
	}
	stmt, err := tx.PrepareContext(ctx, `
		INSERT OR IGNORE INTO object_metadata_nodes (tailnet_id, object_key, node_id)
		VALUES (?, ?, ?)
	`)
	if err != nil {
		return fmt.Errorf("failed to prepare object metadata index: %w", err)
	}
	defer stmt.Close()

	seen := make(map[string]struct{}, len(nodeIDs))
	for _, nodeID := range nodeIDs {
		if nodeID == "" {
			continue
		}
		if _, ok := seen[nodeID]; ok {
			continue
		}
		seen[nodeID] = struct{}{}
		if _, err := stmt.ExecContext(ctx, tailnetID, key, nodeID); err != nil {
			return fmt.Errorf("failed to index metadata for object %q: %w", key, err)
		}
	}
	result, err := tx.ExecContext(ctx,
		`UPDATE ingested_objects SET metadata_hydrated = 1 WHERE tailnet_id = ? AND object_key = ?`,
		tailnetID, key,
	)
	if err != nil {
		return fmt.Errorf("failed to mark object metadata hydrated: %w", err)
	}
	if affected, err := result.RowsAffected(); err != nil {
		return fmt.Errorf("failed to verify object metadata hydration: %w", err)
	} else if affected != 1 {
		return fmt.Errorf("object %q is not recorded as ingested", key)
	}
	return nil
}

func (s *SQLiteStore) UpsertNodeMetadata(ctx context.Context, tailnetID string, nodes []NodeMetadata) error {
	if err := checkTailnetID(tailnetID); err != nil {
		return err
	}
	if len(nodes) == 0 {
		return nil
	}
	unlock := s.lockTailnet(tailnetID)
	defer unlock()

	tx, err := s.beginWrite(ctx, tailnetID)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()
	if err := upsertNodeMetadataTx(ctx, tx, tailnetID, nodes); err != nil {
		return err
	}
	return s.commitWrite(tx, tailnetID)
}

func (s *SQLiteStore) GetNodeMetadata(ctx context.Context, tailnetID string) ([]NodeMetadata, error) {
	if err := checkTailnetID(tailnetID); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT node_id, name, hostname, owner, ips, tags, updated_at
		FROM node_metadata
		WHERE tailnet_id = ?
	`, tailnetID)
	if err != nil {
		return nil, fmt.Errorf("failed to query node metadata: %w", err)
	}
	defer rows.Close()

	var result []NodeMetadata
	for rows.Next() {
		var node NodeMetadata
		var ipsJSON, tagsJSON string
		var updated sql.NullString
		if err := rows.Scan(&node.NodeID, &node.Name, &node.Hostname, &node.Owner, &ipsJSON, &tagsJSON, &updated); err != nil {
			return nil, fmt.Errorf("failed to scan node metadata: %w", err)
		}
		if err := json.Unmarshal([]byte(ipsJSON), &node.IPs); err != nil {
			return nil, fmt.Errorf("failed to decode IP metadata for node %q: %w", node.NodeID, err)
		}
		if err := json.Unmarshal([]byte(tagsJSON), &node.Tags); err != nil {
			return nil, fmt.Errorf("failed to decode tag metadata for node %q: %w", node.NodeID, err)
		}
		if updated.Valid {
			node.Updated = parseTime(updated.String)
		}
		result = append(result, node)
	}
	return result, rows.Err()
}

// GetDataRange returns the time range of data stored in node_pairs.
func (s *SQLiteStore) GetDataRange(ctx context.Context, tailnetID string) (*DataRange, error) {
	if err := checkTailnetID(tailnetID); err != nil {
		return nil, err
	}
	var minBucket, maxBucket sql.NullInt64
	var count int64
	err := s.db.QueryRowContext(ctx,
		"SELECT MIN(bucket), MAX(bucket), COUNT(*) FROM node_pairs WHERE tailnet_id = ?",
		tailnetID,
	).Scan(&minBucket, &maxBucket, &count)
	if err != nil {
		return nil, fmt.Errorf("failed to get data range: %w", err)
	}
	if count == 0 || !minBucket.Valid {
		return &DataRange{}, nil
	}
	return &DataRange{
		Earliest: time.Unix(minBucket.Int64, 0).UTC(),
		// Buckets are minute-start timestamps and all range queries use a
		// half-open end. Return the end of the newest bucket so a database with
		// one bucket still describes a usable range.
		Latest: time.Unix(maxBucket.Int64+60, 0).UTC(),
		Count:  count,
	}, nil
}

// Cleanup deletes rows older than retention from all four data tables, and
// drops ingest bookkeeping and node metadata that retention no longer needs.
func (s *SQLiteStore) Cleanup(ctx context.Context, tailnetID string, retention time.Duration) (int64, error) {
	if err := checkTailnetID(tailnetID); err != nil {
		return 0, err
	}
	if retention <= 0 {
		return 0, nil
	}

	unlock := s.lockTailnet(tailnetID)
	defer unlock()

	tx, err := s.beginWrite(ctx, tailnetID)
	if err != nil {
		return 0, fmt.Errorf("failed to begin cleanup transaction: %w", err)
	}
	defer tx.Rollback()

	cutoff := time.Now().UTC().Unix() - int64(retention.Seconds())
	var total int64
	for _, table := range []string{"node_pairs", "bandwidth", "bandwidth_by_node", "traffic_stats"} {
		result, err := tx.ExecContext(ctx,
			fmt.Sprintf("DELETE FROM %s WHERE tailnet_id = ? AND bucket < ?", table), tailnetID, cutoff,
		)
		if err != nil {
			return 0, fmt.Errorf("failed to cleanup %s: %w", table, err)
		}
		n, err := result.RowsAffected()
		if err != nil {
			return 0, fmt.Errorf("failed to count cleanup rows for %s: %w", table, err)
		}
		if n > 0 {
			total += n
		}
	}
	pruned, err := pruneHourRollups(ctx, tx, tailnetID, cutoff)
	if err != nil {
		return 0, err
	}
	if pruned > 0 {
		total += pruned
	}
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM object_metadata_nodes
		 WHERE tailnet_id = ?
		   AND object_key IN (
			SELECT object_key FROM ingested_objects
			WHERE tailnet_id = ? AND ingested_at < datetime('now', '-' || ? || ' seconds')
		 )`,
		tailnetID, tailnetID, int64(retention.Seconds()),
	); err != nil {
		return 0, fmt.Errorf("failed to cleanup object metadata index: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		"DELETE FROM ingested_objects WHERE tailnet_id = ? AND ingested_at < datetime('now', '-' || ? || ' seconds')",
		tailnetID, int64(retention.Seconds()),
	); err != nil {
		return 0, fmt.Errorf("failed to cleanup ingested_objects: %w", err)
	}
	// Node metadata is refreshed whenever a node appears in an ingested
	// object. A row not refreshed within retention belongs to a node that is
	// gone (ephemeral nodes churn fast), and the poller reloads the whole
	// table on every device refresh. Keep any node still named by a retained
	// node pair.
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM node_metadata
		 WHERE tailnet_id = ?
		   AND updated_at < datetime('now', '-' || ? || ' seconds')
		   AND NOT EXISTS (SELECT 1 FROM node_pairs p WHERE p.tailnet_id = node_metadata.tailnet_id AND p.src_node_id = node_metadata.node_id)
		   AND NOT EXISTS (SELECT 1 FROM node_pairs p WHERE p.tailnet_id = node_metadata.tailnet_id AND p.dst_node_id = node_metadata.node_id)`,
		tailnetID, int64(retention.Seconds()),
	); err != nil {
		return 0, fmt.Errorf("failed to cleanup node_metadata: %w", err)
	}
	if err := s.commitWrite(tx, tailnetID); err != nil {
		return 0, fmt.Errorf("failed to commit cleanup: %w", err)
	}
	return total, nil
}

// GetStats returns row counts, database size, and data range.
// Counts and the data range are limited to tailnetID. dbSizeBytes is the
// whole database file, which is shared by every tailnet.
func (s *SQLiteStore) GetStats(ctx context.Context, tailnetID string) (map[string]any, error) {
	if err := checkTailnetID(tailnetID); err != nil {
		return nil, err
	}
	tx, err := s.beginRead(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	tableCounts := make(map[string]int64)
	for _, table := range []string{"node_pairs", "bandwidth", "bandwidth_by_node", "traffic_stats", "ingested_objects", "node_metadata"} {
		var count int64
		if err := tx.QueryRowContext(ctx, fmt.Sprintf("SELECT COUNT(*) FROM %s WHERE tailnet_id = ?", table), tailnetID).Scan(&count); err != nil {
			return nil, fmt.Errorf("failed to count %s: %w", table, err)
		}
		tableCounts[table] = count
	}

	var pageCount, pageSize int64
	if err := tx.QueryRowContext(ctx, "PRAGMA page_count").Scan(&pageCount); err != nil {
		return nil, fmt.Errorf("failed to read database page count: %w", err)
	}
	if err := tx.QueryRowContext(ctx, "PRAGMA page_size").Scan(&pageSize); err != nil {
		return nil, fmt.Errorf("failed to read database page size: %w", err)
	}

	var minB, maxB sql.NullInt64
	var cnt int64
	if err := tx.QueryRowContext(ctx,
		"SELECT MIN(bucket), MAX(bucket), COUNT(*) FROM node_pairs WHERE tailnet_id = ?", tailnetID,
	).Scan(&minB, &maxB, &cnt); err != nil {
		return nil, fmt.Errorf("failed to read database data range: %w", err)
	}
	dr := &DataRange{}
	if cnt > 0 && minB.Valid {
		dr.Earliest = time.Unix(minB.Int64, 0).UTC()
		dr.Latest = time.Unix(maxB.Int64+60, 0).UTC()
		dr.Count = cnt
	}

	return map[string]any{
		"tableCounts": tableCounts,
		"dbSizeBytes": pageCount * pageSize,
		"dataRange":   dr,
	}, nil
}
