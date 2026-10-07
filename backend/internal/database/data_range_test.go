package database

import (
	"context"
	"testing"
	"time"
)

func TestContinuousBucketStart(t *testing.T) {
	minute := int64(60)
	hour := int64(3600)
	base := time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC).Unix()
	stray := time.Date(2026, 9, 29, 9, 45, 0, 0, time.UTC).Unix()

	series := func(start, minutes int64) []int64 {
		out := make([]int64, 0, minutes)
		for i := int64(0); i < minutes; i++ {
			out = append(out, start+i*minute)
		}
		return out
	}

	tests := []struct {
		name string
		in   []int64
		want int64
	}{
		{name: "empty", in: nil, want: 0},
		{name: "single", in: []int64{base}, want: base},
		{name: "continuous", in: series(base, 180), want: base},
		{
			name: "stray prefix",
			in:   append([]int64{stray, stray + minute}, series(base, 180)...),
			want: base,
		},
		{
			name: "short gap stays",
			in:   append(series(base, 30), series(base+2*hour, 30)...),
			want: base,
		},
		{
			name: "long early run stays",
			in:   append(series(base, 180), series(base+15*hour, 60)...),
			want: base,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, ok := continuousBucketStart(test.in)
			if len(test.in) == 0 {
				if ok {
					t.Fatal("empty input reported coverage")
				}
				return
			}
			if !ok || got != test.want {
				t.Fatalf("start = %d ok=%v, want %d", got, ok, test.want)
			}
		})
	}
}

func TestGetDataRangeSkipsStrayPrefix(t *testing.T) {
	store := setupTestDB(t)
	ctx := context.Background()
	base := time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC).Unix()
	stray := time.Date(2026, 9, 29, 9, 45, 0, 0, time.UTC).Unix()
	stray = stray / 60 * 60

	rows := []NodePairAggregate{
		{Bucket: stray, SrcNodeID: "a", DstNodeID: "b", TrafficType: "virtual", TxBytes: 10, Protocols: "[6]"},
		{Bucket: stray + 60, SrcNodeID: "a", DstNodeID: "b", TrafficType: "virtual", TxBytes: 8, Protocols: "[6]"},
	}
	stats := []TrafficStats{
		{Bucket: stray, TCPBytes: 10, TotalFlows: 1, TopPorts: "[]"},
		{Bucket: stray + 60, TCPBytes: 8, TotalFlows: 1, TopPorts: "[]"},
	}
	for i := int64(0); i < 180; i++ {
		bucket := base + i*60
		rows = append(rows, NodePairAggregate{
			Bucket: bucket, SrcNodeID: "a", DstNodeID: "b", TrafficType: "virtual", TxBytes: 100, Protocols: "[6]",
		})
		stats = append(stats, TrafficStats{Bucket: bucket, TCPBytes: 100, TotalFlows: 1, TopPorts: "[]"})
	}
	if err := store.UpsertNodePairAggregates(ctx, DefaultTailnetID, rows); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertTrafficStats(ctx, DefaultTailnetID, stats); err != nil {
		t.Fatal(err)
	}

	got, err := store.GetDataRange(ctx, DefaultTailnetID)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Earliest.Equal(time.Unix(base, 0).UTC()) {
		t.Fatalf("earliest = %s, want %s", got.Earliest, time.Unix(base, 0).UTC())
	}
	if !got.Latest.Equal(time.Unix(base+180*60, 0).UTC()) {
		t.Fatalf("latest = %s", got.Latest)
	}
	if got.Count != 180 {
		t.Fatalf("count = %d, want 180 covered rows", got.Count)
	}
}

func TestGetDataRangeKeepsInternalGap(t *testing.T) {
	store := setupTestDB(t)
	ctx := context.Background()
	base := time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC).Unix()
	rows := make([]NodePairAggregate, 0, 60)
	stats := make([]TrafficStats, 0, 60)
	for i := int64(0); i < 30; i++ {
		bucket := base + i*60
		rows = append(rows, NodePairAggregate{
			Bucket: bucket, SrcNodeID: "a", DstNodeID: "b", TrafficType: "virtual", TxBytes: 5, Protocols: "[6]",
		})
		stats = append(stats, TrafficStats{Bucket: bucket, TCPBytes: 5, TotalFlows: 1, TopPorts: "[]"})
	}
	later := base + 2*3600
	for i := int64(0); i < 30; i++ {
		bucket := later + i*60
		rows = append(rows, NodePairAggregate{
			Bucket: bucket, SrcNodeID: "a", DstNodeID: "b", TrafficType: "virtual", TxBytes: 5, Protocols: "[6]",
		})
		stats = append(stats, TrafficStats{Bucket: bucket, TCPBytes: 5, TotalFlows: 1, TopPorts: "[]"})
	}
	if err := store.UpsertNodePairAggregates(ctx, DefaultTailnetID, rows); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertTrafficStats(ctx, DefaultTailnetID, stats); err != nil {
		t.Fatal(err)
	}

	got, err := store.GetDataRange(ctx, DefaultTailnetID)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Earliest.Equal(time.Unix(base, 0).UTC()) {
		t.Fatalf("earliest = %s, want the start before the short gap", got.Earliest)
	}
	if got.Count != 60 {
		t.Fatalf("count = %d, want 60", got.Count)
	}
}
