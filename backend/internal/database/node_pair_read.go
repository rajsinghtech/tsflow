package database

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

// nodePairScanSQL reads one tailnet's minute rows for a half-open window.
// JSON columns are merged in process so the read is one pass instead of a
// correlated json_each per pair per column.
const nodePairScanSQL = `
SELECT bucket, src_node_id, dst_node_id, traffic_type,
       tx_bytes, rx_bytes, tx_pkts, rx_pkts, flow_count,
       protocol_bytes, ports,
       tx_protocol_bytes, rx_protocol_bytes,
       tx_ports, rx_ports,
       directional_ports
FROM node_pairs
WHERE tailnet_id = ? AND bucket >= ? AND bucket < ?
`

func nodePairBounds(start, end time.Time) (int64, int64, error) {
	startUnix := start.UTC().Unix()
	endUnix := end.UTC().Unix()
	if startUnix >= endUnix {
		return 0, 0, fmt.Errorf("invalid time range: start (%v) must be before end (%v)", start, end)
	}
	return startUnix, endUnix, nil
}

// nodePairHourScanSQL reads hourly rollup rows. min_bucket is the earliest
// minute that contributed, which is what the graph aggregate reports.
const nodePairHourScanSQL = `
SELECT min_bucket, src_node_id, dst_node_id, traffic_type,
       tx_bytes, rx_bytes, tx_pkts, rx_pkts, flow_count,
       protocol_bytes, ports,
       tx_protocol_bytes, rx_protocol_bytes,
       tx_ports, rx_ports,
       directional_ports
FROM node_pair_hours
WHERE tailnet_id = ? AND bucket >= ? AND bucket < ?
`

// GetNodePairAggregates retrieves node-pair aggregates for a time range.
//
// Hours that sit entirely inside the window and are already rolled up are
// read from node_pair_hours. The partial hour at each end is read from
// minute rows. A window with no complete rolled hour is one scan of
// node_pairs, which is the previous query.
//
// The read uses the read pool and does not take a tailnet lock. A poll
// commit uses the writer connection, and WAL keeps this read on one snapshot.
func (s *SQLiteStore) GetNodePairAggregates(ctx context.Context, tailnetID string, start, end time.Time) ([]NodePairAggregate, error) {
	if err := checkTailnetID(tailnetID); err != nil {
		return nil, err
	}
	startUnix, endUnix, err := nodePairBounds(start, end)
	if err != nil {
		return nil, err
	}
	if s.nodePairReadHook != nil {
		s.nodePairReadHook()
	}
	aggs, err := s.readNodePairAggregates(ctx, tailnetID, startUnix, endUnix, true)
	if errors.Is(err, errSnapshotMoved) {
		s.openReadFallbacks.Add(1)
		return s.readNodePairAggregates(ctx, tailnetID, startUnix, endUnix, false)
	}
	return aggs, err
}

