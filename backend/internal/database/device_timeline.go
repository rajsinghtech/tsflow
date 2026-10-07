package database

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// TrafficByteTotal is transmitted and received bytes for one series.
type TrafficByteTotal struct {
	TxBytes int64 `json:"txBytes"`
	RxBytes int64 `json:"rxBytes"`
}

// TimelineBucket is one device's bytes in a time bucket, split by traffic type.
// Physical is omitted unless the caller asked for physical traffic.
type TimelineBucket struct {
	Time     time.Time         `json:"time"`
	Seconds  int64             `json:"seconds,omitempty"`
	Virtual  TrafficByteTotal  `json:"virtual"`
	Subnet   TrafficByteTotal  `json:"subnet"`
	Exit     TrafficByteTotal  `json:"exit"`
	Physical *TrafficByteTotal `json:"physical,omitempty"`
}

// TimelinePeer is one peer of a device over the window.
type TimelinePeer struct {
	PeerID     string `json:"peerId"`
	Hostname   string `json:"hostname"`
	TxBytes    int64  `json:"txBytes"`
	RxBytes    int64  `json:"rxBytes"`
	TotalBytes int64  `json:"totalBytes"`
	FlowCount  int64  `json:"flowCount"`
}

// DeviceTimeline is bytes over time for one device, plus one page of peers.
type DeviceTimeline struct {
	NodeID        string           `json:"nodeId"`
	Hostname      string           `json:"hostname"`
	BucketSeconds int64            `json:"bucketSeconds"`
	Buckets       []TimelineBucket `json:"buckets"`
	Peers         []TimelinePeer   `json:"peers"`
	HasMore       bool             `json:"hasMore"`
}

// TimelineQuery pages the peer list. Limit <= 0 selects the ranked default.
// An empty TrafficTypes list leaves out physical traffic.
type TimelineQuery struct {
	Limit        int
	Offset       int
	TrafficTypes []string
}

// GetDeviceTimeline reads one device's bytes over time and its top peers.
// Complete hours come from node_pair_hours. A window of two hours or less
// stays on minute rows so the chart can keep one-minute buckets. Peer pages
// are ordered by volume descending.
func (s *SQLiteStore) GetDeviceTimeline(ctx context.Context, tailnetID, nodeID string, start, end time.Time, query TimelineQuery) (*DeviceTimeline, error) {
	if err := checkTailnetID(tailnetID); err != nil {
		return nil, err
	}
	nodeID = strings.TrimSpace(nodeID)
	if nodeID == "" {
		return nil, fmt.Errorf("node is required")
	}
	startUnix, endUnix, err := nodePairBounds(start, end)
	if err != nil {
		return nil, err
	}
	limit := query.Limit
	if limit <= 0 {
		limit = RankDefaultLimit
	}
	if limit > RankMaxLimit {
		limit = RankMaxLimit
	}
	if query.Offset < 0 || query.Offset > RankMaxOffset {
		return nil, fmt.Errorf("offset must be a non-negative integer no larger than %d", RankMaxOffset)
	}
	bucketSeconds := resolveBucketSize(endUnix - startUnix)

	tx, err := s.beginRead(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	plan, err := s.hourPlan(ctx, tx, tailnetID, startUnix, endUnix, bucketSeconds)
	if err != nil {
		return nil, err
	}
	clause, typeArgs := countedTrafficClause(query.TrafficTypes)
	extra := clause + " AND (src_node_id = ? OR dst_node_id = ?)"
	extraArgs := append(append([]any{}, typeArgs...), nodeID, nodeID)
	source, args := plan.unionPairRows(tailnetID,
		"bucket, src_node_id, dst_node_id, traffic_type, tx_bytes, rx_bytes, flow_count",
		"bucket, src_node_id, dst_node_id, traffic_type, tx_bytes, rx_bytes, flow_count",
		extra, extraArgs,
	)
	result := &DeviceTimeline{
		NodeID:        nodeID,
		BucketSeconds: bucketSeconds,
		Buckets:       []TimelineBucket{},
		Peers:         []TimelinePeer{},
	}
	result.Hostname, err = lookupHostname(ctx, tx, tailnetID, nodeID)
	if err != nil {
		return nil, err
	}
	if source == "" {
		return result, nil
	}
	includePhysical := timelineIncludesPhysical(query.TrafficTypes)
	buckets, err := queryTimelineBuckets(ctx, tx, source, args, nodeID, bucketSeconds, includePhysical)
	if err != nil {
		return nil, err
	}
	peers, hasMore, err := queryTimelinePeers(ctx, tx, source, args, tailnetID, nodeID, limit, query.Offset)
	if err != nil {
		return nil, err
	}
	if buckets != nil {
		result.Buckets = buckets
	}
	if peers != nil {
		result.Peers = peers
	}
	result.HasMore = hasMore
	return result, nil
}

func timelineIncludesPhysical(trafficTypes []string) bool {
	for _, trafficType := range trafficTypes {
		if trafficType == "physical" {
			return true
		}
	}
	return false
}

func lookupHostname(ctx context.Context, q queryRower, tailnetID, nodeID string) (string, error) {
	var hostname string
	err := q.QueryRowContext(ctx, `
		SELECT COALESCE(NULLIF(hostname, ''), NULLIF(name, ''), '')
		FROM node_metadata
		WHERE tailnet_id = ? AND node_id = ?
	`, tailnetID, nodeID).Scan(&hostname)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("failed to query device hostname: %w", err)
	}
	return hostname, nil
}

