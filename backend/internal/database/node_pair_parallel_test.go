package database

import (
	"context"
	"database/sql"
	"reflect"
	"strings"
	"sync/atomic"
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

func TestOpenSpans(t *testing.T) {
	const h = hourSeconds
	got := openSpans([][2]int64{{4 * h, 4*h + 25*60}}, [][2]int64{{4*h + 25*60, 4*h + 30*60}})
	want := []closedSpan{
		{lo: 4 * h, hi: 4*h + 25*60, hours: true, open: true},
		{lo: 4*h + 25*60, hi: 4*h + 27*60, open: true},
		{lo: 4*h + 27*60, hi: 4*h + 29*60, open: true},
		{lo: 4*h + 29*60, hi: 4*h + 30*60, open: true},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("open spans = %+v, want %+v", got, want)
	}
	if got := openSpans(nil, nil); got != nil {
		t.Fatalf("empty plan = %+v", got)
	}
	// A long span uses wider chunks, capped at maxOpenMinuteItems, and the
	// chunks still tile it exactly.
	long := [][2]int64{{h, 200 * 24 * h}}
	got = openSpans(nil, long)
	if len(got) > maxOpenMinuteItems || len(got) < maxOpenMinuteItems/2 {
		t.Fatalf("long span made %d items", len(got))
	}
	for i, sp := range got {
		if sp.lo%minuteSeconds != h%minuteSeconds || sp.hours || !sp.open {
			t.Fatalf("item %d = %+v", i, sp)
		}
		if i > 0 && sp.lo != got[i-1].hi {
			t.Fatalf("items %d and %d leave a gap", i-1, i)
		}
	}
	if got[0].lo != long[0][0] || got[len(got)-1].hi != long[0][1] {
		t.Fatalf("items cover [%d, %d), want %v", got[0].lo, got[len(got)-1].hi, long[0])
	}
}

func TestClampMinuteSpans(t *testing.T) {
	store := setupTestDB(t)
	ctx := context.Background()
	base := int64(1_790_000_000) / hourSeconds * hourSeconds
	var rows []rawNodePair
	for _, b := range []int64{base + 60, base + 600, base + 3600} {
		rows = append(rows, rawNodePair{tailnet: DefaultTailnetID, bucket: b, src: "a", dst: "b", traffic: "virtual",
			tx: 1, flows: 1, protocols: "[6]", protocolBytes: "{}", ports: "[]", txPorts: "[]", rxPorts: "[]", txProto: "{}", rxProto: "{}"})
	}
	rows = append(rows, rawNodePair{tailnet: "other", bucket: base, src: "a", dst: "b", traffic: "virtual",
		tx: 1, flows: 1, protocols: "[6]", protocolBytes: "{}", ports: "[]", txPorts: "[]", rxPorts: "[]", txProto: "{}", rxProto: "{}"})
	insertRawNodePairs(t, store, rows)
	got, err := clampMinuteSpans(ctx, store.db, DefaultTailnetID, [][2]int64{{0, base + 601}, {base + 601, base + 3600}, {base + 3600, 1 << 40}})
	if err != nil {
		t.Fatal(err)
	}
	if want := [][2]int64{{base + 60, base + 601}, {base + 3600, base + 3601}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("clamped = %v, want %v", got, want)
	}
}

// A window from the epoch with the mark far behind sparse minute rows, like
// a poller readiness check, splits only the minutes that hold rows.
func TestOpenRegionFromTheEpochStaysSmall(t *testing.T) {
	store := setupTestDB(t)
	ctx := context.Background()
	base := int64(1_790_000_000) / hourSeconds * hourSeconds
	var rows []rawNodePair
	for _, b := range []int64{base + 60, base + 120*hourSeconds, base + 3000*hourSeconds + 120} {
		rows = append(rows, rawNodePair{tailnet: DefaultTailnetID, bucket: b, src: "a", dst: "b", traffic: "virtual",
			tx: b % 997, flows: 1, protocols: "[6]", protocolBytes: "{}", ports: "[]", txPorts: "[]", rxPorts: "[]", txProto: "{}", rxProto: "{}"})
	}
	insertRawNodePairs(t, store, rows)
	if err := setHourMark(ctx, store.db, DefaultTailnetID, base-hourSeconds-minuteSeconds); err != nil {
		t.Fatal(err)
	}
	var items atomic.Int64
	store.openSpanHook = func() { items.Add(1) }
	start, end := time.Unix(0, 0).UTC(), time.Unix(base+5000*hourSeconds, 0).UTC()
	got, err := store.GetNodePairAggregates(ctx, DefaultTailnetID, start, end)
	if err != nil {
		t.Fatal(err)
	}
	if n := items.Load(); n == 0 || n > maxOpenMinuteItems+1 {
		t.Fatalf("open region made %d work items", n)
	}
	want, err := store.legacyNodePairAggregates(ctx, DefaultTailnetID, start, end)
	if err != nil || !reflect.DeepEqual(want, got) {
		t.Fatalf("epoch window err=%v\nwant %s\ngot  %s", err, pairRowsJSON(want), pairRowsJSON(got))
	}
}

func TestSplitClosedMinutes(t *testing.T) {
	const h = hourSeconds
	mark := 4*h + 29*60 // rolled through 4:29, so 4:30 is the first open minute
	closed, open := splitClosedMinutes([][2]int64{{3*h + 35*60, 4 * h}, {4 * h, 4*h + 40*60}}, mark)
	var wantClosed [][2]int64
	for _, seg := range [][2]int64{{3*h + 35*60, 4 * h}, {4 * h, 4*h + 30*60}} {
		for lo := seg[0]; lo < seg[1]; lo += closedMinuteChunk {
			wantClosed = append(wantClosed, [2]int64{lo, min(lo+closedMinuteChunk, seg[1])})
		}
	}
	if len(wantClosed) < 4 {
		t.Fatalf("fixture: %d chunks do not exercise the split", len(wantClosed))
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
// and rolls the filling hour forward leaves the sequential read at the
// pre-commit state; the parallel read sees the overlap and starts over at
// the post-commit state. A late write into a closed hour is read in full on
// the newer snapshot, so the answer is the post-commit state. Nothing is
// double counted.
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
	pollHook := func() {
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
	store.closedReadHook = pollHook
	// With the open part on the planning snapshot, the poll is not seen.
	got, err := store.readNodePairAggregates(ctx, DefaultTailnetID, start.Unix(), end.Unix(), false)
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

	// Read in parallel, the open part overlaps the poll, so the read starts
	// over and answers exactly as of after the poll.
	store, _ = buildManyHourStore(t)
	store.closedReadHook = pollHook
	fired = false
	got, err = store.GetNodePairAggregates(ctx, DefaultTailnetID, start, end)
	if err != nil || !fired {
		t.Fatalf("parallel read err=%v hook fired=%v", err, fired)
	}
	store.closedReadHook = nil
	if want, err = store.legacyNodePairAggregates(ctx, DefaultTailnetID, start, end); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(want, got) {
		t.Fatalf("parallel read across a poll\nwant %s\ngot  %s", pairRowsJSON(want), pairRowsJSON(got))
	}
	if n := store.openReadFallbacks.Load(); n != 1 {
		t.Fatalf("fallbacks = %d, want 1", n)
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

// A poll that rolls minutes into the filling hour and moves the mark while
// the open spans are being read must not count those minutes twice. The
// read sees the commit and starts over on one snapshot.
func TestParallelOpenRegionAcrossAMarkMove(t *testing.T) {
	prev := maxClosedHourReaders
	maxClosedHourReaders = 4
	t.Cleanup(func() { maxClosedHourReaders = prev })
	ctx := context.Background()
	for _, tc := range []struct {
		name   string
		start  int64
		hookAt int // fire on this open span
	}{
		{"first span", manyHourBase + 26*hourSeconds, 1},
		{"last span", manyHourBase + 26*hourSeconds, 4},
		{"filling hour only", manyHourBase + 29*hourSeconds, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store, mark := buildManyHourStore(t)
			// Minutes after the mark, so the window has an open region to split.
			var rows []rawNodePair
			for _, m := range []int64{1, 2, 3, 4, 5, 6, 8} { // the fixture already has mark+7m
				seen := map[[3]string]bool{}
				for i, tmpl := range manyHourTemplates() {
					if k := [3]string{tmpl.src, tmpl.dst, tmpl.traffic}; seen[k] {
						continue
					} else {
						seen[k] = true
					}
					row := tmpl
					row.tailnet = DefaultTailnetID
					row.bucket = mark + m*minuteSeconds
					row.tx, row.rx, row.txPkts, row.flows = 100*m+int64(i), m, 1, 1
					rows = append(rows, row)
				}
			}
			insertRawNodePairs(t, store, rows)
			start := time.Unix(tc.start, 0).UTC()
			end := time.Unix(mark+9*minuteSeconds, 0).UTC()

			var spans atomic.Int64
			var hookErr error
			store.openSpanHook = func() {
				if int(spans.Add(1)) != tc.hookAt {
					return
				}
				// Roll four more minutes into the filling hour and move the mark.
				unlock := store.lockTailnet(DefaultTailnetID)
				defer unlock()
				tx, err := store.beginWrite(ctx, DefaultTailnetID)
				if err != nil {
					hookErr = err
					return
				}
				defer tx.Rollback()
				if err := rollClosedMinutes(ctx, tx, DefaultTailnetID, mark+5*minuteSeconds); err != nil {
					hookErr = err
					return
				}
				hookErr = store.commitWrite(tx, DefaultTailnetID)
			}
			got, err := store.GetNodePairAggregates(ctx, DefaultTailnetID, start, end)
			if err != nil || hookErr != nil {
				t.Fatalf("read err=%v hook err=%v", err, hookErr)
			}
			store.openSpanHook = nil
			if m, _ := readHourMark(ctx, store.db, DefaultTailnetID); m != mark+4*minuteSeconds {
				t.Fatalf("fixture: mark = %d, want %d", m, mark+4*minuteSeconds)
			}
			want, err := store.legacyNodePairAggregates(ctx, DefaultTailnetID, start, end)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(want, got) {
				t.Fatalf("read across a mark move\nwant %s\ngot  %s", pairRowsJSON(want), pairRowsJSON(got))
			}
			if n := store.openReadFallbacks.Load(); n != 1 {
				t.Fatalf("fallbacks = %d, want 1", n)
			}
			// A quiet read takes the parallel path and gives the same answer.
			again, err := store.GetNodePairAggregates(ctx, DefaultTailnetID, start, end)
			if err != nil || !reflect.DeepEqual(want, again) || store.openReadFallbacks.Load() != 1 {
				t.Fatalf("quiet read err=%v fallbacks=%d equal=%v", err, store.openReadFallbacks.Load(), reflect.DeepEqual(want, again))
			}
		})
	}
}

// A read that starts while a commit for its tailnet is in flight keeps the
// open part on one snapshot and sees the state before that commit.
func TestReadDuringACommitStaysOnOneSnapshot(t *testing.T) {
	ctx := context.Background()
	store, mark := buildManyHourStore(t)
	start := time.Unix(manyHourBase+27*hourSeconds, 0).UTC()
	end := time.Unix(mark+9*minuteSeconds, 0).UTC()
	want, err := store.legacyNodePairAggregates(ctx, DefaultTailnetID, start, end)
	if err != nil {
		t.Fatal(err)
	}
	var opened atomic.Int64
	store.openSpanHook = func() { opened.Add(1) }
	var got []NodePairAggregate
	var readErr error
	store.beforeCommit = func(tailnetID string) {
		if tailnetID == DefaultTailnetID && got == nil {
			got, readErr = store.GetNodePairAggregates(ctx, DefaultTailnetID, start, end)
		}
	}
	lateWrite(t, store, DefaultTailnetID, mark+2*minuteSeconds, "tag:app", "tag:db", 31337, 443)
	store.beforeCommit = nil
	if readErr != nil || !reflect.DeepEqual(want, got) {
		t.Fatalf("read during a commit err=%v\nwant %s\ngot  %s", readErr, pairRowsJSON(want), pairRowsJSON(got))
	}
	if opened.Load() != 0 {
		t.Fatalf("read during a commit split the open part into %d spans", opened.Load())
	}
}

// The filling hour is read from its rollup row on every request, even if
// the cache holds an entry under the same hour.
func TestOpenFillingHourNeverUsesTheCache(t *testing.T) {
	ctx := context.Background()
	store, mark := buildManyHourStore(t)
	store.SetClosedHourCacheBytes(64 << 20)
	filling := mark / hourSeconds * hourSeconds
	stale := map[pairGroupKey]*pairGroup{}
	var g pairGroup
	g.tx.add(1<<40, true)
	g.rx.add(0, true)
	g.txPkts.add(1, true)
	g.rxPkts.add(0, true)
	g.flows.add(1, true)
	g.bucket, g.hasBucket = filling, true
	stale[pairGroupKey{src: "stale", dst: "entry", traffic: "virtual"}] = &g
	entry := packHour(DefaultTailnetID, filling, stale)
	if entry == nil {
		t.Fatal("fixture: stale entry did not pack")
	}
	store.hourCache.put(entry, store.hourCache.start())
	if store.hourCache.get(DefaultTailnetID, filling) == nil {
		t.Fatal("fixture: stale entry not cached")
	}
	start := time.Unix(filling-2*hourSeconds, 0).UTC()
	end := time.Unix(mark+6*minuteSeconds, 0).UTC()
	want, err := store.legacyNodePairAggregates(ctx, DefaultTailnetID, start, end)
	if err != nil {
		t.Fatal(err)
	}
	got, err := store.GetNodePairAggregates(ctx, DefaultTailnetID, start, end)
	if err != nil || !reflect.DeepEqual(want, got) {
		t.Fatalf("filling hour read err=%v\nwant %s\ngot  %s", err, pairRowsJSON(want), pairRowsJSON(got))
	}
}
