package database

import (
	"context"
	"fmt"
	"strings"
	"time"
)

const (
	devicePeerDefaultLimit = 20
	devicePeerMaxLimit     = 500
)

// ListDevicePeers ranks peers of nodeIDs by total bytes.
// Hours that sit inside the window come from the hourly rollup. An empty
// trafficTypes list leaves out physical rows, matching the other totals.
func (s *SQLiteStore) ListDevicePeers(ctx context.Context, tailnetID string, nodeIDs []string, start, end time.Time, trafficTypes []string, limit int) ([]DevicePeer, error) {
	if err := checkTailnetID(tailnetID); err != nil {
		return nil, err
	}
	nodeIDs = dedupeIDs(nodeIDs)
	if len(nodeIDs) == 0 {
		return nil, nil
	}
	if limit <= 0 {
		limit = devicePeerDefaultLimit
	}
	if limit > devicePeerMaxLimit {
		limit = devicePeerMaxLimit
	}
	startUnix, endUnix, err := nodePairBounds(start, end)
	if err != nil {
		return nil, err
	}
	plan, err := s.hourPlan(ctx, s.db, tailnetID, startUnix, endUnix, 0)
	if err != nil {
		return nil, err
	}
	typeClause, typeArgs := countedTrafficClause(trafficTypes)
	srcIn, srcArgs := sqlIn("src_node_id", nodeIDs)
	dstIn, dstArgs := sqlIn("dst_node_id", nodeIDs)
	srcSource, srcSourceArgs := plan.unionPairRows(tailnetID,
		"dst_node_id, tx_bytes, rx_bytes, flow_count",
		"dst_node_id, tx_bytes, rx_bytes, flow_count",
		typeClause+" AND "+srcIn, append(append([]any{}, typeArgs...), srcArgs...),
	)
	dstSource, dstSourceArgs := plan.unionPairRows(tailnetID,
		"src_node_id, tx_bytes, rx_bytes, flow_count",
		"src_node_id, tx_bytes, rx_bytes, flow_count",
		typeClause+" AND "+dstIn+" AND src_node_id != dst_node_id", append(append([]any{}, typeArgs...), dstArgs...),
	)
	if srcSource == "" || dstSource == "" {
		return nil, nil
	}
	notIn, notArgs := sqlNotIn("peer_id", nodeIDs)
	query := fmt.Sprintf(`
		SELECT peer_id, COALESCE(SUM(tx), 0), COALESCE(SUM(rx), 0), COALESCE(SUM(tx), 0) + COALESCE(SUM(rx), 0) AS total, COALESCE(SUM(fc), 0)
		FROM (
			SELECT dst_node_id AS peer_id, SUM(tx_bytes) AS tx, SUM(rx_bytes) AS rx, SUM(flow_count) AS fc
			FROM (%s) AS src_rows
			GROUP BY dst_node_id
			UNION ALL
			SELECT src_node_id AS peer_id, SUM(rx_bytes) AS tx, SUM(tx_bytes) AS rx, SUM(flow_count) AS fc
			FROM (%s) AS dst_rows
			GROUP BY src_node_id
		)
		WHERE %s
		GROUP BY peer_id
		ORDER BY total DESC, peer_id ASC
		LIMIT ?
	`, srcSource, dstSource, notIn)
	args := append(append(srcSourceArgs, dstSourceArgs...), notArgs...)
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query device peers: %w", err)
	}
	defer rows.Close()
	var peers []DevicePeer
	for rows.Next() {
		var peer DevicePeer
		if err := rows.Scan(&peer.PeerID, &peer.TxBytes, &peer.RxBytes, &peer.TotalBytes, &peer.FlowCount); err != nil {
			return nil, fmt.Errorf("failed to scan device peer: %w", err)
		}
		peers = append(peers, peer)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to query device peers: %w", err)
	}
	return peers, nil
}

// FlowsBetween returns aggregated traffic for pairs whose stored endpoints
// lie in the two id sets, in either direction. An empty trafficTypes list
// leaves out physical rows.
func (s *SQLiteStore) FlowsBetween(ctx context.Context, tailnetID string, srcIDs, dstIDs []string, start, end time.Time, trafficTypes []string) ([]NodePairAggregate, error) {
	if err := checkTailnetID(tailnetID); err != nil {
		return nil, err
	}
	srcIDs = dedupeIDs(srcIDs)
	dstIDs = dedupeIDs(dstIDs)
	if len(srcIDs) == 0 || len(dstIDs) == 0 {
		return nil, nil
	}
	startUnix, endUnix, err := nodePairBounds(start, end)
	if err != nil {
		return nil, err
	}
	clause, typeArgs := countedTrafficClause(trafficTypes)
	forwardSrc, forwardSrcArgs := sqlIn("src_node_id", srcIDs)
	forwardDst, forwardDstArgs := sqlIn("dst_node_id", dstIDs)
	reverseSrc, reverseSrcArgs := sqlIn("src_node_id", dstIDs)
	reverseDst, reverseDstArgs := sqlIn("dst_node_id", srcIDs)
	extra := clause + " AND ((" + forwardSrc + " AND " + forwardDst + ") OR (" + reverseSrc + " AND " + reverseDst + "))"
	extraArgs := append([]any{}, typeArgs...)
	extraArgs = append(extraArgs, forwardSrcArgs...)
	extraArgs = append(extraArgs, forwardDstArgs...)
	extraArgs = append(extraArgs, reverseSrcArgs...)
	extraArgs = append(extraArgs, reverseDstArgs...)

	tx, err := s.beginRead(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	plan, err := s.hourPlan(ctx, tx, tailnetID, startUnix, endUnix, 0)
	if err != nil {
		return nil, err
	}
	grouped := make(map[pairGroupKey]*pairGroup)
	for _, span := range plan.minutes {
		args := make([]any, 0, 3+len(extraArgs))
		args = append(args, tailnetID, span[0], span[1])
		args = append(args, extraArgs...)
		rows, err := tx.QueryContext(ctx, nodePairScanSQL+extra, args...)
		if err != nil {
			return nil, fmt.Errorf("failed to query flows between endpoints: %w", err)
		}
		if err := readPairRows(rows, grouped, false); err != nil {
			return nil, err
		}
	}
	for _, span := range plan.hours {
		args := make([]any, 0, 3+len(extraArgs))
		args = append(args, tailnetID, span[0], span[1])
		args = append(args, extraArgs...)
		rows, err := tx.QueryContext(ctx, nodePairHourScanSQL+extra, args...)
		if err != nil {
			return nil, fmt.Errorf("failed to query flows between endpoints: %w", err)
		}
		if err := readHourPairRows(rows, grouped); err != nil {
			return nil, err
		}
	}
	return sortedPairAggregates(grouped)
}

func sqlNotIn(column string, ids []string) (string, []any) {
	clause, args := sqlIn(column, ids)
	return strings.Replace(clause, " IN ", " NOT IN ", 1), args
}

func sqlIn(column string, ids []string) (string, []any) {
	placeholders := strings.TrimRight(strings.Repeat("?,", len(ids)), ",")
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	return column + " IN (" + placeholders + ")", args
}

func dedupeIDs(ids []string) []string {
	if len(ids) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(ids))
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}
