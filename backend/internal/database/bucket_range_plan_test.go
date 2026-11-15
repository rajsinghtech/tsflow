package database

import (
	"context"
	"strings"
	"testing"
)

func TestPartialHourEdgesUseBoundedIndexSearches(t *testing.T) {
	store := setupTestDB(t)
	const base int64 = 1_699_999_200
	plan := hourPlan{
		minutes: [][2]int64{{base + 17*60, base + 3600}, {base + 70*3600, base + 70*3600 + 43*60}},
		hours:   [][2]int64{{base + 3600, base + 70*3600}},
	}
	source, args := plan.unionPairRows(DefaultTailnetID,
		"src_node_id, dst_node_id, tx_bytes, rx_bytes, flow_count",
		"src_node_id, dst_node_id, tx_bytes, rx_bytes, flow_count",
		" AND traffic_type IN (?, ?)", []any{"virtual", "subnet"},
	)
	details := explain(t, store, "EXPLAIN QUERY PLAN SELECT * FROM ("+source+")", args...)
	for _, line := range strings.Split(details, "\n") {
		if strings.Contains(line, "SEARCH node_pairs ") && !strings.Contains(line, "bucket>?") {
			t.Fatalf("partial-hour edges scan the whole tailnet instead of their time ranges:\n%s", details)
		}
	}
	if !strings.Contains(details, "bucket>?") {
		t.Fatalf("missing bounded bucket search:\n%s", details)
	}
}

func TestTailnetBucketRangesKeepOverlapsAndHalfOpenBounds(t *testing.T) {
	store := setupTestDB(t)
	const base int64 = 1_699_999_200
	for i := int64(0); i < 5; i++ {
		insertRankPair(t, store, DefaultTailnetID, base+i*60, "a", "b", "virtual", 1<<i, 0, 1)
	}
	insertRankPair(t, store, "other", base+120, "a", "b", "virtual", 1000, 0, 1)
	ranges := [][2]int64{{base + 60, base + 180}, {base + 120, base + 240}}
	for _, alias := range []string{"", "np"} {
		t.Run("alias="+alias, func(t *testing.T) {
			predicate, args := tailnetBucketRangePredicate(DefaultTailnetID, alias, ranges)
			var count, total int64
			err := store.db.QueryRowContext(context.Background(),
				"SELECT COUNT(*), SUM(tx_bytes) FROM node_pairs "+alias+" WHERE "+predicate,
				args...,
			).Scan(&count, &total)
			if err != nil {
				t.Fatal(err)
			}
			if count != 3 || total != 14 {
				t.Fatalf("range rows=%d total=%d, want 3 rows totaling 14", count, total)
			}
		})
	}
}
