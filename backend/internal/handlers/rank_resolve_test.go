package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/rajsinghtech/tsflow/backend/internal/database"
	"github.com/rajsinghtech/tsflow/backend/internal/services"
)

type rankedTalkersBody struct {
	Talkers  []database.RankedTalker `json:"talkers"`
	Metadata struct {
		Count   int    `json:"count"`
		HasMore bool   `json:"hasMore"`
		Query   string `json:"q"`
	} `json:"metadata"`
}

type rankedPairsBody struct {
	Pairs []database.RankedPair `json:"pairs"`
}

func seedRankResolve(t *testing.T) (*Handlers, string) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	store := setupHandlerTestDB(t)
	start := time.Now().UTC().Add(-30 * time.Minute).Truncate(time.Minute)
	base := start.Unix()
	row := func(bucket int64, src, dst, kind string, tx, rx, flows int64) database.NodePairAggregate {
		return database.NodePairAggregate{Bucket: bucket, SrcNodeID: src, DstNodeID: dst, TrafficType: kind,
			TxBytes: tx, RxBytes: rx, FlowCount: flows, Protocols: "[6]", ProtocolBytes: `{"6":1}`, Ports: "[]"}
	}
	if err := store.UpsertNodePairAggregates(context.Background(), database.DefaultTailnetID, []database.NodePairAggregate{
		// build is stored under its legacy id, its stable id, and its address.
		row(base, "1002", "nDb00001CNTRL", "virtual", 100, 10, 1),
		row(base+60, "nBuild001CNTRL", "nDb00001CNTRL", "virtual", 200, 20, 2),
		row(base+120, "100.64.0.20", "nDb00001CNTRL", "virtual", 300, 30, 3),
		// alice reaches a subnet address that is not a device.
		row(base, "nAlice001CNTRL", "10.20.0.5", "subnet", 50, 5, 4),
		// WireGuard transport via DERP: physical, hidden unless requested.
		row(base, "nAlice001CNTRL", "127.3.3.40", "physical", 9000, 0, 1),
	}); err != nil {
		t.Fatal(err)
	}
	poller := services.NewPoller(nil, store, services.DefaultPollerConfig())
	poller.GetDeviceCache().Update([]services.Device{
		{ID: "nBuild001CNTRL", NodeID: "nBuild001CNTRL", LegacyID: "1002", Name: "build.example.ts.net", Hostname: "build",
			User: "bob@example.com", Addresses: []string{"100.64.0.20"}, Tags: []string{"tag:ci"}},
		{ID: "nDb00001CNTRL", NodeID: "nDb00001CNTRL", Name: "db.example.ts.net", Hostname: "db",
			User: "carol@example.com", Addresses: []string{"100.64.0.30"}},
		{ID: "nAlice001CNTRL", NodeID: "nAlice001CNTRL", Name: "alice-laptop.example.ts.net", Hostname: "alice-laptop",
			User: "alice@example.com", Addresses: []string{"100.64.0.10"}},
	})
	return &Handlers{store: store, poller: poller}, rankWindow(start, start.Add(20*time.Minute))
}

