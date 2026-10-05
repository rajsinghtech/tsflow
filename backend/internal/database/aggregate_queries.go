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
func (s *SQLiteStore) CommitPollResults(ctx context.Context, tailnetID string, results PollResults) error {
	if err := checkTailnetID(tailnetID); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	if err := upsertNodePairsTx(ctx, tx, tailnetID, results.NodePairs); err != nil {
		return err
	}
	if err := upsertBandwidthTx(ctx, tx, tailnetID, results.Bandwidth); err != nil {
		return err
	}
	if err := upsertNodeBandwidthTx(ctx, tx, tailnetID, results.NodeBandwidth); err != nil {
		return err
	}
	if err := upsertTrafficStatsTx(ctx, tx, tailnetID, results.TrafficStats); err != nil {
		return err
	}
	if err := upsertPollCursor(ctx, tx, tailnetID, results.PollEnd); err != nil {
		return err
	}

	return tx.Commit()
}

// CommitObjectIngest atomically writes aggregates for one immutable object and
// records the object key. If the object key already exists, the aggregates are
// not applied again.
func (s *SQLiteStore) CommitObjectIngest(ctx context.Context, tailnetID string, result ObjectIngestResult) error {
	if err := checkTailnetID(tailnetID); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	const sqliteFormat = "2006-01-02 15:04:05"
	insertRes, err := tx.ExecContext(ctx, `
		INSERT OR IGNORE INTO ingested_objects (tailnet_id, object_key, last_modified, size_bytes, flow_count, ingested_at)
		VALUES (?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
	`, tailnetID, result.Key, result.LastModified.UTC().Format(sqliteFormat), result.Size, result.FlowCount)
	if err != nil {
		return fmt.Errorf("failed to mark object as ingested: %w", err)
	}
	rows, err := insertRes.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to determine whether object was newly ingested: %w", err)
	}
	if rows == 0 {
		if err := upsertNodeMetadataTx(ctx, tx, tailnetID, result.NodeMetadata); err != nil {
			return err
		}
		if err := recordObjectMetadataTx(ctx, tx, tailnetID, result.Key, nodeMetadataIDs(result.NodeMetadata)); err != nil {
			return err
		}
		return tx.Commit()
	}

	if err := upsertNodeMetadataTx(ctx, tx, tailnetID, result.NodeMetadata); err != nil {
		return err
	}
	if err := recordObjectMetadataTx(ctx, tx, tailnetID, result.Key, nodeMetadataIDs(result.NodeMetadata)); err != nil {
		return err
	}
	if err := upsertNodePairsTx(ctx, tx, tailnetID, result.NodePairs); err != nil {
		return err
	}
	if err := upsertBandwidthTx(ctx, tx, tailnetID, result.Bandwidth); err != nil {
		return err
	}
	if err := upsertNodeBandwidthTx(ctx, tx, tailnetID, result.NodeBandwidth); err != nil {
		return err
	}
	if err := upsertTrafficStatsTx(ctx, tx, tailnetID, result.TrafficStats); err != nil {
		return err
	}

	// Object-store polls update the cursor after the full object batch has been
	// examined. Leaving PollEnd zero keeps an unreadable earlier object from
	// being skipped when a later object was committed successfully.
	if !result.PollEnd.IsZero() {
		if err := upsertPollCursor(ctx, tx, tailnetID, result.PollEnd); err != nil {
			return err
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

func upsertNodePairsTx(ctx context.Context, tx *sql.Tx, tailnetID string, aggregates []NodePairAggregate) error {
	if len(aggregates) == 0 {
		return nil
	}

	// Directional metadata is kept as JSON for consistency with the existing
	// protocol and port aggregates. Keep the merge expressions here, alongside
	// the legacy metadata merge, so one upsert remains atomic.
	mergeProtocolBytes := func(existing, incoming string) string {
		return fmt.Sprintf(`(
				SELECT COALESCE(json_group_object(proto, bytes), '{}')
				FROM (
					SELECT CAST(key AS INTEGER) AS proto,
					       SUM(CAST(value AS INTEGER)) AS bytes
					FROM (
						SELECT key, value FROM json_each(
							CASE WHEN json_valid(%s) THEN %s ELSE '{}' END)
						UNION ALL
						SELECT key, value FROM json_each(
							CASE WHEN json_valid(%s) THEN %s ELSE '{}' END)
					) AS directional_protocol_values
					GROUP BY proto
					ORDER BY proto
				)
			)`, existing, existing, incoming, incoming)
	}
	mergePorts := func(existing, incoming string) string {
		return fmt.Sprintf(`(
				SELECT COALESCE(json_group_array(json_object('port', port, 'proto', proto, 'bytes', bytes)), '[]')
				FROM (
					SELECT CAST(json_extract(value, '$.port') AS INTEGER) AS port,
					       CAST(json_extract(value, '$.proto') AS INTEGER) AS proto,
					       SUM(CAST(json_extract(value, '$.bytes') AS INTEGER)) AS bytes
					FROM (
						SELECT value FROM json_each(
							CASE WHEN json_valid(%s) THEN %s ELSE '[]' END)
						UNION ALL
						SELECT value FROM json_each(
							CASE WHEN json_valid(%s) THEN %s ELSE '[]' END)
					) AS directional_port_values
					GROUP BY proto, port
					ORDER BY bytes DESC, proto ASC, port ASC
					LIMIT 20
				)
			)`, existing, existing, incoming, incoming)
	}

	stmt, err := tx.PrepareContext(ctx, fmt.Sprintf(`
		INSERT INTO node_pairs (tailnet_id, bucket, src_node_id, dst_node_id, traffic_type,
		                        tx_bytes, rx_bytes, tx_pkts, rx_pkts, flow_count, protocols, protocol_bytes, ports,
		                        tx_ports, rx_ports, tx_protocol_bytes, rx_protocol_bytes, directional_ports)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(tailnet_id, bucket, src_node_id, dst_node_id, traffic_type) DO UPDATE SET
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
			),
			tx_protocol_bytes = %s,
			rx_protocol_bytes = %s,
			tx_ports = %s,
			rx_ports = %s,
			directional_ports = CASE
				WHEN COALESCE(node_pairs.directional_ports, 0) = 1
				 AND COALESCE(excluded.directional_ports, 0) = 1 THEN 1
				ELSE 0
			END
	`,
		mergeProtocolBytes("node_pairs.tx_protocol_bytes", "excluded.tx_protocol_bytes"),
		mergeProtocolBytes("node_pairs.rx_protocol_bytes", "excluded.rx_protocol_bytes"),
		mergePorts("node_pairs.tx_ports", "excluded.tx_ports"),
		mergePorts("node_pairs.rx_ports", "excluded.rx_ports"),
	))
	if err != nil {
		return fmt.Errorf("failed to prepare node_pairs upsert: %w", err)
	}
	defer stmt.Close()

	const bucketSize = int64(60)
	for _, agg := range aggregates {
		bucket := (agg.Bucket / bucketSize) * bucketSize
		if _, err := stmt.ExecContext(ctx,
			tailnetID, bucket, agg.SrcNodeID, agg.DstNodeID, agg.TrafficType,
			agg.TxBytes, agg.RxBytes, agg.TxPkts, agg.RxPkts,
			agg.FlowCount, agg.Protocols,
			normalizeProtocolBytes(agg.ProtocolBytes, agg.Protocols, agg.TxBytes+agg.RxBytes),
			agg.Ports, agg.TxPorts, agg.RxPorts,
			normalizeProtocolBytes(agg.TxProtocolBytes, "[]", agg.TxBytes),
			normalizeProtocolBytes(agg.RxProtocolBytes, "[]", agg.RxBytes),
			agg.DirectionalPorts,
		); err != nil {
			return fmt.Errorf("failed to upsert node pair: %w", err)
		}
	}
	return nil
}

func upsertBandwidthTx(ctx context.Context, tx *sql.Tx, tailnetID string, buckets []BandwidthBucket) error {
	if len(buckets) == 0 {
		return nil
	}

	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO bandwidth (tailnet_id, bucket, tx_bytes, rx_bytes) VALUES (?, ?, ?, ?)
		ON CONFLICT(tailnet_id, bucket) DO UPDATE SET
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
		if _, err := stmt.ExecContext(ctx, tailnetID, bucket, b.TxBytes, b.RxBytes); err != nil {
			return fmt.Errorf("failed to upsert bandwidth: %w", err)
		}
	}
	return nil
}

func upsertNodeBandwidthTx(ctx context.Context, tx *sql.Tx, tailnetID string, buckets []NodeBandwidth) error {
	if len(buckets) == 0 {
		return nil
	}

	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO bandwidth_by_node (tailnet_id, bucket, node_id, tx_bytes, rx_bytes) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(tailnet_id, bucket, node_id) DO UPDATE SET
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
		if _, err := stmt.ExecContext(ctx, tailnetID, bucket, b.NodeID, b.TxBytes, b.RxBytes); err != nil {
			return fmt.Errorf("failed to upsert node bandwidth: %w", err)
		}
	}
	return nil
}

