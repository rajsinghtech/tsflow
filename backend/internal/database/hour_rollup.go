package database

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"
)

const (
	minuteSeconds int64 = 60
	hourSeconds   int64 = 3600
)

// hourPlan is how a half-open window is split between minute rows and hourly
// rollup rows. Hours are used only when the whole hour is inside the window
// and every minute of that hour is already rolled up. The partial hours at
// the ends stay on minute rows, so the sum matches a scan of node_pairs.
type hourPlan struct {
	minutes [][2]int64
	hours   [][2]int64
	// legacy is true when a rolled minute still needs the protocol-list
	// fallback. Derived stats then read those minute rows for protocol bytes
	// and leave the integer totals on the hourly rows.
	legacy bool
}

func (p hourPlan) useHours() bool {
	return len(p.hours) > 0
}

// planHours splits [start, end) around hourly rollups covered by mark.
// mark is the newest minute bucket that has been merged, or -1 when none have.
func planHours(start, end, mark int64) hourPlan {
	if start >= end {
		return hourPlan{}
	}
	var rolledUntil int64
	if mark >= 0 {
		rolledUntil = mark + minuteSeconds
	}
	hourStart := start
	if start%hourSeconds != 0 {
		hourStart = (start/hourSeconds + 1) * hourSeconds
	}
	hourEnd := (end / hourSeconds) * hourSeconds
	if rolledUntil < hourEnd {
		hourEnd = (rolledUntil / hourSeconds) * hourSeconds
	}
	if hourStart >= hourEnd || hourEnd <= start || hourStart >= end {
		return hourPlan{minutes: [][2]int64{{start, end}}}
	}
	plan := hourPlan{hours: [][2]int64{{hourStart, hourEnd}}}
	if start < hourStart {
		plan.minutes = append(plan.minutes, [2]int64{start, hourStart})
	}
	if hourEnd < end {
		plan.minutes = append(plan.minutes, [2]int64{hourEnd, end})
	}
	return plan
}

// planHoursThroughMark is planHours plus the hour that is still filling.
// That hour's rollup row holds every minute up to mark, so a window that
// starts at or before the hour and ends at or after mark+1m can read the
// row for [hour, mark+1m) and only scan minute rows after the mark. Live
// windows end at "now", so this drops most of the trailing minute scan.
//
// It is only valid when the mark and the rows are read in one snapshot. A
// mark that moves between the two would count the newly closed minutes in
// both the hour row and the minute scan.
func planHoursThroughMark(start, end, mark int64) hourPlan {
	plan := planHours(start, end, mark)
	if mark < 0 {
		return plan
	}
	rolledUntil := mark + minuteSeconds
	if rolledUntil%hourSeconds == 0 || end < rolledUntil {
		return plan
	}
	filling := (rolledUntil / hourSeconds) * hourSeconds
	if filling < start {
		return plan
	}
	hourStart := filling
	if len(plan.hours) > 0 {
		hourStart = plan.hours[0][0]
	}
	out := hourPlan{hours: [][2]int64{{hourStart, rolledUntil}}}
	if start < hourStart {
		out.minutes = append(out.minutes, [2]int64{start, hourStart})
	}
	if rolledUntil < end {
		out.minutes = append(out.minutes, [2]int64{rolledUntil, end})
	}
	return out
}

func floorMinute(unix int64) int64 {
	if unix <= 0 {
		return 0
	}
	return (unix / minuteSeconds) * minuteSeconds
}

// hourPlan chooses minute rows when the caller groups by a bucket smaller
// than an hour. Unique pairs and bandwidth charts at one-minute resolution
// cannot be rebuilt from an hourly row.
func (s *SQLiteStore) hourPlan(ctx context.Context, q queryRower, tailnetID string, start, end, subdiv int64) (hourPlan, error) {
	return s.hourPlanWith(ctx, q, tailnetID, start, end, subdiv, planHours)
}

// snapshotHourPlan is hourPlan for callers that read the mark and every
// row inside one read transaction, so the filling hour can be used.
func (s *SQLiteStore) snapshotHourPlan(ctx context.Context, tx *sql.Tx, tailnetID string, start, end int64) (hourPlan, error) {
	return s.hourPlanWith(ctx, tx, tailnetID, start, end, 0, planHoursThroughMark)
}

