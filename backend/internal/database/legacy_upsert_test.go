package database

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"testing"
	"time"
)

// legacyUpsertNodePairsTx is the node_pairs upsert as it was before the
// ingest rewrite: one statement per row, merging the JSON columns in SQL on
// conflict. It stays here as the reference the new write path is compared
// against, row for row and byte for byte.
// legacyUpsertNodePairsTx adds each minute delta to node_pairs, and to the hourly
// row when the minute is already rolled up. Hours changed that way are added
// to touches so the caller can drop them from the closed-hour cache after
// the commit.
func legacyUpsertNodePairsTx(ctx context.Context, tx *sql.Tx, tailnetID string, aggregates []NodePairAggregate, touches hourTouches) error {
	if len(aggregates) == 0 {
		return nil
	}

	// Directional metadata is kept as JSON for consistency with the existing
	// protocol and port aggregates. Keep the merge expressions here, alongside
	// the legacy metadata merge, so one upsert remains atomic.
	// A bare number in a protocol bytes column comes from rows written by old
	// builds. json_each gives it a NULL key, and json_group_object would drop
	// that group, so the merge keys it as protocol 0 to keep its bytes.
	mergeProtocolBytes := func(existing, incoming string) string {
		return fmt.Sprintf(`(
				SELECT COALESCE(json_group_object(proto, bytes), '{}')
				FROM (
					SELECT CAST(COALESCE(key, 0) AS INTEGER) AS proto,
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

	mark, err := readHourMark(ctx, tx, tailnetID)
	if err != nil {
		return err
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
					SELECT CAST(COALESCE(key, 0) AS INTEGER) AS proto,
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
		protocols := agg.Protocols
		protocolBytes := normalizeProtocolBytes(agg.ProtocolBytes, agg.Protocols, agg.TxBytes+agg.RxBytes)
		txProto := normalizeProtocolBytes(agg.TxProtocolBytes, "[]", agg.TxBytes)
		rxProto := normalizeProtocolBytes(agg.RxProtocolBytes, "[]", agg.RxBytes)
		directional := int64(0)
		if agg.DirectionalPorts {
			directional = 1
		}
		if _, err := stmt.ExecContext(ctx,
			tailnetID, bucket, agg.SrcNodeID, agg.DstNodeID, agg.TrafficType,
			agg.TxBytes, agg.RxBytes, agg.TxPkts, agg.RxPkts,
			agg.FlowCount, protocols, protocolBytes,
			agg.Ports, agg.TxPorts, agg.RxPorts,
			txProto, rxProto, directional,
		); err != nil {
			return fmt.Errorf("failed to upsert node pair: %w", err)
		}
		// A minute that is already inside the rollup has to take the same
		// delta. The row is the source of truth either way; this keeps the
		// hour equal to the sum of its minutes.
		if bucket <= mark {
			touches.add(bucket)
			if err := mergeHourDelta(ctx, tx, tailnetID, bucket, hourDelta{
				src: agg.SrcNodeID, dst: agg.DstNodeID, traffic: agg.TrafficType,
				tx: agg.TxBytes, rx: agg.RxBytes, txPkts: agg.TxPkts, rxPkts: agg.RxPkts,
				flows: agg.FlowCount, directional: directional,
				protocols: protocols, protocolBytes: protocolBytes, ports: agg.Ports,
				txPorts: agg.TxPorts, rxPorts: agg.RxPorts, txProto: txProto, rxProto: rxProto,
			}); err != nil {
				return err
			}
		}
	}
	return nil
}

// mergeHourDelta applies one delta, as the old per-row path did.
func mergeHourDelta(ctx context.Context, tx *sql.Tx, tailnetID string, minuteBucket int64, values hourDelta) error {
	values.bucket = minuteBucket
	return mergeHourDeltas(ctx, tx, tailnetID, []hourDelta{values})
}

// rollClosedMinutes merges minute rows older than closedThrough into the
// hourly rollup. closedThrough is exclusive and should be minute-aligned.
// The open minute at the cursor stays in node_pairs.
// A pass that merged rows logs its row count and duration.
func rollClosedMinutes(ctx context.Context, tx *sql.Tx, tailnetID string, closedThrough int64) error {
	started := time.Now()
	var rows int64
	chunks := 0
	for {
		n, done, err := rollNextHourChunk(ctx, tx, tailnetID, closedThrough)
		if err != nil {
			return err
		}
		rows += n
		if n > 0 {
			chunks++
		}
		if done {
			if rows > 0 {
				log.Printf("Hour rollup tailnet=%s rows=%d chunks=%d took=%s", tailnetID, rows, chunks, time.Since(started).Round(time.Millisecond))
			}
			return nil
		}
	}
}

// rollThrough rolls minutes through closedThrough, partial hours included,
// the way every poll did before complete-hour rollups. Tests use it to build
// the mid-hour states that a restart-time backfill can still produce.
func rollThrough(t testing.TB, st *SQLiteStore, tailnet string, closedThrough int64) {
	t.Helper()
	ctx := context.Background()
	tx, err := st.writer.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err := rollClosedMinutes(ctx, tx, tailnet, closedThrough); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}