// readNodePairAggregates plans the window on one snapshot and reads it.
//
// Closed hours and closed minutes (at or before the mark) are read after
// that snapshot, in parallel. Only a late write can change them, and it
// updates the minute row and the hour row in one commit. Each bucket is read
// from exactly one of the two tables, so a newer snapshot cannot double
// count.
//
// The filling hour and the minutes after the mark must be read on the
// planning snapshot, because a moving mark shifts rows between the tables.
// With parallelOpen, and no commit for the tailnet in flight, they are read
// in parallel on separate snapshots instead, and the result is kept only if
// no commit for the tailnet started before the last of them was read: then
// every snapshot holds the same rows. Otherwise it returns errSnapshotMoved
// and the caller reads again with the open part on the planning snapshot.
func (s *SQLiteStore) readNodePairAggregates(ctx context.Context, tailnetID string, startUnix, endUnix int64, parallelOpen bool) ([]NodePairAggregate, error) {
	seq := s.commitSeqFor(tailnetID)
	since, quiet := seq.quiet()
	parallelOpen = parallelOpen && quiet

	tx, err := s.beginRead(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	plan, err := s.snapshotHourPlan(ctx, tx, tailnetID, startUnix, endUnix)
	if err != nil {
		return nil, err
	}
	closedHourRanges, openHours := splitClosedHours(plan.hours, plan.mark)
	closedHours, err := listRolledHours(ctx, tx, tailnetID, closedHourRanges)
	if err != nil {
		return nil, err
	}
	closedMinutes, openMinutes := splitClosedMinutes(plan.minutes, plan.mark)
	spans := closedSpans(closedHours, closedMinutes)
	grouped := make(map[pairGroupKey]*pairGroup)
	var stable func() bool
	if parallelOpen {
		// Bound the minutes on this snapshot; the stable check covers it.
		clamped, err := clampMinuteSpans(ctx, tx, tailnetID, openMinutes)
		if err != nil {
			return nil, err
		}
		spans = append(spans, openSpans(openHours, clamped)...)
		stable = func() bool { return seq.unchangedSince(since) }
	} else if err := collectPairGroups(ctx, tx, tailnetID, hourPlan{minutes: openMinutes, hours: openHours}, grouped); err != nil {
		return nil, err
	}
	if err := tx.Rollback(); err != nil {
		return nil, fmt.Errorf("failed to end read transaction: %w", err)
	}
	if s.closedReadHook != nil {
		s.closedReadHook()
	}
	return s.aggregateWithClosedSpans(ctx, tailnetID, spans, grouped, stable)
}

func collectPairGroups(ctx context.Context, q queryRower, tailnetID string, plan hourPlan, grouped map[pairGroupKey]*pairGroup) error {
	for _, span := range plan.minutes {
		rows, err := q.QueryContext(ctx, nodePairScanSQL, tailnetID, span[0], span[1])
		if err != nil {
			return fmt.Errorf("failed to query node pairs: %w", err)
		}
		if err := readPairRows(rows, grouped, false); err != nil {
			return err
		}
	}
	for _, span := range plan.hours {
		rows, err := q.QueryContext(ctx, nodePairHourScanSQL, tailnetID, span[0], span[1])
		if err != nil {
			return fmt.Errorf("failed to query node pairs: %w", err)
		}
		if err := readPairRows(rows, grouped, false); err != nil {
			return err
		}
	}
	return nil
}

func readPairRows(rows *sql.Rows, grouped map[pairGroupKey]*pairGroup, dirty bool) error {
	defer rows.Close()
	for rows.Next() {
		var bucket, tx, rx, txPkts, rxPkts, flows, directional sql.NullInt64
		var src, dst, traffic string
		var protocolBytes, ports, txProto, rxProto, txPorts, rxPorts sql.NullString
		if err := rows.Scan(
			&bucket, &src, &dst, &traffic,
			&tx, &rx, &txPkts, &rxPkts, &flows,
			&protocolBytes, &ports,
			&txProto, &rxProto,
			&txPorts, &rxPorts,
			&directional,
		); err != nil {
			return fmt.Errorf("failed to scan node pair: %w", err)
		}
		key := pairGroupKey{src: src, dst: dst, traffic: traffic}
		group := grouped[key]
		if group == nil {
			group = newPairGroup()
			grouped[key] = group
		}
		if err := group.add(bucket, tx, rx, txPkts, rxPkts, flows, directional, protocolBytes, ports, txProto, rxProto, txPorts, rxPorts); err != nil {
			return fmt.Errorf("failed to scan node pair: %w", err)
		}
		if dirty {
			group.dirty = true
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("failed to query node pairs: %w", err)
	}
	return nil
}

func sortedPairAggregates(grouped map[pairGroupKey]*pairGroup) ([]NodePairAggregate, error) {
	if len(grouped) == 0 {
		return nil, nil
	}
	aggregates := make([]NodePairAggregate, 0, len(grouped))
	for key, group := range grouped {
		agg, err := group.aggregate(key)
		if err != nil {
			return nil, err
		}
		aggregates = append(aggregates, agg)
	}
	sortPairAggregates(aggregates)
	return aggregates, nil
}

// sortPairAggregates orders by total bytes, then by key.
func sortPairAggregates(aggregates []NodePairAggregate) {
	sort.Slice(aggregates, func(i, j int) bool {
		left := aggregates[i].TxBytes + aggregates[i].RxBytes
		right := aggregates[j].TxBytes + aggregates[j].RxBytes
		if left != right {
			return left > right
		}
		if aggregates[i].SrcNodeID != aggregates[j].SrcNodeID {
			return aggregates[i].SrcNodeID < aggregates[j].SrcNodeID
		}
		if aggregates[i].DstNodeID != aggregates[j].DstNodeID {
			return aggregates[i].DstNodeID < aggregates[j].DstNodeID
		}
		return aggregates[i].TrafficType < aggregates[j].TrafficType
	})
}

type pairGroupKey struct {
	src, dst, traffic string
}

type sumState struct {
	n       int64
	numeric bool
}

func (s *sumState) add(n int64, numeric bool) {
	if !numeric {
		return
	}
	s.n += n
	s.numeric = true
}

// smallSumsLimit is how many keys a sum set scans linearly before it builds
// an index. Most pairs carry one to three protocols and a few ports, and a
// map per set was most of the read's allocations.
const smallSumsLimit = 16

type protoEntry struct {
	key int
	st  sumState
}

type protoSums struct {
	items   []protoEntry
	index   map[int]int // position in items, once len(items) > smallSumsLimit
	nullKey sumState
	hasNull bool
}

func (p *protoSums) add(key int, keyNull bool, n int64, numeric bool) {
	if keyNull {
		p.nullKey.add(n, numeric)
		p.hasNull = true
		return
	}
	// A JSON null value still creates the key, with a null sum until a number arrives.
	p.slot(key).add(n, numeric)
}

func (p *protoSums) slot(key int) *sumState {
	if p.index != nil {
		if i, ok := p.index[key]; ok {
			return &p.items[i].st
		}
	} else {
		for i := range p.items {
			if p.items[i].key == key {
				return &p.items[i].st
			}
		}
	}
	p.items = append(p.items, protoEntry{key: key})
	if p.index != nil {
		p.index[key] = len(p.items) - 1
	} else if len(p.items) > smallSumsLimit {
		p.index = make(map[int]int, len(p.items)*2)
		for i, item := range p.items {
			p.index[item.key] = i
		}
	}
	return &p.items[len(p.items)-1].st
}

type portKey struct {
	portNull  bool
	protoNull bool
	port      int
	proto     int
}

type portEntry struct {
	key portKey
	st  sumState
}

type portSums struct {
	items []portEntry
	index map[portKey]int // position in items, once len(items) > smallSumsLimit
}

func (p *portSums) add(key portKey, n int64, numeric bool) {
	p.slot(key).add(n, numeric)
}

func (p *portSums) slot(key portKey) *sumState {
	if p.index != nil {
		if i, ok := p.index[key]; ok {
			return &p.items[i].st
		}
	} else {
		for i := range p.items {
			if p.items[i].key == key {
				return &p.items[i].st
			}
		}
	}
	p.items = append(p.items, portEntry{key: key})
	if p.index != nil {
		p.index[key] = len(p.items) - 1
	} else if len(p.items) > smallSumsLimit {
		p.index = make(map[portKey]int, len(p.items)*2)
		for i, item := range p.items {
			p.index[item.key] = i
		}
	}
	return &p.items[len(p.items)-1].st
}

type pairGroup struct {
	bucket      int64
	hasBucket   bool
	tx          sumState
	rx          sumState
	txPkts      sumState
	rxPkts      sumState
	flows       sumState
	directional int
	hasDir      bool
	protocols   protoSums
	txProto     protoSums
	rxProto     protoSums
	ports       portSums
	txPorts     portSums
	rxPorts     portSums
	// dirty marks a group that absorbed a minute row during an hourly merge.
	// Seeded hourly rows stay clean so an untouched pair is not rewritten.
	dirty bool
}

func newPairGroup() *pairGroup {
	return &pairGroup{}
}

func (g *pairGroup) add(
	bucket, tx, rx, txPkts, rxPkts, flows, directional sql.NullInt64,
	protocolBytes, ports, txProto, rxProto, txPorts, rxPorts sql.NullString,
) error {
	if bucket.Valid && (!g.hasBucket || bucket.Int64 < g.bucket) {
		g.bucket = bucket.Int64
		g.hasBucket = true
	}
	addNullInt(&g.tx, tx)
	addNullInt(&g.rx, rx)
	addNullInt(&g.txPkts, txPkts)
	addNullInt(&g.rxPkts, rxPkts)
	addNullInt(&g.flows, flows)
	dir := 0
	if directional.Valid {
		dir = int(directional.Int64)
	}
	if !g.hasDir || dir < g.directional {
		g.directional = dir
		g.hasDir = true
	}
	if err := mergeProtocolColumn(&g.protocols, nullString(protocolBytes)); err != nil {
		return err
	}
	if err := mergeProtocolColumn(&g.txProto, nullString(txProto)); err != nil {
		return err
	}
	if err := mergeProtocolColumn(&g.rxProto, nullString(rxProto)); err != nil {
		return err
	}
	if err := mergePortColumn(&g.ports, nullString(ports)); err != nil {
		return err
	}
	if err := mergePortColumn(&g.txPorts, nullString(txPorts)); err != nil {
		return err
	}
	if err := mergePortColumn(&g.rxPorts, nullString(rxPorts)); err != nil {
		return err
	}
	return nil
}

func addNullInt(dst *sumState, v sql.NullInt64) {
	if v.Valid {
		dst.add(v.Int64, true)
	}
}

func nullString(v sql.NullString) string {
	if !v.Valid {
		return ""
	}
	return v.String
}

func (g *pairGroup) aggregate(key pairGroupKey) (NodePairAggregate, error) {
	// SUM of only NULL inputs is NULL. Scanning that into an integer fails,
	// which is what the previous query did.
	if !g.hasBucket || !g.tx.numeric || !g.rx.numeric || !g.txPkts.numeric || !g.rxPkts.numeric || !g.flows.numeric {
		return NodePairAggregate{}, fmt.Errorf("failed to scan node pair: converting NULL to int64 is unsupported")
	}
	return NodePairAggregate{
		Bucket:           g.bucket,
		SrcNodeID:        key.src,
		DstNodeID:        key.dst,
		TrafficType:      key.traffic,
		TxBytes:          g.tx.n,
		RxBytes:          g.rx.n,
		TxPkts:           g.txPkts.n,
		RxPkts:           g.rxPkts.n,
		FlowCount:        g.flows.n,
		Protocols:        formatProtocols(g.protocols),
		ProtocolBytes:    formatProtocolBytes(g.protocols),
		Ports:            formatPorts(g.ports),
		TxProtocolBytes:  formatProtocolBytes(g.txProto),
		RxProtocolBytes:  formatProtocolBytes(g.rxProto),
		TxPorts:          formatPorts(g.txPorts),
		RxPorts:          formatPorts(g.rxPorts),
		DirectionalPorts: g.directional != 0,
	}, nil
}

type protoItem struct {
	keyNull bool
	key     int
	n       int64
	numeric bool
}

func lessProto(a, b protoItem) bool {
	if a.numeric != b.numeric {
		return a.numeric
	}
	if a.numeric && a.n != b.n {
		return a.n > b.n
	}
	if a.keyNull != b.keyNull {
		return a.keyNull
	}
	return a.key < b.key
}

func formatProtocols(sums protoSums) string {
	items := protoItems(sums)
	if len(items) == 0 {
		return "[]"
	}
	sort.Slice(items, func(i, j int) bool { return lessProto(items[i], items[j]) })
	var b strings.Builder
	b.WriteByte('[')
	for i, item := range items {
		if i > 0 {
			b.WriteByte(',')
		}
		if item.keyNull {
			b.WriteString("null")
			continue
		}
		b.WriteString(strconv.Itoa(item.key))
	}
	b.WriteByte(']')
	return b.String()
}

func formatProtocolBytes(sums protoSums) string {
	if len(sums.items) == 0 {
		return "{}"
	}
	items := append([]protoEntry(nil), sums.items...)
	sort.Slice(items, func(i, j int) bool { return items[i].key < items[j].key })
	var b strings.Builder
	b.WriteByte('{')
	for i, item := range items {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteByte('"')
		b.WriteString(strconv.Itoa(item.key))
		b.WriteString(`":`)
		writeSum(&b, item.st)
	}
	b.WriteByte('}')
	return b.String()
}

func protoItems(sums protoSums) []protoItem {
	items := make([]protoItem, 0, len(sums.items)+1)
	for _, item := range sums.items {
		items = append(items, protoItem{key: item.key, n: item.st.n, numeric: item.st.numeric})
	}
	if sums.hasNull {
		items = append(items, protoItem{keyNull: true, n: sums.nullKey.n, numeric: sums.nullKey.numeric})
	}
	return items
}

type portItem struct {
	key     portKey
	n       int64
	numeric bool
}

func formatPorts(sums portSums) string {
	return formatPortsLimit(sums, 20)
}

func formatPortsLimit(sums portSums, limit int) string {
	if len(sums.items) == 0 {
		return "[]"
	}
	items := make([]portItem, 0, len(sums.items))
	for _, item := range sums.items {
		items = append(items, portItem{key: item.key, n: item.st.n, numeric: item.st.numeric})
	}
	sort.Slice(items, func(i, j int) bool {
		a, b := items[i], items[j]
		if a.numeric != b.numeric {
			return a.numeric
		}
		if a.numeric && a.n != b.n {
			return a.n > b.n
		}
		if a.key.protoNull != b.key.protoNull {
			return a.key.protoNull
		}
		if a.key.proto != b.key.proto {
			return a.key.proto < b.key.proto
		}
		if a.key.portNull != b.key.portNull {
			return a.key.portNull
		}
		return a.key.port < b.key.port
	})
	if limit > 0 && len(items) > limit {
		items = items[:limit]
	}
	var b strings.Builder
	b.WriteByte('[')
	for i, item := range items {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(`{"port":`)
		writeMaybeInt(&b, item.key.port, item.key.portNull)
		b.WriteString(`,"proto":`)
		writeMaybeInt(&b, item.key.proto, item.key.protoNull)
		b.WriteString(`,"bytes":`)
		writeSum(&b, sumState{n: item.n, numeric: item.numeric})
		b.WriteByte('}')
	}
	b.WriteByte(']')
	return b.String()
}

func writeSum(b *strings.Builder, state sumState) {
	if !state.numeric {
		b.WriteString("null")
		return
	}
	b.WriteString(strconv.FormatInt(state.n, 10))
}

func writeMaybeInt(b *strings.Builder, n int, isNull bool) {
	if isNull {
		b.WriteString("null")
		return
	}
	b.WriteString(strconv.Itoa(n))
}

func mergeProtocolColumn(dst *protoSums, raw string) error {
	if raw == "" || raw == "{}" {
		return nil
	}
	var simple [8]protoItem
	if n, ok := scanSimpleProtocolObject(raw, &simple); ok {
		for i := 0; i < n; i++ {
			dst.add(simple[i].key, simple[i].keyNull, simple[i].n, simple[i].numeric)
		}
		return nil
	}
	value, ok, err := decodeSQLiteJSON(raw)
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}
	applyProtocolValue(dst, value)
	return nil
}

func mergePortColumn(dst *portSums, raw string) error {
	if raw == "" || raw == "[]" {
		return nil
	}
	var simple [8]portItem
	if n, ok := scanSimplePortArray(raw, &simple); ok {
		for i := 0; i < n; i++ {
			dst.add(simple[i].key, simple[i].n, simple[i].numeric)
		}
		return nil
	}
	value, ok, err := decodeSQLiteJSON(raw)
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}
	applyPortValue(dst, value)
	return nil
}

