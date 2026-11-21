package database

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// Write path for node_pairs.
//
// A poll or object hands over every pair row for a few minutes at once. The
// old path ran one upsert per row, in whatever order the aggregator's map
// produced, and merged the JSON columns inside SQL on each conflict. This
// path does the work in memory first and then writes in primary key order:
//
//  1. Normalise each aggregate and sort the batch by the primary key.
//  2. Merge rows for the same key inside the batch.
//  3. Ask the table which keys already exist, one scan per minute.
//  4. Insert the new keys with multi-row INSERTs, in key order, so the b-tree
//     is appended to instead of rewritten at random pages.
//  5. Merge the few existing keys in Go and update them.
//
// The stored format is unchanged: the JSON columns keep the exact text the
// SQL merge produced, so readers, rollups and existing databases are not
// affected.

// pairRow is one normalised node_pairs row.
type pairRow struct {
	bucket                     int64
	src, dst, traffic          string
	tx, rx, txPkts, rxPkts     int64
	flows                      int64
	directional                int64
	protocols, protocolBytes   string
	ports, txPorts, rxPorts    string
	txProtocolBytes, rxProtoBy string
}

func (r *pairRow) lessKey(o *pairRow) bool {
	if r.bucket != o.bucket {
		return r.bucket < o.bucket
	}
	if r.src != o.src {
		return r.src < o.src
	}
	if r.dst != o.dst {
		return r.dst < o.dst
	}
	return r.traffic < o.traffic
}

func (r *pairRow) sameKey(o *pairRow) bool {
	return r.bucket == o.bucket && r.src == o.src && r.dst == o.dst && r.traffic == o.traffic
}

const minuteBucketSize = int64(60)

func normalizePairRow(agg NodePairAggregate) pairRow {
	directional := int64(0)
	if agg.DirectionalPorts {
		directional = 1
	}
	return pairRow{
		bucket: (agg.Bucket / minuteBucketSize) * minuteBucketSize,
		src:    agg.SrcNodeID, dst: agg.DstNodeID, traffic: agg.TrafficType,
		tx: agg.TxBytes, rx: agg.RxBytes, txPkts: agg.TxPkts, rxPkts: agg.RxPkts,
		flows:           agg.FlowCount,
		directional:     directional,
		protocols:       agg.Protocols,
		protocolBytes:   normalizeProtocolBytes(agg.ProtocolBytes, agg.Protocols, agg.TxBytes+agg.RxBytes),
		ports:           agg.Ports,
		txPorts:         agg.TxPorts,
		rxPorts:         agg.RxPorts,
		txProtocolBytes: normalizeProtocolBytes(agg.TxProtocolBytes, "[]", agg.TxBytes),
		rxProtoBy:       normalizeProtocolBytes(agg.RxProtocolBytes, "[]", agg.RxBytes),
	}
}

// mergePairRows adds the incoming row's counters and merges its metadata into
// dst, with the same result the SQL ON CONFLICT clause produced.
func mergePairRows(dst *pairRow, in *pairRow) {
	dst.tx += in.tx
	dst.rx += in.rx
	dst.txPkts += in.txPkts
	dst.rxPkts += in.rxPkts
	dst.flows += in.flows
	dst.protocols = mergeProtocolList(dst.protocols, in.protocols)
	dst.protocolBytes = mergeProtocolBytesJSON(dst.protocolBytes, in.protocolBytes)
	dst.ports = mergePortsJSON(dst.ports, in.ports)
	dst.txProtocolBytes = mergeProtocolBytesJSON(dst.txProtocolBytes, in.txProtocolBytes)
	dst.rxProtoBy = mergeProtocolBytesJSON(dst.rxProtoBy, in.rxProtoBy)
	dst.txPorts = mergePortsJSON(dst.txPorts, in.txPorts)
	dst.rxPorts = mergePortsJSON(dst.rxPorts, in.rxPorts)
	if dst.directional == 1 && in.directional == 1 {
		dst.directional = 1
	} else {
		dst.directional = 0
	}
}

