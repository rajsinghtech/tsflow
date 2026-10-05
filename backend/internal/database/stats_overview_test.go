package database

import (
	"context"
	"reflect"
	"testing"
	"time"
)

func TestMissingAggregatedRanges(t *testing.T) {
	start := time.Unix(100, 0).UTC()
	end := time.Unix(250, 0).UTC()

	got := missingAggregatedRanges([]TrafficStats{{Bucket: 120}}, start, end)
	want := [][2]int64{{100, 120}, {180, 250}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("partial cover = %v, want %v", got, want)
	}

	got = missingAggregatedRanges([]TrafficStats{
		{Bucket: 60}, {Bucket: 120}, {Bucket: 180}, {Bucket: 240},
	}, start, end)
	if len(got) != 0 {
		t.Fatalf("full cover = %v, want none", got)
	}

	got = missingAggregatedRanges(nil, start, end)
	want = [][2]int64{{100, 250}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("empty primary = %v, want %v", got, want)
	}

	hourStart := time.Unix(0, 0).UTC()
	hourEnd := time.Unix(3*3600, 0).UTC()
	got = missingAggregatedRanges([]TrafficStats{{Bucket: 0}, {Bucket: 3600}}, hourStart, hourEnd)
	want = [][2]int64{{7200, 10800}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("hour gap = %v, want %v", got, want)
	}
}

func TestFillMissingTrafficStatsMatchesFullRead(t *testing.T) {
	store := setupTestDB(t)
	ctx := context.Background()
	const base int64 = 1800000000

	seedOverviewFixture(t, store, base)

	cases := []struct {
		name   string
		start  int64
		end    int64
		ranges map[string][][2]int64
	}{
		{
			name:  "minute gap",
			start: base,
			end:   base + 4*60,
			ranges: map[string][][2]int64{
				DefaultTailnetID: {{base + 60, base + 120}, {base + 180, base + 240}},
				"other":          {{base + 60, base + 240}},
			},
		},
		{
			name:  "covered minutes",
			start: base + 3600,
			end:   base + 3600 + 3*60,
			ranges: map[string][][2]int64{
				DefaultTailnetID: nil,
				"other":          nil,
			},
		},
		{
			name:  "no traffic stats",
			start: base + 7200,
			end:   base + 7200 + 60,
			ranges: map[string][][2]int64{
				DefaultTailnetID: {{base + 7200, base + 7200 + 60}},
				"other":          {{base + 7200, base + 7200 + 60}},
			},
		},
		{
			name:  "hour gap",
			start: base + 86400,
			end:   base + 86400 + 3*3600,
			ranges: map[string][][2]int64{
				DefaultTailnetID: {{base + 86400 + 3600, base + 86400 + 2*3600}},
				"other":          {{base + 86400 + 3600, base + 86400 + 3*3600}},
			},
		},
		{
			name:  "unaligned minute gap",
			start: base + 10,
			end:   base + 10 + 180,
			ranges: map[string][][2]int64{
				DefaultTailnetID: {{base + 10, base + 120}, {base + 180, base + 190}},
				"other":          {{base + 10, base + 190}},
			},
		},
	}

	for _, tailnetID := range []string{DefaultTailnetID, "other"} {
		for _, tc := range cases {
			t.Run(tailnetID+"/"+tc.name, func(t *testing.T) {
				start := time.Unix(tc.start, 0).UTC()
				end := time.Unix(tc.end, 0).UTC()
				primary, err := store.GetTrafficStats(ctx, tailnetID, start, end)
				if err != nil {
					t.Fatal(err)
				}
				full, err := store.GetTrafficStatsFromNodePairs(ctx, tailnetID, start, end)
				if err != nil {
					t.Fatal(err)
				}
				want := statsOutside(primary, full)
				wantRanges := tc.ranges[tailnetID]

				var queried [][2]int64
				store.derivedStatsHook = func(ranges [][2]int64) {
					queried = append([][2]int64(nil), ranges...)
				}
				t.Cleanup(func() { store.derivedStatsHook = nil })
				before := store.DerivedStatsScanCount()
				got, err := store.FillMissingTrafficStats(ctx, tailnetID, start, end, primary)
				if err != nil {
					t.Fatal(err)
				}
				scans := store.DerivedStatsScanCount() - before
				if len(wantRanges) == 0 {
					if scans != 0 {
						t.Fatalf("covered window scanned node_pairs %d times", scans)
					}
					if queried != nil {
						t.Fatalf("covered window queried ranges %v", queried)
					}
				} else if scans != 1 {
					t.Fatalf("gap scan count = %d, want 1", scans)
				} else if !reflect.DeepEqual(queried, wantRanges) {
					t.Fatalf("queried ranges = %v, want %v", queried, wantRanges)
				}
				assertTrafficStatsEqual(t, got, want)
				if tc.name == "no traffic stats" && len(got) != 1 {
					t.Fatalf("expected one derived bucket, got %#v", got)
				}

				// The other tailnet's bytes must not show up in default, and
				// the reverse. Compare a field that the fixture sets far apart.
				if tailnetID == DefaultTailnetID {
					for _, bucket := range got {
						if bucket.TCPBytes > 10000 || bucket.VirtualBytes > 10000 {
							t.Fatalf("default bucket includes the other tailnet: %+v", bucket)
						}
					}
				}
			})
		}
	}
}

