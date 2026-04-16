package database

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"time"
)

// GetPollState retrieves the current poll state
func (s *SQLiteStore) GetPollState(ctx context.Context) (*PollState, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var state PollState
	var lastPollEnd, updatedAt sql.NullString

	err := s.db.QueryRowContext(ctx, `
		SELECT last_poll_end, updated_at FROM poll_state WHERE id = 1
	`).Scan(&lastPollEnd, &updatedAt)
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

// UpdatePollState updates the poll state
func (s *SQLiteStore) UpdatePollState(ctx context.Context, lastPollEnd time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	const sqliteFormat = "2006-01-02 15:04:05"
	_, err := s.db.ExecContext(ctx, `
		UPDATE poll_state SET last_poll_end = ?, updated_at = CURRENT_TIMESTAMP WHERE id = 1
	`, lastPollEnd.UTC().Format(sqliteFormat))
	if err != nil {
		return fmt.Errorf("failed to update poll state: %w", err)
	}

	return nil
}

// GetDataRange returns the available data time range.
// Checks minutely first, then falls back to hourly and daily aggregation tables.
func (s *SQLiteStore) GetDataRange(ctx context.Context) (*DataRange, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	// Check tables in order of granularity: minutely → hourly → daily
	tables := []string{"node_pairs_minutely", "node_pairs_hourly", "node_pairs_daily"}
	for _, table := range tables {
		var minBucket, maxBucket sql.NullInt64
		var count int64
		err := s.db.QueryRowContext(ctx, fmt.Sprintf(
			"SELECT MIN(bucket), MAX(bucket), COUNT(*) FROM %s", table,
		)).Scan(&minBucket, &maxBucket, &count)
		if err != nil {
			return nil, fmt.Errorf("failed to get data range from %s: %w", table, err)
		}
		if count > 0 && minBucket.Valid && maxBucket.Valid {
			return &DataRange{
				Earliest: time.Unix(minBucket.Int64, 0).UTC(),
				Latest:   time.Unix(maxBucket.Int64, 0).UTC(),
				Count:    count,
			}, nil
		}
	}

	// No data in any table
	return &DataRange{}, nil
}

// Cleanup removes old data based on retention period
func (s *SQLiteStore) Cleanup(ctx context.Context, retention time.Duration) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now().UTC().Unix()
	cutoff := now - int64(retention.Seconds())
	var totalDeleted int64

	tables := []string{"node_pairs", "bandwidth", "bandwidth_by_node", "traffic_stats"}
	for _, table := range tables {
		result, err := s.db.ExecContext(ctx, fmt.Sprintf("DELETE FROM %s WHERE bucket < ?", table), cutoff)
		if err != nil {
			log.Printf("Warning: failed to cleanup %s: %v", table, err)
			continue
		}
		if deleted, _ := result.RowsAffected(); deleted > 0 {
			totalDeleted += deleted
		}
	}

	return totalDeleted, nil
}

// GetStats returns database statistics
func (s *SQLiteStore) GetStats(ctx context.Context) (map[string]any, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	stats := make(map[string]any)

	// Count records in each table
	tables := []string{
		"flow_logs_current",
		"node_pairs_minutely", "node_pairs_hourly", "node_pairs_daily",
		"bandwidth_minutely", "bandwidth_hourly", "bandwidth_daily",
		"bandwidth_by_node_minutely", "bandwidth_by_node_hourly", "bandwidth_by_node_daily",
		"traffic_stats_minutely", "traffic_stats_hourly", "traffic_stats_daily",
	}

	tableCounts := make(map[string]int64)
	for _, table := range tables {
		var count int64
		// Table names are hardcoded constants, safe to use fmt.Sprintf
		if err := s.db.QueryRowContext(ctx, fmt.Sprintf("SELECT COUNT(*) FROM %s", table)).Scan(&count); err != nil {
			// Log but don't fail - stats are best-effort
			count = 0
		}
		tableCounts[table] = count
	}
	stats["tableCounts"] = tableCounts

	// Database size
	var pageCount, pageSize int64
	if err := s.db.QueryRowContext(ctx, "PRAGMA page_count").Scan(&pageCount); err != nil {
		pageCount = 0
	}
	if err := s.db.QueryRowContext(ctx, "PRAGMA page_size").Scan(&pageSize); err != nil {
		pageSize = 0
	}
	stats["dbSizeBytes"] = pageCount * pageSize

	// Data range (inline cascade: minutely → hourly → daily to avoid nested lock)
	dataRange := &DataRange{}
	for _, drTable := range []string{"node_pairs_minutely", "node_pairs_hourly", "node_pairs_daily"} {
		var minB, maxB sql.NullInt64
		var cnt int64
		qErr := s.db.QueryRowContext(ctx, fmt.Sprintf(
			"SELECT MIN(bucket), MAX(bucket), COUNT(*) FROM %s", drTable,
		)).Scan(&minB, &maxB, &cnt)
		if qErr == nil && cnt > 0 && minB.Valid && maxB.Valid {
			dataRange.Earliest = time.Unix(minB.Int64, 0).UTC()
			dataRange.Latest = time.Unix(maxB.Int64, 0).UTC()
			dataRange.Count = cnt
			break
		}
	}
	stats["dataRange"] = dataRange

	return stats, nil
}
