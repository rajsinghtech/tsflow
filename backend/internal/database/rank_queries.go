package database

import (
	"context"
	"fmt"
	"time"
)

// ListRankedTalkers returns devices ordered by volume or flow count.
//
// Hours that sit entirely inside the window are read from node_pair_hours.
// The partial hour at each end, and any window with no rolled hour, is read
// from node_pairs. An empty source returns an empty page.
func (s *SQLiteStore) ListRankedTalkers(ctx context.Context, tailnetID string, start, end time.Time, query RankQuery) ([]RankedTalker, bool, error) {
	source, args, limit, offset, sort, err := s.prepareRank(ctx, tailnetID, start, end, query)
	if err != nil {
		return nil, false, err
	}
	if source == "" {
		return nil, false, nil
	}
	statement := fmt.Sprintf(rankedTalkerSQL, source, rankTalkerOrder(sort, ""), rankTalkerOrder(sort, "ranked."))
	rows, err := s.db.QueryContext(ctx, statement, append(append([]any{}, args...), limit+1, offset, tailnetID)...)
	if err != nil {
		return nil, false, fmt.Errorf("failed to query ranked talkers: %w", err)
	}
	defer rows.Close()

	talkers := make([]RankedTalker, 0)
	for rows.Next() {
		var talker RankedTalker
		if err := rows.Scan(&talker.NodeID, &talker.Hostname, &talker.TxBytes, &talker.RxBytes, &talker.TotalBytes, &talker.FlowCount); err != nil {
			return nil, false, fmt.Errorf("failed to scan ranked talker: %w", err)
		}
		talkers = append(talkers, talker)
	}
	if err := rows.Err(); err != nil {
		return nil, false, fmt.Errorf("failed to query ranked talkers: %w", err)
	}
	if len(talkers) > limit {
		return talkers[:limit], true, nil
	}
	return talkers, false, nil
}

// ListRankedPairs returns directed pairs ordered by volume or flow count.
// The read uses the same hourly rollup split as ListRankedTalkers.
func (s *SQLiteStore) ListRankedPairs(ctx context.Context, tailnetID string, start, end time.Time, query RankQuery) ([]RankedPair, bool, error) {
	source, args, limit, offset, sort, err := s.prepareRank(ctx, tailnetID, start, end, query)
	if err != nil {
		return nil, false, err
	}
	if source == "" {
		return nil, false, nil
	}
	statement := fmt.Sprintf(rankedPairSQL, source, rankPairOrder(sort, ""), rankPairOrder(sort, "ranked."))
	rows, err := s.db.QueryContext(ctx, statement, append(append([]any{}, args...), limit+1, offset, tailnetID, tailnetID)...)
	if err != nil {
		return nil, false, fmt.Errorf("failed to query ranked pairs: %w", err)
	}
	defer rows.Close()

	pairs := make([]RankedPair, 0)
	for rows.Next() {
		var pair RankedPair
		if err := rows.Scan(
			&pair.SrcNodeID, &pair.SrcHostname,
			&pair.DstNodeID, &pair.DstHostname,
			&pair.TxBytes, &pair.RxBytes, &pair.TotalBytes, &pair.FlowCount,
		); err != nil {
			return nil, false, fmt.Errorf("failed to scan ranked pair: %w", err)
		}
		pairs = append(pairs, pair)
	}
	if err := rows.Err(); err != nil {
		return nil, false, fmt.Errorf("failed to query ranked pairs: %w", err)
	}
	if len(pairs) > limit {
		return pairs[:limit], true, nil
	}
	return pairs, false, nil
}

func (s *SQLiteStore) prepareRank(ctx context.Context, tailnetID string, start, end time.Time, query RankQuery) (string, []any, int, int, string, error) {
	if err := checkTailnetID(tailnetID); err != nil {
		return "", nil, 0, 0, "", err
	}
	startUnix, endUnix, err := nodePairBounds(start, end)
	if err != nil {
		return "", nil, 0, 0, "", err
	}
	limit, offset, sort, err := normalizeRankQuery(query)
	if err != nil {
		return "", nil, 0, 0, "", err
	}
	plan, err := s.hourPlan(ctx, s.db, tailnetID, startUnix, endUnix, 0)
	if err != nil {
		return "", nil, 0, 0, "", err
	}
	clause, typeArgs := countedTrafficClause(query.TrafficTypes)
	source, args := plan.unionPairRows(tailnetID,
		"src_node_id, dst_node_id, tx_bytes, rx_bytes, flow_count",
		"src_node_id, dst_node_id, tx_bytes, rx_bytes, flow_count",
		clause, typeArgs,
	)
	return source, args, limit, offset, sort, nil
}

