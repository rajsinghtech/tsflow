package database

import (
	"context"
	"database/sql"
	"encoding/json"
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

func (s *SQLiteStore) IsObjectIngested(ctx context.Context, key string) (bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var exists int
	err := s.db.QueryRowContext(ctx,
		"SELECT 1 FROM ingested_objects WHERE object_key = ? LIMIT 1",
		key,
	).Scan(&exists)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("failed to check ingested object: %w", err)
	}
	return true, nil
}

func upsertNodeMetadataTx(ctx context.Context, tx *sql.Tx, nodes []NodeMetadata) error {
	if len(nodes) == 0 {
		return nil
	}
	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO node_metadata (node_id, name, hostname, owner, ips, tags, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(node_id) DO UPDATE SET
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
		if _, err := stmt.ExecContext(ctx, node.NodeID, node.Name, node.Hostname, node.Owner, string(ips), string(tags)); err != nil {
			return fmt.Errorf("failed to upsert node metadata: %w", err)
		}
	}
	return nil
}

func (s *SQLiteStore) UpsertNodeMetadata(ctx context.Context, nodes []NodeMetadata) error {
	if len(nodes) == 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()
	if err := upsertNodeMetadataTx(ctx, tx, nodes); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *SQLiteStore) GetNodeMetadata(ctx context.Context) ([]NodeMetadata, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	rows, err := s.db.QueryContext(ctx, `
		SELECT node_id, name, hostname, owner, ips, tags, updated_at
		FROM node_metadata
	`)
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
		_ = json.Unmarshal([]byte(ipsJSON), &node.IPs)
		_ = json.Unmarshal([]byte(tagsJSON), &node.Tags)
		if updated.Valid {
			node.Updated = parseTime(updated.String)
		}
		result = append(result, node)
	}
	return result, rows.Err()
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
	if retention <= 0 {
		return 0, nil
	}

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
	if _, err := s.db.ExecContext(ctx,
		"DELETE FROM ingested_objects WHERE ingested_at < datetime('now', '-' || ? || ' seconds')",
		int64(retention.Seconds()),
	); err != nil {
		log.Printf("Warning: failed to cleanup ingested_objects: %v", err)
	}
	return total, nil
}

// GetStats returns row counts, database size, and data range.
func (s *SQLiteStore) GetStats(ctx context.Context) (map[string]any, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	tableCounts := make(map[string]int64)
	for _, table := range []string{"node_pairs", "bandwidth", "bandwidth_by_node", "traffic_stats", "ingested_objects", "node_metadata"} {
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
