package database

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

const (
	// NewPairDefaultLookback is how far behind the selected window a pair
	// must be absent before it counts as new.
	NewPairDefaultLookback = 7 * 24 * time.Hour
	// NewPairMinLookback is the shortest lookback a caller can ask for.
	NewPairMinLookback = time.Hour
	// NewPairMaxLookback bounds the rollup scan.
	NewPairMaxLookback = 90 * 24 * time.Hour
)

// NewPair is a directed src/dst first seen inside the selected window.
type NewPair struct {
	SrcNodeID   string    `json:"srcNodeId"`
	SrcHostname string    `json:"srcHostname"`
	DstNodeID   string    `json:"dstNodeId"`
	DstHostname string    `json:"dstHostname"`
	TxBytes     int64     `json:"txBytes"`
	RxBytes     int64     `json:"rxBytes"`
	TotalBytes  int64     `json:"totalBytes"`
	FlowCount   int64     `json:"flowCount"`
	FirstSeen   time.Time `json:"firstSeen"`
}

// NewPairQuery selects one page of pairs that are new relative to a lookback.
// Lookback <= 0 selects NewPairDefaultLookback. An empty TrafficTypes list
// leaves out physical rows.
type NewPairQuery struct {
	Lookback     time.Duration
	Limit        int
	Offset       int
	TrafficTypes []string
}

// ListNewPairs returns src/dst pairs that appear in [start, end) and do not
// appear in the lookback [start-lookback, start). Complete hours are read
// from node_pair_hours. The partial hour at each end is read from minute
// rows. Pages are ordered by first seen descending, then by volume.
func (s *SQLiteStore) ListNewPairs(ctx context.Context, tailnetID string, start, end time.Time, query NewPairQuery) ([]NewPair, bool, error) {
	if err := checkTailnetID(tailnetID); err != nil {
		return nil, false, err
	}
	startUnix, endUnix, err := nodePairBounds(start, end)
	if err != nil {
		return nil, false, err
	}
	lookback := query.Lookback
	if lookback <= 0 {
		lookback = NewPairDefaultLookback
	}
	if lookback < NewPairMinLookback || lookback > NewPairMaxLookback {
		return nil, false, fmt.Errorf("lookback must be between %s and %s", NewPairMinLookback, NewPairMaxLookback)
	}
	limit := query.Limit
	if limit <= 0 {
		limit = RankDefaultLimit
	}
	if limit > RankMaxLimit {
		limit = RankMaxLimit
	}
	if query.Offset < 0 || query.Offset > RankMaxOffset {
		return nil, false, fmt.Errorf("offset must be a non-negative integer no larger than %d", RankMaxOffset)
	}
	priorStart := start.Add(-lookback)
	priorUnix := priorStart.UTC().Unix()
	if priorUnix >= startUnix {
		return nil, false, fmt.Errorf("lookback must end before the selected window")
	}

	tx, err := s.beginRead(ctx)
	if err != nil {
		return nil, false, err
	}
	defer tx.Rollback()

	windowPlan, err := s.hourPlan(ctx, tx, tailnetID, startUnix, endUnix, 0)
	if err != nil {
		return nil, false, err
	}
	priorPlan, err := s.hourPlan(ctx, tx, tailnetID, priorUnix, startUnix, 0)
	if err != nil {
		return nil, false, err
	}
	clause, typeArgs := countedTrafficClause(query.TrafficTypes)
	windowSource, windowArgs := windowPlan.unionPairRows(tailnetID,
		"src_node_id, dst_node_id, tx_bytes, rx_bytes, flow_count, bucket AS seen",
		"src_node_id, dst_node_id, tx_bytes, rx_bytes, flow_count, min_bucket AS seen",
		clause, typeArgs,
	)
	if windowSource == "" {
		return nil, false, nil
	}
	priorSource, priorArgs := priorPlan.unionPairRows(tailnetID,
		"src_node_id, dst_node_id",
		"src_node_id, dst_node_id",
		clause, typeArgs,
	)
	priorSQL := ""
	args := append([]any{}, windowArgs...)
	if priorSource != "" {
		priorSQL = fmt.Sprintf(`
		AND NOT EXISTS (
			SELECT 1 FROM (%s) AS prior
			WHERE prior.src_node_id = window_pairs.src_node_id
			  AND prior.dst_node_id = window_pairs.dst_node_id
		)`, priorSource)
		args = append(args, priorArgs...)
	}
	statement := fmt.Sprintf(newPairSQL, windowSource, priorSQL)
	args = append(args, limit+1, query.Offset, tailnetID, tailnetID)
	rows, err := tx.QueryContext(ctx, statement, args...)
	if err != nil {
		return nil, false, fmt.Errorf("failed to query new pairs: %w", err)
	}
	defer rows.Close()

	pairs := make([]NewPair, 0)
	for rows.Next() {
		var pair NewPair
		var seen sql.NullInt64
		if err := rows.Scan(
			&pair.SrcNodeID, &pair.SrcHostname,
			&pair.DstNodeID, &pair.DstHostname,
			&pair.TxBytes, &pair.RxBytes, &pair.TotalBytes, &pair.FlowCount,
			&seen,
		); err != nil {
			return nil, false, fmt.Errorf("failed to scan new pair: %w", err)
		}
		if seen.Valid {
			pair.FirstSeen = time.Unix(seen.Int64, 0).UTC()
		}
		pairs = append(pairs, pair)
	}
	if err := rows.Err(); err != nil {
		return nil, false, fmt.Errorf("failed to query new pairs: %w", err)
	}
	if len(pairs) > limit {
		return pairs[:limit], true, nil
	}
	return pairs, false, nil
}

const newPairSQL = `
WITH window_rows AS (%s),
window_pairs AS (
	SELECT src_node_id,
	       dst_node_id,
	       COALESCE(SUM(tx_bytes), 0) AS tx,
	       COALESCE(SUM(rx_bytes), 0) AS rx,
	       COALESCE(SUM(tx_bytes), 0) + COALESCE(SUM(rx_bytes), 0) AS total,
	       COALESCE(SUM(flow_count), 0) AS flows,
	       MIN(seen) AS first_seen
	FROM window_rows
	GROUP BY src_node_id, dst_node_id
)
SELECT ranked.src_node_id,
       COALESCE(NULLIF(src.hostname, ''), NULLIF(src.name, ''), ''),
       ranked.dst_node_id,
       COALESCE(NULLIF(dst.hostname, ''), NULLIF(dst.name, ''), ''),
       COALESCE(ranked.tx, 0),
       COALESCE(ranked.rx, 0),
       COALESCE(ranked.total, 0),
       COALESCE(ranked.flows, 0),
       ranked.first_seen
FROM (
	SELECT src_node_id, dst_node_id, tx, rx, total, flows, first_seen
	FROM window_pairs
	WHERE 1 = 1
	%s
	ORDER BY first_seen DESC, total DESC, src_node_id ASC, dst_node_id ASC
	LIMIT ? OFFSET ?
) AS ranked
LEFT JOIN node_metadata AS src
  ON src.tailnet_id = ? AND src.node_id = ranked.src_node_id
LEFT JOIN node_metadata AS dst
  ON dst.tailnet_id = ? AND dst.node_id = ranked.dst_node_id
ORDER BY ranked.first_seen DESC, ranked.total DESC, ranked.src_node_id ASC, ranked.dst_node_id ASC
`
