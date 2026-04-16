package database

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"time"
)

// resolveBucketSize returns the SQL grouping interval in seconds for a query window.
//   ≤ 2 hours  → 60 s  (1-minute buckets, raw)
//   ≤ 48 hours → 3600 s (1-hour buckets)
//   otherwise  → 86400 s (1-day buckets)
func resolveBucketSize(rangeSeconds int64) int64 {
	if rangeSeconds <= 2*3600 {
		return 60
	}
	if rangeSeconds <= 48*3600 {
		return 3600
	}
	return 86400
}

// CommitPollResults atomically writes all aggregates and updates poll state.
func (s *SQLiteStore) CommitPollResults(ctx context.Context, results PollResults) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	if err := upsertNodePairsTx(ctx, tx, results.NodePairs); err != nil {
		return err
	}
	if err := upsertBandwidthTx(ctx, tx, results.Bandwidth); err != nil {
		return err
	}
	if err := upsertNodeBandwidthTx(ctx, tx, results.NodeBandwidth); err != nil {
		return err
	}
	if err := upsertTrafficStatsTx(ctx, tx, results.TrafficStats); err != nil {
		return err
	}

	const sqliteFormat = "2006-01-02 15:04:05"
	_, err = tx.ExecContext(ctx,
		"UPDATE poll_state SET last_poll_end = ?, updated_at = CURRENT_TIMESTAMP WHERE id = 1",
		results.PollEnd.UTC().Format(sqliteFormat),
	)
	if err != nil {
		return fmt.Errorf("failed to update poll state: %w", err)
	}

	return tx.Commit()
}

func upsertNodePairsTx(ctx context.Context, tx *sql.Tx, aggregates []NodePairAggregate) error {
	if len(aggregates) == 0 {
		return nil
	}

	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO node_pairs (bucket, src_node_id, dst_node_id, traffic_type,
		                        tx_bytes, rx_bytes, tx_pkts, rx_pkts, flow_count, protocols, ports)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(bucket, src_node_id, dst_node_id, traffic_type) DO UPDATE SET
			tx_bytes   = tx_bytes   + excluded.tx_bytes,
			rx_bytes   = rx_bytes   + excluded.rx_bytes,
			tx_pkts    = tx_pkts    + excluded.tx_pkts,
			rx_pkts    = rx_pkts    + excluded.rx_pkts,
			flow_count = flow_count + excluded.flow_count,
			protocols  = (SELECT json_group_array(value) FROM (
			                 SELECT value FROM json_each(node_pairs.protocols)
			                 UNION
			                 SELECT value FROM json_each(excluded.protocols)
			              )),
			ports = excluded.ports
	`)
	if err != nil {
		return fmt.Errorf("failed to prepare node_pairs upsert: %w", err)
	}
	defer stmt.Close()

	const bucketSize = int64(60)
	for _, agg := range aggregates {
		bucket := (agg.Bucket / bucketSize) * bucketSize
		if _, err := stmt.ExecContext(ctx,
			bucket, agg.SrcNodeID, agg.DstNodeID, agg.TrafficType,
			agg.TxBytes, agg.RxBytes, agg.TxPkts, agg.RxPkts,
			agg.FlowCount, agg.Protocols, agg.Ports,
		); err != nil {
			return fmt.Errorf("failed to upsert node pair: %w", err)
		}
	}
	return nil
}

func upsertBandwidthTx(ctx context.Context, tx *sql.Tx, buckets []BandwidthBucket) error {
	if len(buckets) == 0 {
		return nil
	}

	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO bandwidth (bucket, tx_bytes, rx_bytes) VALUES (?, ?, ?)
		ON CONFLICT(bucket) DO UPDATE SET
			tx_bytes = tx_bytes + excluded.tx_bytes,
			rx_bytes = rx_bytes + excluded.rx_bytes
	`)
	if err != nil {
		return fmt.Errorf("failed to prepare bandwidth upsert: %w", err)
	}
	defer stmt.Close()

	const bucketSize = int64(60)
	for _, b := range buckets {
		bucket := (b.Time.UTC().Unix() / bucketSize) * bucketSize
		if _, err := stmt.ExecContext(ctx, bucket, b.TxBytes, b.RxBytes); err != nil {
			return fmt.Errorf("failed to upsert bandwidth: %w", err)
		}
	}
	return nil
}

