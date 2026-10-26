package database

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

// bucketRowsFromTable and bucketRowsFromPairs return per-bucket counts keyed
// by tailnet and bucket.
func bucketRowsFromTable(t *testing.T, s *SQLiteStore) map[[2]any]int64 {
	t.Helper()
	return readBucketCounts(t, s, "SELECT tailnet_id, bucket, rows FROM node_pair_bucket_rows")
}

func bucketRowsFromPairs(t *testing.T, s *SQLiteStore) map[[2]any]int64 {
	t.Helper()
	return readBucketCounts(t, s, "SELECT tailnet_id, bucket, COUNT(*) FROM node_pairs GROUP BY tailnet_id, bucket")
}

func readBucketCounts(t *testing.T, s *SQLiteStore, query string) map[[2]any]int64 {
	t.Helper()
	rows, err := s.db.Query(query)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := map[[2]any]int64{}
	for rows.Next() {
		var tailnet string
		var bucket, n int64
		if err := rows.Scan(&tailnet, &bucket, &n); err != nil {
			t.Fatal(err)
		}
		out[[2]any{tailnet, bucket}] = n
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// assertRangeMatchesCount checks the counted data range, the scanning one,
// and the poller stats against each other for one tailnet.
func assertRangeMatchesCount(t *testing.T, s *SQLiteStore, tailnetID string) *DataRange {
	t.Helper()
	ctx := context.Background()
	ready, err := bucketRowsReady(ctx, s.db)
	if err != nil {
		t.Fatal(err)
	}
	if !ready {
		t.Fatal("pair row counters are not ready")
	}
	counted, err := readDataRangeWith(ctx, s.db, tailnetID, true)
	if err != nil {
		t.Fatal(err)
	}
	scanned, err := readDataRangeWith(ctx, s.db, tailnetID, false)
	if err != nil {
		t.Fatal(err)
	}
	legacy := legacyDataRange(t, s, tailnetID)
	if !reflect.DeepEqual(counted, scanned) || !reflect.DeepEqual(counted, legacy) {
		t.Fatalf("data range differs: counted %+v scanned %+v legacy %+v", counted, scanned, legacy)
	}
	stats, err := s.GetStats(ctx, tailnetID)
	if err != nil {
		t.Fatal(err)
	}
	var total int64
	if err := s.db.QueryRow("SELECT COUNT(*) FROM node_pairs WHERE tailnet_id = ?", tailnetID).Scan(&total); err != nil {
		t.Fatal(err)
	}
	if got := stats["tableCounts"].(map[string]int64)["node_pairs"]; got != total {
		t.Fatalf("GetStats node_pairs = %d, want %d", got, total)
	}
	if got := stats["dataRange"].(*DataRange); !reflect.DeepEqual(got, legacy) {
		t.Fatalf("GetStats dataRange = %+v, want %+v", got, legacy)
	}
	return counted
}

// legacyDataRange is the data range as it was computed before the counters:
// one MIN/MAX/COUNT statement and a second count for a trimmed start.
func legacyDataRange(t *testing.T, s *SQLiteStore, tailnetID string) *DataRange {
	t.Helper()
	ctx := context.Background()
	var minBucket, maxBucket *int64
	var count int64
	if err := s.db.QueryRow("SELECT MIN(bucket), MAX(bucket), COUNT(*) FROM node_pairs WHERE tailnet_id = ?", tailnetID).Scan(&minBucket, &maxBucket, &count); err != nil {
		t.Fatal(err)
	}
	if count == 0 || minBucket == nil || maxBucket == nil {
		return &DataRange{}
	}
	coverage, err := coverageBuckets(ctx, s.db, tailnetID)
	if err != nil {
		t.Fatal(err)
	}
	start := *minBucket
	if len(coverage) > 0 {
		if trimmed, ok := continuousBucketStart(coverage); ok {
			start = trimmed
		}
	}
	covered := count
	if start != *minBucket {
		if err := s.db.QueryRow("SELECT COUNT(*) FROM node_pairs WHERE tailnet_id = ? AND bucket >= ?", tailnetID, start).Scan(&covered); err != nil {
			t.Fatal(err)
		}
	}
	return &DataRange{Earliest: time.Unix(start, 0).UTC(), Latest: time.Unix(*maxBucket+60, 0).UTC(), Count: covered}
}

func pairRows(base int64, minutes int, pairs ...string) []NodePairAggregate {
	var out []NodePairAggregate
	for i := 0; i < minutes; i++ {
		for j := 0; j+1 < len(pairs); j += 2 {
			out = append(out, NodePairAggregate{
				Bucket: base + int64(i)*60, SrcNodeID: pairs[j], DstNodeID: pairs[j+1],
				TrafficType: "virtual", TxBytes: 10, RxBytes: 5, Protocols: "[6]",
			})
		}
	}
	return out
}

func TestBucketRowCountsTrackEveryWrite(t *testing.T) {
	store := setupTestDB(t)
	ctx := context.Background()
	const other = "other.example"
	now := time.Now().UTC().Unix() / 60 * 60
	old := now - 10*24*3600
	recent := now - 3*3600

	// An old stray burst, a long gap, then continuous traffic.
	if err := store.UpsertNodePairAggregates(ctx, DefaultTailnetID, pairRows(old, 3, "a", "b")); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertNodePairAggregates(ctx, DefaultTailnetID, pairRows(recent, 120, "a", "b", "b", "c", "c", "a")); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertNodePairAggregates(ctx, other, pairRows(recent, 30, "x", "y")); err != nil {
		t.Fatal(err)
	}
	var stats []TrafficStats
	for _, b := range []int64{old, old + 60, old + 120} {
		stats = append(stats, TrafficStats{Bucket: b, TCPBytes: 15, TotalFlows: 1, TopPorts: "[]"})
	}
	for i := int64(0); i < 120; i++ {
		stats = append(stats, TrafficStats{Bucket: recent + i*60, TCPBytes: 45, TotalFlows: 3, TopPorts: "[]"})
	}
	if err := store.UpsertTrafficStats(ctx, DefaultTailnetID, stats); err != nil {
		t.Fatal(err)
	}
	if got, want := bucketRowsFromTable(t, store), bucketRowsFromPairs(t, store); !reflect.DeepEqual(got, want) {
		t.Fatalf("after insert: counters %v, pairs %v", got, want)
	}
	dr := assertRangeMatchesCount(t, store, DefaultTailnetID)
	if dr.Count != 360 || !dr.Earliest.Equal(time.Unix(recent, 0).UTC()) {
		t.Fatalf("range = %+v, want 360 rows from %d", dr, recent)
	}
	assertRangeMatchesCount(t, store, other)

	// A second poll that merges into the same pairs adds no rows, and one new
	// pair in an existing minute adds one.
	merge := pairRows(recent, 120, "a", "b")
	merge = append(merge, NodePairAggregate{Bucket: recent, SrcNodeID: "d", DstNodeID: "a", TrafficType: "subnet", TxBytes: 1, Protocols: "[17]"})
	if err := store.UpsertNodePairAggregates(ctx, DefaultTailnetID, merge); err != nil {
		t.Fatal(err)
	}
	if got, want := bucketRowsFromTable(t, store), bucketRowsFromPairs(t, store); !reflect.DeepEqual(got, want) {
		t.Fatalf("after merge: counters %v, pairs %v", got, want)
	}
	if dr := assertRangeMatchesCount(t, store, DefaultTailnetID); dr.Count != 361 {
		t.Fatalf("count after merge = %d, want 361", dr.Count)
	}

	// Retention removes the old burst and its counters.
	if _, err := store.Cleanup(ctx, DefaultTailnetID, 7*24*time.Hour); err != nil {
		t.Fatal(err)
	}
	if got, want := bucketRowsFromTable(t, store), bucketRowsFromPairs(t, store); !reflect.DeepEqual(got, want) {
		t.Fatalf("after cleanup: counters %v, pairs %v", got, want)
	}
	assertRangeMatchesCount(t, store, DefaultTailnetID)

	// An older binary's retention deletes a prefix without touching the
	// counters. Totals still match, and the next Init drops the leftovers.
	if _, err := store.writer.Exec("DELETE FROM node_pairs WHERE tailnet_id = ? AND bucket < ?", DefaultTailnetID, recent+30*60); err != nil {
		t.Fatal(err)
	}
	if _, err := store.writer.Exec("DELETE FROM node_pairs WHERE tailnet_id = ?", other); err != nil {
		t.Fatal(err)
	}
	if dr := assertRangeMatchesCount(t, store, DefaultTailnetID); dr.Count != 270 {
		t.Fatalf("count after old-binary cleanup = %d, want 270", dr.Count)
	}
	if dr := assertRangeMatchesCount(t, store, other); !reflect.DeepEqual(dr, &DataRange{}) {
		t.Fatalf("empty tailnet range = %+v", dr)
	}
	if err := store.Init(ctx); err != nil {
		t.Fatal(err)
	}
	if got, want := bucketRowsFromTable(t, store), bucketRowsFromPairs(t, store); !reflect.DeepEqual(got, want) {
		t.Fatalf("after re-init: counters %v, pairs %v", got, want)
	}
	assertRangeMatchesCount(t, store, DefaultTailnetID)
}

// An existing database has node_pairs rows but no counters. Init builds them
// once, and a second Init does not count anything twice.
func TestBucketRowCountsUpgradeExistingDatabase(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "upgrade.db")
	ctx := context.Background()
	store, err := NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Init(ctx); err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC).Unix()
	if err := store.UpsertNodePairAggregates(ctx, DefaultTailnetID, pairRows(base, 90, "a", "b", "b", "a")); err != nil {
		t.Fatal(err)
	}
	// Roll the schema back to what the previous release left on disk.
	if _, err := store.writer.Exec("DROP TRIGGER " + bucketRowsTrigger); err != nil {
		t.Fatal(err)
	}
	if _, err := store.writer.Exec("DROP TABLE node_pair_bucket_rows"); err != nil {
		t.Fatal(err)
	}
	// Rows written without counters, as the previous release did.
	if err := store.UpsertNodePairAggregates(ctx, DefaultTailnetID, pairRows(base+90*60, 30, "c", "d")); err != nil {
		t.Fatal(err)
	}
	if ready, err := bucketRowsReady(ctx, store.db); err != nil || ready {
		t.Fatalf("ready = %v err = %v before upgrade", ready, err)
	}
	// Reads before the upgrade still count node_pairs directly.
	before, err := store.GetDataRange(ctx, DefaultTailnetID)
	if err != nil {
		t.Fatal(err)
	}
	if before.Count != 210 {
		t.Fatalf("count before upgrade = %d, want 210", before.Count)
	}
	store.Close()

	for i := 0; i < 2; i++ {
		store, err = NewSQLiteStore(dbPath)
		if err != nil {
			t.Fatal(err)
		}
		if err := store.Init(ctx); err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			// Init leaves the existing rows to the background build, and
			// reads keep counting node_pairs meanwhile.
			if ready, err := bucketRowsReady(ctx, store.db); err != nil || ready {
				t.Fatalf("ready = %v err = %v before the build", ready, err)
			}
			if dr, err := store.GetDataRange(ctx, DefaultTailnetID); err != nil || !reflect.DeepEqual(dr, before) {
				t.Fatalf("range before build = %+v err = %v, want %+v", dr, err, before)
			}
			// A poll lands between Init and the build.
			if err := store.UpsertNodePairAggregates(ctx, DefaultTailnetID, pairRows(base+60*60, 60, "e", "f")); err != nil {
				t.Fatal(err)
			}
			if before, err = store.GetDataRange(ctx, DefaultTailnetID); err != nil || before.Count != 270 {
				t.Fatalf("range after poll = %+v err = %v, want 270 rows", before, err)
			}
		}
		if err := store.BuildPairRowCounts(ctx); err != nil {
			t.Fatal(err)
		}
		if got, want := bucketRowsFromTable(t, store), bucketRowsFromPairs(t, store); !reflect.DeepEqual(got, want) {
			t.Fatalf("init %d: counters %v, pairs %v", i, got, want)
		}
		if dr := assertRangeMatchesCount(t, store, DefaultTailnetID); !reflect.DeepEqual(dr, before) {
			t.Fatalf("init %d: range %+v, want %+v", i, dr, before)
		}
		store.Close()
	}
}

