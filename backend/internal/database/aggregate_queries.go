package database

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

// resolveBucketSize returns the SQL grouping interval in seconds for a query window.
//
//	≤ 2 hours  → 60 s  (1-minute buckets, raw)
//	≤ 48 hours → 3600 s (1-hour buckets)
//	otherwise  → 86400 s (1-day buckets)
func resolveBucketSize(rangeSeconds int64) int64 {
	if rangeSeconds <= 2*3600 {
		return 60
	}
	if rangeSeconds <= 48*3600 {
		return 3600
	}
	return 86400
}

// normalizeProtocolBytes returns a usable protocol-to-byte map for an
// aggregate. New callers provide exact byte totals; older callers only have
// the set of protocols, so their bytes are divided deterministically across
// that set as a compatibility fallback.
func normalizeProtocolBytes(raw, protocolsJSON string, totalBytes int64) string {
	var existing map[string]int64
	if json.Unmarshal([]byte(raw), &existing) == nil && len(existing) > 0 {
		encoded, err := json.Marshal(existing)
		if err == nil {
			return string(encoded)
		}
	}

	var protocols []int
	if json.Unmarshal([]byte(protocolsJSON), &protocols) != nil || len(protocols) == 0 {
		return "{}"
	}
	sort.Ints(protocols)
	perProtocol := totalBytes / int64(len(protocols))
	remainder := totalBytes - perProtocol*int64(len(protocols))
	derived := make(map[string]int64, len(protocols))
	for i, protocol := range protocols {
		bytes := perProtocol
		if i == 0 {
			bytes += remainder
		}
		derived[fmt.Sprint(protocol)] = bytes
	}
	encoded, err := json.Marshal(derived)
	if err != nil {
		return "{}"
	}
	return string(encoded)
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
		`UPDATE poll_state
		 SET last_poll_end = CASE
		       WHEN last_poll_end IS NULL OR last_poll_end = '' OR datetime(last_poll_end) IS NULL OR datetime(last_poll_end) < datetime(?)
		       THEN ? ELSE last_poll_end END,
		     updated_at = CURRENT_TIMESTAMP
		 WHERE id = 1`,
		results.PollEnd.UTC().Format(sqliteFormat), results.PollEnd.UTC().Format(sqliteFormat),
	)
	if err != nil {
		return fmt.Errorf("failed to update poll state: %w", err)
	}

	return tx.Commit()
}

// CommitObjectIngest atomically writes aggregates for one immutable object and
// records the object key. If the object key already exists, the aggregates are
// not applied again.
func (s *SQLiteStore) CommitObjectIngest(ctx context.Context, result ObjectIngestResult) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	const sqliteFormat = "2006-01-02 15:04:05"
	insertRes, err := tx.ExecContext(ctx, `
		INSERT OR IGNORE INTO ingested_objects (object_key, last_modified, size_bytes, flow_count, ingested_at)
		VALUES (?, ?, ?, ?, CURRENT_TIMESTAMP)
	`, result.Key, result.LastModified.UTC().Format(sqliteFormat), result.Size, result.FlowCount)
	if err != nil {
		return fmt.Errorf("failed to mark object as ingested: %w", err)
	}
	rows, err := insertRes.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to determine whether object was newly ingested: %w", err)
	}
	if rows == 0 {
		if err := upsertNodeMetadataTx(ctx, tx, result.NodeMetadata); err != nil {
			return err
		}
		if err := recordObjectMetadataTx(ctx, tx, result.Key, nodeMetadataIDs(result.NodeMetadata)); err != nil {
			return err
		}
		return tx.Commit()
	}

	if err := upsertNodeMetadataTx(ctx, tx, result.NodeMetadata); err != nil {
		return err
	}
	if err := recordObjectMetadataTx(ctx, tx, result.Key, nodeMetadataIDs(result.NodeMetadata)); err != nil {
		return err
	}
	if err := upsertNodePairsTx(ctx, tx, result.NodePairs); err != nil {
		return err
	}
	if err := upsertBandwidthTx(ctx, tx, result.Bandwidth); err != nil {
		return err
	}
	if err := upsertNodeBandwidthTx(ctx, tx, result.NodeBandwidth); err != nil {
		return err
	}
	if err := upsertTrafficStatsTx(ctx, tx, result.TrafficStats); err != nil {
		return err
	}

	// Object-store polls update the cursor after the full object batch has been
	// examined. Leaving PollEnd zero keeps an unreadable earlier object from
	// being skipped when a later object was committed successfully.
	if !result.PollEnd.IsZero() {
		_, err = tx.ExecContext(ctx,
			`UPDATE poll_state
			 SET last_poll_end = CASE
			       WHEN last_poll_end IS NULL OR last_poll_end = '' OR datetime(last_poll_end) IS NULL OR datetime(last_poll_end) < datetime(?)
			       THEN ? ELSE last_poll_end END,
			     updated_at = CURRENT_TIMESTAMP
			 WHERE id = 1`,
			result.PollEnd.UTC().Format(sqliteFormat), result.PollEnd.UTC().Format(sqliteFormat),
		)
		if err != nil {
			return fmt.Errorf("failed to update poll state: %w", err)
		}
	}

	return tx.Commit()
}