func getRankedTalkers(t *testing.T, h *Handlers, query string) rankedTalkersBody {
	t.Helper()
	code, body := serve(dataRouter(h), http.MethodGet, "/api/analytics/talkers"+query)
	if code != http.StatusOK {
		t.Fatalf("talkers %s: %d %s", query, code, body)
	}
	var out rankedTalkersBody
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func talkerNames(talkers []database.RankedTalker) []string {
	names := make([]string, 0, len(talkers))
	for _, talker := range talkers {
		name := talker.Hostname
		if name == "" {
			name = talker.NodeID
		}
		names = append(names, name)
	}
	return names
}

func TestRankedTalkersResolveDevicesOwnersAndDERP(t *testing.T) {
	h, window := seedRankResolve(t)

	got := getRankedTalkers(t, h, window)
	// build appears once under its canonical id with the three stored rows summed.
	var build *database.RankedTalker
	for i := range got.Talkers {
		if got.Talkers[i].Hostname == "build" {
			if build != nil {
				t.Fatalf("build listed twice: %+v", got.Talkers)
			}
			build = &got.Talkers[i]
		}
		if got.Talkers[i].NodeID == "127.3.3.40" {
			t.Fatalf("DERP relay ranked by default: %+v", got.Talkers)
		}
	}
	if build == nil || build.NodeID != "nBuild001CNTRL" || build.Owner != "bob@example.com" ||
		build.TxBytes != 600 || build.RxBytes != 60 || build.FlowCount != 6 {
		t.Fatalf("build = %+v, want canonical id, owner, and summed totals", build)
	}
	if got.Talkers[0].Hostname != "db" || got.Talkers[0].Owner != "carol@example.com" {
		t.Fatalf("top talker = %+v, want db (660 bytes) with its owner", got.Talkers[0])
	}

	physical := getRankedTalkers(t, h, window+"&trafficTypes=physical")
	// Both ends carry 9000 bytes; ties rank by stored id.
	if names := talkerNames(physical.Talkers); len(names) != 2 || names[0] != "DERP relay" || names[1] != "alice-laptop" {
		t.Fatalf("physical talkers = %v, want DERP relay and alice-laptop", names)
	}

	code, body := serve(dataRouter(h), http.MethodGet, "/api/analytics/pairs"+window)
	if code != http.StatusOK {
		t.Fatalf("pairs: %d %s", code, body)
	}
	var pairs rankedPairsBody
	if err := json.Unmarshal(body, &pairs); err != nil {
		t.Fatal(err)
	}
	if len(pairs.Pairs) != 2 {
		t.Fatalf("pairs = %+v, want build->db once and alice->10.20.0.5", pairs.Pairs)
	}
	top := pairs.Pairs[0]
	if top.SrcHostname != "build" || top.SrcOwner != "bob@example.com" || top.DstHostname != "db" ||
		top.DstOwner != "carol@example.com" || top.TotalBytes != 660 || top.FlowCount != 6 {
		t.Fatalf("top pair = %+v", top)
	}
}

func TestRankedTalkersSearch(t *testing.T) {
	h, window := seedRankResolve(t)
	cases := []struct {
		q    string
		want []string
	}{
		{"bob@example.com", []string{"build"}},
		{"BOB@EXAMPLE", []string{"build"}},
		{"user@carol", []string{"db"}},
		{"tag:ci", []string{"build"}},
		{"ip:100.64.0.1", []string{"alice-laptop"}},
		// A stored address that is not a device matches directly.
		{"10.20", []string{"10.20.0.5"}},
		{"alice", []string{"alice-laptop"}},
	}
	for _, tc := range cases {
		got := getRankedTalkers(t, h, window+"&q="+tc.q)
		names := talkerNames(got.Talkers)
		if len(names) != len(tc.want) {
			t.Fatalf("q=%q talkers = %v, want %v", tc.q, names, tc.want)
		}
		for i := range names {
			if names[i] != tc.want[i] {
				t.Fatalf("q=%q talkers = %v, want %v", tc.q, names, tc.want)
			}
		}
		if got.Metadata.Query != tc.q {
			t.Fatalf("q=%q metadata.q = %q", tc.q, got.Metadata.Query)
		}
	}

	none := getRankedTalkers(t, h, window+"&q=tag:nothing")
	if len(none.Talkers) != 0 || none.Metadata.HasMore {
		t.Fatalf("unmatched search = %+v, want an empty page", none)
	}

	code, body := serve(dataRouter(h), http.MethodGet, "/api/analytics/pairs"+window+"&q=bob@example.com")
	var pairs rankedPairsBody
	if code != http.StatusOK || json.Unmarshal(body, &pairs) != nil || len(pairs.Pairs) != 1 || pairs.Pairs[0].SrcHostname != "build" {
		t.Fatalf("pair search: %d %s", code, body)
	}
}
