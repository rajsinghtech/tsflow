package database

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func TestRankedTrafficEmptyRollupFallsBack(t *testing.T) {
	store := setupTestDB(t)
	ctx := context.Background()

	talkers, more, err := store.ListRankedTalkers(ctx, DefaultTailnetID, time.Unix(0, 0), time.Unix(3600, 0), RankQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if len(talkers) != 0 || more {
		t.Fatalf("empty talkers = %#v more=%v", talkers, more)
	}
	pairs, more, err := store.ListRankedPairs(ctx, DefaultTailnetID, time.Unix(0, 0), time.Unix(3600, 0), RankQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if len(pairs) != 0 || more {
		t.Fatalf("empty pairs = %#v more=%v", pairs, more)
	}

	const base int64 = 1_699_999_200
	insertRankPair(t, store, DefaultTailnetID, base, "a", "b", "virtual", 10, 1, 1)
	insertRankPair(t, store, DefaultTailnetID, base+60, "a", "b", "virtual", 15, 2, 2)
	insertRankPair(t, store, DefaultTailnetID, base+3600, "a", "b", "virtual", 7, 3, 4)
	insertRankPair(t, store, "beta", base, "z", "y", "virtual", 500, 1, 9)

	start := time.Unix(base, 0).UTC()
	end := time.Unix(base+3600+1800, 0).UTC()
	withRollup := listRankPage(t, store, DefaultTailnetID, start, end)
	if withRollup.talkers[0].NodeID != "a" || withRollup.talkers[0].TotalBytes != 38 || withRollup.talkers[0].FlowCount != 7 {
		t.Fatalf("rolled talkers = %#v", withRollup.talkers)
	}
	if withRollup.pairs[0].TotalBytes != 38 || withRollup.pairs[0].FlowCount != 7 {
		t.Fatalf("rolled pairs = %#v", withRollup.pairs)
	}
	assertRankMatchesTop(t, store, DefaultTailnetID, start, end, withRollup)

	// Drop the rollup and its mark. The same window has to come back from
	// minute rows, including a tailnet that has never had an hour row.
	if _, err := store.db.ExecContext(ctx, `DELETE FROM node_pair_hours`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.ExecContext(ctx, `UPDATE backfill_state SET hour_rollup_bucket = -1`); err != nil {
		t.Fatal(err)
	}
	var hourRows int
	if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM node_pair_hours`).Scan(&hourRows); err != nil {
		t.Fatal(err)
	}
	if hourRows != 0 {
		t.Fatalf("hour rows = %d after clearing the rollup", hourRows)
	}
	fromMinutes := listRankPage(t, store, DefaultTailnetID, start, end)
	if !sameRankPage(withRollup, fromMinutes) {
		t.Fatalf("empty rollup did not fall back to minutes\nrollup %#v\nminutes %#v", withRollup, fromMinutes)
	}

	if err := store.backfillHourRollups(ctx); err != nil {
		t.Fatal(err)
	}
	if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM node_pair_hours WHERE tailnet_id = ?`, DefaultTailnetID).Scan(&hourRows); err != nil {
		t.Fatal(err)
	}
	if hourRows == 0 {
		t.Fatal("rollup left node_pair_hours empty")
	}
	rolled := listRankPage(t, store, DefaultTailnetID, start, end)
	if !sameRankPage(withRollup, rolled) {
		t.Fatalf("rollup page differs\nbefore %#v\nrolled %#v", withRollup, rolled)
	}

	if _, err := store.db.ExecContext(ctx, `
		DELETE FROM node_pairs
		WHERE tailnet_id = ? AND bucket >= ? AND bucket < ?
	`, DefaultTailnetID, base, base+3600); err != nil {
		t.Fatal(err)
	}
	fromHours := listRankPage(t, store, DefaultTailnetID, start, end)
	if !sameRankPage(withRollup, fromHours) {
		t.Fatalf("hour rows did not cover the closed hour\nwant %#v\ngot  %#v", withRollup, fromHours)
	}

	beta, _, err := store.ListRankedTalkers(ctx, "beta", start, end, RankQuery{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(beta) != 2 || beta[0].NodeID != "y" || beta[1].NodeID != "z" || beta[0].TotalBytes != 501 {
		t.Fatalf("beta talkers = %#v", beta)
	}
}

func TestRankedTrafficCoveredEmptyHourReturnsEmpty(t *testing.T) {
	store := setupTestDB(t)
	ctx := context.Background()
	const base int64 = 1_699_999_200
	if _, err := store.db.ExecContext(ctx, `
		INSERT INTO backfill_state (tailnet_id, hour_rollup_bucket, completed_at)
		VALUES (?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(tailnet_id) DO UPDATE SET hour_rollup_bucket = excluded.hour_rollup_bucket
	`, DefaultTailnetID, base+3600-60); err != nil {
		t.Fatal(err)
	}
	start := time.Unix(base, 0).UTC()
	end := time.Unix(base+3600, 0).UTC()
	talkers, more, err := store.ListRankedTalkers(ctx, DefaultTailnetID, start, end, RankQuery{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(talkers) != 0 || more {
		t.Fatalf("covered empty hour talkers = %#v more=%v", talkers, more)
	}
	pairs, more, err := store.ListRankedPairs(ctx, DefaultTailnetID, start, end, RankQuery{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(pairs) != 0 || more {
		t.Fatalf("covered empty hour pairs = %#v more=%v", pairs, more)
	}
}

func TestRankedTrafficPagesByDevice(t *testing.T) {
	store := setupTestDB(t)
	ctx := context.Background()
	base := time.Now().UTC().Truncate(time.Minute).Add(-2 * time.Minute).Unix()
	insertRankPair(t, store, DefaultTailnetID, base, "a", "a", "virtual", 100, 10, 1)
	insertRankPair(t, store, DefaultTailnetID, base, "a", "b", "virtual", 50, 20, 3)
	insertRankPair(t, store, DefaultTailnetID, base, "c", "a", "virtual", 5, 1, 2)
	insertRankPair(t, store, DefaultTailnetID, base, "b", "c", "subnet", 7, 0, 4)
	insertRankPair(t, store, "other", base, "z", "y", "virtual", 900, 0, 1)
	if err := store.UpsertNodeMetadata(ctx, DefaultTailnetID, []NodeMetadata{
		{NodeID: "a", Hostname: "laptop"},
		{NodeID: "b", Name: "bob-device"},
	}); err != nil {
		t.Fatal(err)
	}

	start := time.Unix(base, 0).UTC()
	end := time.Unix(base+60, 0).UTC()
	page, _, err := store.ListRankedTalkers(ctx, DefaultTailnetID, start, end, RankQuery{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	want := []RankedTalker{
		{NodeID: "a", Hostname: "laptop", TxBytes: 151, RxBytes: 35, TotalBytes: 186, FlowCount: 6},
		{NodeID: "b", Hostname: "bob-device", TxBytes: 27, RxBytes: 50, TotalBytes: 77, FlowCount: 7},
		{NodeID: "c", Hostname: "", TxBytes: 5, RxBytes: 8, TotalBytes: 13, FlowCount: 6},
	}
	if !sameTalkers(page, want) {
		t.Fatalf("talkers = %#v, want %#v", page, want)
	}

	second, more, err := store.ListRankedTalkers(ctx, DefaultTailnetID, start, end, RankQuery{Limit: 1, Offset: 1})
	if err != nil {
		t.Fatal(err)
	}
	if !more || len(second) != 1 || second[0].NodeID != "b" {
		t.Fatalf("second page = %#v more=%v", second, more)
	}
	last, more, err := store.ListRankedTalkers(ctx, DefaultTailnetID, start, end, RankQuery{Limit: 1, Offset: 2})
	if err != nil {
		t.Fatal(err)
	}
	if more || len(last) != 1 || last[0].NodeID != "c" {
		t.Fatalf("last page = %#v more=%v", last, more)
	}
	none, more, err := store.ListRankedTalkers(ctx, DefaultTailnetID, start, end, RankQuery{Limit: 1, Offset: 3})
	if err != nil {
		t.Fatal(err)
	}
	if more || len(none) != 0 {
		t.Fatalf("past the end = %#v more=%v", none, more)
	}

	byFlows, _, err := store.ListRankedTalkers(ctx, DefaultTailnetID, start, end, RankQuery{Limit: 10, Sort: RankSortFlows})
	if err != nil {
		t.Fatal(err)
	}
	if len(byFlows) != 3 || byFlows[0].NodeID != "b" || byFlows[1].NodeID != "a" || byFlows[2].NodeID != "c" {
		t.Fatalf("flow order = %#v", byFlows)
	}

	virtual, _, err := store.ListRankedTalkers(ctx, DefaultTailnetID, start, end, RankQuery{Limit: 10, TrafficTypes: []string{"virtual"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(virtual) != 3 || virtual[0].NodeID != "a" || virtual[0].TotalBytes != 186 || virtual[0].FlowCount != 6 ||
		virtual[1].NodeID != "b" || virtual[1].TotalBytes != 70 || virtual[1].FlowCount != 3 {
		t.Fatalf("virtual talkers = %#v", virtual)
	}

	pairs, _, err := store.ListRankedPairs(ctx, DefaultTailnetID, start, end, RankQuery{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	wantPairs := []RankedPair{
		{SrcNodeID: "a", SrcHostname: "laptop", DstNodeID: "a", DstHostname: "laptop", TxBytes: 100, RxBytes: 10, TotalBytes: 110, FlowCount: 1},
		{SrcNodeID: "a", SrcHostname: "laptop", DstNodeID: "b", DstHostname: "bob-device", TxBytes: 50, RxBytes: 20, TotalBytes: 70, FlowCount: 3},
		{SrcNodeID: "b", SrcHostname: "bob-device", DstNodeID: "c", DstHostname: "", TxBytes: 7, RxBytes: 0, TotalBytes: 7, FlowCount: 4},
		{SrcNodeID: "c", SrcHostname: "", DstNodeID: "a", DstHostname: "laptop", TxBytes: 5, RxBytes: 1, TotalBytes: 6, FlowCount: 2},
	}
	if !samePairs(pairs, wantPairs) {
		t.Fatalf("pairs = %#v, want %#v", pairs, wantPairs)
	}
	pairPage, more, err := store.ListRankedPairs(ctx, DefaultTailnetID, start, end, RankQuery{Limit: 1, Offset: 1})
	if err != nil {
		t.Fatal(err)
	}
	if !more || len(pairPage) != 1 || pairPage[0].SrcNodeID != "a" || pairPage[0].DstNodeID != "b" {
		t.Fatalf("pair page = %#v more=%v", pairPage, more)
	}
	flowPairs, _, err := store.ListRankedPairs(ctx, DefaultTailnetID, start, end, RankQuery{Limit: 1, Sort: RankSortFlows})
	if err != nil {
		t.Fatal(err)
	}
	if len(flowPairs) != 1 || flowPairs[0].SrcNodeID != "b" || flowPairs[0].DstNodeID != "c" || flowPairs[0].FlowCount != 4 {
		t.Fatalf("flow pairs = %#v", flowPairs)
	}

	if _, _, err := store.ListRankedTalkers(ctx, DefaultTailnetID, start, end, RankQuery{Sort: "packets"}); err == nil {
		t.Fatal("invalid sort was accepted")
	}
	if _, _, err := store.ListRankedPairs(ctx, DefaultTailnetID, start, end, RankQuery{Offset: -1}); err == nil {
		t.Fatal("negative offset was accepted")
	}
}

type rankPage struct {
	talkers []RankedTalker
	pairs   []RankedPair
}

func listRankPage(t *testing.T, store *SQLiteStore, tailnetID string, start, end time.Time) rankPage {
	t.Helper()
	ctx := context.Background()
	talkers, _, err := store.ListRankedTalkers(ctx, tailnetID, start, end, RankQuery{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	pairs, _, err := store.ListRankedPairs(ctx, tailnetID, start, end, RankQuery{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	return rankPage{talkers: talkers, pairs: pairs}
}

func assertRankMatchesTop(t *testing.T, store *SQLiteStore, tailnetID string, start, end time.Time, page rankPage) {
	t.Helper()
	ctx := context.Background()
	topTalkers, err := store.GetTopTalkers(ctx, tailnetID, start, end, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(topTalkers) != len(page.talkers) {
		t.Fatalf("talker count %d != top %d", len(page.talkers), len(topTalkers))
	}
	for i := range topTalkers {
		got := page.talkers[i]
		if got.NodeID != topTalkers[i].NodeID || got.TxBytes != topTalkers[i].TxBytes || got.RxBytes != topTalkers[i].RxBytes || got.TotalBytes != topTalkers[i].TotalBytes {
			t.Fatalf("talker %d = %#v, top %#v", i, got, topTalkers[i])
		}
	}
	topPairs, err := store.GetTopPairs(ctx, tailnetID, start, end, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(topPairs) != len(page.pairs) {
		t.Fatalf("pair count %d != top %d", len(page.pairs), len(topPairs))
	}
	for i := range topPairs {
		got := page.pairs[i]
		if got.SrcNodeID != topPairs[i].SrcNodeID || got.DstNodeID != topPairs[i].DstNodeID ||
			got.TxBytes != topPairs[i].TxBytes || got.RxBytes != topPairs[i].RxBytes ||
			got.TotalBytes != topPairs[i].TotalBytes || got.FlowCount != topPairs[i].FlowCount {
			t.Fatalf("pair %d = %#v, top %#v", i, got, topPairs[i])
		}
	}
}

func sameRankPage(left, right rankPage) bool {
	return sameTalkers(left.talkers, right.talkers) && samePairs(left.pairs, right.pairs)
}

func sameTalkers(got, want []RankedTalker) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range want {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func samePairs(got, want []RankedPair) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range want {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func insertRankPair(t *testing.T, store *SQLiteStore, tailnet string, bucket int64, src, dst, traffic string, tx, rx, flows int64) {
	t.Helper()
	if err := store.UpsertNodePairAggregates(context.Background(), tailnet, []NodePairAggregate{{
		Bucket:        bucket,
		SrcNodeID:     src,
		DstNodeID:     dst,
		TrafficType:   traffic,
		TxBytes:       tx,
		RxBytes:       rx,
		TxPkts:        1,
		RxPkts:        1,
		FlowCount:     flows,
		Protocols:     "[6]",
		ProtocolBytes: fmt.Sprintf(`{"6":%d}`, tx+rx),
		Ports:         "[]",
	}}); err != nil {
		t.Fatal(err)
	}
}

func TestRankedTrafficFiltersBeforePaging(t *testing.T) {
	store := setupTestDB(t)
	start := time.Now().UTC().Add(-30 * time.Minute).Truncate(time.Minute)
	end := start.Add(10 * time.Minute)
	base := start.Unix()
	insertRankPair(t, store, DefaultTailnetID, base, "big", "peer", "virtual", 1000, 0, 1)
	insertRankPair(t, store, DefaultTailnetID, base, "a", "b", "virtual", 30, 0, 1)
	insertRankPair(t, store, DefaultTailnetID, base, "a", "10.1.2.3", "subnet", 20, 0, 1)
	insertRankPair(t, store, DefaultTailnetID, base, "c", "d", "virtual", 10, 0, 1)

	ctx := context.Background()
	talkers, more, err := store.ListRankedTalkers(ctx, DefaultTailnetID, start, end, RankQuery{Limit: 1, NodeIDs: []string{"a", "c"}})
	if err != nil || more != true || len(talkers) != 1 || talkers[0].NodeID != "a" || talkers[0].TotalBytes != 50 {
		t.Fatalf("filtered first page = %+v more=%v err=%v, want a (50) with more", talkers, more, err)
	}
	talkers, more, err = store.ListRankedTalkers(ctx, DefaultTailnetID, start, end, RankQuery{Limit: 1, Offset: 1, NodeIDs: []string{"a", "c"}})
	if err != nil || more || len(talkers) != 1 || talkers[0].NodeID != "c" {
		t.Fatalf("filtered second page = %+v more=%v err=%v, want c and no more", talkers, more, err)
	}
	talkers, _, err = store.ListRankedTalkers(ctx, DefaultTailnetID, start, end, RankQuery{Match: "10.1"})
	if err != nil || len(talkers) != 1 || talkers[0].NodeID != "10.1.2.3" {
		t.Fatalf("match talkers = %+v err=%v", talkers, err)
	}
	// LIKE wildcards in the search text are literal.
	talkers, _, err = store.ListRankedTalkers(ctx, DefaultTailnetID, start, end, RankQuery{Match: "%"})
	if err != nil || len(talkers) != 0 {
		t.Fatalf("literal %% match = %+v err=%v", talkers, err)
	}
	pairs, _, err := store.ListRankedPairs(ctx, DefaultTailnetID, start, end, RankQuery{NodeIDs: []string{"b"}, Match: "10.1"})
	if err != nil || len(pairs) != 2 || pairs[0].DstNodeID != "b" || pairs[1].DstNodeID != "10.1.2.3" {
		t.Fatalf("filtered pairs = %+v err=%v", pairs, err)
	}
}
