package database

import (
	"context"
	"database/sql"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestSplitClosedHours(t *testing.T) {
	const h = hourSeconds
	mark := 4*h + 29*60 // rolled through 4:29
	closed, open := splitClosedHours([][2]int64{{h, 4*h + 30*60}}, mark)
	if want := [][2]int64{{h, 4 * h}}; !reflect.DeepEqual(closed, want) {
		t.Fatalf("closed = %v, want %v", closed, want)
	}
	if want := [][2]int64{{4 * h, 4*h + 30*60}}; !reflect.DeepEqual(open, want) {
		t.Fatalf("open = %v, want %v", open, want)
	}
	closed, open = splitClosedHours([][2]int64{{h, 3 * h}}, mark)
	if want := [][2]int64{{h, 3 * h}}; !reflect.DeepEqual(closed, want) || open != nil {
		t.Fatalf("all closed: closed=%v open=%v", closed, open)
	}
	closed, open = splitClosedHours([][2]int64{{h, 3 * h}}, -1)
	if closed != nil || !reflect.DeepEqual(open, [][2]int64{{h, 3 * h}}) {
		t.Fatalf("no mark: closed=%v open=%v", closed, open)
	}
	// An hour-aligned mark closes the hour it ends.
	closed, open = splitClosedHours([][2]int64{{h, 3 * h}}, 3*h-60)
	if want := [][2]int64{{h, 3 * h}}; !reflect.DeepEqual(closed, want) || open != nil {
		t.Fatalf("aligned mark: closed=%v open=%v", closed, open)
	}
	// A mark inside the first hour closes nothing.
	closed, open = splitClosedHours([][2]int64{{h, 3 * h}}, h+60)
	if closed != nil || !reflect.DeepEqual(open, [][2]int64{{h, 3 * h}}) {
		t.Fatalf("early mark: closed=%v open=%v", closed, open)
	}
}

// Only hours with rollup rows become work items, so a window from the Unix
// epoch costs one read per stored hour, not one per hour since 1970.
func TestListRolledHoursSkipsEmptyHours(t *testing.T) {
	store := setupTestDB(t)
	ctx := context.Background()
	const h = hourSeconds
	base := int64(1_790_000_000) / h * h
	stored := []int64{base, base + 3*h, base + 4*h, base + 50*h}
	for _, hour := range stored {
		insertHourRow(t, store, DefaultTailnetID, hour)
	}
	insertHourRow(t, store, "other", base+h)

	got, err := listRolledHours(ctx, store.db, DefaultTailnetID, [][2]int64{{0, base + 100*h}})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, stored) {
		t.Fatalf("hours = %v, want %v", got, stored)
	}
	got, err = listRolledHours(ctx, store.db, DefaultTailnetID, [][2]int64{{base + h, base + 4*h}, {base + 5*h, base + 51*h}})
	if err != nil {
		t.Fatal(err)
	}
	if want := []int64{base + 3*h, base + 50*h}; !reflect.DeepEqual(got, want) {
		t.Fatalf("bounded hours = %v, want %v", got, want)
	}
	got, err = listRolledHours(ctx, store.db, DefaultTailnetID, [][2]int64{{base + 5*h, base + 50*h}})
	if err != nil || got != nil {
		t.Fatalf("empty range = %v, %v", got, err)
	}

	plan, err := store.db.Query("EXPLAIN QUERY PLAN "+rolledHourSeekSQL, DefaultTailnetID, 0, base)
	if err != nil {
		t.Fatal(err)
	}
	var detail string
	for plan.Next() {
		var id, parent, unused int
		var d string
		if err := plan.Scan(&id, &parent, &unused, &d); err != nil {
			t.Fatal(err)
		}
		detail += d + ";"
	}
	plan.Close()
	if !strings.Contains(detail, "PRIMARY KEY") || strings.Contains(detail, "SCAN") {
		t.Fatalf("rolled hour seek plan = %q", detail)
	}

	// The whole read from the epoch sees every stored hour once.
	if err := setHourMark(ctx, store.db, DefaultTailnetID, base+60*h); err != nil {
		t.Fatal(err)
	}
	pairs, err := store.GetNodePairAggregates(ctx, DefaultTailnetID, time.Unix(0, 0), time.Unix(base+100*h, 0))
	if err != nil {
		t.Fatal(err)
	}
	if len(pairs) != 1 || pairs[0].TxBytes != int64(len(stored)) {
		t.Fatalf("epoch window pairs = %+v", pairs)
	}
}