func applyProtocolValue(dst *protoSums, value sqliteValue) {
	switch value.kind {
	case kindObject:
		for _, field := range value.obj {
			n, numeric := castSQLiteValue(field.val)
			dst.add(int(sqliteAtoi(field.key)), false, n, numeric)
		}
	case kindArray:
		for i, elem := range value.arr {
			n, numeric := castSQLiteValue(elem)
			dst.add(i, false, n, numeric)
		}
	default:
		n, numeric := castSQLiteValue(value)
		dst.add(0, true, n, numeric)
	}
}

func applyPortValue(dst *portSums, value sqliteValue) {
	entries := []sqliteValue{value}
	if value.kind == kindArray || value.kind == kindObject {
		entries = jsonEachValues(value)
	}
	for _, entry := range entries {
		key, n, numeric := portFromValue(entry)
		dst.add(key, n, numeric)
	}
}

func jsonEachValues(value sqliteValue) []sqliteValue {
	if value.kind == kindArray {
		return value.arr
	}
	out := make([]sqliteValue, len(value.obj))
	for i, field := range value.obj {
		out[i] = field.val
	}
	return out
}

func portFromValue(value sqliteValue) (portKey, int64, bool) {
	if value.kind != kindObject {
		return portKey{portNull: true, protoNull: true}, 0, false
	}
	port, portNull := extractField(value, "port")
	proto, protoNull := extractField(value, "proto")
	bytes, bytesNull := extractField(value, "bytes")
	key := portKey{portNull: portNull, protoNull: protoNull, port: int(port), proto: int(proto)}
	if bytesNull {
		return key, 0, false
	}
	return key, bytes, true
}