func upsertNodeBandwidthTx(ctx context.Context, tx *sql.Tx, buckets []NodeBandwidth) error {
	if len(buckets) == 0 {
		return nil
	}

	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO bandwidth_by_node (bucket, node_id, tx_bytes, rx_bytes) VALUES (?, ?, ?, ?)
		ON CONFLICT(bucket, node_id) DO UPDATE SET
			tx_bytes = tx_bytes + excluded.tx_bytes,
			rx_bytes = rx_bytes + excluded.rx_bytes
	`)
	if err != nil {
		return fmt.Errorf("failed to prepare bandwidth_by_node upsert: %w", err)
	}
	defer stmt.Close()

	const bucketSize = int64(60)
	for _, b := range buckets {
		bucket := (b.Bucket / bucketSize) * bucketSize
		if _, err := stmt.ExecContext(ctx, bucket, b.NodeID, b.TxBytes, b.RxBytes); err != nil {
			return fmt.Errorf("failed to upsert node bandwidth: %w", err)
		}
	}
	return nil
}

func upsertTrafficStatsTx(ctx context.Context, tx *sql.Tx, stats []TrafficStats) error {
	if len(stats) == 0 {
		return nil
	}

	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO traffic_stats (bucket, tcp_bytes, udp_bytes, other_proto_bytes,
		                           virtual_bytes, subnet_bytes, physical_bytes,
		                           total_flows, unique_pairs, top_ports)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(bucket) DO UPDATE SET
			tcp_bytes         = tcp_bytes         + excluded.tcp_bytes,
			udp_bytes         = udp_bytes         + excluded.udp_bytes,
			other_proto_bytes = other_proto_bytes + excluded.other_proto_bytes,
			virtual_bytes     = virtual_bytes     + excluded.virtual_bytes,
			subnet_bytes      = subnet_bytes      + excluded.subnet_bytes,
			physical_bytes    = physical_bytes    + excluded.physical_bytes,
			total_flows       = total_flows       + excluded.total_flows,
			unique_pairs      = MAX(unique_pairs, excluded.unique_pairs),
			top_ports         = excluded.top_ports
	`)
	if err != nil {
		return fmt.Errorf("failed to prepare traffic_stats upsert: %w", err)
	}
	defer stmt.Close()

	const bucketSize = int64(60)
	for _, st := range stats {
		bucket := (st.Bucket / bucketSize) * bucketSize
		if _, err := stmt.ExecContext(ctx,
			bucket, st.TCPBytes, st.UDPBytes, st.OtherProtoBytes,
			st.VirtualBytes, st.SubnetBytes, st.PhysicalBytes,
			st.TotalFlows, st.UniquePairs, st.TopPorts,
		); err != nil {
			return fmt.Errorf("failed to upsert traffic stats: %w", err)
		}
	}
	return nil
}

// UpsertNodePairAggregates upserts node-pair aggregates into node_pairs.
func (s *SQLiteStore) UpsertNodePairAggregates(ctx context.Context, aggregates []NodePairAggregate) error {
	if len(aggregates) == 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()
	if err := upsertNodePairsTx(ctx, tx, aggregates); err != nil {
		return err
	}
	return tx.Commit()
}

