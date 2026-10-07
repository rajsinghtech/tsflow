package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/rajsinghtech/tsflow/backend/internal/database"
)

// TestStatsOverviewMatchesLegacyMerge seeds two tailnets and compares the
// stats API with the previous overview: traffic_stats, plus a full node_pairs
// derivation, with traffic_stats winning on overlap. default must not pick up
// the other tailnet's bytes.
func TestStatsOverviewMatchesLegacyMerge(t *testing.T) {
	store := setupHandlerTestDB(t)
	now := time.Now().UTC()
	base := now.Truncate(time.Minute).Add(-3 * time.Hour).Unix()
	hourBase := now.Truncate(time.Hour).Add(-6 * time.Hour).Unix()
	seedHandlerOverview(t, store, base, hourBase)

	cases := []struct {
		name       string
		start      int64
		end        int64
		wantScans  int64
		wantSource string
		check      func(t *testing.T, body overviewBody)
	}{
		{
			name:       "minute gap",
			start:      base,
			end:        base + 4*60,
			wantScans:  1,
			wantSource: "database",
			check: func(t *testing.T, body overviewBody) {
				if len(body.Buckets) != 3 {
					t.Fatalf("buckets = %+v", body.Buckets)
				}
				overlap := body.Buckets[0]
				if overlap.Bucket != base || overlap.TCPBytes != 10 || overlap.UDPBytes != 3 || overlap.VirtualBytes != 20 || overlap.UniquePairs != 2 || overlap.TotalFlows != 7 {
					t.Fatalf("overlap bucket changed: %+v", overlap)
				}
				gap := body.Buckets[1]
				if gap.Bucket != base+60 || gap.TCPBytes != 40 || gap.UDPBytes != 200 || gap.VirtualBytes != 200 || gap.ExitBytes != 40 || gap.TotalFlows != 3 || gap.UniquePairs != 2 {
					t.Fatalf("gap bucket = %+v", gap)
				}
				if body.Buckets[2].Bucket != base+120 || body.Buckets[2].TCPBytes != 8 || body.Buckets[2].UniquePairs != 1 {
					t.Fatalf("neighbor bucket = %+v", body.Buckets[2])
				}
				if body.Summary.TCPBytes != 58 || body.Summary.UDPBytes != 203 || body.Summary.VirtualBytes != 228 || body.Summary.ExitBytes != 44 || body.Summary.TotalFlows != 11 || body.Summary.UniquePairs != 2 {
					t.Fatalf("summary = %+v", body.Summary)
				}
				if body.Summary.TCPBytes > 10000 {
					t.Fatalf("summary includes the other tailnet: %+v", body.Summary)
				}
			},
		},
		{
			name:       "covered",
			start:      base + 600,
			end:        base + 600 + 3*60,
			wantScans:  0,
			wantSource: "database",
			check: func(t *testing.T, body overviewBody) {
				if len(body.Buckets) != 3 {
					t.Fatalf("buckets = %+v", body.Buckets)
				}
				if body.Buckets[0].TCPBytes != 11 || body.Buckets[0].UDPBytes != 12 || body.Buckets[0].UniquePairs != 2 {
					t.Fatalf("first covered bucket = %+v", body.Buckets[0])
				}
				if body.Buckets[1].PhysicalBytes != 13 || body.Buckets[1].UniquePairs != 1 {
					t.Fatalf("second covered bucket = %+v", body.Buckets[1])
				}
				if body.Summary.TCPBytes != 24 || body.Summary.OtherProtoBytes != 14 || body.Summary.UniquePairs != 2 {
					t.Fatalf("covered summary = %+v", body.Summary)
				}
			},
		},
		{
			name:       "no traffic stats",
			start:      base + 1200,
			end:        base + 1200 + 60,
			wantScans:  1,
			wantSource: "database (derived)",
			check: func(t *testing.T, body overviewBody) {
				if len(body.Buckets) != 1 || body.Buckets[0].UDPBytes != 70 || body.Buckets[0].SubnetBytes != 70 || body.Buckets[0].TotalFlows != 3 {
					t.Fatalf("derived bucket = %+v", body.Buckets)
				}
				if body.Summary.UDPBytes != 70 || body.Summary.SubnetBytes != 70 {
					t.Fatalf("derived summary = %+v", body.Summary)
				}
			},
		},
		{
			name:       "hour gap",
			start:      hourBase,
			end:        hourBase + 3*3600,
			wantScans:  1,
			wantSource: "database",
			check: func(t *testing.T, body overviewBody) {
				if len(body.Buckets) != 3 {
					t.Fatalf("hour buckets = %+v", body.Buckets)
				}
				if body.Buckets[0].Bucket != hourBase || body.Buckets[0].TCPBytes != 11 || body.Buckets[0].UniquePairs != 2 {
					t.Fatalf("covered hour = %+v", body.Buckets[0])
				}
				if body.Buckets[1].Bucket != hourBase+3600 || body.Buckets[1].TCPBytes != 100 || body.Buckets[1].VirtualBytes != 100 || body.Buckets[1].TotalFlows != 2 || body.Buckets[1].UniquePairs != 2 {
					t.Fatalf("gap hour = %+v", body.Buckets[1])
				}
				if body.Buckets[2].Bucket != hourBase+2*3600 || body.Buckets[2].TCPBytes != 5 || body.Buckets[2].UniquePairs != 1 {
					t.Fatalf("trailing hour = %+v", body.Buckets[2])
				}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			start := time.Unix(tc.start, 0).UTC()
			end := time.Unix(tc.end, 0).UTC()
			before := store.DerivedStatsScanCount()
			body := readOverview(t, store, start, end, "")
			if scans := store.DerivedStatsScanCount() - before; scans != tc.wantScans {
				t.Fatalf("derived scans = %d, want %d", scans, tc.wantScans)
			}
			if body.Metadata.Source != tc.wantSource {
				t.Fatalf("source = %q, want %q", body.Metadata.Source, tc.wantSource)
			}
			legacy := legacyOverview(t, store, database.DefaultTailnetID, start, end)
			if body.Metadata.Source != legacy.Metadata.Source {
				t.Fatalf("source = %q, legacy %q", body.Metadata.Source, legacy.Metadata.Source)
			}
			// totalNodes is a separate population count, not part of the
			// traffic_stats merge this comparison locks.
			legacy.Summary.TotalNodes = body.Summary.TotalNodes
			if body.Summary != legacy.Summary {
				t.Fatalf("summary\ngot  %+v\nwant %+v", body.Summary, legacy.Summary)
			}
			if len(body.Buckets) != len(legacy.Buckets) {
				t.Fatalf("buckets\ngot  %+v\nwant %+v", body.Buckets, legacy.Buckets)
			}
			for i := range body.Buckets {
				if body.Buckets[i] != legacy.Buckets[i] {
					t.Fatalf("bucket %d\ngot  %+v\nwant %+v", i, body.Buckets[i], legacy.Buckets[i])
				}
			}
			other := legacyOverview(t, store, "other", start, end)
			if other.Summary.TCPBytes+other.Summary.UDPBytes == body.Summary.TCPBytes+body.Summary.UDPBytes {
				t.Fatalf("default summary matched the other tailnet\ndefault %+v\nother   %+v", body.Summary, other.Summary)
			}
			tc.check(t, body)
		})
	}
}