func queryTimelineBuckets(ctx context.Context, q queryRower, source string, args []any, nodeID string, bucketSeconds int64, includePhysical bool) ([]TimelineBucket, error) {
	statement := fmt.Sprintf(`
		WITH pair_rows AS (%s)
		SELECT b, traffic_type, COALESCE(SUM(tx), 0), COALESCE(SUM(rx), 0)
		FROM (
			SELECT (bucket / %d) * %d AS b, traffic_type, tx_bytes AS tx, rx_bytes AS rx
			FROM pair_rows
			WHERE src_node_id = ?
			UNION ALL
			SELECT (bucket / %d) * %d AS b, traffic_type, rx_bytes AS tx, tx_bytes AS rx
			FROM pair_rows
			WHERE dst_node_id = ? AND src_node_id != dst_node_id
		) AS directed
		GROUP BY b, traffic_type
		ORDER BY b ASC, traffic_type ASC
	`, source, bucketSeconds, bucketSeconds, bucketSeconds, bucketSeconds)
	queryArgs := append(append([]any{}, args...), nodeID, nodeID)
	rows, err := q.QueryContext(ctx, statement, queryArgs...)
	if err != nil {
		return nil, fmt.Errorf("failed to query device timeline: %w", err)
	}
	defer rows.Close()

	byBucket := map[int64]*TimelineBucket{}
	var order []int64
	for rows.Next() {
		var bucket int64
		var trafficType string
		var tx, rx int64
		if err := rows.Scan(&bucket, &trafficType, &tx, &rx); err != nil {
			return nil, fmt.Errorf("failed to scan device timeline: %w", err)
		}
		item := byBucket[bucket]
		if item == nil {
			item = &TimelineBucket{Time: time.Unix(bucket, 0).UTC()}
			if includePhysical {
				item.Physical = &TrafficByteTotal{}
			}
			byBucket[bucket] = item
			order = append(order, bucket)
		}
		addTimelineBytes(item, trafficType, tx, rx, includePhysical)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to query device timeline: %w", err)
	}
	buckets := make([]TimelineBucket, 0, len(order))
	for _, bucket := range order {
		buckets = append(buckets, *byBucket[bucket])
	}
	return buckets, nil
}

func addTimelineBytes(bucket *TimelineBucket, trafficType string, tx, rx int64, includePhysical bool) {
	switch trafficType {
	case "virtual":
		bucket.Virtual.TxBytes += tx
		bucket.Virtual.RxBytes += rx
	case "subnet":
		bucket.Subnet.TxBytes += tx
		bucket.Subnet.RxBytes += rx
	case "exit":
		bucket.Exit.TxBytes += tx
		bucket.Exit.RxBytes += rx
	case "physical":
		if !includePhysical {
			return
		}
		if bucket.Physical == nil {
			bucket.Physical = &TrafficByteTotal{}
		}
		bucket.Physical.TxBytes += tx
		bucket.Physical.RxBytes += rx
	}
}

func queryTimelinePeers(ctx context.Context, q queryRower, source string, args []any, tailnetID, nodeID string, limit, offset int) ([]TimelinePeer, bool, error) {
	statement := fmt.Sprintf(`
		WITH pair_rows AS (%s)
		SELECT ranked.peer_id,
		       COALESCE(NULLIF(m.hostname, ''), NULLIF(m.name, ''), ''),
		       COALESCE(ranked.tx, 0),
		       COALESCE(ranked.rx, 0),
		       COALESCE(ranked.total, 0),
		       COALESCE(ranked.flows, 0)
		FROM (
			SELECT peer_id,
			       SUM(tx) AS tx,
			       SUM(rx) AS rx,
			       SUM(tx) + SUM(rx) AS total,
			       SUM(flows) AS flows
			FROM (
				SELECT dst_node_id AS peer_id, SUM(tx_bytes) AS tx, SUM(rx_bytes) AS rx, SUM(flow_count) AS flows
				FROM pair_rows
				WHERE src_node_id = ?
				GROUP BY dst_node_id
				UNION ALL
				SELECT src_node_id AS peer_id, SUM(rx_bytes) AS tx, SUM(tx_bytes) AS rx, SUM(flow_count) AS flows
				FROM pair_rows
				WHERE dst_node_id = ? AND src_node_id != dst_node_id
				GROUP BY src_node_id
			) AS directed
			GROUP BY peer_id
			ORDER BY total DESC, peer_id ASC
			LIMIT ? OFFSET ?
		) AS ranked
		LEFT JOIN node_metadata AS m
		  ON m.tailnet_id = ? AND m.node_id = ranked.peer_id
		ORDER BY ranked.total DESC, ranked.peer_id ASC
	`, source)
	queryArgs := append(append([]any{}, args...), nodeID, nodeID, limit+1, offset, tailnetID)
	rows, err := q.QueryContext(ctx, statement, queryArgs...)
	if err != nil {
		return nil, false, fmt.Errorf("failed to query device peers: %w", err)
	}
	defer rows.Close()
	peers := make([]TimelinePeer, 0)
	for rows.Next() {
		var peer TimelinePeer
		if err := rows.Scan(&peer.PeerID, &peer.Hostname, &peer.TxBytes, &peer.RxBytes, &peer.TotalBytes, &peer.FlowCount); err != nil {
			return nil, false, fmt.Errorf("failed to scan device peer: %w", err)
		}
		peers = append(peers, peer)
	}
	if err := rows.Err(); err != nil {
		return nil, false, fmt.Errorf("failed to query device peers: %w", err)
	}
	if len(peers) > limit {
		return peers[:limit], true, nil
	}
	return peers, false, nil
}