func statsOutside(primary, derived []TrafficStats) []TrafficStats {
	covered := make(map[int64]struct{}, len(primary))
	for _, bucket := range primary {
		covered[bucket.Bucket] = struct{}{}
	}
	var out []TrafficStats
	for _, bucket := range derived {
		if _, ok := covered[bucket.Bucket]; !ok {
			out = append(out, bucket)
		}
	}
	return out
}

func assertTrafficStatsEqual(t *testing.T, got, want []TrafficStats) {
	t.Helper()
	if len(got) == 0 && len(want) == 0 {
		return
	}
	if len(got) != len(want) {
		t.Fatalf("bucket count = %d, want %d\ngot  %#v\nwant %#v", len(got), len(want), got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("bucket %d\ngot  %#v\nwant %#v", i, got[i], want[i])
		}
	}
}

func seedOverviewFixture(t *testing.T, store *SQLiteStore, base int64) {
	t.Helper()
	ctx := context.Background()
	type pair struct {
		tailnet string
		bucket  int64
		src     string
		dst     string
		kind    string
		tx      int64
		flows   int64
		proto   string
		bytes   string
		ports   string
	}
	pairs := []pair{
		// Overlaps a traffic_stats minute. Must not be added to it.
		{DefaultTailnetID, base, "a", "b", "virtual", 900, 9, "[17]", `{"17":900}`, `[{"port":53,"proto":17,"bytes":900}]`},
		{DefaultTailnetID, base, "c", "d", "virtual", 50, 1, "[6]", `{"6":50}`, `[{"port":80,"proto":6,"bytes":50}]`},
		// Gap minute, pairs only.
		{DefaultTailnetID, base + 60, "a", "b", "virtual", 200, 2, "[17]", `{"17":200}`, `[{"port":53,"proto":17,"bytes":200}]`},
		{DefaultTailnetID, base + 60, "c", "d", "exit", 40, 1, "[6]", `{"6":40}`, `[{"port":443,"proto":6,"bytes":40}]`},
		// Other tailnet, same minutes, much larger totals.
		{"other", base, "a", "b", "virtual", 500000, 4, "[6]", `{"6":500000}`, `[{"port":443,"proto":6,"bytes":500000}]`},
		{"other", base + 60, "a", "b", "virtual", 400000, 4, "[6]", `{"6":400000}`, `[{"port":443,"proto":6,"bytes":400000}]`},
		// Covered window, three minutes, both tailnets.
		{DefaultTailnetID, base + 3600, "a", "b", "virtual", 11, 1, "[6]", `{"6":11}`, "[]"},
		{DefaultTailnetID, base + 3600, "c", "d", "subnet", 12, 1, "[17]", `{"17":12}`, "[]"},
		{DefaultTailnetID, base + 3660, "a", "b", "physical", 13, 1, "[6]", `{"6":13}`, "[]"},
		{DefaultTailnetID, base + 3720, "a", "b", "virtual", 14, 1, "[1]", `{"1":14}`, "[]"},
		{"other", base + 3600, "a", "b", "virtual", 900000, 1, "[6]", `{"6":900000}`, "[]"},
		// No traffic_stats minute.
		{DefaultTailnetID, base + 7200, "a", "b", "subnet", 70, 3, "[17]", `{"17":70}`, `[{"port":53,"proto":17,"bytes":70}]`},
		{"other", base + 7200, "a", "b", "subnet", 800000, 1, "[17]", `{"17":800000}`, "[]"},
		// Hour window. Hour 0 overlaps stats, hour 1 is only pairs, hour 2 is stats only.
		{DefaultTailnetID, base + 86400, "a", "b", "virtual", 900, 1, "[17]", `{"17":900}`, "[]"},
		{DefaultTailnetID, base + 86400 + 60, "c", "d", "virtual", 10, 1, "[6]", `{"6":10}`, "[]"},
		{DefaultTailnetID, base + 86400 + 3600, "a", "b", "virtual", 30, 1, "[6]", `{"6":30}`, `[{"port":443,"proto":6,"bytes":30}]`},
		{DefaultTailnetID, base + 86400 + 3660, "c", "d", "virtual", 70, 1, "[6]", `{"6":70}`, `[{"port":443,"proto":6,"bytes":70}]`},
		{"other", base + 86400, "a", "b", "virtual", 700000, 1, "[6]", `{"6":700000}`, "[]"},
		{"other", base + 86400 + 3600, "a", "b", "virtual", 600000, 1, "[6]", `{"6":600000}`, "[]"},
	}
	for _, p := range pairs {
		ports := p.ports
		if ports == "" {
			ports = "[]"
		}
		if err := store.UpsertNodePairAggregates(ctx, p.tailnet, []NodePairAggregate{{
			Bucket: p.bucket, SrcNodeID: p.src, DstNodeID: p.dst, TrafficType: p.kind,
			TxBytes: p.tx, FlowCount: p.flows, Protocols: p.proto, ProtocolBytes: p.bytes, Ports: ports,
		}}); err != nil {
			t.Fatal(err)
		}
	}

	type stat struct {
		tailnet string
		bucket  int64
		tcp     int64
		udp     int64
		other   int64
		virtual int64
		exit    int64
		subnet  int64
		phys    int64
		flows   int64
		pairs   int64
		ports   string
	}
	stats := []stat{
		{DefaultTailnetID, base, 10, 3, 1, 20, 4, 5, 6, 7, 1, `[{"port":443,"proto":6,"bytes":10}]`},
		{DefaultTailnetID, base + 120, 8, 0, 0, 8, 0, 0, 0, 1, 1, "[]"},
		{"other", base, 100000, 0, 0, 100000, 0, 0, 0, 1, 1, "[]"},
		{DefaultTailnetID, base + 3600, 11, 12, 0, 11, 0, 12, 0, 2, 1, "[]"},
		{DefaultTailnetID, base + 3660, 13, 0, 0, 0, 0, 0, 13, 1, 1, "[]"},
		{DefaultTailnetID, base + 3720, 0, 0, 14, 14, 0, 0, 0, 1, 1, "[]"},
		{"other", base + 3600, 900000, 0, 0, 900000, 0, 0, 0, 1, 1, "[]"},
		{"other", base + 3660, 1, 0, 0, 1, 0, 0, 0, 1, 1, "[]"},
		{"other", base + 3720, 1, 0, 0, 1, 0, 0, 0, 1, 1, "[]"},
		{DefaultTailnetID, base + 86400, 11, 0, 0, 11, 0, 0, 0, 1, 1, "[]"},
		{DefaultTailnetID, base + 86400 + 2*3600, 5, 0, 0, 5, 0, 0, 0, 1, 1, "[]"},
		{"other", base + 86400, 700000, 0, 0, 700000, 0, 0, 0, 1, 1, "[]"},
	}
	for _, st := range stats {
		if err := store.UpsertTrafficStats(ctx, st.tailnet, []TrafficStats{{
			Bucket: st.bucket, TCPBytes: st.tcp, UDPBytes: st.udp, OtherProtoBytes: st.other,
			VirtualBytes: st.virtual, ExitBytes: st.exit, SubnetBytes: st.subnet, PhysicalBytes: st.phys,
			TotalFlows: st.flows, UniquePairs: st.pairs, TopPorts: st.ports,
		}}); err != nil {
			t.Fatal(err)
		}
	}
	// Minute base+60 is the gap: pairs exist, traffic_stats does not.
	var gap int
	if err := store.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM traffic_stats WHERE tailnet_id = ? AND bucket = ?
	`, DefaultTailnetID, base+60).Scan(&gap); err != nil {
		t.Fatal(err)
	}
	if gap != 0 {
		t.Fatalf("gap minute has %d traffic_stats rows", gap)
	}
}