// mergeProtocolBytesJSON sums two protocol-to-bytes objects. A bare number,
// which old builds wrote, has no key and counts as protocol 0.
func mergeProtocolBytesJSON(a, b string) string {
	var sums protoSums
	if err := mergeProtocolColumn(&sums, a); err != nil {
		sums = protoSums{}
	}
	if err := mergeProtocolColumn(&sums, b); err != nil {
		// An undecodable column counts as empty, like json_valid() = 0.
		sums = protoSums{}
		_ = mergeProtocolColumn(&sums, a)
	}
	if sums.hasNull {
		sums.add(0, false, sums.nullKey.n, sums.nullKey.numeric)
		sums.hasNull = false
		sums.nullKey = sumState{}
	}
	return formatProtocolBytes(sums)
}

// mergePortsJSON sums two port arrays and keeps the 20 busiest.
func mergePortsJSON(a, b string) string {
	var sums portSums
	if err := mergePortColumn(&sums, a); err != nil {
		sums = portSums{}
	}
	if err := mergePortColumn(&sums, b); err != nil {
		sums = portSums{}
		_ = mergePortColumn(&sums, a)
	}
	return formatPortsLimit(sums, 20)
}

// mergeProtocolList is the sorted union of two protocol number arrays.
func mergeProtocolList(a, b string) string {
	seen := map[int64]struct{}{}
	var all []int64
	for _, raw := range []string{a, b} {
		for _, n := range parseProtocolList(raw) {
			if _, ok := seen[n]; ok {
				continue
			}
			seen[n] = struct{}{}
			all = append(all, n)
		}
	}
	if len(all) == 0 {
		return "[]"
	}
	sort.Slice(all, func(i, j int) bool { return all[i] < all[j] })
	var sb strings.Builder
	sb.WriteByte('[')
	for i, n := range all {
		if i > 0 {
			sb.WriteByte(',')
		}
		sb.WriteString(strconv.FormatInt(n, 10))
	}
	sb.WriteByte(']')
	return sb.String()
}

func parseProtocolList(raw string) []int64 {
	if raw == "" || raw == "[]" {
		return nil
	}
	// Fast path for the usual "[6,17]".
	if raw[0] == '[' && raw[len(raw)-1] == ']' {
		var out []int64
		inner := raw[1 : len(raw)-1]
		ok := true
		for _, part := range strings.Split(inner, ",") {
			n, err := strconv.ParseInt(part, 10, 64)
			if err != nil {
				ok = false
				break
			}
			out = append(out, n)
		}
		if ok {
			return out
		}
	}
	value, ok, err := decodeSQLiteJSON(raw)
	if err != nil || !ok {
		return nil
	}
	var out []int64
	for _, v := range jsonEachValues(value) {
		if n, numeric := castSQLiteValue(v); numeric {
			out = append(out, n)
		}
	}
	return out
}

// pairBatchRows is how many rows one multi-row INSERT carries. 18 columns per
// row keeps a statement under SQLite's 32766 bound variable limit.
const pairBatchRows = 1000

const pairInsertPrefix = `INSERT INTO node_pairs (tailnet_id, bucket, src_node_id, dst_node_id, traffic_type,
	tx_bytes, rx_bytes, tx_pkts, rx_pkts, flow_count, protocols, protocol_bytes, ports,
	tx_ports, rx_ports, tx_protocol_bytes, rx_protocol_bytes, directional_ports) VALUES `

func pairInsertSQL(n int) string {
	var sb strings.Builder
	sb.WriteString(pairInsertPrefix)
	for i := 0; i < n; i++ {
		if i > 0 {
			sb.WriteByte(',')
		}
		sb.WriteString("(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)")
	}
	return sb.String()
}

func pairInsertArgs(args []any, tailnetID string, r *pairRow) []any {
	return append(args,
		tailnetID, r.bucket, r.src, r.dst, r.traffic,
		r.tx, r.rx, r.txPkts, r.rxPkts, r.flows,
		r.protocols, r.protocolBytes, r.ports, r.txPorts, r.rxPorts,
		r.txProtocolBytes, r.rxProtoBy, r.directional,
	)
}

