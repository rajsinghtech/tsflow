package database

import (
	"context"
	"database/sql"
	"reflect"
	"strconv"
	"testing"
	"time"
)

func TestPlanHoursThroughMarkUsesTheFillingHour(t *testing.T) {
	const hour int64 = 3600
	// Minutes are merged through 4:29, so hour 4 is filling up to 4:30.
	mark := 4*hour + 29*60
	cases := []struct {
		name       string
		start, end int64
		minutes    [][2]int64
		hours      [][2]int64
	}{
		{
			name:    "live window ends after the mark",
			start:   hour + 15*60,
			end:     4*hour + 32*60,
			minutes: [][2]int64{{hour + 15*60, 2 * hour}, {4*hour + 30*60, 4*hour + 32*60}},
			hours:   [][2]int64{{2 * hour, 4*hour + 30*60}},
		},
		{
			name:  "window ends exactly at the mark",
			start: 2 * hour,
			end:   4*hour + 30*60,
			hours: [][2]int64{{2 * hour, 4*hour + 30*60}},
		},
		{
			name:    "one-hour live window",
			start:   3*hour + 40*60,
			end:     4*hour + 40*60,
			minutes: [][2]int64{{3*hour + 40*60, 4 * hour}, {4*hour + 30*60, 4*hour + 40*60}},
			hours:   [][2]int64{{4 * hour, 4*hour + 30*60}},
		},
		{
			name:    "window ends before the mark keeps the old plan",
			start:   2 * hour,
			end:     4*hour + 10*60,
			minutes: [][2]int64{{4 * hour, 4*hour + 10*60}},
			hours:   [][2]int64{{2 * hour, 4 * hour}},
		},
		{
			name:    "window starts inside the filling hour",
			start:   4*hour + 5*60,
			end:     4*hour + 40*60,
			minutes: [][2]int64{{4*hour + 5*60, 4*hour + 40*60}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := planHoursThroughMark(tc.start, tc.end, mark)
			if !reflect.DeepEqual(got.minutes, tc.minutes) || !reflect.DeepEqual(got.hours, tc.hours) {
				t.Fatalf("plan minutes=%v hours=%v, want minutes=%v hours=%v", got.minutes, got.hours, tc.minutes, tc.hours)
			}
		})
	}
	// An hour-aligned mark has no filling hour: same as planHours.
	aligned := int64(4*hour - 60)
	for _, w := range [][2]int64{{hour + 60, 5 * hour}, {0, 4 * hour}, {3 * hour, 3*hour + 60}} {
		if got, want := planHoursThroughMark(w[0], w[1], aligned), planHours(w[0], w[1], aligned); !reflect.DeepEqual(got, want) {
			t.Fatalf("aligned mark window %v: got %+v want %+v", w, got, want)
		}
	}
	if got := planHoursThroughMark(0, 5*hour, -1); !reflect.DeepEqual(got, planHours(0, 5*hour, -1)) {
		t.Fatalf("no mark: %+v", got)
	}
}

// The graph read must match a plain minute scan when the newest hour is
// only partly rolled up, including a late write into a closed minute of that
// hour and minutes that are still open.
func TestGraphReadMatchesMinuteScanWithFillingHour(t *testing.T) {
	store := setupTestDB(t)
	ctx := context.Background()
	const base int64 = 1_699_999_200 // hour-aligned
	row := func(bucket int64, src, dst string, tx int64, port int) NodePairAggregate {
		return NodePairAggregate{
			Bucket: bucket, SrcNodeID: src, DstNodeID: dst, TrafficType: "virtual",
			TxBytes: tx, RxBytes: tx / 2, TxPkts: 1, RxPkts: 1, FlowCount: 1,
			Protocols: "[6]", ProtocolBytes: `{"6":` + strconv.FormatInt(tx+tx/2, 10) + `}`,
			Ports:            `[{"port":` + strconv.FormatInt(int64(port), 10) + `,"proto":6,"bytes":` + strconv.FormatInt(tx+tx/2, 10) + `}]`,
			TxPorts:          `[{"port":` + strconv.FormatInt(int64(port), 10) + `,"proto":6,"bytes":` + strconv.FormatInt(tx, 10) + `}]`,
			RxPorts:          `[{"port":` + strconv.FormatInt(int64(port), 10) + `,"proto":6,"bytes":` + strconv.FormatInt(tx/2, 10) + `}]`,
			TxProtocolBytes:  `{"6":` + strconv.FormatInt(tx, 10) + `}`,
			RxProtocolBytes:  `{"6":` + strconv.FormatInt(tx/2, 10) + `}`,
			DirectionalPorts: true,
		}
	}
	var rows []NodePairAggregate
	// Two full hours and 25 minutes of a third.
	for m := int64(0); m < 145; m++ {
		rows = append(rows, row(base+m*60, "a", "b", 100+m, 443))
		if m%7 == 0 {
			rows = append(rows, row(base+m*60, "a", "c", 50+m, 22))
		}
		if m >= 125 {
			rows = append(rows, row(base+m*60, "c", "d", 10+m, 5432)) // only in the filling hour
		}
	}
	// Close minutes through 2:19, so hour 2 is filling and 2:20-2:24 stay open.
	if err := store.CommitPollResults(ctx, DefaultTailnetID, PollResults{
		NodePairs: rows,
		PollEnd:   time.Unix(base+140*60, 0).UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	mark, err := readHourMark(ctx, store.db, DefaultTailnetID)
	if err != nil {
		t.Fatal(err)
	}
	if mark != base+139*60 {
		t.Fatalf("mark = %d, want %d", mark, base+139*60)
	}
	// A late write into a closed minute of the filling hour, committed by a
	// poll that does not move the mark.
	if err := store.CommitPollResults(ctx, DefaultTailnetID, PollResults{
		NodePairs: []NodePairAggregate{row(base+130*60, "a", "b", 9999, 8080)},
		PollEnd:   time.Unix(base+140*60, 0).UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	if mark, err = readHourMark(ctx, store.db, DefaultTailnetID); err != nil || mark != base+139*60 {
		t.Fatalf("mark after late write = %d (%v)", mark, err)
	}
	// The fixture must reach the filling-hour plan, or the loop below only
	// re-tests planHours.
	tx, err := store.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := store.snapshotHourPlan(ctx, tx, DefaultTailnetID, base, base+145*60)
	_ = tx.Rollback()
	if err != nil {
		t.Fatal(err)
	}
	if want := [][2]int64{{base, base + 140*60}}; !reflect.DeepEqual(plan.hours, want) {
		t.Fatalf("plan hours = %v, want %v", plan.hours, want)
	}
	ends := []int64{140, 142, 145, 150}
	starts := []int64{0, 17, 60, 119, 120, 121, 131}
	for _, e := range ends {
		for _, s := range starts {
			assertSamePairAPI(t, store, DefaultTailnetID, time.Unix(base+s*60, 0).UTC(), time.Unix(base+e*60, 0).UTC())
		}
	}
}