func upsertTrafficStatsTx(ctx context.Context, tx *sql.Tx, tailnetID string, stats []TrafficStats) error {
	if len(stats) == 0 {
		return nil
	}

	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO traffic_stats (tailnet_id, bucket, tcp_bytes, udp_bytes, other_proto_bytes,
		                           virtual_bytes, exit_bytes, subnet_bytes, physical_bytes,
		                           total_flows, unique_pairs, top_ports)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(tailnet_id, bucket) DO UPDATE SET
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
			tailnetID, bucket, st.TCPBytes, st.UDPBytes, st.OtherProtoBytes,
			st.VirtualBytes, st.ExitBytes, st.SubnetBytes, st.PhysicalBytes,
			st.TotalFlows, st.UniquePairs, st.TopPorts,
		); err != nil {
			return fmt.Errorf("failed to upsert traffic stats: %w", err)
		}
	}
	return nil
}

// UpsertNodePairAggregates upserts node-pair aggregates into node_pairs.
func (s *SQLiteStore) UpsertNodePairAggregates(ctx context.Context, tailnetID string, aggregates []NodePairAggregate) error {
	if err := checkTailnetID(tailnetID); err != nil {
		return err
	}
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
	if err := upsertNodePairsTx(ctx, tx, tailnetID, aggregates); err != nil {
		return err
	}
	return tx.Commit()
}