// GetNodePairAggregates retrieves node-pair aggregates for a time range.
func (s *SQLiteStore) GetNodePairAggregates(ctx context.Context, start, end time.Time, bucketSize int64) ([]NodePairAggregate, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	startUnix := start.UTC().Unix()
	endUnix := end.UTC().Unix()
	if startUnix >= endUnix {
		return nil, fmt.Errorf("invalid time range: start (%v) must be before end (%v)", start, end)
	}

	query := `
		SELECT MIN(bucket), src_node_id, dst_node_id, traffic_type,
		       SUM(tx_bytes), SUM(rx_bytes), SUM(tx_pkts), SUM(rx_pkts),
		       SUM(flow_count),
		       COALESCE((SELECT protocols FROM node_pairs sub
		                 WHERE sub.src_node_id = main.src_node_id
		                   AND sub.dst_node_id = main.dst_node_id
		                   AND sub.traffic_type = main.traffic_type
		                   AND sub.bucket >= ? AND sub.bucket <= ?
		                 ORDER BY sub.bucket DESC LIMIT 1), '[]'),
		       COALESCE((SELECT ports FROM node_pairs sub
		                 WHERE sub.src_node_id = main.src_node_id
		                   AND sub.dst_node_id = main.dst_node_id
		                   AND sub.traffic_type = main.traffic_type
		                   AND sub.bucket >= ? AND sub.bucket <= ?
		                 ORDER BY sub.bucket DESC LIMIT 1), '[]')
		FROM node_pairs main
		WHERE bucket >= ? AND bucket <= ?
		GROUP BY src_node_id, dst_node_id, traffic_type
		ORDER BY SUM(tx_bytes) + SUM(rx_bytes) DESC
	`
	rows, err := s.db.QueryContext(ctx, query,
		startUnix, endUnix,
		startUnix, endUnix,
		startUnix, endUnix,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to query node pairs: %w", err)
	}
	defer rows.Close()

	var aggregates []NodePairAggregate
	for rows.Next() {
		var agg NodePairAggregate
		if err := rows.Scan(
			&agg.Bucket, &agg.SrcNodeID, &agg.DstNodeID, &agg.TrafficType,
			&agg.TxBytes, &agg.RxBytes, &agg.TxPkts, &agg.RxPkts,
			&agg.FlowCount, &agg.Protocols, &agg.Ports,
		); err != nil {
			return nil, fmt.Errorf("failed to scan node pair: %w", err)
		}
		aggregates = append(aggregates, agg)
	}
	return aggregates, rows.Err()
}

// UpsertBandwidth upserts total bandwidth into bandwidth.
func (s *SQLiteStore) UpsertBandwidth(ctx context.Context, buckets []BandwidthBucket) error {
	if len(buckets) == 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()
	if err := upsertBandwidthTx(ctx, tx, buckets); err != nil {
		return err
	}
	return tx.Commit()
}

// UpsertNodeBandwidth upserts per-node bandwidth into bandwidth_by_node.
func (s *SQLiteStore) UpsertNodeBandwidth(ctx context.Context, buckets []NodeBandwidth) error {
	if len(buckets) == 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()
	if err := upsertNodeBandwidthTx(ctx, tx, buckets); err != nil {
		return err
	}
	return tx.Commit()
}

// GetBandwidth retrieves total bandwidth for a time range, bucketed by window size.
func (s *SQLiteStore) GetBandwidth(ctx context.Context, start, end time.Time) ([]BandwidthBucket, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	startUnix := start.UTC().Unix()
	endUnix := end.UTC().Unix()
	if startUnix >= endUnix {
		return nil, fmt.Errorf("invalid time range: start (%v) must be before end (%v)", start, end)
	}

	bs := resolveBucketSize(endUnix - startUnix)
	query := fmt.Sprintf(`
		SELECT (bucket / %d) * %d AS b, SUM(tx_bytes), SUM(rx_bytes)
		FROM bandwidth
		WHERE bucket >= ? AND bucket <= ?
		GROUP BY b
		ORDER BY b ASC
	`, bs, bs)

	rows, err := s.db.QueryContext(ctx, query, startUnix, endUnix)
	if err != nil {
		return nil, fmt.Errorf("failed to query bandwidth: %w", err)
	}
	defer rows.Close()

	var result []BandwidthBucket
	for rows.Next() {
		var bucket int64
		var b BandwidthBucket
		if err := rows.Scan(&bucket, &b.TxBytes, &b.RxBytes); err != nil {
			return nil, fmt.Errorf("failed to scan bandwidth bucket: %w", err)
		}
		b.Time = time.Unix(bucket, 0).UTC()
		result = append(result, b)
	}
	return result, rows.Err()
}

// GetNodeBandwidth retrieves bandwidth for a specific node, bucketed by window size.
func (s *SQLiteStore) GetNodeBandwidth(ctx context.Context, start, end time.Time, nodeID string) ([]BandwidthBucket, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	startUnix := start.UTC().Unix()
	endUnix := end.UTC().Unix()
	if startUnix >= endUnix {
		return nil, fmt.Errorf("invalid time range: start (%v) must be before end (%v)", start, end)
	}

	bs := resolveBucketSize(endUnix - startUnix)
	query := fmt.Sprintf(`
		SELECT (bucket / %d) * %d AS b, SUM(tx_bytes), SUM(rx_bytes)
		FROM bandwidth_by_node
		WHERE bucket >= ? AND bucket <= ? AND node_id = ?
		GROUP BY b
		ORDER BY b ASC
	`, bs, bs)

	rows, err := s.db.QueryContext(ctx, query, startUnix, endUnix, nodeID)
	if err != nil {
		return nil, fmt.Errorf("failed to query node bandwidth: %w", err)
	}
	defer rows.Close()

	var result []BandwidthBucket
	for rows.Next() {
		var bucket int64
		var b BandwidthBucket
		if err := rows.Scan(&bucket, &b.TxBytes, &b.RxBytes); err != nil {
			return nil, fmt.Errorf("failed to scan node bandwidth bucket: %w", err)
		}
		b.Time = time.Unix(bucket, 0).UTC()
		result = append(result, b)
	}
	return result, rows.Err()
}