func (s *SQLiteStore) hourPlanWith(ctx context.Context, q queryRower, tailnetID string, start, end, subdiv int64, planner func(start, end, mark int64) hourPlan) (hourPlan, error) {
	if subdiv > 0 && subdiv < hourSeconds {
		return hourPlan{minutes: [][2]int64{{start, end}}}, nil
	}
	mark, err := readHourMark(ctx, q, tailnetID)
	if err != nil {
		return hourPlan{}, err
	}
	plan := planner(start, end, mark)
	if !plan.useHours() {
		return plan, nil
	}
	plan.legacy, err = readHourLegacy(ctx, q, tailnetID)
	if err != nil {
		return hourPlan{}, err
	}
	return plan, nil
}

func combineHourPlans(ranges [][2]int64, mark int64, allowHours bool) hourPlan {
	if !allowHours {
		return hourPlan{minutes: append([][2]int64(nil), ranges...)}
	}
	var plan hourPlan
	for _, span := range ranges {
		one := planHours(span[0], span[1], mark)
		plan.minutes = append(plan.minutes, one.minutes...)
		plan.hours = append(plan.hours, one.hours...)
	}
	return plan
}

// hourRollupNewerMinSQL is the startup probe for one tailnet. The primary key
// starts with (tailnet_id, bucket), so a covered tailnet is a seek.
const hourRollupNewerMinSQL = `
	SELECT MIN(bucket) FROM node_pairs
	WHERE tailnet_id = ? AND bucket > ? AND bucket < ?
`

