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