// UpsertBandwidth upserts total bandwidth into bandwidth.
func (s *SQLiteStore) UpsertBandwidth(ctx context.Context, tailnetID string, buckets []BandwidthBucket) error {
	if err := checkTailnetID(tailnetID); err != nil {
		return err
	}
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
	if err := upsertBandwidthTx(ctx, tx, tailnetID, buckets); err != nil {
		return err
	}
	return tx.Commit()
}

// UpsertNodeBandwidth upserts per-node bandwidth into bandwidth_by_node.
func (s *SQLiteStore) UpsertNodeBandwidth(ctx context.Context, tailnetID string, buckets []NodeBandwidth) error {
	if err := checkTailnetID(tailnetID); err != nil {
		return err
	}
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
	if err := upsertNodeBandwidthTx(ctx, tx, tailnetID, buckets); err != nil {
		return err
	}
	return tx.Commit()
}

// GetBandwidth retrieves total bandwidth for a time range, bucketed by window size.
func (s *SQLiteStore) GetBandwidth(ctx context.Context, tailnetID string, start, end time.Time) ([]BandwidthBucket, error) {
	if err := checkTailnetID(tailnetID); err != nil {
		return nil, err
	}
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
		WHERE tailnet_id = ? AND bucket >= ? AND bucket < ?
		GROUP BY b
		ORDER BY b ASC
	`, bs, bs)

	rows, err := s.db.QueryContext(ctx, query, tailnetID, startUnix, endUnix)
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
func (s *SQLiteStore) GetBandwidthByTrafficTypes(ctx context.Context, tailnetID string, start, end time.Time, trafficTypes []string) ([]BandwidthBucket, error) {
	if err := checkTailnetID(tailnetID); err != nil {
		return nil, err
	}
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
		WHERE tailnet_id = ? AND bucket >= ? AND bucket < ? AND traffic_type IN (%s)
		GROUP BY b
		ORDER BY b ASC
	`, bs, bs, placeholders)

	args := make([]any, 0, 3+len(trafficTypes))
	args = append(args, tailnetID, startUnix, endUnix)
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
func (s *SQLiteStore) GetNodeBandwidth(ctx context.Context, tailnetID string, start, end time.Time, nodeID string) ([]BandwidthBucket, error) {
	if err := checkTailnetID(tailnetID); err != nil {
		return nil, err
	}
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
			WHERE tailnet_id = ? AND bucket >= ? AND bucket < ?
			GROUP BY b, src_node_id
			UNION ALL
			SELECT (bucket / %d) * %d AS b,
			       dst_node_id AS node_id,
			       SUM(rx_bytes) AS tx,
			       SUM(tx_bytes) AS rx
			FROM node_pairs
			WHERE tailnet_id = ? AND bucket >= ? AND bucket < ?
			  AND src_node_id != dst_node_id
			GROUP BY b, dst_node_id
		)
		SELECT b, SUM(tx), SUM(rx)
		FROM node_bytes
		WHERE node_id = ?
		GROUP BY b
		ORDER BY b ASC
	`, bs, bs, bs, bs)

	rows, err := s.db.QueryContext(ctx, query, tailnetID, startUnix, endUnix, tailnetID, startUnix, endUnix, nodeID)
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
func (s *SQLiteStore) UpsertTrafficStats(ctx context.Context, tailnetID string, stats []TrafficStats) error {
	if err := checkTailnetID(tailnetID); err != nil {
		return err
	}
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
	if err := upsertTrafficStatsTx(ctx, tx, tailnetID, stats); err != nil {
		return err
	}
	return tx.Commit()
}

// GetTrafficStats retrieves network-wide traffic statistics for a time range, bucketed by window size.
func (s *SQLiteStore) GetTrafficStats(ctx context.Context, tailnetID string, start, end time.Time) ([]TrafficStats, error) {
	if err := checkTailnetID(tailnetID); err != nil {
		return nil, err
	}
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
			WHERE tailnet_id = ? AND bucket >= ? AND bucket < ?
			GROUP BY b
		), pair_buckets AS (
			SELECT b, COUNT(*) AS unique_pairs
			FROM (
				SELECT (bucket / %d) * %d AS b, src_node_id, dst_node_id
				FROM node_pairs
				WHERE tailnet_id = ? AND bucket >= ? AND bucket < ?
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

	rows, err := s.db.QueryContext(ctx, query, tailnetID, startUnix, endUnix, tailnetID, startUnix, endUnix)
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
			WHERE ts.tailnet_id = ? AND ts.bucket >= ? AND ts.bucket < ?
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
	portRows, err := s.db.QueryContext(ctx, portQuery, tailnetID, startUnix, endUnix)
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
func (s *SQLiteStore) GetTrafficStatsFromNodePairs(ctx context.Context, tailnetID string, start, end time.Time) ([]TrafficStats, error) {
	return s.GetTrafficStatsFromNodePairsByTrafficTypes(ctx, tailnetID, start, end, nil)
}

// GetTrafficStatsFromNodePairsByTrafficTypes synthesizes traffic stats from node_pairs
// and limits the result to the requested traffic types when provided.
func (s *SQLiteStore) GetTrafficStatsFromNodePairsByTrafficTypes(ctx context.Context, tailnetID string, start, end time.Time, trafficTypes []string) ([]TrafficStats, error) {
	if err := checkTailnetID(tailnetID); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.queryTrafficStatsFromNodePairs(ctx, tailnetID, start, end, nil, trafficTypes)
}

// queryTrafficStatsFromNodePairs reads derived traffic stats from node_pairs.
// ranges limits the read to those half-open intervals. Nil reads the whole
// window. Bucket grouping always uses the full window, so a short gap inside
// a long window lands on the same timestamps as a full read. Caller holds s.mu.
func (s *SQLiteStore) queryTrafficStatsFromNodePairs(ctx context.Context, tailnetID string, start, end time.Time, ranges [][2]int64, trafficTypes []string) ([]TrafficStats, error) {
	startUnix := start.UTC().Unix()
	endUnix := end.UTC().Unix()
	if startUnix >= endUnix {
		return nil, fmt.Errorf("invalid time range: start (%v) must be before end (%v)", start, end)
	}
	if len(ranges) == 0 {
		ranges = [][2]int64{{startUnix, endUnix}}
	}
	s.noteDerivedStatsRead(ranges)

	bs := resolveBucketSize(endUnix - startUnix)
	typeClause, typeArgs := trafficTypeWhereClause(trafficTypes)
	rangeSQL, rangeArgs := bucketRangePredicate("bucket", ranges)
	query := fmt.Sprintf(`
		WITH filtered_pairs AS (
			SELECT (bucket / %d) * %d AS b,
			       src_node_id,
			       dst_node_id,
			       traffic_type,
			       tx_bytes,
			       rx_bytes,
			       flow_count
			FROM node_pairs
			WHERE tailnet_id = ? AND %s%s
		), traffic_totals AS (
			SELECT b,
			       SUM(CASE WHEN traffic_type = 'virtual'
			                THEN tx_bytes + rx_bytes ELSE 0 END) AS virtual_bytes,
			       SUM(CASE WHEN traffic_type = 'exit'
			                THEN tx_bytes + rx_bytes ELSE 0 END) AS exit_bytes,
			       SUM(CASE WHEN traffic_type = 'subnet'
			                THEN tx_bytes + rx_bytes ELSE 0 END) AS subnet_bytes,
			       SUM(CASE WHEN traffic_type = 'physical'
			                THEN tx_bytes + rx_bytes ELSE 0 END) AS physical_bytes,
			       SUM(flow_count) AS total_flows
			FROM filtered_pairs
			GROUP BY b
		), unique_pair_counts AS (
			SELECT b, COUNT(*) AS unique_pairs
			FROM (
				SELECT DISTINCT b, src_node_id, dst_node_id
				FROM filtered_pairs
			)
			GROUP BY b
		)
		SELECT t.b,
		       t.virtual_bytes,
		       t.exit_bytes,
		       t.subnet_bytes,
		       t.physical_bytes,
		       t.total_flows,
		       p.unique_pairs
		FROM traffic_totals t
		JOIN unique_pair_counts p ON p.b = t.b
		ORDER BY t.b ASC
	`, bs, bs, rangeSQL, typeClause)

	args := append([]any{tailnetID}, rangeArgs...)
	args = append(args, typeArgs...)
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
	protoRangeSQL, protoRangeArgs := bucketRangePredicate("np.bucket", ranges)
	protoQuery := fmt.Sprintf(`
		WITH protocol_values AS (
			SELECT (np.bucket / %d) * %d AS b,
			       CAST(j.key AS INTEGER) AS proto,
			       CAST(j.value AS INTEGER) AS bytes
			FROM node_pairs np, json_each(
				CASE WHEN json_valid(np.protocol_bytes) AND np.protocol_bytes != '{}'
				     THEN np.protocol_bytes ELSE '{}' END) AS j
			WHERE np.tailnet_id = ? AND %s%s
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
			WHERE np.tailnet_id = ? AND %s%s
			  AND (np.protocol_bytes IS NULL OR np.protocol_bytes = '' OR np.protocol_bytes = '{}'
			       OR NOT json_valid(np.protocol_bytes))
			  AND json_array_length(CASE WHEN json_valid(np.protocols) THEN np.protocols ELSE '[]' END) > 0
		)
		SELECT b, proto, SUM(bytes)
		FROM protocol_values
		GROUP BY b, proto
	`, bs, bs, protoRangeSQL, typeClause, bs, bs, protoRangeSQL, typeClause)
	protoArgs := append([]any{tailnetID}, protoRangeArgs...)
	protoArgs = append(protoArgs, typeArgs...)
	protoArgs = append(protoArgs, tailnetID)
	protoArgs = append(protoArgs, protoRangeArgs...)
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
			WHERE tailnet_id = ? AND %s%s
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
	`, bs, bs, rangeSQL, typeClause)
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
func (s *SQLiteStore) GetTopTalkers(ctx context.Context, tailnetID string, start, end time.Time, limit int) ([]TopTalker, error) {
	if err := checkTailnetID(tailnetID); err != nil {
		return nil, err
	}
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
			WHERE tailnet_id = ? AND bucket >= ? AND bucket < ?
			GROUP BY src_node_id
			UNION ALL
			SELECT dst_node_id AS node_id, SUM(rx_bytes) AS tx, SUM(tx_bytes) AS rx
			FROM node_pairs
			WHERE tailnet_id = ? AND bucket >= ? AND bucket < ?
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
	`, tailnetID, startUnix, endUnix, tailnetID, startUnix, endUnix, limit)
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
func (s *SQLiteStore) GetTopTalkersByTrafficTypes(ctx context.Context, tailnetID string, start, end time.Time, trafficTypes []string, limit int) ([]TopTalker, error) {
	if err := checkTailnetID(tailnetID); err != nil {
		return nil, err
	}
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
			WHERE tailnet_id = ? AND bucket >= ? AND bucket < ?%s
			GROUP BY src_node_id
			UNION ALL
			SELECT dst_node_id AS node_id, SUM(rx_bytes) AS tx, SUM(tx_bytes) AS rx
			FROM node_pairs
			WHERE tailnet_id = ? AND bucket >= ? AND bucket < ?
			  AND src_node_id != dst_node_id%s
			GROUP BY dst_node_id
		)
		SELECT node_id, SUM(tx) AS tx, SUM(rx) AS rx, SUM(tx + rx) AS total
		FROM node_bytes
		GROUP BY node_id
		ORDER BY total DESC, node_id ASC
		LIMIT ?
	`, typeClause, typeClause)
	args := append([]any{tailnetID, startUnix, endUnix}, typeArgs...)
	args = append(args, tailnetID, startUnix, endUnix)
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
func (s *SQLiteStore) GetTopPairs(ctx context.Context, tailnetID string, start, end time.Time, limit int) ([]TopPair, error) {
	if err := checkTailnetID(tailnetID); err != nil {
		return nil, err
	}
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
		WHERE tailnet_id = ? AND bucket >= ? AND bucket < ?
		GROUP BY src_node_id, dst_node_id
		ORDER BY total DESC, src_node_id ASC, dst_node_id ASC
		LIMIT ?
	`, tailnetID, startUnix, endUnix, limit)
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
func (s *SQLiteStore) GetTopPairsByTrafficTypes(ctx context.Context, tailnetID string, start, end time.Time, trafficTypes []string, limit int) ([]TopPair, error) {
	if err := checkTailnetID(tailnetID); err != nil {
		return nil, err
	}
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
		WHERE tailnet_id = ? AND bucket >= ? AND bucket < ?%s
		GROUP BY src_node_id, dst_node_id
		ORDER BY total DESC, src_node_id ASC, dst_node_id ASC
		LIMIT ?
	`, typeClause)
	args := append([]any{tailnetID, startUnix, endUnix}, typeArgs...)
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
func (s *SQLiteStore) GetNodeStats(ctx context.Context, tailnetID string, nodeID string, start, end time.Time) (*NodeDetailStats, error) {
	if err := checkTailnetID(tailnetID); err != nil {
		return nil, err
	}
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
			WHERE tailnet_id = ? AND src_node_id = ? AND bucket >= ? AND bucket < ?
			UNION ALL
			SELECT SUM(rx_bytes) AS tx, SUM(tx_bytes) AS rx
			FROM node_pairs
			WHERE tailnet_id = ? AND dst_node_id = ? AND bucket >= ? AND bucket < ?
			  AND src_node_id != dst_node_id
		)
		SELECT COALESCE(SUM(tx), 0), COALESCE(SUM(rx), 0)
		FROM node_bytes
	`, tailnetID, nodeID, startUnix, endUnix, tailnetID, nodeID, startUnix, endUnix).Scan(&result.TotalTx, &result.TotalRx); err != nil {
		return nil, fmt.Errorf("failed to query node bandwidth: %w", err)
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT peer_id, SUM(tx), SUM(rx), SUM(tx+rx) AS total, SUM(fc)
		FROM (
			SELECT dst_node_id AS peer_id, SUM(tx_bytes) AS tx, SUM(rx_bytes) AS rx, SUM(flow_count) AS fc
			FROM node_pairs
			WHERE tailnet_id = ? AND src_node_id = ? AND bucket >= ? AND bucket < ?
			GROUP BY dst_node_id
			UNION ALL
			SELECT src_node_id AS peer_id, SUM(rx_bytes) AS tx, SUM(tx_bytes) AS rx, SUM(flow_count) AS fc
			FROM node_pairs
			WHERE tailnet_id = ? AND dst_node_id = ? AND bucket >= ? AND bucket < ?
			  AND src_node_id != dst_node_id
			GROUP BY src_node_id
		)
		GROUP BY peer_id
		ORDER BY total DESC, peer_id ASC
		LIMIT 10
	`, tailnetID, nodeID, startUnix, endUnix, tailnetID, nodeID, startUnix, endUnix)
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
		WHERE tailnet_id = ?
		  AND (src_node_id = ? OR dst_node_id = ?)
		  AND bucket >= ? AND bucket < ?
		  AND ports != '[]'
	`, tailnetID, nodeID, nodeID, startUnix, endUnix)
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
