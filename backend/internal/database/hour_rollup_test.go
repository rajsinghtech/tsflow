package database

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestPlanHoursSplitsEdgesAndMiddle(t *testing.T) {
	const hour int64 = 3600
	// mark covers minute buckets through 3:59 of a clock that starts at 0,
	// so hours [0, 4h) are fully rolled.
	mark := int64(4*hour - 60)

	cases := []struct {
		name       string
		start, end int64
		mark       int64
		minutes    [][2]int64
		hours      [][2]int64
	}{
		{
			name:  "aligned hours",
			start: hour,
			end:   3 * hour,
			mark:  mark,
			hours: [][2]int64{{hour, 3 * hour}},
		},
		{
			name:    "unaligned edges",
			start:   hour + 15*60,
			end:     3*hour + 20*60,
			mark:    mark,
			minutes: [][2]int64{{hour + 15*60, 2 * hour}, {3 * hour, 3*hour + 20*60}},
			hours:   [][2]int64{{2 * hour, 3 * hour}},
		},
		{
			name:    "shorter than an hour",
			start:   hour + 60,
			end:     hour + 30*60,
			mark:    mark,
			minutes: [][2]int64{{hour + 60, hour + 30*60}},
		},
		{
			name:  "exact hour",
			start: 2 * hour,
			end:   3 * hour,
			mark:  mark,
			hours: [][2]int64{{2 * hour, 3 * hour}},
		},
		{
			name:    "open hour stays on minutes",
			start:   3 * hour,
			end:     5 * hour,
			mark:    mark,
			minutes: [][2]int64{{4 * hour, 5 * hour}},
			hours:   [][2]int64{{3 * hour, 4 * hour}},
		},
		{
			name:    "nothing rolled",
			start:   0,
			end:     3 * hour,
			mark:    -1,
			minutes: [][2]int64{{0, 3 * hour}},
		},
		{
			name:  "hour boundary is the end",
			start: 0,
			end:   2 * hour,
			mark:  2*hour - 60,
			hours: [][2]int64{{0, 2 * hour}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := planHours(tc.start, tc.end, tc.mark)
			if !reflect.DeepEqual(got.minutes, tc.minutes) || !reflect.DeepEqual(got.hours, tc.hours) {
				t.Fatalf("plan minutes=%v hours=%v, want minutes=%v hours=%v", got.minutes, got.hours, tc.minutes, tc.hours)
			}
		})
	}
}

func TestHourRollupQueriesMatchAcrossTailnetsEdgesAndRetention(t *testing.T) {
	store := setupTestDB(t)
	ctx := context.Background()
	const base int64 = 1_699_999_200 // 3600 * 472222, on an hour boundary
	seedHourRollupFixture(t, store, base)

	ranges := rollupRanges(base)
	before := map[string]rollupSnapshot{}
	for _, tailnetID := range []string{DefaultTailnetID, "other"} {
		before[tailnetID] = snapshotRollupQueries(t, store, tailnetID, base, ranges)
	}

	scans := store.hourRollupBackfillScans.Load()
	if err := store.backfillHourRollups(ctx); err != nil {
		t.Fatal(err)
	}
	if got := store.hourRollupBackfillScans.Load(); got != scans+2 {
		t.Fatalf("backfill scans = %d, want %d", got, scans+2)
	}
	for _, tailnetID := range []string{DefaultTailnetID, "other"} {
		mark, err := readHourMark(ctx, store.db, tailnetID)
		if err != nil {
			t.Fatal(err)
		}
		if mark < base+3*3600 {
			t.Fatalf("%s mark = %d, want at least the closed fixture", tailnetID, mark)
		}
		after := snapshotRollupQueries(t, store, tailnetID, base, ranges)
		if !reflect.DeepEqual(before[tailnetID], after) {
			t.Fatalf("%s rollup queries differ\nbefore: %#v\nafter:  %#v", tailnetID, before[tailnetID], after)
		}
		for _, row := range after.graphs {
			assertSamePairAPI(t, store, tailnetID, time.Unix(row.start, 0).UTC(), time.Unix(row.end, 0).UTC())
		}
	}

	// The middle of an aligned window is served by the rollup. Dropping those
	// minute rows leaves the graph result in place.
	middleStart := time.Unix(base+3600, 0).UTC()
	middleEnd := time.Unix(base+2*3600, 0).UTC()
	kept, err := store.GetNodePairAggregates(ctx, DefaultTailnetID, middleStart, middleEnd)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.ExecContext(ctx, `
		DELETE FROM node_pairs
		WHERE tailnet_id = ? AND bucket >= ? AND bucket < ?
	`, DefaultTailnetID, base+3600, base+2*3600); err != nil {
		t.Fatal(err)
	}
	still, err := store.GetNodePairAggregates(ctx, DefaultTailnetID, middleStart, middleEnd)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(kept, still) {
		t.Fatalf("aligned hour changed after minute rows were removed\nkept: %#v\nstill: %#v", kept, still)
	}
	other, err := store.GetNodePairAggregates(ctx, "other", middleStart, middleEnd)
	if err != nil {
		t.Fatal(err)
	}
	if reflect.DeepEqual(other, still) {
		t.Fatal("deleting the default tailnet changed the comparison shape with the other tailnet")
	}

	again := store.hourRollupBackfillScans.Load()
	if err := store.backfillHourRollups(ctx); err != nil {
		t.Fatal(err)
	}
	if got := store.hourRollupBackfillScans.Load(); got != again {
		t.Fatalf("covered startup scanned again: %d -> %d", again, got)
	}

	plan := explain(t, store, "EXPLAIN QUERY PLAN "+hourRollupNewerMinSQL, DefaultTailnetID, base, base+3600)
	if !strings.Contains(plan, "PRIMARY KEY") || !strings.Contains(plan, "tailnet_id") || strings.Contains(plan, "SCAN") {
		t.Fatalf("rollup probe plan = %q", plan)
	}
}