func normalizeRankQuery(query RankQuery) (int, int, string, error) {
	limit := query.Limit
	if limit <= 0 {
		limit = RankDefaultLimit
	}
	if limit > RankMaxLimit {
		limit = RankMaxLimit
	}
	if query.Offset < 0 || query.Offset > RankMaxOffset {
		return 0, 0, "", fmt.Errorf("offset must be a non-negative integer no larger than %d", RankMaxOffset)
	}
	switch query.Sort {
	case "", RankSortBytes:
		return limit, query.Offset, RankSortBytes, nil
	case RankSortFlows:
		return limit, query.Offset, RankSortFlows, nil
	default:
		return 0, 0, "", fmt.Errorf("sort must be bytes or flows")
	}
}

func rankTalkerOrder(sort, prefix string) string {
	total := prefix + "total"
	flows := prefix + "flows"
	nodeID := prefix + "node_id"
	if sort == RankSortFlows {
		return fmt.Sprintf("%s DESC, %s DESC, %s ASC", flows, total, nodeID)
	}
	return fmt.Sprintf("%s DESC, %s ASC", total, nodeID)
}

func rankPairOrder(sort, prefix string) string {
	total := prefix + "total"
	flows := prefix + "flows"
	src := prefix + "src_node_id"
	dst := prefix + "dst_node_id"
	if sort == RankSortFlows {
		return fmt.Sprintf("%s DESC, %s DESC, %s ASC, %s ASC", flows, total, src, dst)
	}
	return fmt.Sprintf("%s DESC, %s ASC, %s ASC", total, src, dst)
}

const rankedTalkerSQL = `
WITH pair_rows AS (%s)
SELECT ranked.node_id,
       COALESCE(NULLIF(m.hostname, ''), NULLIF(m.name, ''), ''),
       COALESCE(ranked.tx, 0),
       COALESCE(ranked.rx, 0),
       COALESCE(ranked.total, 0),
       COALESCE(ranked.flows, 0)
FROM (
	SELECT node_id,
	       COALESCE(SUM(tx), 0) AS tx,
	       COALESCE(SUM(rx), 0) AS rx,
	       COALESCE(SUM(tx), 0) + COALESCE(SUM(rx), 0) AS total,
	       COALESCE(SUM(flows), 0) AS flows
	FROM (
		SELECT src_node_id AS node_id,
		       SUM(tx_bytes) AS tx,
		       SUM(rx_bytes) AS rx,
		       SUM(flow_count) AS flows
		FROM pair_rows
		GROUP BY src_node_id
		UNION ALL
		SELECT dst_node_id AS node_id,
		       SUM(rx_bytes) AS tx,
		       SUM(tx_bytes) AS rx,
		       SUM(flow_count) AS flows
		FROM pair_rows
		WHERE src_node_id != dst_node_id
		GROUP BY dst_node_id
	) AS node_bytes
	GROUP BY node_id
	ORDER BY %s
	LIMIT ? OFFSET ?
) AS ranked
LEFT JOIN node_metadata AS m
  ON m.tailnet_id = ? AND m.node_id = ranked.node_id
ORDER BY %s
`

const rankedPairSQL = `
WITH pair_rows AS (%s)
SELECT ranked.src_node_id,
       COALESCE(NULLIF(src.hostname, ''), NULLIF(src.name, ''), ''),
       ranked.dst_node_id,
       COALESCE(NULLIF(dst.hostname, ''), NULLIF(dst.name, ''), ''),
       COALESCE(ranked.tx, 0),
       COALESCE(ranked.rx, 0),
       COALESCE(ranked.total, 0),
       COALESCE(ranked.flows, 0)
FROM (
	SELECT src_node_id,
	       dst_node_id,
	       COALESCE(SUM(tx_bytes), 0) AS tx,
	       COALESCE(SUM(rx_bytes), 0) AS rx,
	       COALESCE(SUM(tx_bytes), 0) + COALESCE(SUM(rx_bytes), 0) AS total,
	       COALESCE(SUM(flow_count), 0) AS flows
	FROM pair_rows
	GROUP BY src_node_id, dst_node_id
	ORDER BY %s
	LIMIT ? OFFSET ?
) AS ranked
LEFT JOIN node_metadata AS src
  ON src.tailnet_id = ? AND src.node_id = ranked.src_node_id
LEFT JOIN node_metadata AS dst
  ON dst.tailnet_id = ? AND dst.node_id = ranked.dst_node_id
ORDER BY %s
`