func extractField(value sqliteValue, name string) (int64, bool) {
	for i := len(value.obj) - 1; i >= 0; i-- {
		if value.obj[i].key != name {
			continue
		}
		n, numeric := castSQLiteValue(value.obj[i].val)
		if !numeric {
			return 0, true
		}
		return n, false
	}
	return 0, true
}

func castSQLiteValue(value sqliteValue) (int64, bool) {
	switch value.kind {
	case kindNull:
		return 0, false
	case kindBool:
		if value.boolv {
			return 1, true
		}
		return 0, true
	case kindNumber:
		return truncNumber(value.num), true
	case kindString:
		return sqliteAtoi(value.str), true
	case kindObject, kindArray:
		return 0, true
	default:
		return 0, false
	}
}

func truncNumber(num json.Number) int64 {
	if i, err := num.Int64(); err == nil {
		return i
	}
	f, err := num.Float64()
	if err != nil {
		return 0
	}
	return int64(f)
}

func sqliteAtoi(s string) int64 {
	i := 0
	for i < len(s) && (s[i] == ' ' || s[i] == '\t' || s[i] == '\n' || s[i] == '\r') {
		i++
	}
	sign := int64(1)
	if i < len(s) && (s[i] == '+' || s[i] == '-') {
		if s[i] == '-' {
			sign = -1
		}
		i++
	}
	n := int64(0)
	digits := false
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		digits = true
		n = n*10 + int64(s[i]-'0')
		i++
	}
	if !digits {
		return 0
	}
	return sign * n
}

