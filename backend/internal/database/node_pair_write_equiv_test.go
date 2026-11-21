package database

import (
	"context"
	"database/sql"
	"fmt"
	"math/rand"
	"reflect"
	"testing"
	"time"
)

// TestNodePairWriteMatchesLegacySQL feeds the same batches to the old
// per-row SQL upsert and to the new in-memory write path, then compares every
// stored column of node_pairs and node_pair_hours.
func TestNodePairWriteMatchesLegacySQL(t *testing.T) {
	ctx := context.Background()
	for _, seed := range []int64{1, 2, 3} {
		t.Run(fmt.Sprintf("seed%d", seed), func(t *testing.T) {
			oldStore := newTestStoreAt(t)
			newStore := newTestStoreAt(t)
			rng := rand.New(rand.NewSource(seed))
			base := (time.Now().UTC().Add(-3*time.Hour).Unix() / 3600) * 3600

			// Mark the first hour as rolled so late rows also exercise the
			// hourly delta path.
			seedRows := genEquivBatch(rng, base, 3, 60, false)
			for _, st := range []*SQLiteStore{oldStore, newStore} {
				if err := st.UpsertNodePairAggregates(ctx, "t1", nil); err != nil {
					t.Fatal(err)
				}
			}
			applyLegacy(t, oldStore, "t1", seedRows)
			if err := newStore.UpsertNodePairAggregates(ctx, "t1", seedRows); err != nil {
				t.Fatal(err)
			}
			for _, st := range []*SQLiteStore{oldStore, newStore} {
				if err := st.UpdatePollState(ctx, "t1", time.Unix(base+3600, 0).UTC()); err != nil {
					t.Fatal(err)
				}
			}

			for round := 0; round < 6; round++ {
				// Rounds reach back into rolled minutes, and repeat keys.
				rows := genEquivBatch(rng, base+int64(round%3)*1800, 4, 80, round >= 3)
				applyLegacy(t, oldStore, "t1", rows)
				if err := newStore.UpsertNodePairAggregates(ctx, "t1", rows); err != nil {
					t.Fatal(err)
				}
			}
			for _, table := range []string{"node_pairs", "node_pair_hours"} {
				got, want := dumpTable(t, newStore, table), dumpTable(t, oldStore, table)
				if len(want) == 0 {
					t.Fatalf("%s is empty, test would prove nothing", table)
				}
				if !reflect.DeepEqual(got, want) {
					for i := range want {
						if i >= len(got) || !reflect.DeepEqual(got[i], want[i]) {
							t.Fatalf("%s row %d differs:\n new: %v\n old: %v", table, i, safeIdx(got, i), want[i])
						}
					}
					t.Fatalf("%s: %d rows, want %d", table, len(got), len(want))
				}
			}
		})
	}
}

func safeIdx(rows [][]any, i int) any {
	if i < len(rows) {
		return rows[i]
	}
	return nil
}

func newTestStoreAt(t *testing.T) *SQLiteStore {
	t.Helper()
	return setupBenchDB(t)
}

func applyLegacy(t *testing.T, st *SQLiteStore, tailnet string, rows []NodePairAggregate) {
	t.Helper()
	ctx := context.Background()
	tx, err := st.writer.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err := legacyUpsertNodePairsTx(ctx, tx, tailnet, rows, hourTouches{}); err != nil {
		t.Fatal(err)
	}
	if err := rollClosedMinutes(ctx, tx, tailnet, (time.Now().UTC().Unix()/3600)*3600); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func dumpTable(t *testing.T, st *SQLiteStore, table string) [][]any {
	t.Helper()
	rows, err := st.db.Query("SELECT * FROM " + table + " ORDER BY tailnet_id, bucket, src_node_id, dst_node_id, traffic_type")
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
		for i, v := range vals {
			if b, ok := v.([]byte); ok {
				vals[i] = string(b)
			}
		}
		out = append(out, vals)
	}
	return out
}

var equivProtoText = []string{
	`{"6":100}`, `{"17":50,"6":10}`, `{"1":5,"6":7,"17":9}`, `{}`, ``, `not json`, `42`,
}

func genEquivBatch(rng *rand.Rand, base int64, minutes, perMinute int, messy bool) []NodePairAggregate {
	var out []NodePairAggregate
	nodes := 25
	for m := 0; m < minutes; m++ {
		for i := 0; i < perMinute; i++ {
			a, b := rng.Intn(nodes), rng.Intn(nodes)
			src, dst := fmt.Sprintf("n%02d", a), fmt.Sprintf("n%02d", b)
			if src > dst {
				src, dst = dst, src
			}
			g := &ingestGenerator{rng: rng, ports: []int{22, 53, 80, 443, 8080, 9000, 9001, 9002, 9003, 9004, 9005, 9006, 9007, 9008, 9009, 9010, 9011, 9012, 9013, 9014, 9015, 9016, 9017, 9018, 9019, 9020, 9021, 9022, 9023, 9024}}
			r := g.row(base+int64(m)*60+int64(rng.Intn(60)), src, dst)
			r.TrafficType = []string{"virtual", "subnet", "physical"}[rng.Intn(3)]
			if messy && rng.Intn(6) == 0 {
				r.ProtocolBytes = equivProtoText[rng.Intn(len(equivProtoText))]
				r.TxProtocolBytes = equivProtoText[rng.Intn(len(equivProtoText))]
				r.DirectionalPorts = rng.Intn(2) == 0
				if rng.Intn(2) == 0 {
					r.Ports = `[{"port":1,"proto":6,"bytes":5},{"port":1,"proto":6,"bytes":7}]`
				}
				if rng.Intn(3) == 0 {
					r.Ports = "garbage"
				}
			}
			out = append(out, r)
		}
	}
	// Duplicate keys inside one batch.
	for i := 0; i < perMinute/4 && i < len(out); i++ {
		d := out[rng.Intn(len(out))]
		d.TxBytes += int64(rng.Intn(100))
		out = append(out, d)
	}
	rng.Shuffle(len(out), func(i, j int) { out[i], out[j] = out[j], out[i] })
	return out
}

var _ = sql.ErrNoRows