func insertHourRow(t *testing.T, store *SQLiteStore, tailnetID string, hour int64) {
	t.Helper()
	if _, err := store.db.Exec(`
		INSERT INTO node_pair_hours (tailnet_id, bucket, src_node_id, dst_node_id, traffic_type, tx_bytes, rx_bytes, tx_pkts, rx_pkts, flow_count, protocols, ports, min_bucket)
		VALUES (?, ?, 'a', 'b', 'virtual', 1, 0, 1, 0, 1, '[6]', '[]', ?)`, tailnetID, hour, hour); err != nil {
		t.Fatal(err)
	}
}

// Merging partial groups must equal adding every row to one group, for
// every shape of row the read accepts.
func TestPairGroupMergeMatchesSequentialAdd(t *testing.T) {
	var rows []rawNodePair
	templates := append(manyHourTemplates(), rawNodePair{ // scalar JSON gives a null protocol key
		protocols: "[6]", protocolBytes: "6", ports: "[6]", txPorts: "[]", rxPorts: "[]", txProto: "6", rxProto: "null"})
	for i, tmpl := range templates {
		for j := 0; j < 4; j++ {
			row := tmpl
			row.bucket = int64(1000 + 60*((i+j)%5))
			row.tx, row.rx, row.txPkts, row.rxPkts, row.flows = int64(10*i+j), int64(j), 1, int64(j%2), 1
			if j == 2 {
				row.directional = 1 - row.directional
			}
			rows = append(rows, row)
		}
	}
	str := func(v any) sql.NullString {
		if v == nil {
			return sql.NullString{}
		}
		return sql.NullString{String: v.(string), Valid: true}
	}
	add := func(g *pairGroup, r rawNodePair) {
		n := func(v int64) sql.NullInt64 { return sql.NullInt64{Int64: v, Valid: true} }
		if err := g.add(n(r.bucket), n(r.tx), n(r.rx), n(r.txPkts), n(r.rxPkts), n(r.flows), n(int64(r.directional)),
			str(r.protocolBytes), str(r.ports), str(r.txProto), str(r.rxProto), str(r.txPorts), str(r.rxPorts)); err != nil {
			t.Fatal(err)
		}
	}
	key := pairGroupKey{src: "s", dst: "d", traffic: "virtual"}
	for parts := 2; parts <= 5; parts++ {
		whole := newPairGroup()
		split := make([]*pairGroup, parts)
		for i := range split {
			split[i] = newPairGroup()
		}
		for i, r := range rows {
			add(whole, r)
			add(split[i%parts], r)
		}
		merged := newPairGroup()
		for _, g := range split {
			merged.merge(g)
		}
		want, err1 := whole.aggregate(key)
		got, err2 := merged.aggregate(key)
		if err1 != nil || err2 != nil || !reflect.DeepEqual(want, got) {
			t.Fatalf("parts=%d merge mismatch (%v %v)\nwant %+v\ngot  %+v", parts, err1, err2, want, got)
		}
	}
	// A group that only saw NULL sums stays NULL after a merge.
	empty, nulls := newPairGroup(), newPairGroup()
	if err := nulls.add(sql.NullInt64{}, sql.NullInt64{}, sql.NullInt64{}, sql.NullInt64{}, sql.NullInt64{}, sql.NullInt64{}, sql.NullInt64{},
		sql.NullString{}, sql.NullString{}, sql.NullString{}, sql.NullString{}, sql.NullString{}, sql.NullString{}); err != nil {
		t.Fatal(err)
	}
	empty.merge(nulls)
	if _, err := empty.aggregate(key); err == nil {
		t.Fatal("merged NULL sums should still fail like SUM() of NULLs")
	}
}

// Reading closed hours in parallel must match a plain minute scan, before
// and after late writes into closed hours.
func TestParallelClosedHoursMatchMinuteScan(t *testing.T) {
	prev := maxClosedHourReaders
	maxClosedHourReaders = 4
	t.Cleanup(func() { maxClosedHourReaders = prev })

	store, mark := buildManyHourStore(t)
	assertFixtureWindows(t, store, mark, 60)

	lateWrite(t, store, DefaultTailnetID, manyHourBase+3*hourSeconds+7*minuteSeconds, "tag:app", "tag:db", 777, 8443)
	lateWrite(t, store, DefaultTailnetID, manyHourBase+12*hourSeconds+59*minuteSeconds, "late-new", "late-peer", 55, 22)
	lateWrite(t, store, "other", manyHourBase+20*hourSeconds, "a|b", "c|d", 9, 1)
	assertFixtureWindows(t, store, mark, 60)
}