func scanSimpleProtocolObject(s string, buf *[8]protoItem) (int, bool) {
	i := skipSpace(s, 0)
	if i >= len(s) || s[i] != '{' {
		return 0, false
	}
	i = skipSpace(s, i+1)
	if i < len(s) && s[i] == '}' {
		return 0, true
	}
	n := 0
	for {
		if n == len(buf) {
			return 0, false
		}
		var key string
		var ok bool
		key, i, ok = scanJSONString(s, i)
		if !ok {
			return 0, false
		}
		i = skipSpace(s, i)
		if i >= len(s) || s[i] != ':' {
			return 0, false
		}
		i = skipSpace(s, i+1)
		item := protoItem{key: int(sqliteAtoi(key))}
		item.n, item.numeric, i, ok = scanSimpleValue(s, i)
		if !ok {
			return 0, false
		}
		buf[n] = item
		n++
		i = skipSpace(s, i)
		if i >= len(s) {
			return 0, false
		}
		if s[i] == '}' {
			i = skipSpace(s, i+1)
			return n, i == len(s)
		}
		if s[i] != ',' {
			return 0, false
		}
		i = skipSpace(s, i+1)
	}
}

func scanSimplePortArray(s string, buf *[8]portItem) (int, bool) {
	i := skipSpace(s, 0)
	if i >= len(s) || s[i] != '[' {
		return 0, false
	}
	i = skipSpace(s, i+1)
	if i < len(s) && s[i] == ']' {
		return 0, true
	}
	n := 0
	for {
		if n == len(buf) {
			return 0, false
		}
		var item portItem
		var ok bool
		item, i, ok = scanSimplePortObject(s, i)
		if !ok {
			return 0, false
		}
		buf[n] = item
		n++
		i = skipSpace(s, i)
		if i >= len(s) {
			return 0, false
		}
		if s[i] == ']' {
			i = skipSpace(s, i+1)
			return n, i == len(s)
		}
		if s[i] != ',' {
			return 0, false
		}
		i = skipSpace(s, i+1)
	}
}