func (s *SQLiteStore) ensureHourRollupSchema(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS node_pair_hours (
			tailnet_id TEXT NOT NULL,
			bucket INTEGER NOT NULL,
			src_node_id TEXT NOT NULL,
			dst_node_id TEXT NOT NULL,
			traffic_type TEXT NOT NULL,
			tx_bytes INTEGER,
			rx_bytes INTEGER,
			tx_pkts INTEGER,
			rx_pkts INTEGER,
			flow_count INTEGER,
			protocols TEXT DEFAULT '[]',
			protocol_bytes TEXT DEFAULT '{}',
			ports TEXT DEFAULT '[]',
			tx_ports TEXT DEFAULT '[]',
			rx_ports TEXT DEFAULT '[]',
			tx_protocol_bytes TEXT DEFAULT '{}',
			rx_protocol_bytes TEXT DEFAULT '{}',
			directional_ports INTEGER NOT NULL DEFAULT 0,
			min_bucket INTEGER NOT NULL,
			PRIMARY KEY (tailnet_id, bucket, src_node_id, dst_node_id, traffic_type)
		) WITHOUT ROWID
	`)
	if err != nil {
		return fmt.Errorf("failed to create node_pair_hours: %w", err)
	}
	for _, index := range []string{
		`CREATE INDEX IF NOT EXISTS idx_node_pair_hours_src ON node_pair_hours(tailnet_id, src_node_id, bucket)`,
		`CREATE INDEX IF NOT EXISTS idx_node_pair_hours_dst ON node_pair_hours(tailnet_id, dst_node_id, bucket)`,
	} {
		if _, err := s.db.ExecContext(ctx, index); err != nil {
			return fmt.Errorf("failed to create node_pair_hours index: %w", err)
		}
	}
	return nil
}

func readHourMark(ctx context.Context, q queryRower, tailnetID string) (int64, error) {
	var bucket int64
	err := q.QueryRowContext(ctx, `
		SELECT hour_rollup_bucket FROM backfill_state WHERE tailnet_id = ?
	`, tailnetID).Scan(&bucket)
	if err == sql.ErrNoRows {
		return -1, nil
	}
	if err != nil {
		return 0, err
	}
	return bucket, nil
}

func readHourLegacy(ctx context.Context, q queryRower, tailnetID string) (bool, error) {
	var legacy int
	err := q.QueryRowContext(ctx, `
		SELECT hour_rollup_legacy FROM backfill_state WHERE tailnet_id = ?
	`, tailnetID).Scan(&legacy)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return legacy != 0, nil
}

func setHourMark(ctx context.Context, exec sqlExecer, tailnetID string, bucket int64) error {
	_, err := exec.ExecContext(ctx, `
		INSERT INTO backfill_state (tailnet_id, hour_rollup_bucket, completed_at)
		VALUES (?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(tailnet_id) DO UPDATE SET
			hour_rollup_bucket = excluded.hour_rollup_bucket,
			completed_at = CURRENT_TIMESTAMP
		WHERE excluded.hour_rollup_bucket > backfill_state.hour_rollup_bucket
	`, tailnetID, bucket)
	if err != nil {
		return fmt.Errorf("failed to record hourly rollup mark for %q: %w", tailnetID, err)
	}
	return nil
}

func setHourLegacy(ctx context.Context, exec sqlExecer, tailnetID string) error {
	_, err := exec.ExecContext(ctx, `
		INSERT INTO backfill_state (tailnet_id, hour_rollup_legacy, completed_at)
		VALUES (?, 1, CURRENT_TIMESTAMP)
		ON CONFLICT(tailnet_id) DO UPDATE SET hour_rollup_legacy = 1
	`, tailnetID)
	if err != nil {
		return fmt.Errorf("failed to record legacy hourly rollup rows for %q: %w", tailnetID, err)
	}
	return nil
}

// backfillHourRollups merges closed minute rows into node_pair_hours.
// Each tailnet stores the newest minute the pass has covered. A later start
// seeks past that minute and returns when nothing newer is closed. The mark
// moves forward in the same transaction as the merge, so a crash resumes
// from the previous minute.
func (s *SQLiteStore) backfillHourRollups(ctx context.Context) error {
	exists, err := s.tableExists(ctx, "node_pairs")
	if err != nil || !exists {
		return err
	}
	tailnets, err := s.protocolBackfillTailnetIDs(ctx)
	if err != nil {
		return err
	}
	if len(tailnets) == 0 {
		return nil
	}
	closedThrough := floorMinute(time.Now().UTC().Unix())
	for _, tailnetID := range tailnets {
		if err := s.backfillHourRollupTailnet(ctx, tailnetID, closedThrough); err != nil {
			return err
		}
	}
	return nil
}

func (s *SQLiteStore) backfillHourRollupTailnet(ctx context.Context, tailnetID string, closedThrough int64) error {
	unlock := s.lockTailnet(tailnetID)
	defer unlock()
	var wrote int64
	merged := false
	for {
		tx, err := s.beginWrite(ctx, tailnetID)
		if err != nil {
			return fmt.Errorf("failed to begin hourly rollup backfill: %w", err)
		}
		n, done, err := rollNextHourChunk(ctx, tx, tailnetID, closedThrough)
		if err != nil {
			tx.Rollback()
			return err
		}
		if err := s.commitWrite(tx, tailnetID); err != nil {
			return fmt.Errorf("failed to commit hourly rollup backfill: %w", err)
		}
		wrote += n
		if n > 0 {
			merged = true
		}
		if done {
			break
		}
	}
	if merged {
		s.hourRollupBackfillScans.Add(1)
		log.Printf("Rolled %d hourly node pair rows for tailnet %s", wrote, tailnetID)
	}
	return nil
}

// rollClosedMinutes merges minute rows older than closedThrough into the
// hourly rollup. closedThrough is exclusive and should be minute-aligned.
// The open minute at the cursor stays in node_pairs.
func rollClosedMinutes(ctx context.Context, tx *sql.Tx, tailnetID string, closedThrough int64) error {
	for {
		_, done, err := rollNextHourChunk(ctx, tx, tailnetID, closedThrough)
		if err != nil {
			return err
		}
		if done {
			return nil
		}
	}
}

func rollNextHourChunk(ctx context.Context, tx *sql.Tx, tailnetID string, closedThrough int64) (int64, bool, error) {
	closedThrough = floorMinute(closedThrough)
	if closedThrough <= 0 {
		return 0, true, nil
	}
	mark, err := readHourMark(ctx, tx, tailnetID)
	if err != nil {
		return 0, false, err
	}
	if closedThrough <= mark+minuteSeconds {
		return 0, true, nil
	}
	var next sql.NullInt64
	if err := tx.QueryRowContext(ctx, hourRollupNewerMinSQL, tailnetID, mark, closedThrough).Scan(&next); err != nil {
		return 0, false, err
	}
	if !next.Valid {
		// No minute has been merged yet and none are waiting. Leave the mark
		// alone so a later insert of older rows is still eligible. Once a mark
		// exists, advance it across the empty tail so the next probe starts
		// at the new cursor.
		if mark < 0 {
			return 0, true, nil
		}
		if err := setHourMark(ctx, tx, tailnetID, closedThrough-minuteSeconds); err != nil {
			return 0, false, err
		}
		return 0, true, nil
	}
	hour := (next.Int64 / hourSeconds) * hourSeconds
	chunkEnd := hour + hourSeconds
	if chunkEnd > closedThrough {
		chunkEnd = closedThrough
	}
	if chunkEnd <= next.Int64 {
		return 0, false, fmt.Errorf("hour rollup chunk did not advance past bucket %d", next.Int64)
	}
	wrote, legacy, err := mergeMinutesIntoHour(ctx, tx, tailnetID, hour, mark, chunkEnd)
	if err != nil {
		return 0, false, err
	}
	newMark := chunkEnd - minuteSeconds
	if newMark < next.Int64 {
		newMark = next.Int64
	}
	if newMark <= mark {
		return 0, false, fmt.Errorf("hour rollup mark did not advance past %d", mark)
	}
	if err := setHourMark(ctx, tx, tailnetID, newMark); err != nil {
		return 0, false, err
	}
	if legacy {
		if err := setHourLegacy(ctx, tx, tailnetID); err != nil {
			return 0, false, err
		}
	}
	return wrote, newMark+minuteSeconds >= closedThrough, nil
}

const hourSeedSQL = `
SELECT min_bucket, src_node_id, dst_node_id, traffic_type,
       tx_bytes, rx_bytes, tx_pkts, rx_pkts, flow_count,
       protocol_bytes, ports,
       tx_protocol_bytes, rx_protocol_bytes,
       tx_ports, rx_ports,
       directional_ports
FROM node_pair_hours
WHERE tailnet_id = ? AND bucket = ?
`

const hourMinuteSQL = `
SELECT bucket, src_node_id, dst_node_id, traffic_type,
       tx_bytes, rx_bytes, tx_pkts, rx_pkts, flow_count,
       protocols, protocol_bytes, ports,
       tx_protocol_bytes, rx_protocol_bytes,
       tx_ports, rx_ports,
       directional_ports
FROM node_pairs
WHERE tailnet_id = ? AND bucket > ? AND bucket < ?
`

func mergeMinutesIntoHour(ctx context.Context, tx *sql.Tx, tailnetID string, hour, fromExclusive, toExclusive int64) (int64, bool, error) {
	grouped := make(map[pairGroupKey]*pairGroup)
	seed, err := tx.QueryContext(ctx, hourSeedSQL, tailnetID, hour)
	if err != nil {
		return 0, false, err
	}
	if err := readPairRows(seed, grouped, false); err != nil {
		return 0, false, err
	}
	rows, err := tx.QueryContext(ctx, hourMinuteSQL, tailnetID, fromExclusive, toExclusive)
	if err != nil {
		return 0, false, err
	}
	legacy, err := readMinuteRows(rows, grouped)
	if err != nil {
		return 0, false, err
	}
	wrote, err := writeDirtyHourGroups(ctx, tx, tailnetID, hour, grouped)
	if err != nil {
		return 0, false, err
	}
	return wrote, legacy, nil
}

func readMinuteRows(rows *sql.Rows, grouped map[pairGroupKey]*pairGroup) (bool, error) {
	defer rows.Close()
	legacy := false
	for rows.Next() {
		var bucket, tx, rx, txPkts, rxPkts, flows, directional sql.NullInt64
		var src, dst, traffic string
		var protocols, protocolBytes, ports, txProto, rxProto, txPorts, rxPorts sql.NullString
		if err := rows.Scan(
			&bucket, &src, &dst, &traffic,
			&tx, &rx, &txPkts, &rxPkts, &flows,
			&protocols, &protocolBytes, &ports,
			&txProto, &rxProto,
			&txPorts, &rxPorts,
			&directional,
		); err != nil {
			return false, fmt.Errorf("failed to scan node pair: %w", err)
		}
		if rowNeedsProtocolFallback(protocolBytes, protocols) {
			legacy = true
		}
		key := pairGroupKey{src: src, dst: dst, traffic: traffic}
		group := grouped[key]
		if group == nil {
			group = newPairGroup()
			grouped[key] = group
		}
		if err := group.add(bucket, tx, rx, txPkts, rxPkts, flows, directional, protocolBytes, ports, txProto, rxProto, txPorts, rxPorts); err != nil {
			return false, fmt.Errorf("failed to scan node pair: %w", err)
		}
		group.dirty = true
	}
	if err := rows.Err(); err != nil {
		return false, fmt.Errorf("failed to query node pairs: %w", err)
	}
	return legacy, nil
}

func rowNeedsProtocolFallback(protocolBytes, protocols sql.NullString) bool {
	protoText := strings.TrimSpace(nullString(protocols))
	if protoText == "" || protoText == "[]" {
		return false
	}
	raw := strings.TrimSpace(nullString(protocolBytes))
	if raw == "" || raw == "{}" || !json.Valid([]byte(raw)) {
		return true
	}
	return false
}

const upsertHourSQL = `
INSERT INTO node_pair_hours (
	tailnet_id, bucket, src_node_id, dst_node_id, traffic_type,
	tx_bytes, rx_bytes, tx_pkts, rx_pkts, flow_count,
	protocols, protocol_bytes, ports,
	tx_ports, rx_ports, tx_protocol_bytes, rx_protocol_bytes,
	directional_ports, min_bucket
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(tailnet_id, bucket, src_node_id, dst_node_id, traffic_type) DO UPDATE SET
	tx_bytes = excluded.tx_bytes,
	rx_bytes = excluded.rx_bytes,
	tx_pkts = excluded.tx_pkts,
	rx_pkts = excluded.rx_pkts,
	flow_count = excluded.flow_count,
	protocols = excluded.protocols,
	protocol_bytes = excluded.protocol_bytes,
	ports = excluded.ports,
	tx_ports = excluded.tx_ports,
	rx_ports = excluded.rx_ports,
	tx_protocol_bytes = excluded.tx_protocol_bytes,
	rx_protocol_bytes = excluded.rx_protocol_bytes,
	directional_ports = excluded.directional_ports,
	min_bucket = excluded.min_bucket
`

func writeDirtyHourGroups(ctx context.Context, tx *sql.Tx, tailnetID string, hour int64, grouped map[pairGroupKey]*pairGroup) (int64, error) {
	var dirty int
	for _, group := range grouped {
		if group.dirty {
			dirty++
		}
	}
	if dirty == 0 {
		return 0, nil
	}
	stmt, err := tx.PrepareContext(ctx, upsertHourSQL)
	if err != nil {
		return 0, fmt.Errorf("failed to prepare hourly node pair upsert: %w", err)
	}
	defer stmt.Close()
	var wrote int64
	for key, group := range grouped {
		if !group.dirty {
			continue
		}
		if err := execHourGroup(ctx, stmt, tailnetID, hour, key, group); err != nil {
			return 0, err
		}
		wrote++
	}
	return wrote, nil
}

func execHourGroup(ctx context.Context, stmt *sql.Stmt, tailnetID string, hour int64, key pairGroupKey, group *pairGroup) error {
	if !group.hasBucket {
		return fmt.Errorf("hourly rollup for %s -> %s has no minute bucket", key.src, key.dst)
	}
	_, err := stmt.ExecContext(ctx,
		tailnetID, hour, key.src, key.dst, key.traffic,
		sumArg(group.tx), sumArg(group.rx), sumArg(group.txPkts), sumArg(group.rxPkts), sumArg(group.flows),
		formatProtocols(group.protocols), formatProtocolBytes(group.protocols), formatPortsLimit(group.ports, 0),
		formatPortsLimit(group.txPorts, 0), formatPortsLimit(group.rxPorts, 0),
		formatProtocolBytes(group.txProto), formatProtocolBytes(group.rxProto),
		group.directional, group.bucket,
	)
	if err != nil {
		return fmt.Errorf("failed to write hourly node pair: %w", err)
	}
	return nil
}

func sumArg(state sumState) any {
	if !state.numeric {
		return nil
	}
	return state.n
}

// mergeHourDelta adds one already-written minute delta onto the hourly row.
// The minute bucket is at or behind the high-water mark, so the rollup would
// otherwise miss it.
func mergeHourDelta(ctx context.Context, tx *sql.Tx, tailnetID string, minuteBucket int64, values hourDelta) error {
	hour := (minuteBucket / hourSeconds) * hourSeconds
	grouped := make(map[pairGroupKey]*pairGroup)
	rows, err := tx.QueryContext(ctx, `
		SELECT min_bucket, src_node_id, dst_node_id, traffic_type,
		       tx_bytes, rx_bytes, tx_pkts, rx_pkts, flow_count,
		       protocol_bytes, ports,
		       tx_protocol_bytes, rx_protocol_bytes,
		       tx_ports, rx_ports,
		       directional_ports
		FROM node_pair_hours
		WHERE tailnet_id = ? AND bucket = ? AND src_node_id = ? AND dst_node_id = ? AND traffic_type = ?
	`, tailnetID, hour, values.src, values.dst, values.traffic)
	if err != nil {
		return err
	}
	if err := readPairRows(rows, grouped, false); err != nil {
		return err
	}
	key := pairGroupKey{src: values.src, dst: values.dst, traffic: values.traffic}
	group := grouped[key]
	if group == nil {
		group = newPairGroup()
		grouped[key] = group
	}
	if err := group.add(
		sql.NullInt64{Int64: minuteBucket, Valid: true},
		nullInt(values.tx), nullInt(values.rx), nullInt(values.txPkts), nullInt(values.rxPkts), nullInt(values.flows),
		sql.NullInt64{Int64: values.directional, Valid: true},
		nullText(values.protocolBytes), nullText(values.ports),
		nullText(values.txProto), nullText(values.rxProto),
		nullText(values.txPorts), nullText(values.rxPorts),
	); err != nil {
		return err
	}
	group.dirty = true
	if rowNeedsProtocolFallback(nullText(values.protocolBytes), nullText(values.protocols)) {
		if err := setHourLegacy(ctx, tx, tailnetID); err != nil {
			return err
		}
	}
	_, err = writeDirtyHourGroups(ctx, tx, tailnetID, hour, grouped)
	return err
}

type hourDelta struct {
	src, dst, traffic                          string
	tx, rx, txPkts, rxPkts, flows, directional int64
	protocols, protocolBytes, ports            string
	txPorts, rxPorts, txProto, rxProto         string
}

func nullInt(n int64) sql.NullInt64 {
	return sql.NullInt64{Int64: n, Valid: true}
}

func nullText(s string) sql.NullString {
	return sql.NullString{String: s, Valid: true}
}

// pruneHourRollups deletes hourly rows whose minutes retention already
// removed, and rebuilds an hour that the cutoff splits so the surviving
// minutes still match a minute scan.
func pruneHourRollups(ctx context.Context, tx *sql.Tx, tailnetID string, cutoff int64) (int64, error) {
	res, err := tx.ExecContext(ctx, `
		DELETE FROM node_pair_hours
		WHERE tailnet_id = ? AND bucket + ? <= ?
	`, tailnetID, hourSeconds, cutoff)
	if err != nil {
		return 0, fmt.Errorf("failed to cleanup node_pair_hours: %w", err)
	}
	deleted, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("failed to count cleanup rows for node_pair_hours: %w", err)
	}
	boundary := (cutoff / hourSeconds) * hourSeconds
	if cutoff <= boundary {
		return deleted, nil
	}
	res, err = tx.ExecContext(ctx, `
		DELETE FROM node_pair_hours WHERE tailnet_id = ? AND bucket = ?
	`, tailnetID, boundary)
	if err != nil {
		return 0, fmt.Errorf("failed to cleanup node_pair_hours: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("failed to count cleanup rows for node_pair_hours: %w", err)
	}
	deleted += n

	mark, err := readHourMark(ctx, tx, tailnetID)
	if err != nil {
		return 0, err
	}
	if mark < cutoff {
		return deleted, nil
	}
	chunkEnd := boundary + hourSeconds
	if mark+minuteSeconds < chunkEnd {
		chunkEnd = mark + minuteSeconds
	}
	if _, _, err := mergeMinutesIntoHour(ctx, tx, tailnetID, boundary, cutoff-1, chunkEnd); err != nil {
		return 0, err
	}
	return deleted, nil
}

// unionPairRows stacks minute edges and hourly middles. minuteCols and
// hourCols are the select lists. extra is appended to both WHERE clauses
// and extraArgs is bound once per branch.
func (p hourPlan) unionPairRows(tailnetID, minuteCols, hourCols, extra string, extraArgs []any) (string, []any) {
	var parts []string
	var args []any
	if len(p.minutes) > 0 {
		pred, predArgs := bucketRangePredicate("bucket", p.minutes)
		parts = append(parts, fmt.Sprintf("SELECT %s FROM node_pairs WHERE tailnet_id = ? AND %s%s", minuteCols, pred, extra))
		args = append(args, tailnetID)
		args = append(args, predArgs...)
		args = append(args, extraArgs...)
	}
	if len(p.hours) > 0 {
		pred, predArgs := bucketRangePredicate("bucket", p.hours)
		parts = append(parts, fmt.Sprintf("SELECT %s FROM node_pair_hours WHERE tailnet_id = ? AND %s%s", hourCols, pred, extra))
		args = append(args, tailnetID)
		args = append(args, predArgs...)
		args = append(args, extraArgs...)
	}
	if len(parts) == 0 {
		return "", nil
	}
	if len(parts) == 1 {
		return parts[0], args
	}
	return strings.Join(parts, " UNION ALL "), args
}

func (p hourPlan) fallbackRanges() [][2]int64 {
	out := append([][2]int64(nil), p.minutes...)
	if p.legacy {
		out = append(out, p.hours...)
	}
	return out
}