// UpsertTrafficStats upserts network-wide traffic statistics into traffic_stats.
func (s *SQLiteStore) UpsertTrafficStats(ctx context.Context, stats []TrafficStats) error {
	if len(stats) == 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()
	if err := upsertTrafficStatsTx(ctx, tx, stats); err != nil {
		return err
	}
	return tx.Commit()
}

// GetTrafficStats retrieves network-wide traffic statistics for a time range, bucketed by window size.
func (s *SQLiteStore) GetTrafficStats(ctx context.Context, start, end time.Time) ([]TrafficStats, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	startUnix := start.UTC().Unix()
	endUnix := end.UTC().Unix()
	if startUnix >= endUnix {
		return nil, fmt.Errorf("invalid time range: start (%v) must be before end (%v)", start, end)
	}

	bs := resolveBucketSize(endUnix - startUnix)
	// top_ports: SQLite picks an arbitrary row's value within each group.
	query := fmt.Sprintf(`
		SELECT (bucket / %d) * %d AS b,
		       SUM(tcp_bytes), SUM(udp_bytes), SUM(other_proto_bytes),
		       SUM(virtual_bytes), SUM(subnet_bytes), SUM(physical_bytes),
		       SUM(total_flows), MAX(unique_pairs), top_ports
		FROM traffic_stats
		WHERE bucket >= ? AND bucket <= ?
		GROUP BY b
		ORDER BY b ASC
	`, bs, bs)

	rows, err := s.db.QueryContext(ctx, query, startUnix, endUnix)
	if err != nil {
		return nil, fmt.Errorf("failed to query traffic stats: %w", err)
	}
	defer rows.Close()

	var results []TrafficStats
	for rows.Next() {
		var st TrafficStats
		if err := rows.Scan(
			&st.Bucket, &st.TCPBytes, &st.UDPBytes, &st.OtherProtoBytes,
			&st.VirtualBytes, &st.SubnetBytes, &st.PhysicalBytes,
			&st.TotalFlows, &st.UniquePairs, &st.TopPorts,
		); err != nil {
			return nil, fmt.Errorf("failed to scan traffic stats: %w", err)
		}
		results = append(results, st)
	}
	return results, rows.Err()
}