func scanSimplePortObject(s string, i int) (portItem, int, bool) {
	i = skipSpace(s, i)
	if i >= len(s) || s[i] != '{' {
		return portItem{}, i, false
	}
	i = skipSpace(s, i+1)
	item := portItem{key: portKey{portNull: true, protoNull: true}}
	if i < len(s) && s[i] == '}' {
		return item, i + 1, true
	}
	for {
		var key string
		var ok bool
		key, i, ok = scanJSONString(s, i)
		if !ok {
			return portItem{}, i, false
		}
		i = skipSpace(s, i)
		if i >= len(s) || s[i] != ':' {
			return portItem{}, i, false
		}
		i = skipSpace(s, i+1)
		n, numeric, next, ok := scanSimpleValue(s, i)
		if !ok {
			return portItem{}, i, false
		}
		i = next
		switch key {
		case "port":
			if !numeric {
				item.key.portNull = true
			} else {
				item.key.portNull = false
				item.key.port = int(n)
			}
		case "proto":
			if !numeric {
				item.key.protoNull = true
			} else {
				item.key.protoNull = false
				item.key.proto = int(n)
			}
		case "bytes":
			item.n = n
			item.numeric = numeric
		}
		i = skipSpace(s, i)
		if i >= len(s) {
			return portItem{}, i, false
		}
		if s[i] == '}' {
			return item, i + 1, true
		}
		if s[i] != ',' {
			return portItem{}, i, false
		}
		i = skipSpace(s, i+1)
	}
}

