package database

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

const (
	// coverageGapSeconds is the quiet stretch that separates a short early
	// burst from the data that follows it.
	coverageGapSeconds int64 = 6 * 3600
	// maxStraySpanSeconds is how long that burst can be and still be ignored.
	// A longer run is treated as real coverage, even when a gap follows it.
	maxStraySpanSeconds int64 = 15 * 60
	maxStrayBuckets           = 20
	maxStraySkips             = 8
	minuteBucketSeconds int64 = 60
)

// rowQuerier is the read API shared by *sql.DB and *sql.Tx.
type rowQuerier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// continuousBucketStart is the first bucket of real coverage.
//
// buckets is sorted ascending and holds minutes that actually carried
// traffic. A prefix is only dropped when it is a short burst (at most 15
// minutes and 20 buckets) followed by at least six quiet hours. A gap inside
// a longer run stays in the range so the caller can show it.
func continuousBucketStart(buckets []int64) (int64, bool) {
	if len(buckets) == 0 {
		return 0, false
	}
	startIdx := 0
	for skip := 0; skip < maxStraySkips; skip++ {
		endIdx := startIdx
		splitIdx := -1
		for endIdx+1 < len(buckets) && endIdx-startIdx+1 < maxStrayBuckets {
			next := endIdx + 1
			gap := buckets[next] - buckets[endIdx]
			span := buckets[next] - buckets[startIdx]
			if gap >= coverageGapSeconds || span > maxStraySpanSeconds {
				splitIdx = next
				break
			}
			endIdx = next
		}
		if splitIdx < 0 {
			if endIdx+1 >= len(buckets) {
				return buckets[startIdx], true
			}
			splitIdx = endIdx + 1
		}
		gap := buckets[splitIdx] - buckets[endIdx]
		span := buckets[endIdx] - buckets[startIdx]
		if buckets[splitIdx]-buckets[startIdx] > maxStraySpanSeconds && gap < coverageGapSeconds {
			return buckets[startIdx], true
		}
		if gap >= coverageGapSeconds && span <= maxStraySpanSeconds && endIdx-startIdx+1 <= maxStrayBuckets {
			startIdx = splitIdx
			continue
		}
		return buckets[startIdx], true
	}
	return buckets[startIdx], true
}

func readDataRange(ctx context.Context, q rowQuerier, tailnetID string) (*DataRange, error) {
	var minBucket, maxBucket sql.NullInt64
	var count int64
	err := q.QueryRowContext(ctx,
		"SELECT MIN(bucket), MAX(bucket), COUNT(*) FROM node_pairs WHERE tailnet_id = ?",
		tailnetID,
	).Scan(&minBucket, &maxBucket, &count)
	if err != nil {
		return nil, fmt.Errorf("failed to get data range: %w", err)
	}
	if count == 0 || !minBucket.Valid || !maxBucket.Valid {
		return &DataRange{}, nil
	}

	coverage, err := coverageBuckets(ctx, q, tailnetID)
	if err != nil {
		return nil, err
	}
	start := minBucket.Int64
	if len(coverage) > 0 {
		if trimmed, ok := continuousBucketStart(coverage); ok {
			start = trimmed
		}
	}
	covered := count
	if start != minBucket.Int64 {
		if err := q.QueryRowContext(ctx,
			"SELECT COUNT(*) FROM node_pairs WHERE tailnet_id = ? AND bucket >= ?",
			tailnetID, start,
		).Scan(&covered); err != nil {
			return nil, fmt.Errorf("failed to count covered rows: %w", err)
		}
	}

	return &DataRange{
		Earliest: time.Unix(start, 0).UTC(),
		// Buckets are minute-start timestamps and range queries are half-open.
		// The newest bucket still has to describe a usable end.
		Latest: time.Unix(maxBucket.Int64+minuteBucketSeconds, 0).UTC(),
		Count:  covered,
	}, nil
}

// coverageBuckets reads one timestamp per minute from the stats table, which
// has a single row per bucket. That stays cheap when node_pairs has a row
// per device pair. Bandwidth is the fallback for a database that has totals
// but no stats rows.
func coverageBuckets(ctx context.Context, q rowQuerier, tailnetID string) ([]int64, error) {
	buckets, err := scanBuckets(ctx, q, `
		SELECT bucket FROM traffic_stats
		WHERE tailnet_id = ?
		  AND tcp_bytes + udp_bytes + other_proto_bytes
		    + virtual_bytes + exit_bytes + subnet_bytes + physical_bytes
		    + total_flows > 0
		ORDER BY bucket`, tailnetID)
	if err != nil || len(buckets) > 0 {
		return buckets, err
	}
	return scanBuckets(ctx, q, `
		SELECT bucket FROM bandwidth
		WHERE tailnet_id = ? AND tx_bytes + rx_bytes > 0
		ORDER BY bucket`, tailnetID)
}

func scanBuckets(ctx context.Context, q rowQuerier, query, tailnetID string) ([]int64, error) {
	result, err := q.QueryContext(ctx, query, tailnetID)
	if err != nil {
		return nil, fmt.Errorf("failed to read coverage buckets: %w", err)
	}
	defer result.Close()

	var buckets []int64
	for result.Next() {
		var bucket int64
		if err := result.Scan(&bucket); err != nil {
			return nil, fmt.Errorf("failed to scan coverage bucket: %w", err)
		}
		buckets = append(buckets, bucket)
	}
	if err := result.Err(); err != nil {
		return nil, fmt.Errorf("failed to read coverage buckets: %w", err)
	}
	return buckets, nil
}