func TestSplitClosedMinutes(t *testing.T) {
	const h = hourSeconds
	mark := 4*h + 29*60 // rolled through 4:29, so 4:30 is the first open minute
	closed, open := splitClosedMinutes([][2]int64{{3*h + 35*60, 4 * h}, {4 * h, 4*h + 40*60}}, mark)
	wantClosed := [][2]int64{
		{3*h + 35*60, 3*h + 45*60}, {3*h + 45*60, 3*h + 55*60}, {3*h + 55*60, 4 * h},
		{4 * h, 4*h + 10*60}, {4*h + 10*60, 4*h + 20*60}, {4*h + 20*60, 4*h + 30*60},
	}
	if !reflect.DeepEqual(closed, wantClosed) {
		t.Fatalf("closed = %v, want %v", closed, wantClosed)
	}
	if want := [][2]int64{{4*h + 30*60, 4*h + 40*60}}; !reflect.DeepEqual(open, want) {
		t.Fatalf("open = %v, want %v", open, want)
	}
	// A span wholly after the mark stays open; no mark keeps everything open.
	if closed, open := splitClosedMinutes([][2]int64{{5 * h, 5*h + 60}}, mark); closed != nil || len(open) != 1 {
		t.Fatalf("after mark: closed=%v open=%v", closed, open)
	}
	if closed, open := splitClosedMinutes([][2]int64{{h, 2 * h}}, -1); closed != nil || len(open) != 1 {
		t.Fatalf("no mark: closed=%v open=%v", closed, open)
	}
}

// A commit that lands between the snapshot and the parallel reads must not
// leak into rows the snapshot owns. A poll that adds minutes after the mark
// and rolls the filling hour forward leaves the answer at the pre-commit
// state. A late write into a closed hour is read in full on the newer
// snapshot, so the answer is the post-commit state. Neither double counts.
func TestParallelReadAcrossAConcurrentCommit(t *testing.T) {
	prev := maxClosedHourReaders
	maxClosedHourReaders = 4
	t.Cleanup(func() { maxClosedHourReaders = prev })
	ctx := context.Background()
	store, mark := buildManyHourStore(t)
	start := time.Unix(manyHourBase+20*hourSeconds+13*minuteSeconds, 0).UTC()
	end := time.Unix(mark+8*minuteSeconds, 0).UTC()

	want, err := store.legacyNodePairAggregates(ctx, DefaultTailnetID, start, end)
	if err != nil {
		t.Fatal(err)
	}
	fired := false
	store.closedReadHook = func() {
		if fired {
			return
		}
		fired = true
		poll := mark + 3*minuteSeconds
		if err := store.CommitPollResults(ctx, DefaultTailnetID, PollResults{
			NodePairs: []NodePairAggregate{{
				Bucket: poll, SrcNodeID: "tag:app", DstNodeID: "tag:db", TrafficType: "virtual",
				TxBytes: 5000, TxPkts: 1, FlowCount: 1, Protocols: "[6]", ProtocolBytes: `{"6":5000}`,
				Ports: "[]", TxPorts: "[]", RxPorts: "[]", TxProtocolBytes: "{}", RxProtocolBytes: "{}",
			}},
			PollEnd: time.Unix(mark+6*minuteSeconds, 0).UTC(),
		}); err != nil {
			t.Error(err)
		}
	}
	got, err := store.GetNodePairAggregates(ctx, DefaultTailnetID, start, end)
	if err != nil || !fired {
		t.Fatalf("read err=%v hook fired=%v", err, fired)
	}
	if !reflect.DeepEqual(want, got) {
		t.Fatalf("a poll after the snapshot changed the answer\nwant %s\ngot  %s", pairRowsJSON(want), pairRowsJSON(got))
	}
	newMark, _ := readHourMark(ctx, store.db, DefaultTailnetID)
	if newMark != mark+5*minuteSeconds {
		t.Fatalf("fixture: hook poll should move the mark, got %d", newMark)
	}

	// Late write into a closed hour inside the window.
	fired = false
	store.closedReadHook = func() {
		if fired {
			return
		}
		fired = true
		lateWrite(t, store, DefaultTailnetID, manyHourBase+22*hourSeconds+31*minuteSeconds, "tag:app", "tag:db", 4242, 9000)
	}
	got, err = store.GetNodePairAggregates(ctx, DefaultTailnetID, start, end)
	if err != nil || !fired {
		t.Fatalf("read err=%v hook fired=%v", err, fired)
	}
	store.closedReadHook = nil
	want, err = store.legacyNodePairAggregates(ctx, DefaultTailnetID, start, end)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(want, got) {
		t.Fatalf("late write into a closed hour was not read exactly once\nwant %s\ngot  %s", pairRowsJSON(want), pairRowsJSON(got))
	}
}