func scanSimpleValue(s string, i int) (int64, bool, int, bool) {
	if i >= len(s) {
		return 0, false, i, false
	}
	switch s[i] {
	case '"':
		text, next, ok := scanJSONString(s, i)
		if !ok {
			return 0, false, i, false
		}
		return sqliteAtoi(text), true, next, true
	case 't':
		if strings.HasPrefix(s[i:], "true") {
			return 1, true, i + 4, true
		}
	case 'f':
		if strings.HasPrefix(s[i:], "false") {
			return 0, true, i + 5, true
		}
	case 'n':
		if strings.HasPrefix(s[i:], "null") {
			return 0, false, i + 4, true
		}
	case '-', '0', '1', '2', '3', '4', '5', '6', '7', '8', '9':
		return scanJSONNumber(s, i)
	}
	return 0, false, i, false
}

func scanJSONNumber(s string, i int) (int64, bool, int, bool) {
	start := i
	if s[i] == '-' {
		i++
	}
	if i >= len(s) || s[i] < '0' || s[i] > '9' {
		return 0, false, start, false
	}
	if s[i] == '0' {
		i++
	} else {
		for i < len(s) && s[i] >= '0' && s[i] <= '9' {
			i++
		}
	}
	frac := false
	if i < len(s) && s[i] == '.' {
		frac = true
		i++
		if i >= len(s) || s[i] < '0' || s[i] > '9' {
			return 0, false, start, false
		}
		for i < len(s) && s[i] >= '0' && s[i] <= '9' {
			i++
		}
	}
	if i < len(s) && (s[i] == 'e' || s[i] == 'E') {
		frac = true
		i++
		if i < len(s) && (s[i] == '+' || s[i] == '-') {
			i++
		}
		if i >= len(s) || s[i] < '0' || s[i] > '9' {
			return 0, false, start, false
		}
		for i < len(s) && s[i] >= '0' && s[i] <= '9' {
			i++
		}
	}
	if !frac {
		n, err := strconv.ParseInt(s[start:i], 10, 64)
		if err != nil {
			return 0, false, start, false
		}
		return n, true, i, true
	}
	f, err := strconv.ParseFloat(s[start:i], 64)
	if err != nil {
		return 0, false, start, false
	}
	return int64(f), true, i, true
}