// existingPairKeys lists the keys already stored for one minute.
func existingPairKeys(ctx context.Context, tx *sql.Tx, tailnetID string, bucket int64) (map[pairGroupKey]struct{}, error) {
	rows, err := tx.QueryContext(ctx,
		`SELECT src_node_id, dst_node_id, traffic_type FROM node_pairs WHERE tailnet_id = ? AND bucket = ?`,
		tailnetID, bucket)
	if err != nil {
		return nil, fmt.Errorf("failed to read existing node pairs: %w", err)
	}
	defer rows.Close()
	keys := make(map[pairGroupKey]struct{})
	for rows.Next() {
		var k pairGroupKey
		if err := rows.Scan(&k.src, &k.dst, &k.traffic); err != nil {
			return nil, fmt.Errorf("failed to read existing node pairs: %w", err)
		}
		keys[k] = struct{}{}
	}
	return keys, rows.Err()
}

// storedPair is a node_pairs row as stored. The counters stay nullable
// because the old SQL left a NULL counter NULL when it added to it.
type storedPair struct {
	pairRow
	null [5]bool
}

func loadStoredPair(ctx context.Context, stmt *sql.Stmt, tailnetID string, r *pairRow) (storedPair, error) {
	var tx, rx, txPkts, rxPkts, flows, directional sql.NullInt64
	var protocols, protocolBytes, ports, txPorts, rxPorts, txProto, rxProto sql.NullString
	err := stmt.QueryRowContext(ctx, tailnetID, r.bucket, r.src, r.dst, r.traffic).Scan(
		&tx, &rx, &txPkts, &rxPkts, &flows,
		&protocols, &protocolBytes, &ports, &txPorts, &rxPorts, &txProto, &rxProto, &directional)
	if err != nil {
		return storedPair{}, err
	}
	sp := storedPair{pairRow: pairRow{
		bucket: r.bucket, src: r.src, dst: r.dst, traffic: r.traffic,
		tx: tx.Int64, rx: rx.Int64, txPkts: txPkts.Int64, rxPkts: rxPkts.Int64, flows: flows.Int64,
		protocols: protocols.String, protocolBytes: protocolBytes.String, ports: ports.String,
		txPorts: txPorts.String, rxPorts: rxPorts.String,
		txProtocolBytes: txProto.String, rxProtoBy: rxProto.String,
		directional: directional.Int64,
	}}
	sp.null = [5]bool{!tx.Valid, !rx.Valid, !txPkts.Valid, !rxPkts.Valid, !flows.Valid}
	return sp, nil
}

const pairSelectSQL = `SELECT tx_bytes, rx_bytes, tx_pkts, rx_pkts, flow_count,
	protocols, protocol_bytes, ports, tx_ports, rx_ports, tx_protocol_bytes, rx_protocol_bytes, directional_ports
	FROM node_pairs WHERE tailnet_id = ? AND bucket = ? AND src_node_id = ? AND dst_node_id = ? AND traffic_type = ?`

const pairUpdateSQL = `UPDATE node_pairs SET tx_bytes = ?, rx_bytes = ?, tx_pkts = ?, rx_pkts = ?, flow_count = ?,
	protocols = ?, protocol_bytes = ?, ports = ?, tx_ports = ?, rx_ports = ?,
	tx_protocol_bytes = ?, rx_protocol_bytes = ?, directional_ports = ?
	WHERE tailnet_id = ? AND bucket = ? AND src_node_id = ? AND dst_node_id = ? AND traffic_type = ?`