func TestHourRollupClosesMinutesOnTheHourAndAcceptsLateDeltas(t *testing.T) {
	store := setupTestDB(t)
	ctx := context.Background()
	const base int64 = 1_699_999_200
	var pairs []NodePairAggregate
	for minute := int64(0); minute < 60; minute++ {
		pairs = append(pairs, NodePairAggregate{
			Bucket: base + minute*60, SrcNodeID: "a", DstNodeID: "b", TrafficType: "virtual",
			TxBytes: 10 + minute, RxBytes: 1, TxPkts: 1, RxPkts: 1, FlowCount: 1,
			Protocols: "[6]", ProtocolBytes: `{"6":11}`,
			Ports:   `[{"port":443,"proto":6,"bytes":11}]`,
			TxPorts: `[{"port":443,"proto":6,"bytes":10}]`, RxPorts: `[{"port":53,"proto":17,"bytes":1}]`,
			TxProtocolBytes: `{"6":10}`, RxProtocolBytes: `{"17":1}`,
			DirectionalPorts: true,
		})
	}
	// One legacy minute clears the directional flag for the hour.
	pairs[10].DirectionalPorts = false
	pairs[10].ProtocolBytes = "{}"
	pairs[10].Protocols = "[6,17]"
	if err := store.CommitPollResults(ctx, DefaultTailnetID, PollResults{
		NodePairs: pairs,
		PollEnd:   time.Unix(base+3600, 0).UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	mark, err := readHourMark(ctx, store.db, DefaultTailnetID)
	if err != nil {
		t.Fatal(err)
	}
	if mark != base+3600-60 {
		t.Fatalf("mark = %d, want %d", mark, base+3600-60)
	}
	var hourRows int
	if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM node_pair_hours WHERE tailnet_id = ?`, DefaultTailnetID).Scan(&hourRows); err != nil {
		t.Fatal(err)
	}
	if hourRows != 1 {
		t.Fatalf("hourly rows = %d, want 1", hourRows)
	}
	start := time.Unix(base, 0).UTC()
	end := time.Unix(base+3600, 0).UTC()
	assertSamePairAPI(t, store, DefaultTailnetID, start, end)

	// The next minute is still open, so it stays out of the rollup.
	open := NodePairAggregate{
		Bucket: base + 3600, SrcNodeID: "a", DstNodeID: "b", TrafficType: "virtual",
		TxBytes: 7, RxBytes: 2, TxPkts: 1, RxPkts: 1, FlowCount: 1,
		Protocols: "[17]", ProtocolBytes: `{"17":9}`,
		Ports:            `[{"port":53,"proto":17,"bytes":9}]`,
		DirectionalPorts: true,
	}
	if err := store.CommitPollResults(ctx, DefaultTailnetID, PollResults{
		NodePairs: []NodePairAggregate{open},
		PollEnd:   time.Unix(base+3600+30, 0).UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	var openHour int
	if err := store.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM node_pair_hours WHERE tailnet_id = ? AND bucket = ?
	`, DefaultTailnetID, base+3600).Scan(&openHour); err != nil {
		t.Fatal(err)
	}
	if openHour != 0 {
		t.Fatal("the open minute was rolled into its hour")
	}
	assertSamePairAPI(t, store, DefaultTailnetID, start, time.Unix(base+3600+60, 0).UTC())

	// A later write into a closed minute updates the hour in place.
	if err := store.UpsertNodePairAggregates(ctx, DefaultTailnetID, []NodePairAggregate{{
		Bucket: base + 5*60, SrcNodeID: "a", DstNodeID: "b", TrafficType: "virtual",
		TxBytes: 100, RxBytes: 40, TxPkts: 2, RxPkts: 1, FlowCount: 2,
		Protocols: "[6]", ProtocolBytes: `{"6":140}`,
		Ports:            `[{"port":443,"proto":6,"bytes":140}]`,
		DirectionalPorts: true,
	}}); err != nil {
		t.Fatal(err)
	}
	assertSamePairAPI(t, store, DefaultTailnetID, start, end)
	derived, err := store.GetTrafficStatsFromNodePairs(ctx, DefaultTailnetID, start, end)
	if err != nil {
		t.Fatal(err)
	}
	legacyDerived := minuteDerivedStats(t, store, DefaultTailnetID, start, end)
	if !reflect.DeepEqual(derived, legacyDerived) {
		t.Fatalf("derived stats\nrollup: %#v\nminute: %#v", derived, legacyDerived)
	}
}

func TestHourRollupRetentionPrunesAndRebuildsTheBoundaryHour(t *testing.T) {
	store := setupTestDB(t)
	ctx := context.Background()
	retention := 90 * time.Minute
	cutoff := time.Now().UTC().Unix() - int64(retention.Seconds())
	boundary := (cutoff / hourSeconds) * hourSeconds

	insert := func(tailnet string, bucket, tx int64) {
		t.Helper()
		if _, err := store.db.ExecContext(ctx, `
			INSERT INTO node_pairs (
				tailnet_id, bucket, src_node_id, dst_node_id, traffic_type,
				tx_bytes, rx_bytes, tx_pkts, rx_pkts, flow_count,
				protocols, protocol_bytes, ports,
				tx_ports, rx_ports, tx_protocol_bytes, rx_protocol_bytes,
				directional_ports
			) VALUES (?, ?, 'src', 'dst', 'virtual', ?, 1, 1, 1, 1,
				'[6]', ?, '[{"port":443,"proto":6,"bytes":5}]',
				'[]', '[]', '{"6":5}', '{}', 1)
		`, tailnet, bucket, tx, fmt.Sprintf(`{"6":%d}`, tx+1)); err != nil {
			t.Fatal(err)
		}
	}
	for _, tailnetID := range []string{DefaultTailnetID, "other"} {
		insert(tailnetID, boundary-3600, 10)    // fully expired hour
		insert(tailnetID, boundary, 20)         // start of the boundary hour
		insert(tailnetID, boundary+30*60, 30)   // may fall on either side of the cutoff
		insert(tailnetID, boundary+50*60, 40)   // late in the boundary hour
		insert(tailnetID, boundary+3600+60, 50) // next hour, retained
		insert(tailnetID, boundary+2*3600+120, 60)
	}
	closedThrough := boundary + 4*3600
	if closedThrough < time.Now().UTC().Unix() {
		closedThrough = floorMinute(time.Now().UTC().Unix())
	}
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := rollClosedMinutes(ctx, tx, DefaultTailnetID, closedThrough); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if err := rollClosedMinutes(ctx, tx, "other", closedThrough); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	start := time.Unix(boundary-3600, 0).UTC()
	end := time.Unix(boundary+3*3600, 0).UTC()
	wantDefault, err := store.legacyNodePairAggregates(ctx, DefaultTailnetID, start, end)
	if err != nil {
		t.Fatal(err)
	}
	wantOther, err := store.GetNodePairAggregates(ctx, "other", start, end)
	if err != nil {
		t.Fatal(err)
	}
	var otherHours int
	if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM node_pair_hours WHERE tailnet_id = 'other'`).Scan(&otherHours); err != nil {
		t.Fatal(err)
	}

	deleted, err := store.Cleanup(ctx, DefaultTailnetID, retention)
	if err != nil {
		t.Fatal(err)
	}
	if deleted == 0 {
		t.Fatal("cleanup deleted nothing")
	}
	var expired int
	if err := store.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM node_pair_hours
		WHERE tailnet_id = ? AND bucket + ? <= ?
	`, DefaultTailnetID, hourSeconds, cutoff).Scan(&expired); err != nil {
		t.Fatal(err)
	}
	if expired != 0 {
		t.Fatalf("expired hourly rows left behind: %d", expired)
	}
	var otherHoursAfter int
	if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM node_pair_hours WHERE tailnet_id = 'other'`).Scan(&otherHoursAfter); err != nil {
		t.Fatal(err)
	}
	if otherHoursAfter != otherHours {
		t.Fatalf("other tailnet hourly rows %d -> %d", otherHours, otherHoursAfter)
	}
	gotOther, err := store.GetNodePairAggregates(ctx, "other", start, end)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(wantOther, gotOther) {
		t.Fatalf("cleanup changed the other tailnet\nbefore: %#v\nafter: %#v", wantOther, gotOther)
	}

	// Legacy reads whatever minutes remain. The rollup read has to match.
	wantRemaining, err := store.legacyNodePairAggregates(ctx, DefaultTailnetID, start, end)
	if err != nil {
		t.Fatal(err)
	}
	got, err := store.GetNodePairAggregates(ctx, DefaultTailnetID, start, end)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(wantRemaining, got) {
		t.Fatalf("retained graph\nminute: %#v\nrollup: %#v", wantRemaining, got)
	}
	if reflect.DeepEqual(wantDefault, got) && deleted > 0 {
		// Deletion removed bytes. The retained graph must be smaller unless
		// every deleted minute was outside the window, which it is not.
		var expiredPairs int
		if err := store.db.QueryRowContext(ctx, `
			SELECT COUNT(*) FROM node_pairs WHERE tailnet_id = ? AND bucket < ?
		`, DefaultTailnetID, cutoff).Scan(&expiredPairs); err != nil {
			t.Fatal(err)
		}
		if expiredPairs != 0 {
			t.Fatalf("expired minute rows left behind: %d", expiredPairs)
		}
	}
	talkers, err := store.GetTopTalkers(ctx, DefaultTailnetID, start, end, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(talkers) == 0 {
		t.Fatal("retained window has no talkers")
	}
	stats, err := store.GetTrafficStatsFromNodePairs(ctx, DefaultTailnetID, start, end)
	if err != nil {
		t.Fatal(err)
	}
	minuteStats := minuteDerivedStats(t, store, DefaultTailnetID, start, end)
	if !reflect.DeepEqual(stats, minuteStats) {
		t.Fatalf("retained derived stats\nrollup: %#v\nminute: %#v", stats, minuteStats)
	}
}

type graphSnap struct {
	start, end int64
	rows       []NodePairAggregate
}

type rollupSnapshot struct {
	graphs    []graphSnap
	talkers   [][]TopTalker
	pairs     [][]TopPair
	bandwidth [][]BandwidthBucket
	nodeBW    [][]BandwidthBucket
	derived   [][]TrafficStats
	nodeStats []*NodeDetailStats
}

func rollupRanges(base int64) []struct {
	name       string
	start, end int64
} {
	return []struct {
		name       string
		start, end int64
	}{
		{name: "thirty minutes", start: base + 15*60, end: base + 45*60},
		{name: "exact hour", start: base, end: base + 3600},
		{name: "hour boundary", start: base + 3600, end: base + 2*3600},
		{name: "two aligned hours", start: base, end: base + 2*3600},
		{name: "unaligned ninety minutes", start: base + 20*60, end: base + 20*60 + 90*60},
		{name: "three hours shifted", start: base + 5*60, end: base + 3*3600 + 5*60},
		{name: "crosses into open tail", start: base + 2*3600, end: base + 3*3600 + 10*60},
		{name: "fifty hours", start: base, end: base + 50*3600},
		{name: "day with a short edge", start: base + 90*60, end: base + 50*3600 + 15*60},
	}
}

func snapshotRollupQueries(t *testing.T, store *SQLiteStore, tailnetID string, base int64, ranges []struct {
	name       string
	start, end int64
}) rollupSnapshot {
	t.Helper()
	ctx := context.Background()
	var snap rollupSnapshot
	for _, rg := range ranges {
		start := time.Unix(rg.start, 0).UTC()
		end := time.Unix(rg.end, 0).UTC()
		graphs, err := store.GetNodePairAggregates(ctx, tailnetID, start, end)
		if err != nil {
			t.Fatalf("%s %s graph: %v", tailnetID, rg.name, err)
		}
		snap.graphs = append(snap.graphs, graphSnap{start: rg.start, end: rg.end, rows: graphs})
		talkers, err := store.GetTopTalkers(ctx, tailnetID, start, end, 10)
		if err != nil {
			t.Fatal(err)
		}
		snap.talkers = append(snap.talkers, talkers)
		filteredTalkers, err := store.GetTopTalkersByTrafficTypes(ctx, tailnetID, start, end, []string{"virtual"}, 10)
		if err != nil {
			t.Fatal(err)
		}
		snap.talkers = append(snap.talkers, filteredTalkers)
		pairs, err := store.GetTopPairs(ctx, tailnetID, start, end, 10)
		if err != nil {
			t.Fatal(err)
		}
		snap.pairs = append(snap.pairs, pairs)
		typedPairs, err := store.GetTopPairsByTrafficTypes(ctx, tailnetID, start, end, []string{"subnet"}, 10)
		if err != nil {
			t.Fatal(err)
		}
		snap.pairs = append(snap.pairs, typedPairs)
		bw, err := store.GetBandwidthByTrafficTypes(ctx, tailnetID, start, end, []string{"virtual", "subnet"})
		if err != nil {
			t.Fatal(err)
		}
		snap.bandwidth = append(snap.bandwidth, bw)
		nodeBW, err := store.GetNodeBandwidth(ctx, tailnetID, start, end, "src")
		if err != nil {
			t.Fatal(err)
		}
		snap.nodeBW = append(snap.nodeBW, nodeBW)
		derived, err := store.GetTrafficStatsFromNodePairs(ctx, tailnetID, start, end)
		if err != nil {
			t.Fatal(err)
		}
		snap.derived = append(snap.derived, derived)
		typed, err := store.GetTrafficStatsFromNodePairsByTrafficTypes(ctx, tailnetID, start, end, []string{"virtual"})
		if err != nil {
			t.Fatal(err)
		}
		snap.derived = append(snap.derived, typed)
		stats, err := store.GetNodeStats(ctx, tailnetID, "src", start, end)
		if err != nil {
			t.Fatal(err)
		}
		snap.nodeStats = append(snap.nodeStats, stats)
	}
	// Traffic stats unique-pair recount for a window long enough to use hours.
	start := time.Unix(base, 0).UTC()
	end := time.Unix(base+3*3600, 0).UTC()
	overview, err := store.GetTrafficStats(ctx, tailnetID, start, end)
	if err != nil {
		t.Fatal(err)
	}
	snap.derived = append(snap.derived, overview)
	return snap
}

func seedHourRollupFixture(t *testing.T, store *SQLiteStore, base int64) {
	t.Helper()
	ports := make([]string, 0, 21)
	for port := 1; port <= 21; port++ {
		ports = append(ports, fmt.Sprintf(`{"port":%d,"proto":6,"bytes":%d}`, port, 500-port))
	}
	widePorts := "[" + strings.Join(ports, ",") + "]"
	type row struct {
		tailnet, src, dst, traffic string
		bucket                     int64
		tx, rx                     int64
		protocols, protocolBytes   string
		ports                      string
		directional                int
	}
	var rows []row
	for _, tailnetID := range []string{DefaultTailnetID, "other"} {
		scale := int64(1)
		if tailnetID == "other" {
			scale = 1000
		}
		for minute := int64(0); minute < 180; minute += 7 {
			rows = append(rows, row{
				tailnet: tailnetID, src: "src", dst: "dst", traffic: "virtual",
				bucket: base + minute*60, tx: (10 + minute) * scale, rx: 3 * scale,
				protocols: "[6,17]", protocolBytes: fmt.Sprintf(`{"6":%d,"17":%d}`, 8*scale, 2*scale),
				ports: `[{"port":443,"proto":6,"bytes":8},{"port":53,"proto":17,"bytes":2}]`, directional: 1,
			})
		}
		rows = append(rows,
			row{tailnet: tailnetID, src: "src", dst: "src", traffic: "virtual", bucket: base + 60, tx: 40 * scale, rx: 5 * scale, protocols: "[6]", protocolBytes: fmt.Sprintf(`{"6":%d}`, 45*scale), ports: `[{"port":22,"proto":6,"bytes":40}]`, directional: 1},
			row{tailnet: tailnetID, src: "src", dst: "peer", traffic: "subnet", bucket: base + 120, tx: 70 * scale, rx: 1 * scale, protocols: "[6]", protocolBytes: fmt.Sprintf(`{"6":%d}`, 71*scale), ports: widePorts, directional: 1},
			row{tailnet: tailnetID, src: "src", dst: "peer", traffic: "subnet", bucket: base + 3600 + 120, tx: 15 * scale, rx: 1 * scale, protocols: "[6]", protocolBytes: fmt.Sprintf(`{"6":%d}`, 16*scale), ports: `[{"port":21,"proto":6,"bytes":900}]`, directional: 1},
			row{tailnet: tailnetID, src: "src", dst: "dst", traffic: "virtual", bucket: base + 2*3600 + 30*60, tx: 9 * scale, rx: 9 * scale, protocols: "[17]", protocolBytes: fmt.Sprintf(`{"17":%d}`, 18*scale), ports: `[{"port":53,"proto":17,"bytes":18}]`, directional: 0},
			row{tailnet: tailnetID, src: "src", dst: "dst", traffic: "exit", bucket: base + 2*3600 + 40*60, tx: 4 * scale, rx: 1 * scale, protocols: "[6,17]", protocolBytes: "{}", ports: "[]", directional: 0},
			row{tailnet: tailnetID, src: "late", dst: "dst", traffic: "physical", bucket: base + 49*3600 + 10*60, tx: 6 * scale, rx: 1 * scale, protocols: "[6]", protocolBytes: fmt.Sprintf(`{"6":%d}`, 7*scale), ports: `[{"port":80,"proto":6,"bytes":7}]`, directional: 1},
		)
	}
	tx, err := store.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	stmt, err := tx.Prepare(`
		INSERT INTO node_pairs (
			tailnet_id, bucket, src_node_id, dst_node_id, traffic_type,
			tx_bytes, rx_bytes, tx_pkts, rx_pkts, flow_count,
			protocols, protocol_bytes, ports,
			tx_ports, rx_ports, tx_protocol_bytes, rx_protocol_bytes,
			directional_ports
		) VALUES (?, ?, ?, ?, ?, ?, ?, 1, 1, 1, ?, ?, ?, '[]', '[]', '{}', '{}', ?)
	`)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	for _, row := range rows {
		if _, err := stmt.Exec(row.tailnet, row.bucket, row.src, row.dst, row.traffic, row.tx, row.rx, row.protocols, row.protocolBytes, row.ports, row.directional); err != nil {
			t.Fatal(err)
		}
	}
	for _, tailnetID := range []string{DefaultTailnetID, "other"} {
		for _, bucket := range []int64{base, base + 3600, base + 2*3600, base + 49*3600} {
			if _, err := tx.Exec(`
				INSERT INTO traffic_stats (
					tailnet_id, bucket, tcp_bytes, virtual_bytes, total_flows, unique_pairs, top_ports
				) VALUES (?, ?, 10, 10, 1, 1, '[{"port":443,"proto":6,"bytes":10}]')
			`, tailnetID, bucket); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func minuteDerivedStats(t *testing.T, store *SQLiteStore, tailnetID string, start, end time.Time) []TrafficStats {
	t.Helper()
	ctx := context.Background()
	mark, err := readHourMark(ctx, store.db, tailnetID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.ExecContext(ctx, `UPDATE backfill_state SET hour_rollup_bucket = -1 WHERE tailnet_id = ?`, tailnetID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := store.db.ExecContext(context.Background(), `
			UPDATE backfill_state SET hour_rollup_bucket = ? WHERE tailnet_id = ?
		`, mark, tailnetID); err != nil {
			t.Errorf("restore mark: %v", err)
		}
	})
	stats, err := store.GetTrafficStatsFromNodePairs(ctx, tailnetID, start, end)
	if err != nil {
		t.Fatal(err)
	}
	return stats
}
