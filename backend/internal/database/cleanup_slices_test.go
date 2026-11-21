package database

import (
	"context"
	"math/rand"
	"reflect"
	"testing"
	"time"
)

// TestCleanupSlicesMatchOnePass runs retention with small slices and with one
// slice over the same data. Every table must end up identical.
func TestCleanupSlicesMatchOnePass(t *testing.T) {
	ctx := context.Background()
	build := func() *SQLiteStore {
		st := setupBenchDB(t)
		rng := rand.New(rand.NewSource(9))
		base := (time.Now().UTC().Add(-5*time.Hour).Unix() / 3600) * 3600
		rows := genEquivBatch(rng, base, 200, 20, true)
		if err := st.CommitPollResults(ctx, "t1", PollResults{
			NodePairs:     rows,
			Bandwidth:     nil,
			NodeBandwidth: []NodeBandwidth{{Bucket: base + 30, NodeID: "n01", TxBytes: 1}, {Bucket: base + 7200, NodeID: "n01", TxBytes: 2}},
			TrafficStats:  []TrafficStats{{Bucket: base + 60, TotalFlows: 3, TopPorts: "[]"}, {Bucket: base + 9000, TotalFlows: 4, TopPorts: "[]"}},
			PollEnd:       time.Unix(base+4*3600, 0).UTC(),
		}); err != nil {
			t.Fatal(err)
		}
		return st
	}
	sliced, whole := build(), build()
	prev := cleanupSliceSeconds
	t.Cleanup(func() { cleanupSliceSeconds = prev })

	retention := 100 * time.Minute
	cleanupSliceSeconds = 180
	n1, err := sliced.Cleanup(ctx, "t1", retention)
	if err != nil {
		t.Fatal(err)
	}
	cleanupSliceSeconds = 1 << 40
	n2, err := whole.Cleanup(ctx, "t1", retention)
	if err != nil {
		t.Fatal(err)
	}
	if n1 == 0 || n1 != n2 {
		t.Fatalf("deleted %d rows in slices, %d in one pass", n1, n2)
	}
	for _, table := range []string{"node_pairs", "node_pair_hours", "bandwidth_by_node", "traffic_stats", "node_pair_bucket_rows"} {
		q := "SELECT * FROM " + table + " ORDER BY 1, 2"
		if table == "node_pairs" || table == "node_pair_hours" {
			q = "SELECT * FROM " + table + " ORDER BY tailnet_id, bucket, src_node_id, dst_node_id, traffic_type"
		}
		got, want := queryAll(t, sliced, q), queryAll(t, whole, q)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("%s differs after sliced cleanup: %d rows vs %d", table, len(got), len(want))
		}
	}
}

func queryAll(t *testing.T, st *SQLiteStore, q string) [][]any {
	t.Helper()
	rows, err := st.db.Query(q)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	cols, _ := rows.Columns()
	var out [][]any
	for rows.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			t.Fatal(err)
		}
		out = append(out, vals)
	}
	return out
}
