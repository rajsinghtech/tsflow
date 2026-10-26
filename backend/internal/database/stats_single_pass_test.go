package database

import (
	"context"
	"fmt"
	"math"
	"math/rand"
	"reflect"
	"strings"
	"testing"
	"time"
)

const statsPassBase int64 = 1_699_999_200 // on an hour boundary

type statsPassRow struct {
	bucket                   int64
	src, dst, traffic        string
	tx, rx, flows            any
	protocols, protocolBytes any
	ports                    any
}

func insertStatsPassRows(t *testing.T, store *SQLiteStore, tailnetID string, rows []statsPassRow) {
	t.Helper()
	tx, err := store.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	stmt, err := tx.Prepare(`
		INSERT OR REPLACE INTO node_pairs (
			tailnet_id, bucket, src_node_id, dst_node_id, traffic_type,
			tx_bytes, rx_bytes, tx_pkts, rx_pkts, flow_count,
			protocols, protocol_bytes, ports,
			tx_ports, rx_ports, tx_protocol_bytes, rx_protocol_bytes, directional_ports
		) VALUES (?, ?, ?, ?, ?, ?, ?, 1, 1, ?, ?, ?, ?, '[]', '[]', '{}', '{}', 0)`)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	for _, r := range rows {
		if _, err := stmt.Exec(tailnetID, r.bucket, r.src, r.dst, r.traffic, r.tx, r.rx, r.flows, r.protocols, r.protocolBytes, r.ports); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

// randomStatsRows writes rows in the shape the poller stores: several types,
// self pairs, protocol lists without byte totals, wide port lists and ties.
func randomStatsRows(rng *rand.Rand, base int64, minutes int) []statsPassRow {
	types := []string{"virtual", "virtual", "subnet", "exit", "physical"}
	nodes := []string{"n1", "n2", "n3", "n4", "n5", "100.64.0.9", "12345"}
	var rows []statsPassRow
	for m := 0; m < minutes; m++ {
		if rng.Intn(5) == 0 {
			continue // gaps
		}
		seen := map[string]bool{}
		for k := rng.Intn(6); k >= 0; k-- {
			src, dst := nodes[rng.Intn(len(nodes))], nodes[rng.Intn(len(nodes))]
			traffic := types[rng.Intn(len(types))]
			key := src + dst + traffic
			if seen[key] {
				continue
			}
			seen[key] = true
			tx, rx := int64(rng.Intn(5000)), int64(rng.Intn(5000))
			row := statsPassRow{
				bucket: base + int64(m)*60, src: src, dst: dst, traffic: traffic,
				tx: tx, rx: rx, flows: int64(rng.Intn(9)),
			}
			switch rng.Intn(4) {
			case 0: // only a protocol list, split evenly with the remainder on the first
				row.protocols, row.protocolBytes = "[6,17,1]", "{}"
			case 1:
				row.protocols, row.protocolBytes = "[6]", fmt.Sprintf(`{"6":%d}`, tx+rx)
			case 2:
				row.protocols, row.protocolBytes = "[6,17]", fmt.Sprintf(`{"6":%d,"17":%d}`, tx, rx)
			default:
				row.protocols, row.protocolBytes = "[]", fmt.Sprintf(`{"1":%d,"58":%d}`, tx, rx)
			}
			var ports []string
			for p := rng.Intn(25); p > 0; p-- {
				ports = append(ports, fmt.Sprintf(`{"port":%d,"proto":%d,"bytes":%d}`, rng.Intn(30)+1, []int{6, 17}[rng.Intn(2)], rng.Intn(4)*100))
			}
			row.ports = "[" + strings.Join(ports, ",") + "]"
			rows = append(rows, row)
		}
	}
	return rows
}

var statsPassTypeSets = [][]string{
	nil,
	{"virtual"},
	{"virtual", "subnet"},
	{"physical"},
	{"exit", "physical"},
	{"virtual", "subnet", "exit", "physical"},
}

func statsPassWindows(base int64) [][2]int64 {
	return [][2]int64{
		{base + 5*60, base + 50*60},           // minutes
		{base + 17*60, base + 2*3600 + 17*60}, // just over 2h, rollup with minute edges
		{base, base + 3*3600},                 // aligned hours
		{base + 30*60, base + 26*3600},        // daily buckets
		{base - 3*86400, base + 4*86400},      // a week
	}
}

// statsBothWays reads the derived stats with the single pass and with the
// SQL statements and fails if they differ in any field or in the error.
func statsBothWays(t *testing.T, store *SQLiteStore, tailnetID string, start, end int64, types []string) {
	t.Helper()
	ctx := context.Background()
	s, e := time.Unix(start, 0).UTC(), time.Unix(end, 0).UTC()
	read := func(disabled bool) ([]TrafficStats, []TrafficStats, error, error) {
		statsSinglePassDisabled = disabled
		defer func() { statsSinglePassDisabled = false }()
		typed, err1 := store.GetTrafficStatsFromNodePairsByTrafficTypes(ctx, tailnetID, s, e, types)
		var filled []TrafficStats
		var err2 error
		if types == nil {
			filled, err2 = store.FillMissingTrafficStats(ctx, tailnetID, s, e, nil)
		}
		return typed, filled, err1, err2
	}
	fastTyped, fastFilled, fastErr1, fastErr2 := read(false)
	sqlTyped, sqlFilled, sqlErr1, sqlErr2 := read(true)
	if (fastErr1 == nil) != (sqlErr1 == nil) || (fastErr2 == nil) != (sqlErr2 == nil) {
		t.Fatalf("[%d,%d) %v errors differ: fast %v/%v sql %v/%v", start, end, types, fastErr1, fastErr2, sqlErr1, sqlErr2)
	}
	if !reflect.DeepEqual(fastTyped, sqlTyped) {
		t.Fatalf("[%d,%d) %v stats differ\nfast: %+v\nsql:  %+v", start, end, types, fastTyped, sqlTyped)
	}
	if !reflect.DeepEqual(fastFilled, sqlFilled) {
		t.Fatalf("[%d,%d) filled stats differ\nfast: %+v\nsql:  %+v", start, end, fastFilled, sqlFilled)
	}
}

func TestStatsSinglePassMatchesSQL(t *testing.T) {
	for seed := int64(1); seed <= 4; seed++ {
		t.Run(fmt.Sprint("seed", seed), func(t *testing.T) {
			store := setupTestDB(t)
			ctx := context.Background()
			rng := rand.New(rand.NewSource(seed))
			insertStatsPassRows(t, store, DefaultTailnetID, randomStatsRows(rng, statsPassBase, 5*60))
			insertStatsPassRows(t, store, "other", randomStatsRows(rng, statsPassBase, 3*60))

			check := func() {
				t.Helper()
				for _, w := range statsPassWindows(statsPassBase) {
					for _, types := range statsPassTypeSets {
						statsBothWays(t, store, DefaultTailnetID, w[0], w[1], types)
					}
				}
			}
			check() // minute rows only
			if err := store.backfillHourRollups(ctx); err != nil {
				t.Fatal(err)
			}
			mark, err := readHourMark(ctx, store.db, DefaultTailnetID)
			if err != nil || mark < statsPassBase+3*3600 {
				t.Fatalf("mark = %d, %v", mark, err)
			}
			check() // hourly middles

			// Canonical rows must take the single pass, or the comparison
			// above only proves SQL equals SQL.
			tx, err := store.beginRead(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			if _, err := singlePassMinuteStats(ctx, tx, DefaultTailnetID, 60, [][2]int64{{statsPassBase, statsPassBase + 5*3600}}, nil); err != nil {
				t.Fatalf("minute single pass: %v", err)
			}
			plan := combineHourPlans([][2]int64{{statsPassBase + 17*60, statsPassBase + 4*3600}}, mark, true)
			if len(plan.hours) == 0 || len(plan.minutes) == 0 {
				t.Fatalf("plan = %+v, want hours and minute edges", plan)
			}
			if _, err := singlePassRollupStats(ctx, tx, DefaultTailnetID, 3600, plan, []string{"virtual"}); err != nil {
				t.Fatalf("rollup single pass: %v", err)
			}
		})
	}
}

// Rows outside the strict shape send the whole read to SQL, so the numbers
// (or the error) are whatever SQLite says.
func TestStatsSinglePassFallsBackOnUnusualRows(t *testing.T) {
	odd := map[string]statsPassRow{
		"spaced protocol bytes": {protocolBytes: `{ "6": 5 }`, protocols: "[6]", ports: "[]"},
		"bare number bytes":     {protocolBytes: `12`, protocols: "[6]", ports: "[]"},
		"null key on minute":    {protocolBytes: `{"null":5,"6":1}`, protocols: "[6]", ports: "[]"},
		"duplicate key":         {protocolBytes: `{"6":1,"6":2}`, protocols: "[6]", ports: "[]"},
		"broken bytes":          {protocolBytes: `{"6":`, protocols: "[6,17]", ports: "[]"},
		"text key":              {protocolBytes: `{"tcp":9}`, protocols: "[6]", ports: "[]"},
		"leading zero key":      {protocolBytes: `{"06":9}`, protocols: "[6]", ports: "[]"},
		"float bytes":           {protocolBytes: `{"6":1.5}`, protocols: "[6]", ports: "[]"},
		"null protocol bytes":   {protocolBytes: nil, protocols: "[6,17]", ports: "[]"},
		"empty protocol bytes":  {protocolBytes: "", protocols: "[17,6,1]", ports: "[]"},
		"broken protocol list":  {protocolBytes: "{}", protocols: "[6,", ports: "[]"},
		"null protocol list":    {protocolBytes: "{}", protocols: nil, ports: "[]"},
		"port extra key":        {protocolBytes: `{"6":3}`, protocols: "[6]", ports: `[{"port":1,"proto":6,"bytes":3,"x":1}]`},
		"port missing key":      {protocolBytes: `{"6":3}`, protocols: "[6]", ports: `[{"port":1,"bytes":3}]`},
		"port string value":     {protocolBytes: `{"6":3}`, protocols: "[6]", ports: `[{"port":"1","proto":6,"bytes":3}]`},
		"port float bytes":      {protocolBytes: `{"6":3}`, protocols: "[6]", ports: `[{"port":1,"proto":6,"bytes":2.5}]`},
		"port reordered keys":   {protocolBytes: `{"6":3}`, protocols: "[6]", ports: `[{"bytes":3,"proto":6,"port":1}]`},
		"broken ports":          {protocolBytes: `{"6":3}`, protocols: "[6]", ports: `[{"port":1`},
		"empty ports":           {protocolBytes: `{"6":3}`, protocols: "[6]", ports: ""},
		"null ports":            {protocolBytes: `{"6":3}`, protocols: "[6]", ports: nil},
		"blob ports":            {protocolBytes: `{"6":3}`, protocols: "[6]", ports: []byte(`[{"port":1,"proto":6,"bytes":3}]`)},
		"null tx":               {tx: nil, protocolBytes: `{"6":3}`, protocols: "[6]", ports: "[]"},
		"null flows":            {flows: nil, protocolBytes: `{"6":3}`, protocols: "[6]", ports: "[]"},
		"huge bytes":            {tx: int64(math.MaxInt64 - 1), protocolBytes: `{"6":9223372036854775806}`, protocols: "[6]", ports: `[{"port":1,"proto":6,"bytes":9223372036854775806}]`},
		"too big for int64":     {protocolBytes: `{"6":99999999999999999999}`, protocols: "[6]", ports: "[]"},
		"negative values":       {protocolBytes: `{"-1":-3}`, protocols: "[-1]", ports: `[{"port":-1,"proto":-6,"bytes":-3}]`},
	}
	for name, row := range odd {
		t.Run(name, func(t *testing.T) {
			store := setupTestDB(t)
			ctx := context.Background()
			rng := rand.New(rand.NewSource(7))
			insertStatsPassRows(t, store, DefaultTailnetID, randomStatsRows(rng, statsPassBase, 4*60))
			for _, bucket := range []int64{statsPassBase + 20*60, statsPassBase + 3600 + 20*60} {
				r := row
				r.bucket, r.src, r.dst, r.traffic = bucket, "odd", "n1", "virtual"
				if r.tx == nil && name != "null tx" {
					r.tx = int64(2)
				}
				if r.flows == nil && name != "null flows" {
					r.flows = int64(1)
				}
				r.rx = int64(1)
				insertStatsPassRows(t, store, DefaultTailnetID, []statsPassRow{r})
			}
			if err := store.backfillHourRollups(ctx); err != nil {
				t.Fatal(err)
			}
			for _, w := range statsPassWindows(statsPassBase) {
				for _, types := range statsPassTypeSets {
					statsBothWays(t, store, DefaultTailnetID, w[0], w[1], types)
				}
			}
		})
	}
}

func TestParseJSONIntIsStrict(t *testing.T) {
	good := map[string]int64{"0": 0, "7": 7, "-12": -12, "9223372036854775807": math.MaxInt64}
	for in, want := range good {
		got, end, ok := parseJSONInt([]byte(in), 0)
		if !ok || got != want || end != len(in) {
			t.Fatalf("%q = %d,%d,%v", in, got, end, ok)
		}
	}
	for _, in := range []string{"", "-", "-0", "01", "1.0", "1e3", "9223372036854775808", "-9223372036854775808", "+1", "a"} {
		if _, end, ok := parseJSONInt([]byte(in), 0); ok && end == len(in) {
			t.Fatalf("%q accepted", in)
		}
	}
}