// GetTrafficStatsFromNodePairs synthesizes traffic stats from node_pairs (fallback for old data).
func (s *SQLiteStore) GetTrafficStatsFromNodePairs(ctx context.Context, start, end time.Time) ([]TrafficStats, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	startUnix := start.UTC().Unix()
	endUnix := end.UTC().Unix()
	if startUnix >= endUnix {
		return nil, fmt.Errorf("invalid time range: start (%v) must be before end (%v)", start, end)
	}

	bs := resolveBucketSize(endUnix - startUnix)
	query := fmt.Sprintf(`
		SELECT (bucket / %d) * %d AS b, traffic_type,
		       SUM(tx_bytes + rx_bytes) AS total_bytes,
		       SUM(flow_count) AS total_flows,
		       COUNT(DISTINCT src_node_id || '|' || dst_node_id) AS unique_pairs
		FROM node_pairs
		WHERE bucket >= ? AND bucket <= ?
		GROUP BY b, traffic_type
		ORDER BY b ASC
	`, bs, bs)

	rows, err := s.db.QueryContext(ctx, query, startUnix, endUnix)
	if err != nil {
		return nil, fmt.Errorf("failed to query node pairs for traffic stats: %w", err)
	}
	defer rows.Close()

	bucketMap := make(map[int64]*TrafficStats)
	for rows.Next() {
		var bucket int64
		var trafficType string
		var totalBytes, totalFlows, uniquePairs int64
		if err := rows.Scan(&bucket, &trafficType, &totalBytes, &totalFlows, &uniquePairs); err != nil {
			return nil, fmt.Errorf("failed to scan: %w", err)
		}
		st, ok := bucketMap[bucket]
		if !ok {
			st = &TrafficStats{Bucket: bucket, TopPorts: "[]"}
			bucketMap[bucket] = st
		}
		switch trafficType {
		case "virtual", "exit":
			st.VirtualBytes += totalBytes
		case "subnet":
			st.SubnetBytes += totalBytes
		case "physical":
			st.PhysicalBytes += totalBytes
		}
		st.TotalFlows += totalFlows
		if uniquePairs > st.UniquePairs {
			st.UniquePairs = uniquePairs
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Derive protocol breakdown from protocols JSON column
	protoQuery := fmt.Sprintf(`
		SELECT (bucket / %d) * %d AS b, protocols, SUM(tx_bytes + rx_bytes)
		FROM node_pairs
		WHERE bucket >= ? AND bucket <= ? AND traffic_type != 'physical'
		GROUP BY b, protocols
	`, bs, bs)
	protoRows, err := s.db.QueryContext(ctx, protoQuery, startUnix, endUnix)
	if err == nil {
		for protoRows.Next() {
			var b int64
			var protosJSON string
			var totalBytes int64
			if err := protoRows.Scan(&b, &protosJSON, &totalBytes); err != nil {
				continue
			}
			st, ok := bucketMap[b]
			if !ok {
				continue
			}
			var protos []int
			if err := json.Unmarshal([]byte(protosJSON), &protos); err != nil || len(protos) == 0 {
				continue
			}
			perProto := totalBytes / int64(len(protos))
			rem := totalBytes - perProto*int64(len(protos))
			for i, p := range protos {
				share := perProto
				if i == 0 {
					share += rem
				}
				switch p {
				case 6:
					st.TCPBytes += share
				case 17:
					st.UDPBytes += share
				default:
					st.OtherProtoBytes += share
				}
			}
		}
		protoRows.Close()
	}

	results := make([]TrafficStats, 0, len(bucketMap))
	for _, st := range bucketMap {
		results = append(results, *st)
	}
	sort.Slice(results, func(i, j int) bool { return results[i].Bucket < results[j].Bucket })
	return results, nil
}

// GetTopTalkers returns nodes ranked by total traffic volume.
func (s *SQLiteStore) GetTopTalkers(ctx context.Context, start, end time.Time, limit int) ([]TopTalker, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	startUnix := start.UTC().Unix()
	endUnix := end.UTC().Unix()
	if startUnix >= endUnix {
		return nil, fmt.Errorf("invalid time range: start (%v) must be before end (%v)", start, end)
	}
	if limit <= 0 {
		limit = 10
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT node_id, SUM(tx_bytes), SUM(rx_bytes), SUM(tx_bytes + rx_bytes) AS total
		FROM bandwidth_by_node
		WHERE bucket >= ? AND bucket <= ?
		GROUP BY node_id
		ORDER BY total DESC
		LIMIT ?
	`, startUnix, endUnix, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to query top talkers: %w", err)
	}
	defer rows.Close()

	var results []TopTalker
	for rows.Next() {
		var t TopTalker
		if err := rows.Scan(&t.NodeID, &t.TxBytes, &t.RxBytes, &t.TotalBytes); err != nil {
			return nil, fmt.Errorf("failed to scan top talker: %w", err)
		}
		results = append(results, t)
	}
	return results, rows.Err()
}

// GetTopPairs returns node pairs ranked by total traffic volume.
func (s *SQLiteStore) GetTopPairs(ctx context.Context, start, end time.Time, limit int) ([]TopPair, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	startUnix := start.UTC().Unix()
	endUnix := end.UTC().Unix()
	if startUnix >= endUnix {
		return nil, fmt.Errorf("invalid time range: start (%v) must be before end (%v)", start, end)
	}
	if limit <= 0 {
		limit = 10
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT src_node_id, dst_node_id,
		       SUM(tx_bytes), SUM(rx_bytes),
		       SUM(tx_bytes + rx_bytes) AS total, SUM(flow_count)
		FROM node_pairs
		WHERE bucket >= ? AND bucket <= ?
		GROUP BY src_node_id, dst_node_id
		ORDER BY total DESC
		LIMIT ?
	`, startUnix, endUnix, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to query top pairs: %w", err)
	}
	defer rows.Close()

	var results []TopPair
	for rows.Next() {
		var p TopPair
		if err := rows.Scan(&p.SrcNodeID, &p.DstNodeID, &p.TxBytes, &p.RxBytes, &p.TotalBytes, &p.FlowCount); err != nil {
			return nil, fmt.Errorf("failed to scan top pair: %w", err)
		}
		results = append(results, p)
	}
	return results, rows.Err()
}

// GetNodeStats returns detailed traffic statistics for a single node.
func (s *SQLiteStore) GetNodeStats(ctx context.Context, nodeID string, start, end time.Time) (*NodeDetailStats, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	startUnix := start.UTC().Unix()
	endUnix := end.UTC().Unix()
	if startUnix >= endUnix {
		return nil, fmt.Errorf("invalid time range: start (%v) must be before end (%v)", start, end)
	}

	result := &NodeDetailStats{
		NodeID:   nodeID,
		TopPeers: make([]TopPair, 0),
		TopPorts: make([]PortStat, 0),
	}

	if err := s.db.QueryRowContext(ctx, `
		SELECT COALESCE(SUM(tx_bytes), 0), COALESCE(SUM(rx_bytes), 0)
		FROM bandwidth_by_node
		WHERE node_id = ? AND bucket >= ? AND bucket <= ?
	`, nodeID, startUnix, endUnix).Scan(&result.TotalTx, &result.TotalRx); err != nil {
		return nil, fmt.Errorf("failed to query node bandwidth: %w", err)
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT peer_id, SUM(tx), SUM(rx), SUM(tx+rx) AS total, SUM(fc)
		FROM (
			SELECT dst_node_id AS peer_id, SUM(tx_bytes) AS tx, SUM(rx_bytes) AS rx, SUM(flow_count) AS fc
			FROM node_pairs
			WHERE src_node_id = ? AND bucket >= ? AND bucket <= ?
			GROUP BY dst_node_id
			UNION ALL
			SELECT src_node_id AS peer_id, SUM(rx_bytes) AS tx, SUM(tx_bytes) AS rx, SUM(flow_count) AS fc
			FROM node_pairs
			WHERE dst_node_id = ? AND bucket >= ? AND bucket <= ?
			GROUP BY src_node_id
		)
		GROUP BY peer_id
		ORDER BY total DESC
		LIMIT 10
	`, nodeID, startUnix, endUnix, nodeID, startUnix, endUnix)
	if err != nil {
		return nil, fmt.Errorf("failed to query node peers: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var p TopPair
		if err := rows.Scan(&p.DstNodeID, &p.TxBytes, &p.RxBytes, &p.TotalBytes, &p.FlowCount); err != nil {
			return nil, fmt.Errorf("failed to scan peer: %w", err)
		}
		p.SrcNodeID = nodeID
		result.TopPeers = append(result.TopPeers, p)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	portRows, err := s.db.QueryContext(ctx, `
		SELECT ports FROM node_pairs
		WHERE (src_node_id = ? OR dst_node_id = ?)
		  AND bucket >= ? AND bucket <= ?
		  AND ports != '[]'
	`, nodeID, nodeID, startUnix, endUnix)
	if err != nil {
		return nil, fmt.Errorf("failed to query node ports: %w", err)
	}
	defer portRows.Close()

	type protoPortKey struct{ proto, port int }
	portAgg := make(map[protoPortKey]int64)
	for portRows.Next() {
		var portsJSON string
		if err := portRows.Scan(&portsJSON); err != nil {
			continue
		}
		var entries []PortStat
		if err := json.Unmarshal([]byte(portsJSON), &entries); err != nil {
			continue
		}
		for _, e := range entries {
			portAgg[protoPortKey{e.Proto, e.Port}] += e.Bytes
		}
	}
	for ppk, bytes := range portAgg {
		switch ppk.proto {
		case 6:
			result.TCPBytes += bytes
		case 17:
			result.UDPBytes += bytes
		default:
			result.OtherBytes += bytes
		}
		result.TopPorts = append(result.TopPorts, PortStat{Port: ppk.port, Proto: ppk.proto, Bytes: bytes})
	}
	sort.Slice(result.TopPorts, func(i, j int) bool { return result.TopPorts[i].Bytes > result.TopPorts[j].Bytes })
	if len(result.TopPorts) > 15 {
		result.TopPorts = result.TopPorts[:15]
	}

	return result, nil
}