// upsertNodePairsTx adds each minute delta to node_pairs, and to the hourly
// row when the minute is already rolled up. Hours changed that way are added
// to touches so the caller can drop them from the closed-hour cache after
// the commit.
func upsertNodePairsTx(ctx context.Context, tx *sql.Tx, tailnetID string, aggregates []NodePairAggregate, touches hourTouches) error {
	if len(aggregates) == 0 {
		return nil
	}
	mark, err := readHourMark(ctx, tx, tailnetID)
	if err != nil {
		return err
	}

	rows := make([]pairRow, len(aggregates))
	for i := range aggregates {
		rows[i] = normalizePairRow(aggregates[i])
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].lessKey(&rows[j]) })

	// Rows for the same key inside the batch merge first.
	merged := rows[:0]
	for i := range rows {
		if n := len(merged); n > 0 && merged[n-1].sameKey(&rows[i]) {
			mergePairRows(&merged[n-1], &rows[i])
			continue
		}
		merged = append(merged, rows[i])
	}
	rows = merged

	var selectStmt, updateStmt *sql.Stmt
	defer func() {
		if selectStmt != nil {
			selectStmt.Close()
		}
		if updateStmt != nil {
			updateStmt.Close()
		}
	}()

	pending := make([]*pairRow, 0, pairBatchRows)
	args := make([]any, 0, pairBatchRows*18)
	var insertStmt *sql.Stmt
	var insertStmtRows int
	flush := func() error {
		if len(pending) == 0 {
			return nil
		}
		if insertStmt == nil || insertStmtRows != len(pending) {
			if insertStmt != nil {
				insertStmt.Close()
				insertStmt = nil
			}
			s, err := tx.PrepareContext(ctx, pairInsertSQL(len(pending)))
			if err != nil {
				return fmt.Errorf("failed to prepare node_pairs insert: %w", err)
			}
			insertStmt, insertStmtRows = s, len(pending)
		}
		args = args[:0]
		for _, r := range pending {
			args = pairInsertArgs(args, tailnetID, r)
		}
		if _, err := insertStmt.ExecContext(ctx, args...); err != nil {
			return fmt.Errorf("failed to insert node pairs: %w", err)
		}
		pending = pending[:0]
		return nil
	}
	defer func() {
		if insertStmt != nil {
			insertStmt.Close()
		}
	}()

	var deltas []hourDelta
	for start := 0; start < len(rows); {
		end := start
		for end < len(rows) && rows[end].bucket == rows[start].bucket {
			end++
		}
		bucket := rows[start].bucket
		existing, err := existingPairKeys(ctx, tx, tailnetID, bucket)
		if err != nil {
			return err
		}
		for i := start; i < end; i++ {
			r := &rows[i]
			if _, ok := existing[pairGroupKey{src: r.src, dst: r.dst, traffic: r.traffic}]; ok {
				if err := flush(); err != nil {
					return err
				}
				if selectStmt == nil {
					if selectStmt, err = tx.PrepareContext(ctx, pairSelectSQL); err != nil {
						return fmt.Errorf("failed to prepare node_pairs read: %w", err)
					}
					if updateStmt, err = tx.PrepareContext(ctx, pairUpdateSQL); err != nil {
						return fmt.Errorf("failed to prepare node_pairs update: %w", err)
					}
				}
				stored, err := loadStoredPair(ctx, selectStmt, tailnetID, r)
				if err != nil {
					return fmt.Errorf("failed to read node pair: %w", err)
				}
				mergePairRows(&stored.pairRow, r)
				if err := updateStoredPair(ctx, updateStmt, tailnetID, &stored); err != nil {
					return err
				}
			} else {
				pending = append(pending, r)
				if len(pending) == pairBatchRows {
					if err := flush(); err != nil {
						return err
					}
				}
			}
			// A minute that is already inside the rollup has to take the same
			// delta. The row is the source of truth either way; this keeps the
			// hour equal to the sum of its minutes.
			if bucket <= mark {
				touches.add(bucket)
				deltas = append(deltas, hourDelta{
					bucket: bucket,
					src:    r.src, dst: r.dst, traffic: r.traffic,
					tx: r.tx, rx: r.rx, txPkts: r.txPkts, rxPkts: r.rxPkts,
					flows: r.flows, directional: r.directional,
					protocols: r.protocols, protocolBytes: r.protocolBytes, ports: r.ports,
					txPorts: r.txPorts, rxPorts: r.rxPorts, txProto: r.txProtocolBytes, rxProto: r.rxProtoBy,
				})
			}
		}
		if err := flush(); err != nil {
			return err
		}
		start = end
	}
	return mergeHourDeltas(ctx, tx, tailnetID, deltas)
}

func updateStoredPair(ctx context.Context, stmt *sql.Stmt, tailnetID string, sp *storedPair) error {
	num := func(i int, v int64) any {
		if sp.null[i] {
			return nil
		}
		return v
	}
	_, err := stmt.ExecContext(ctx,
		num(0, sp.tx), num(1, sp.rx), num(2, sp.txPkts), num(3, sp.rxPkts), num(4, sp.flows),
		sp.protocols, sp.protocolBytes, sp.ports, sp.txPorts, sp.rxPorts,
		sp.txProtocolBytes, sp.rxProtoBy, sp.directional,
		tailnetID, sp.bucket, sp.src, sp.dst, sp.traffic)
	if err != nil {
		return fmt.Errorf("failed to update node pair: %w", err)
	}
	return nil
}