// A missing trigger means the counters may have missed writes, so reads fall
// back to counting node_pairs until Init rebuilds them.
func TestBucketRowCountsFallBackWhenTriggerMissing(t *testing.T) {
	store := setupTestDB(t)
	ctx := context.Background()
	base := time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC).Unix()
	if err := store.UpsertNodePairAggregates(ctx, DefaultTailnetID, pairRows(base, 10, "a", "b")); err != nil {
		t.Fatal(err)
	}
	if _, err := store.writer.Exec("DROP TRIGGER " + bucketRowsTrigger); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertNodePairAggregates(ctx, DefaultTailnetID, pairRows(base+600, 5, "a", "b")); err != nil {
		t.Fatal(err)
	}
	dr, err := store.GetDataRange(ctx, DefaultTailnetID)
	if err != nil {
		t.Fatal(err)
	}
	if dr.Count != 15 {
		t.Fatalf("count = %d, want 15 from node_pairs", dr.Count)
	}
	if err := store.Init(ctx); err != nil {
		t.Fatal(err)
	}
	if err := store.BuildPairRowCounts(ctx); err != nil {
		t.Fatal(err)
	}
	assertRangeMatchesCount(t, store, DefaultTailnetID)
}

// Stats coverage that starts after the newest pair still reports that start
// with zero covered rows, as the single-statement read did.
func TestBucketRowCountsCoverageAfterNewestPair(t *testing.T) {
	store := setupTestDB(t)
	ctx := context.Background()
	base := time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC).Unix()
	if err := store.UpsertNodePairAggregates(ctx, DefaultTailnetID, pairRows(base, 3, "a", "b")); err != nil {
		t.Fatal(err)
	}
	later := base + 12*3600
	var stats []TrafficStats
	for _, b := range []int64{base, base + 60, base + 120} {
		stats = append(stats, TrafficStats{Bucket: b, TCPBytes: 15, TotalFlows: 1, TopPorts: "[]"})
	}
	for i := int64(0); i < 60; i++ {
		stats = append(stats, TrafficStats{Bucket: later + i*60, TCPBytes: 15, TotalFlows: 1, TopPorts: "[]"})
	}
	if err := store.UpsertTrafficStats(ctx, DefaultTailnetID, stats); err != nil {
		t.Fatal(err)
	}
	dr := assertRangeMatchesCount(t, store, DefaultTailnetID)
	if dr.Count != 0 || !dr.Earliest.Equal(time.Unix(later, 0).UTC()) {
		t.Fatalf("range = %+v, want zero rows from %d", dr, later)
	}
}