func scanJSONString(s string, i int) (string, int, bool) {
	i = skipSpace(s, i)
	if i >= len(s) || s[i] != '"' {
		return "", i, false
	}
	i++
	start := i
	for i < len(s) {
		if s[i] == '\\' {
			return "", i, false
		}
		if s[i] == '"' {
			return s[start:i], i + 1, true
		}
		i++
	}
	return "", i, false
}

func skipSpace(s string, i int) int {
	for i < len(s) {
		switch s[i] {
		case ' ', '\t', '\n', '\r':
			i++
		default:
			return i
		}
	}
	return i
}

type sqliteKind int

const (
	kindNull sqliteKind = iota
	kindBool
	kindNumber
	kindString
	kindObject
	kindArray
)

type sqliteField struct {
	key string
	val sqliteValue
}

type sqliteValue struct {
	kind  sqliteKind
	boolv bool
	num   json.Number
	str   string
	obj   []sqliteField
	arr   []sqliteValue
}

func decodeSQLiteJSON(s string) (sqliteValue, bool, error) {
	dec := json.NewDecoder(strings.NewReader(s))
	dec.UseNumber()
	value, err := decodeSQLiteValue(dec)
	if err != nil {
		return sqliteValue{}, false, nil
	}
	if dec.More() {
		return sqliteValue{}, false, nil
	}
	return value, true, nil
}

func decodeSQLiteValue(dec *json.Decoder) (sqliteValue, error) {
	tok, err := dec.Token()
	if err != nil {
		return sqliteValue{}, err
	}
	switch t := tok.(type) {
	case json.Delim:
		if t == '{' {
			var fields []sqliteField
			for dec.More() {
				keyTok, err := dec.Token()
				if err != nil {
					return sqliteValue{}, err
				}
				key, ok := keyTok.(string)
				if !ok {
					return sqliteValue{}, fmt.Errorf("json object key %T", keyTok)
				}
				val, err := decodeSQLiteValue(dec)
				if err != nil {
					return sqliteValue{}, err
				}
				fields = append(fields, sqliteField{key: key, val: val})
			}
			end, err := dec.Token()
			if err != nil {
				return sqliteValue{}, err
			}
			if end != json.Delim('}') {
				return sqliteValue{}, fmt.Errorf("json object end %v", end)
			}
			return sqliteValue{kind: kindObject, obj: fields}, nil
		}
		if t == '[' {
			var items []sqliteValue
			for dec.More() {
				item, err := decodeSQLiteValue(dec)
				if err != nil {
					return sqliteValue{}, err
				}
				items = append(items, item)
			}
			end, err := dec.Token()
			if err != nil {
				return sqliteValue{}, err
			}
			if end != json.Delim(']') {
				return sqliteValue{}, fmt.Errorf("json array end %v", end)
			}
			return sqliteValue{kind: kindArray, arr: items}, nil
		}
		return sqliteValue{}, fmt.Errorf("json delim %v", t)
	case json.Number:
		return sqliteValue{kind: kindNumber, num: t}, nil
	case string:
		return sqliteValue{kind: kindString, str: t}, nil
	case bool:
		return sqliteValue{kind: kindBool, boolv: t}, nil
	case nil:
		return sqliteValue{kind: kindNull}, nil
	default:
		return sqliteValue{}, fmt.Errorf("json token %T", tok)
	}
}