func TestStatsOverviewFilteredTypesStillUsePairs(t *testing.T) {
	store := setupHandlerTestDB(t)
	base := time.Now().UTC().Truncate(time.Minute).Add(-time.Hour).Unix()
	ctx := context.Background()
	if err := store.UpsertNodePairAggregates(ctx, database.DefaultTailnetID, []database.NodePairAggregate{
		{Bucket: base, SrcNodeID: "a", DstNodeID: "b", TrafficType: "virtual", TxBytes: 100, FlowCount: 1, Protocols: "[6]", ProtocolBytes: `{"6":100}`},
		{Bucket: base, SrcNodeID: "c", DstNodeID: "d", TrafficType: "exit", TxBytes: 40, FlowCount: 1, Protocols: "[17]", ProtocolBytes: `{"17":40}`},
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertNodePairAggregates(ctx, "other", []database.NodePairAggregate{
		{Bucket: base, SrcNodeID: "a", DstNodeID: "b", TrafficType: "virtual", TxBytes: 900000, FlowCount: 1, Protocols: "[6]", ProtocolBytes: `{"6":900000}`},
	}); err != nil {
		t.Fatal(err)
	}
	start := time.Unix(base, 0).UTC()
	end := start.Add(time.Minute)
	body := readOverview(t, store, start, end, "virtual")
	if body.Metadata.Source != "database" {
		t.Fatalf("source = %q", body.Metadata.Source)
	}
	if len(body.Buckets) != 1 || body.Buckets[0].VirtualBytes != 100 || body.Buckets[0].ExitBytes != 0 || body.Buckets[0].TCPBytes != 100 {
		t.Fatalf("filtered buckets = %+v", body.Buckets)
	}
	if body.Summary.VirtualBytes != 100 || body.Summary.TCPBytes != 100 || body.Summary.ExitBytes != 0 {
		t.Fatalf("filtered summary = %+v", body.Summary)
	}
}

type overviewBody struct {
	Summary struct {
		TCPBytes        int64 `json:"tcpBytes"`
		UDPBytes        int64 `json:"udpBytes"`
		OtherProtoBytes int64 `json:"otherProtoBytes"`
		VirtualBytes    int64 `json:"virtualBytes"`
		ExitBytes       int64 `json:"exitBytes"`
		SubnetBytes     int64 `json:"subnetBytes"`
		PhysicalBytes   int64 `json:"physicalBytes"`
		TotalFlows      int64 `json:"totalFlows"`
		UniquePairs     int64 `json:"uniquePairs"`
		TotalNodes      int64 `json:"totalNodes"`
	} `json:"summary"`
	Buckets  []database.TrafficStats `json:"buckets"`
	Metadata struct {
		BucketCount int    `json:"bucketCount"`
		Source      string `json:"source"`
	} `json:"metadata"`
}

func readOverview(t *testing.T, store *database.SQLiteStore, start, end time.Time, trafficTypes string) overviewBody {
	t.Helper()
	h := &Handlers{store: store}
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	target := "/api/stats/overview?start=" + start.Format(time.RFC3339) + "&end=" + end.Format(time.RFC3339)
	if trafficTypes != "" {
		target += "&trafficTypes=" + trafficTypes
	}
	c.Request = httptest.NewRequest(http.MethodGet, target, nil)
	h.GetStatsOverview(c)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var body overviewBody
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Metadata.BucketCount != len(body.Buckets) {
		t.Fatalf("bucketCount = %d, len %d", body.Metadata.BucketCount, len(body.Buckets))
	}
	return body
}

func legacyOverview(t *testing.T, store *database.SQLiteStore, tailnetID string, start, end time.Time) overviewBody {
	t.Helper()
	ctx := context.Background()
	primary, err := store.GetTrafficStats(ctx, tailnetID, start, end)
	if err != nil {
		t.Fatal(err)
	}
	derived, err := store.GetTrafficStatsFromNodePairs(ctx, tailnetID, start, end)
	if err != nil {
		t.Fatal(err)
	}
	buckets := mergeTrafficStatsBuckets(primary, derived)
	var body overviewBody
	body.Metadata.Source = "database"
	if len(primary) == 0 {
		body.Metadata.Source = "database (derived)"
	}
	body.Buckets = buckets
	body.Metadata.BucketCount = len(buckets)
	for _, b := range buckets {
		body.Summary.TCPBytes += b.TCPBytes
		body.Summary.UDPBytes += b.UDPBytes
		body.Summary.OtherProtoBytes += b.OtherProtoBytes
		body.Summary.VirtualBytes += b.VirtualBytes
		body.Summary.ExitBytes += b.ExitBytes
		body.Summary.SubnetBytes += b.SubnetBytes
		body.Summary.PhysicalBytes += b.PhysicalBytes
		body.Summary.TotalFlows += b.TotalFlows
		if b.UniquePairs > body.Summary.UniquePairs {
			body.Summary.UniquePairs = b.UniquePairs
		}
	}
	return body
}

func seedHandlerOverview(t *testing.T, store *database.SQLiteStore, base, hourBase int64) {
	t.Helper()
	ctx := context.Background()
	pairs := []struct {
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
	}{
		{database.DefaultTailnetID, base, "a", "b", "virtual", 900, 9, "[17]", `{"17":900}`, `[{"port":53,"proto":17,"bytes":900}]`},
		{database.DefaultTailnetID, base, "c", "d", "virtual", 50, 1, "[6]", `{"6":50}`, `[{"port":80,"proto":6,"bytes":50}]`},
		{database.DefaultTailnetID, base + 60, "a", "b", "virtual", 200, 2, "[17]", `{"17":200}`, `[{"port":53,"proto":17,"bytes":200}]`},
		{database.DefaultTailnetID, base + 60, "c", "d", "exit", 40, 1, "[6]", `{"6":40}`, `[{"port":443,"proto":6,"bytes":40}]`},
		{"other", base, "a", "b", "virtual", 500000, 4, "[6]", `{"6":500000}`, "[]"},
		{"other", base + 60, "a", "b", "virtual", 400000, 4, "[6]", `{"6":400000}`, "[]"},
		{database.DefaultTailnetID, base + 600, "a", "b", "virtual", 11, 1, "[6]", `{"6":11}`, "[]"},
		{database.DefaultTailnetID, base + 600, "c", "d", "subnet", 12, 1, "[17]", `{"17":12}`, "[]"},
		{database.DefaultTailnetID, base + 660, "a", "b", "physical", 13, 1, "[6]", `{"6":13}`, "[]"},
		{database.DefaultTailnetID, base + 720, "a", "b", "virtual", 14, 1, "[1]", `{"1":14}`, "[]"},
		{"other", base + 600, "a", "b", "virtual", 900000, 1, "[6]", `{"6":900000}`, "[]"},
		{database.DefaultTailnetID, base + 1200, "a", "b", "subnet", 70, 3, "[17]", `{"17":70}`, `[{"port":53,"proto":17,"bytes":70}]`},
		{"other", base + 1200, "a", "b", "subnet", 800000, 1, "[17]", `{"17":800000}`, "[]"},
		{database.DefaultTailnetID, hourBase, "a", "b", "virtual", 900, 1, "[17]", `{"17":900}`, "[]"},
		{database.DefaultTailnetID, hourBase + 60, "c", "d", "virtual", 10, 1, "[6]", `{"6":10}`, "[]"},
		{database.DefaultTailnetID, hourBase + 3600, "a", "b", "virtual", 30, 1, "[6]", `{"6":30}`, `[{"port":443,"proto":6,"bytes":30}]`},
		{database.DefaultTailnetID, hourBase + 3660, "c", "d", "virtual", 70, 1, "[6]", `{"6":70}`, `[{"port":443,"proto":6,"bytes":70}]`},
		{"other", hourBase, "a", "b", "virtual", 700000, 1, "[6]", `{"6":700000}`, "[]"},
		{"other", hourBase + 3600, "a", "b", "virtual", 600000, 1, "[6]", `{"6":600000}`, "[]"},
	}
	for _, p := range pairs {
		ports := p.ports
		if ports == "" {
			ports = "[]"
		}
		if err := store.UpsertNodePairAggregates(ctx, p.tailnet, []database.NodePairAggregate{{
			Bucket: p.bucket, SrcNodeID: p.src, DstNodeID: p.dst, TrafficType: p.kind,
			TxBytes: p.tx, FlowCount: p.flows, Protocols: p.proto, ProtocolBytes: p.bytes, Ports: ports,
		}}); err != nil {
			t.Fatal(err)
		}
	}
	stats := []database.TrafficStats{
		{Bucket: base, TCPBytes: 10, UDPBytes: 3, OtherProtoBytes: 1, VirtualBytes: 20, ExitBytes: 4, SubnetBytes: 5, PhysicalBytes: 6, TotalFlows: 7, UniquePairs: 1, TopPorts: `[{"port":443,"proto":6,"bytes":10}]`},
		{Bucket: base + 120, TCPBytes: 8, VirtualBytes: 8, TotalFlows: 1, UniquePairs: 1, TopPorts: "[]"},
	}
	if err := store.UpsertTrafficStats(ctx, database.DefaultTailnetID, stats); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertTrafficStats(ctx, "other", []database.TrafficStats{
		{Bucket: base, TCPBytes: 100000, VirtualBytes: 100000, TotalFlows: 1, UniquePairs: 1, TopPorts: "[]"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertTrafficStats(ctx, database.DefaultTailnetID, []database.TrafficStats{
		{Bucket: base + 600, TCPBytes: 11, UDPBytes: 12, VirtualBytes: 11, SubnetBytes: 12, TotalFlows: 2, UniquePairs: 1, TopPorts: "[]"},
		{Bucket: base + 660, TCPBytes: 13, PhysicalBytes: 13, TotalFlows: 1, UniquePairs: 1, TopPorts: "[]"},
		{Bucket: base + 720, OtherProtoBytes: 14, VirtualBytes: 14, TotalFlows: 1, UniquePairs: 1, TopPorts: "[]"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertTrafficStats(ctx, "other", []database.TrafficStats{
		{Bucket: base + 600, TCPBytes: 900000, VirtualBytes: 900000, TotalFlows: 1, UniquePairs: 1, TopPorts: "[]"},
		{Bucket: base + 660, TCPBytes: 1, VirtualBytes: 1, TotalFlows: 1, UniquePairs: 1, TopPorts: "[]"},
		{Bucket: base + 720, TCPBytes: 1, VirtualBytes: 1, TotalFlows: 1, UniquePairs: 1, TopPorts: "[]"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertTrafficStats(ctx, database.DefaultTailnetID, []database.TrafficStats{
		{Bucket: hourBase, TCPBytes: 11, VirtualBytes: 11, TotalFlows: 1, UniquePairs: 1, TopPorts: "[]"},
		{Bucket: hourBase + 2*3600, TCPBytes: 5, VirtualBytes: 5, TotalFlows: 1, UniquePairs: 1, TopPorts: "[]"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertTrafficStats(ctx, "other", []database.TrafficStats{
		{Bucket: hourBase, TCPBytes: 700000, VirtualBytes: 700000, TotalFlows: 1, UniquePairs: 1, TopPorts: "[]"},
	}); err != nil {
		t.Fatal(err)
	}
}

func TestStatsOverviewReportsDistinctActiveNodes(t *testing.T) {
	store := setupHandlerTestDB(t)
	ctx := context.Background()
	base := time.Now().UTC().Truncate(time.Minute).Add(-2 * time.Hour)
	if err := store.UpsertNodePairAggregates(ctx, database.DefaultTailnetID, []database.NodePairAggregate{
		{Bucket: base.Unix(), SrcNodeID: "a", DstNodeID: "b", TrafficType: "virtual", TxBytes: 100, RxBytes: 40, FlowCount: 1, Protocols: "[6]", ProtocolBytes: `{"6":140}`, Ports: "[]"},
		{Bucket: base.Unix(), SrcNodeID: "b", DstNodeID: "c", TrafficType: "virtual", TxBytes: 10, RxBytes: 1, FlowCount: 1, Protocols: "[6]", ProtocolBytes: `{"6":11}`, Ports: "[]"},
		{Bucket: base.Unix(), SrcNodeID: "self", DstNodeID: "self", TrafficType: "subnet", TxBytes: 5, RxBytes: 2, FlowCount: 1, Protocols: "[6]", ProtocolBytes: `{"6":7}`, Ports: "[]"},
		{Bucket: base.Unix(), SrcNodeID: "exit-src", DstNodeID: "exit-dst", TrafficType: "exit", TxBytes: 9, FlowCount: 1, Protocols: "[6]", ProtocolBytes: `{"6":9}`, Ports: "[]"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertNodePairAggregates(ctx, "other", []database.NodePairAggregate{{
		Bucket: base.Unix(), SrcNodeID: "foreign", DstNodeID: "foreign-2", TrafficType: "virtual",
		TxBytes: 500, FlowCount: 1, Protocols: "[6]", ProtocolBytes: `{"6":500}`, Ports: "[]",
	}}); err != nil {
		t.Fatal(err)
	}

	body := readOverview(t, store, base, base.Add(time.Minute), "")
	// a, b, c, self, exit-src, exit-dst. b is shared by two pairs and self counts once.
	if body.Summary.TotalNodes != 6 {
		t.Fatalf("totalNodes = %d, want 6", body.Summary.TotalNodes)
	}

	filtered := readOverview(t, store, base, base.Add(time.Minute), "virtual")
	if filtered.Summary.TotalNodes != 3 {
		t.Fatalf("virtual totalNodes = %d, want 3", filtered.Summary.TotalNodes)
	}
}