func nodeMetadataIDs(nodes []NodeMetadata) []string {
	ids := make([]string, 0, len(nodes))
	for _, node := range nodes {
		if node.NodeID != "" {
			ids = append(ids, node.NodeID)
		}
	}
	return ids
}

func upsertNodePairsTx(ctx context.Context, tx *sql.Tx, aggregates []NodePairAggregate) error {
	if len(aggregates) == 0 {
		return nil
	}

	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO node_pairs (bucket, src_node_id, dst_node_id, traffic_type,
		                        tx_bytes, rx_bytes, tx_pkts, rx_pkts, flow_count, protocols, protocol_bytes, ports)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(bucket, src_node_id, dst_node_id, traffic_type) DO UPDATE SET
			tx_bytes   = tx_bytes   + excluded.tx_bytes,
			rx_bytes   = rx_bytes   + excluded.rx_bytes,
			tx_pkts    = tx_pkts    + excluded.tx_pkts,
			rx_pkts    = rx_pkts    + excluded.rx_pkts,
			flow_count = flow_count + excluded.flow_count,
			protocols  = (SELECT COALESCE(json_group_array(value), '[]') FROM (
			                 SELECT value FROM (
				                 SELECT value FROM json_each(
					                 CASE WHEN json_valid(node_pairs.protocols)
					                              THEN node_pairs.protocols ELSE '[]' END)
				                 UNION
				                 SELECT value FROM json_each(
					                 CASE WHEN json_valid(excluded.protocols)
					                              THEN excluded.protocols ELSE '[]' END)
			                 ) AS protocol_values
				                 ORDER BY CAST(value AS INTEGER)
			              )),
			protocol_bytes = (
				SELECT COALESCE(json_group_object(proto, bytes), '{}')
				FROM (
					SELECT CAST(key AS INTEGER) AS proto,
					       SUM(CAST(value AS INTEGER)) AS bytes
					FROM (
						SELECT key, value FROM json_each(
							CASE WHEN json_valid(node_pairs.protocol_bytes)
							     THEN node_pairs.protocol_bytes ELSE '{}' END)
						UNION ALL
						SELECT key, value FROM json_each(
							CASE WHEN json_valid(excluded.protocol_bytes)
							     THEN excluded.protocol_bytes ELSE '{}' END)
					) AS protocol_values
					GROUP BY proto
					ORDER BY proto
				)
			),
			ports = (
				SELECT COALESCE(json_group_array(json_object('port', port, 'proto', proto, 'bytes', bytes)), '[]')
				FROM (
					SELECT CAST(json_extract(value, '$.port') AS INTEGER) AS port,
					       CAST(json_extract(value, '$.proto') AS INTEGER) AS proto,
					       SUM(CAST(json_extract(value, '$.bytes') AS INTEGER)) AS bytes
					FROM (
						SELECT value FROM json_each(
							CASE WHEN json_valid(node_pairs.ports)
							     THEN node_pairs.ports ELSE '[]' END)
						UNION ALL
						SELECT value FROM json_each(
							CASE WHEN json_valid(excluded.ports)
							     THEN excluded.ports ELSE '[]' END)
					) AS port_values
					GROUP BY proto, port
					ORDER BY bytes DESC, proto ASC, port ASC
					LIMIT 20
				)
			)
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
			agg.FlowCount, agg.Protocols,
			normalizeProtocolBytes(agg.ProtocolBytes, agg.Protocols, agg.TxBytes+agg.RxBytes),
			agg.Ports,
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
		                           virtual_bytes, exit_bytes, subnet_bytes, physical_bytes,
		                           total_flows, unique_pairs, top_ports)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(bucket) DO UPDATE SET
			tcp_bytes         = tcp_bytes         + excluded.tcp_bytes,
			udp_bytes         = udp_bytes         + excluded.udp_bytes,
			other_proto_bytes = other_proto_bytes + excluded.other_proto_bytes,
			virtual_bytes     = virtual_bytes     + excluded.virtual_bytes,
			subnet_bytes      = subnet_bytes      + excluded.subnet_bytes,
			physical_bytes    = physical_bytes    + excluded.physical_bytes,
			exit_bytes       = COALESCE(exit_bytes, 0) + excluded.exit_bytes,
			total_flows       = total_flows       + excluded.total_flows,
			unique_pairs      = MAX(unique_pairs, excluded.unique_pairs),
			top_ports         = (
				SELECT COALESCE(json_group_array(json_object('port', port, 'proto', proto, 'bytes', bytes)), '[]')
				FROM (
					SELECT CAST(json_extract(value, '$.port') AS INTEGER) AS port,
					       CAST(json_extract(value, '$.proto') AS INTEGER) AS proto,
					       SUM(CAST(json_extract(value, '$.bytes') AS INTEGER)) AS bytes
					FROM (
						SELECT value FROM json_each(
							CASE WHEN json_valid(traffic_stats.top_ports)
							     THEN traffic_stats.top_ports ELSE '[]' END)
						UNION ALL
						SELECT value FROM json_each(
							CASE WHEN json_valid(excluded.top_ports)
							     THEN excluded.top_ports ELSE '[]' END)
					) AS port_values
					GROUP BY proto, port
					ORDER BY bytes DESC, proto ASC, port ASC
					LIMIT 20
				)
			)
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
			st.VirtualBytes, st.ExitBytes, st.SubnetBytes, st.PhysicalBytes,
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
func (s *SQLiteStore) GetNodePairAggregates(ctx context.Context, start, end time.Time) ([]NodePairAggregate, error) {
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
		       COALESCE((SELECT json_group_array(proto) FROM (
		                    SELECT CAST(j.key AS INTEGER) AS proto,
		                           SUM(CAST(j.value AS INTEGER)) AS bytes
		                    FROM node_pairs sub, json_each(
		                        CASE WHEN json_valid(sub.protocol_bytes)
		                             THEN sub.protocol_bytes ELSE '{}' END) AS j
		                    WHERE sub.src_node_id = main.src_node_id
		                      AND sub.dst_node_id = main.dst_node_id
		                      AND sub.traffic_type = main.traffic_type
		                      AND sub.bucket >= ? AND sub.bucket < ?
		                    GROUP BY proto
		                    ORDER BY bytes DESC, proto ASC
		                 )), '[]'),
		       COALESCE((SELECT json_group_object(proto, bytes) FROM (
		                    SELECT CAST(j.key AS INTEGER) AS proto,
		                           SUM(CAST(j.value AS INTEGER)) AS bytes
		                    FROM node_pairs sub, json_each(
		                        CASE WHEN json_valid(sub.protocol_bytes)
		                             THEN sub.protocol_bytes ELSE '{}' END) AS j
		                    WHERE sub.src_node_id = main.src_node_id
		                      AND sub.dst_node_id = main.dst_node_id
		                      AND sub.traffic_type = main.traffic_type
		                      AND sub.bucket >= ? AND sub.bucket < ?
		                    GROUP BY proto
		                    ORDER BY proto ASC
		                 )), '{}'),
		       COALESCE((SELECT json_group_array(json_object('port', port, 'proto', proto, 'bytes', bytes)) FROM (
		                    SELECT CAST(json_extract(j.value, '$.port') AS INTEGER) AS port,
		                           CAST(json_extract(j.value, '$.proto') AS INTEGER) AS proto,
		                           SUM(CAST(json_extract(j.value, '$.bytes') AS INTEGER)) AS bytes
		                    FROM node_pairs sub, json_each(
		                        CASE WHEN json_valid(sub.ports)
		                             THEN sub.ports ELSE '[]' END) AS j
		                    WHERE sub.src_node_id = main.src_node_id
		                      AND sub.dst_node_id = main.dst_node_id
		                      AND sub.traffic_type = main.traffic_type
		                      AND sub.bucket >= ? AND sub.bucket < ?
		                    GROUP BY proto, port
		                    ORDER BY bytes DESC, proto ASC, port ASC
		                    LIMIT 20
		                 )), '[]')
		FROM node_pairs main
		WHERE bucket >= ? AND bucket < ?
		GROUP BY src_node_id, dst_node_id, traffic_type
		ORDER BY SUM(tx_bytes) + SUM(rx_bytes) DESC,
		         src_node_id ASC, dst_node_id ASC, traffic_type ASC
	`
	rows, err := s.db.QueryContext(ctx, query,
		startUnix, endUnix,
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
			&agg.FlowCount, &agg.Protocols, &agg.ProtocolBytes, &agg.Ports,
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
		WHERE bucket >= ? AND bucket < ?
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

// GetBandwidthByTrafficTypes retrieves network bandwidth from node-pair aggregates for selected traffic types.
func (s *SQLiteStore) GetBandwidthByTrafficTypes(ctx context.Context, start, end time.Time, trafficTypes []string) ([]BandwidthBucket, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	startUnix := start.UTC().Unix()
	endUnix := end.UTC().Unix()
	if startUnix >= endUnix {
		return nil, fmt.Errorf("invalid time range: start (%v) must be before end (%v)", start, end)
	}
	if len(trafficTypes) == 0 {
		return []BandwidthBucket{}, nil
	}

	bs := resolveBucketSize(endUnix - startUnix)
	placeholders := strings.TrimRight(strings.Repeat("?,", len(trafficTypes)), ",")
	query := fmt.Sprintf(`
		SELECT (bucket / %d) * %d AS b, SUM(tx_bytes + rx_bytes), 0
		FROM node_pairs
		WHERE bucket >= ? AND bucket < ? AND traffic_type IN (%s)
		GROUP BY b
		ORDER BY b ASC
	`, bs, bs, placeholders)

	args := make([]any, 0, 2+len(trafficTypes))
	args = append(args, startUnix, endUnix)
	for _, trafficType := range trafficTypes {
		args = append(args, trafficType)
	}

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query bandwidth by traffic type: %w", err)
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

	// Derive node bandwidth from normalized node_pairs rather than the legacy
	// bandwidth_by_node table. This keeps historical self-flows from appearing
	// as both TX and RX after the self-flow accounting fix.
	bs := resolveBucketSize(endUnix - startUnix)
	query := fmt.Sprintf(`
		WITH node_bytes AS (
			SELECT (bucket / %d) * %d AS b,
			       src_node_id AS node_id,
			       SUM(tx_bytes) AS tx,
			       SUM(rx_bytes) AS rx
			FROM node_pairs
			WHERE bucket >= ? AND bucket < ?
			GROUP BY b, src_node_id
			UNION ALL
			SELECT (bucket / %d) * %d AS b,
			       dst_node_id AS node_id,
			       SUM(rx_bytes) AS tx,
			       SUM(tx_bytes) AS rx
			FROM node_pairs
			WHERE bucket >= ? AND bucket < ?
			  AND src_node_id != dst_node_id
			GROUP BY b, dst_node_id
		)
		SELECT b, SUM(tx), SUM(rx)
		FROM node_bytes
		WHERE node_id = ?
		GROUP BY b
		ORDER BY b ASC
	`, bs, bs, bs, bs)

	rows, err := s.db.QueryContext(ctx, query, startUnix, endUnix, startUnix, endUnix, nodeID)
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
	query := fmt.Sprintf(`
		WITH stat_buckets AS (
			SELECT (bucket / %d) * %d AS b,
			       SUM(tcp_bytes) AS tcp_bytes,
			       SUM(udp_bytes) AS udp_bytes,
			       SUM(other_proto_bytes) AS other_proto_bytes,
			       SUM(virtual_bytes) AS virtual_bytes,
			       SUM(exit_bytes) AS exit_bytes,
			       SUM(subnet_bytes) AS subnet_bytes,
			       SUM(physical_bytes) AS physical_bytes,
			       SUM(total_flows) AS total_flows,
			       MAX(unique_pairs) AS stored_unique_pairs
			FROM traffic_stats
			WHERE bucket >= ? AND bucket < ?
			GROUP BY b
		), pair_buckets AS (
			SELECT b, COUNT(*) AS unique_pairs
			FROM (
				SELECT (bucket / %d) * %d AS b, src_node_id, dst_node_id
				FROM node_pairs
				WHERE bucket >= ? AND bucket < ?
				GROUP BY b, src_node_id, dst_node_id
			)
			GROUP BY b
		)
		SELECT sb.b, sb.tcp_bytes, sb.udp_bytes, sb.other_proto_bytes,
		       sb.virtual_bytes, sb.exit_bytes, sb.subnet_bytes, sb.physical_bytes,
		       sb.total_flows,
		       MAX(COALESCE(pb.unique_pairs, 0), COALESCE(sb.stored_unique_pairs, 0))
		FROM stat_buckets sb
		LEFT JOIN pair_buckets pb ON pb.b = sb.b
		ORDER BY sb.b ASC
	`, bs, bs, bs, bs)

	rows, err := s.db.QueryContext(ctx, query, startUnix, endUnix, startUnix, endUnix)
	if err != nil {
		return nil, fmt.Errorf("failed to query traffic stats: %w", err)
	}
	defer rows.Close()

	var results []TrafficStats
	for rows.Next() {
		var st TrafficStats
		if err := rows.Scan(
			&st.Bucket, &st.TCPBytes, &st.UDPBytes, &st.OtherProtoBytes,
			&st.VirtualBytes, &st.ExitBytes, &st.SubnetBytes, &st.PhysicalBytes,
			&st.TotalFlows, &st.UniquePairs,
		); err != nil {
			return nil, fmt.Errorf("failed to scan traffic stats: %w", err)
		}
		st.TopPorts = "[]"
		results = append(results, st)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	portQuery := fmt.Sprintf(`
		WITH port_totals AS (
			SELECT (ts.bucket / %d) * %d AS b,
			       CAST(json_extract(p.value, '$.proto') AS INTEGER) AS proto,
			       CAST(json_extract(p.value, '$.port') AS INTEGER) AS port,
			       SUM(CAST(json_extract(p.value, '$.bytes') AS INTEGER)) AS bytes
			FROM traffic_stats ts, json_each(
				CASE WHEN json_valid(ts.top_ports) THEN ts.top_ports ELSE '[]' END) AS p
			WHERE ts.bucket >= ? AND ts.bucket < ?
			GROUP BY b, proto, port
		), ranked_ports AS (
			SELECT b, proto, port, bytes,
			       ROW_NUMBER() OVER (PARTITION BY b ORDER BY bytes DESC, proto ASC, port ASC) AS rn
			FROM port_totals
		)
		SELECT b, proto, port, bytes
		FROM ranked_ports
		WHERE rn <= 20
		ORDER BY b ASC, bytes DESC, proto ASC, port ASC
	`, bs, bs)
	portRows, err := s.db.QueryContext(ctx, portQuery, startUnix, endUnix)
	if err != nil {
		return nil, fmt.Errorf("failed to query traffic stat ports: %w", err)
	}
	defer portRows.Close()
	resultsByBucket := make(map[int64]*TrafficStats, len(results))
	for i := range results {
		resultsByBucket[results[i].Bucket] = &results[i]
	}
	for portRows.Next() {
		var bucket int64
		var port PortStat
		if err := portRows.Scan(&bucket, &port.Proto, &port.Port, &port.Bytes); err != nil {
			return nil, fmt.Errorf("failed to scan traffic stat port: %w", err)
		}
		if st := resultsByBucket[bucket]; st != nil {
			st.TopPorts = appendPortStatJSON(st.TopPorts, port)
		}
	}
	if err := portRows.Err(); err != nil {
		return nil, err
	}
	for i := range results {
		if results[i].TopPorts == "" {
			results[i].TopPorts = "[]"
		}
	}
	return results, nil
}

func appendPortStatJSON(raw string, port PortStat) string {
	var ports []PortStat
	if json.Unmarshal([]byte(raw), &ports) != nil {
		ports = nil
	}
	ports = append(ports, port)
	encoded, err := json.Marshal(ports)
	if err != nil {
		return "[]"
	}
	return string(encoded)
}

// GetTrafficStatsFromNodePairs synthesizes traffic stats from node_pairs (fallback for old data).
func (s *SQLiteStore) GetTrafficStatsFromNodePairs(ctx context.Context, start, end time.Time) ([]TrafficStats, error) {
	return s.GetTrafficStatsFromNodePairsByTrafficTypes(ctx, start, end, nil)
}

// GetTrafficStatsFromNodePairsByTrafficTypes synthesizes traffic stats from node_pairs
// and limits the result to the requested traffic types when provided.
func (s *SQLiteStore) GetTrafficStatsFromNodePairsByTrafficTypes(ctx context.Context, start, end time.Time, trafficTypes []string) ([]TrafficStats, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	startUnix := start.UTC().Unix()
	endUnix := end.UTC().Unix()
	if startUnix >= endUnix {
		return nil, fmt.Errorf("invalid time range: start (%v) must be before end (%v)", start, end)
	}

	bs := resolveBucketSize(endUnix - startUnix)
	typeClause, typeArgs := trafficTypeWhereClause(trafficTypes)
	query := fmt.Sprintf(`
		SELECT (bucket / %d) * %d AS b,
		       SUM(CASE WHEN traffic_type = 'virtual'
		                THEN tx_bytes + rx_bytes ELSE 0 END) AS virtual_bytes,
		       SUM(CASE WHEN traffic_type = 'exit'
		                THEN tx_bytes + rx_bytes ELSE 0 END) AS exit_bytes,
		       SUM(CASE WHEN traffic_type = 'subnet'
		                THEN tx_bytes + rx_bytes ELSE 0 END) AS subnet_bytes,
		       SUM(CASE WHEN traffic_type = 'physical'
		                THEN tx_bytes + rx_bytes ELSE 0 END) AS physical_bytes,
		       SUM(flow_count) AS total_flows,
		       COUNT(DISTINCT src_node_id || '|' || dst_node_id) AS unique_pairs
		FROM node_pairs
		WHERE bucket >= ? AND bucket < ?%s
		GROUP BY b
		ORDER BY b ASC
	`, bs, bs, typeClause)

	args := append([]any{startUnix, endUnix}, typeArgs...)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query node pairs for traffic stats: %w", err)
	}
	defer rows.Close()

	bucketMap := make(map[int64]*TrafficStats)
	for rows.Next() {
		var bucket int64
		var virtualBytes, exitBytes, subnetBytes, physicalBytes, totalFlows, uniquePairs int64
		if err := rows.Scan(&bucket, &virtualBytes, &exitBytes, &subnetBytes, &physicalBytes, &totalFlows, &uniquePairs); err != nil {
			return nil, fmt.Errorf("failed to scan: %w", err)
		}
		st, ok := bucketMap[bucket]
		if !ok {
			st = &TrafficStats{Bucket: bucket, TopPorts: "[]"}
			bucketMap[bucket] = st
		}
		st.VirtualBytes = virtualBytes
		st.ExitBytes = exitBytes
		st.SubnetBytes = subnetBytes
		st.PhysicalBytes = physicalBytes
		st.TotalFlows += totalFlows
		st.UniquePairs = uniquePairs
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Derive protocol breakdown from persisted protocol byte totals. The
	// fallback branch keeps pre-migration rows useful if they are inserted by
	// an older writer after startup.
	protoQuery := fmt.Sprintf(`
		WITH protocol_values AS (
			SELECT (np.bucket / %d) * %d AS b,
			       CAST(j.key AS INTEGER) AS proto,
			       CAST(j.value AS INTEGER) AS bytes
			FROM node_pairs np, json_each(
				CASE WHEN json_valid(np.protocol_bytes) AND np.protocol_bytes != '{}'
				     THEN np.protocol_bytes ELSE '{}' END) AS j
			WHERE np.bucket >= ? AND np.bucket < ?%s
			UNION ALL
			SELECT (np.bucket / %d) * %d AS b,
			       CAST(j.value AS INTEGER) AS proto,
			       (np.tx_bytes + np.rx_bytes) / json_array_length(np.protocols)
			       + CASE WHEN CAST(j.key AS INTEGER) = 0 THEN
				           (np.tx_bytes + np.rx_bytes) -
				           ((np.tx_bytes + np.rx_bytes) / json_array_length(np.protocols)) * json_array_length(np.protocols)
				         ELSE 0 END AS bytes
			FROM node_pairs np, json_each(
				CASE WHEN json_valid(np.protocols) THEN np.protocols ELSE '[]' END) AS j
			WHERE np.bucket >= ? AND np.bucket < ?%s
			  AND (np.protocol_bytes IS NULL OR np.protocol_bytes = '' OR np.protocol_bytes = '{}'
			       OR NOT json_valid(np.protocol_bytes))
			  AND json_array_length(CASE WHEN json_valid(np.protocols) THEN np.protocols ELSE '[]' END) > 0
		)
		SELECT b, proto, SUM(bytes)
		FROM protocol_values
		GROUP BY b, proto
	`, bs, bs, typeClause, bs, bs, typeClause)
	protoArgs := append([]any{startUnix, endUnix}, typeArgs...)
	protoArgs = append(protoArgs, startUnix, endUnix)
	protoArgs = append(protoArgs, typeArgs...)
	protoRows, err := s.db.QueryContext(ctx, protoQuery, protoArgs...)
	if err != nil {
		return nil, fmt.Errorf("failed to query node pair protocols: %w", err)
	}
	for protoRows.Next() {
		var b int64
		var protocol int
		var totalBytes int64
		if err := protoRows.Scan(&b, &protocol, &totalBytes); err != nil {
			protoRows.Close()
			return nil, fmt.Errorf("failed to scan node pair protocol: %w", err)
		}
		st, ok := bucketMap[b]
		if !ok {
			continue
		}
		switch protocol {
		case 6:
			st.TCPBytes += totalBytes
		case 17:
			st.UDPBytes += totalBytes
		default:
			st.OtherProtoBytes += totalBytes
		}
	}
	if err := protoRows.Err(); err != nil {
		protoRows.Close()
		return nil, fmt.Errorf("failed to read node pair protocols: %w", err)
	}
	if err := protoRows.Close(); err != nil {
		return nil, fmt.Errorf("failed to close node pair protocol rows: %w", err)
	}

	// Rebuild top ports for the same bucket size and traffic-type filter. The
	// traffic_stats table has this precomputed, but filtered analytics are
	// synthesized from node_pairs and need to carry the same shape forward.
	portQuery := fmt.Sprintf(`
		WITH port_totals AS (
			SELECT (bucket / %d) * %d AS b,
			       CAST(json_extract(p.value, '$.proto') AS INTEGER) AS proto,
			       CAST(json_extract(p.value, '$.port') AS INTEGER) AS port,
			       SUM(CAST(json_extract(p.value, '$.bytes') AS INTEGER)) AS bytes
			FROM node_pairs, json_each(
				CASE WHEN json_valid(ports) THEN ports ELSE '[]' END) AS p
			WHERE bucket >= ? AND bucket < ?%s
			  AND ports != '[]'
			GROUP BY b, proto, port
		),
		ranked_ports AS (
			SELECT b, proto, port, bytes,
			       ROW_NUMBER() OVER (PARTITION BY b ORDER BY bytes DESC, proto ASC, port ASC) AS rn
			FROM port_totals
		)
		SELECT b, proto, port, bytes
		FROM ranked_ports
		WHERE rn <= 20
			ORDER BY b ASC, bytes DESC, proto ASC, port ASC
	`, bs, bs, typeClause)
	portRows, err := s.db.QueryContext(ctx, portQuery, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query node pair ports: %w", err)
	}
	topPortsByBucket := make(map[int64][]PortStat)
	for portRows.Next() {
		var b int64
		var port PortStat
		if err := portRows.Scan(&b, &port.Proto, &port.Port, &port.Bytes); err != nil {
			portRows.Close()
			return nil, fmt.Errorf("failed to scan node pair port: %w", err)
		}
		topPortsByBucket[b] = append(topPortsByBucket[b], port)
	}
	if err := portRows.Err(); err != nil {
		portRows.Close()
		return nil, fmt.Errorf("failed to read node pair ports: %w", err)
	}
	if err := portRows.Close(); err != nil {
		return nil, fmt.Errorf("failed to close node pair port rows: %w", err)
	}

	for b, topPorts := range topPortsByBucket {
		if encoded, err := json.Marshal(topPorts); err != nil {
			return nil, fmt.Errorf("failed to encode node pair ports: %w", err)
		} else if st := bucketMap[b]; st != nil {
			st.TopPorts = string(encoded)
		}
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

	// Use node_pairs as the source of truth so old per-node rows cannot retain
	// the pre-fix self-flow double count.
	rows, err := s.db.QueryContext(ctx, `
		WITH node_bytes AS (
			SELECT src_node_id AS node_id, SUM(tx_bytes) AS tx, SUM(rx_bytes) AS rx
			FROM node_pairs
			WHERE bucket >= ? AND bucket < ?
			GROUP BY src_node_id
			UNION ALL
			SELECT dst_node_id AS node_id, SUM(rx_bytes) AS tx, SUM(tx_bytes) AS rx
			FROM node_pairs
			WHERE bucket >= ? AND bucket < ?
			  AND src_node_id != dst_node_id
			GROUP BY dst_node_id
		), totals AS (
			SELECT node_id, SUM(tx) AS tx, SUM(rx) AS rx
			FROM node_bytes
			GROUP BY node_id
		)
		SELECT node_id, tx, rx, tx + rx AS total
		FROM totals
		ORDER BY total DESC, node_id ASC
		LIMIT ?
	`, startUnix, endUnix, startUnix, endUnix, limit)
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

// GetTopTalkersByTrafficTypes returns top talkers limited to selected traffic types.
func (s *SQLiteStore) GetTopTalkersByTrafficTypes(ctx context.Context, start, end time.Time, trafficTypes []string, limit int) ([]TopTalker, error) {
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

	typeClause, typeArgs := trafficTypeWhereClause(trafficTypes)
	query := fmt.Sprintf(`
		WITH node_bytes AS (
			SELECT src_node_id AS node_id, SUM(tx_bytes) AS tx, SUM(rx_bytes) AS rx
			FROM node_pairs
			WHERE bucket >= ? AND bucket < ?%s
			GROUP BY src_node_id
			UNION ALL
			SELECT dst_node_id AS node_id, SUM(rx_bytes) AS tx, SUM(tx_bytes) AS rx
			FROM node_pairs
			WHERE bucket >= ? AND bucket < ?
			  AND src_node_id != dst_node_id%s
			GROUP BY dst_node_id
		)
		SELECT node_id, SUM(tx) AS tx, SUM(rx) AS rx, SUM(tx + rx) AS total
		FROM node_bytes
		GROUP BY node_id
		ORDER BY total DESC, node_id ASC
		LIMIT ?
	`, typeClause, typeClause)
	args := append([]any{startUnix, endUnix}, typeArgs...)
	args = append(args, startUnix, endUnix)
	args = append(args, typeArgs...)
	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query top talkers by traffic types: %w", err)
	}
	defer rows.Close()

	var results []TopTalker
	for rows.Next() {
		var t TopTalker
		if err := rows.Scan(&t.NodeID, &t.TxBytes, &t.RxBytes, &t.TotalBytes); err != nil {
			return nil, fmt.Errorf("failed to scan filtered top talker: %w", err)
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
		WHERE bucket >= ? AND bucket < ?
		GROUP BY src_node_id, dst_node_id
		ORDER BY total DESC, src_node_id ASC, dst_node_id ASC
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

// GetTopPairsByTrafficTypes returns node pairs limited to selected traffic types.
func (s *SQLiteStore) GetTopPairsByTrafficTypes(ctx context.Context, start, end time.Time, trafficTypes []string, limit int) ([]TopPair, error) {
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

	typeClause, typeArgs := trafficTypeWhereClause(trafficTypes)
	query := fmt.Sprintf(`
		SELECT src_node_id, dst_node_id,
		       SUM(tx_bytes), SUM(rx_bytes),
		       SUM(tx_bytes + rx_bytes) AS total, SUM(flow_count)
		FROM node_pairs
		WHERE bucket >= ? AND bucket < ?%s
		GROUP BY src_node_id, dst_node_id
		ORDER BY total DESC, src_node_id ASC, dst_node_id ASC
		LIMIT ?
	`, typeClause)
	args := append([]any{startUnix, endUnix}, typeArgs...)
	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query top pairs by traffic types: %w", err)
	}
	defer rows.Close()

	var results []TopPair
	for rows.Next() {
		var p TopPair
		if err := rows.Scan(&p.SrcNodeID, &p.DstNodeID, &p.TxBytes, &p.RxBytes, &p.TotalBytes, &p.FlowCount); err != nil {
			return nil, fmt.Errorf("failed to scan filtered top pair: %w", err)
		}
		results = append(results, p)
	}
	return results, rows.Err()
}

func trafficTypeWhereClause(trafficTypes []string) (string, []any) {
	if len(trafficTypes) == 0 {
		return "", nil
	}
	placeholders := strings.TrimRight(strings.Repeat("?,", len(trafficTypes)), ",")
	args := make([]any, 0, len(trafficTypes))
	for _, trafficType := range trafficTypes {
		args = append(args, trafficType)
	}
	return fmt.Sprintf(" AND traffic_type IN (%s)", placeholders), args
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

	// Keep totals consistent with GetNodeBandwidth and GetTopTalkers: derive
	// them from normalized pairs instead of legacy per-node bandwidth rows.
	if err := s.db.QueryRowContext(ctx, `
		WITH node_bytes AS (
			SELECT SUM(tx_bytes) AS tx, SUM(rx_bytes) AS rx
			FROM node_pairs
			WHERE src_node_id = ? AND bucket >= ? AND bucket < ?
			UNION ALL
			SELECT SUM(rx_bytes) AS tx, SUM(tx_bytes) AS rx
			FROM node_pairs
			WHERE dst_node_id = ? AND bucket >= ? AND bucket < ?
			  AND src_node_id != dst_node_id
		)
		SELECT COALESCE(SUM(tx), 0), COALESCE(SUM(rx), 0)
		FROM node_bytes
	`, nodeID, startUnix, endUnix, nodeID, startUnix, endUnix).Scan(&result.TotalTx, &result.TotalRx); err != nil {
		return nil, fmt.Errorf("failed to query node bandwidth: %w", err)
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT peer_id, SUM(tx), SUM(rx), SUM(tx+rx) AS total, SUM(fc)
		FROM (
			SELECT dst_node_id AS peer_id, SUM(tx_bytes) AS tx, SUM(rx_bytes) AS rx, SUM(flow_count) AS fc
			FROM node_pairs
			WHERE src_node_id = ? AND bucket >= ? AND bucket < ?
			GROUP BY dst_node_id
			UNION ALL
			SELECT src_node_id AS peer_id, SUM(rx_bytes) AS tx, SUM(tx_bytes) AS rx, SUM(flow_count) AS fc
			FROM node_pairs
			WHERE dst_node_id = ? AND bucket >= ? AND bucket < ?
			  AND src_node_id != dst_node_id
			GROUP BY src_node_id
		)
		GROUP BY peer_id
		ORDER BY total DESC, peer_id ASC
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
		  AND bucket >= ? AND bucket < ?
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
			return nil, fmt.Errorf("failed to scan node ports: %w", err)
		}
		var entries []PortStat
		if err := json.Unmarshal([]byte(portsJSON), &entries); err != nil {
			return nil, fmt.Errorf("failed to decode node ports: %w", err)
		}
		for _, e := range entries {
			portAgg[protoPortKey{e.Proto, e.Port}] += e.Bytes
		}
	}
	if err := portRows.Err(); err != nil {
		return nil, fmt.Errorf("failed to read node ports: %w", err)
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
	sort.Slice(result.TopPorts, func(i, j int) bool {
		if result.TopPorts[i].Bytes != result.TopPorts[j].Bytes {
			return result.TopPorts[i].Bytes > result.TopPorts[j].Bytes
		}
		if result.TopPorts[i].Proto != result.TopPorts[j].Proto {
			return result.TopPorts[i].Proto < result.TopPorts[j].Proto
		}
		return result.TopPorts[i].Port < result.TopPorts[j].Port
	})
	if len(result.TopPorts) > 15 {
		result.TopPorts = result.TopPorts[:15]
	}

	return result, nil
}
