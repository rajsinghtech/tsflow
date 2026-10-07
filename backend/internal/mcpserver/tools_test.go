package mcpserver

import (
	"context"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/rajsinghtech/tsflow/backend/internal/access"
	"github.com/rajsinghtech/tsflow/backend/internal/config"
	"github.com/rajsinghtech/tsflow/backend/internal/database"
	"github.com/rajsinghtech/tsflow/backend/internal/handlers"
	"github.com/rajsinghtech/tsflow/backend/internal/services"
)

func TestSearchAndGetDeviceScope(t *testing.T) {
	svc, _, _ := setupMCP(t)
	open := Viewer{}
	listed, err := svc.searchDevices(context.Background(), open, searchDevicesIn{})
	if err != nil {
		t.Fatal(err)
	}
	if listed.Count != 2 || listed.Tailnet != database.DefaultTailnetID {
		t.Fatalf("open search = %+v", listed)
	}

	ada := adaViewer()
	scoped, err := svc.searchDevices(context.Background(), ada, searchDevicesIn{Query: "laptop"})
	if err != nil {
		t.Fatal(err)
	}
	if scoped.Scope != "mine" || scoped.Count != 1 || scoped.Devices[0].ID != "ada" || scoped.Devices[0].User != "ada@example.com" {
		t.Fatalf("default search = %+v", scoped)
	}
	everyone, err := svc.searchDevices(context.Background(), ada, searchDevicesIn{Scope: "all"})
	if err != nil {
		t.Fatal(err)
	}
	if everyone.Scope != "all" || everyone.Count != 2 {
		t.Fatalf("scope=all search = %+v", everyone)
	}
	if _, err := svc.getDevice(context.Background(), ada, getDeviceIn{Device: "bob"}); err == nil {
		t.Fatal("default scope returned bob")
	}
	bob, err := svc.getDevice(context.Background(), ada, getDeviceIn{Device: "bob", Scope: "all"})
	if err != nil || bob.Device.ID != "bob" || bob.Scope != "all" {
		t.Fatalf("scope=all get bob = %+v err=%v", bob, err)
	}
	if _, err := svc.searchDevices(context.Background(), ada, searchDevicesIn{Scope: "nope"}); err == nil {
		t.Fatal("invalid scope was accepted")
	}
	got, err := svc.getDevice(context.Background(), ada, getDeviceIn{Device: "ada-laptop"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Device.Tags) != 1 || got.Device.Tags[0] != "tag:eng" || got.Device.User != "ada@example.com" {
		t.Fatalf("device = %+v", got.Device)
	}

	page, err := svc.searchDevices(context.Background(), open, searchDevicesIn{Limit: 10000, Offset: 1})
	if err != nil {
		t.Fatal(err)
	}
	if page.Limit != maxLimit || page.Offset != 1 || page.Count != 1 || page.HasMore {
		t.Fatalf("page = %+v", page)
	}
}

func TestTopTalkersOmitPhysicalUnlessRequested(t *testing.T) {
	svc, store, _ := setupMCP(t)
	base := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	seedPair(t, store, database.DefaultTailnetID, base, "ada", "bob", "virtual", 15)
	seedPair(t, store, database.DefaultTailnetID, base, "127.3.3.40", "ada", "physical", 900)
	window := rankedIn{Start: base.Format(time.RFC3339), End: base.Add(time.Minute).Format(time.RFC3339)}

	got, err := svc.topTalkers(context.Background(), Viewer{}, window)
	if err != nil {
		t.Fatal(err)
	}
	for _, talker := range got.Talkers {
		if talker.NodeID == "127.3.3.40" {
			t.Fatalf("default talkers include DERP: %+v", got.Talkers)
		}
	}
	if len(got.Talkers) == 0 || got.Talkers[0].TotalBytes == 0 {
		t.Fatalf("talkers = %+v", got.Talkers)
	}

	window.TrafficTypes = []string{"physical"}
	physical, err := svc.topTalkers(context.Background(), Viewer{}, window)
	if err != nil {
		t.Fatal(err)
	}
	if len(physical.Talkers) == 0 || physical.Talkers[0].Name != "DERP relay" {
		t.Fatalf("physical talkers = %+v", physical.Talkers)
	}

	scoped, err := svc.topTalkers(context.Background(), adaViewer(), rankedIn{
		Start: window.Start, End: window.End,
	})
	if err != nil {
		t.Fatal(err)
	}
	if scoped.Scope != "mine" {
		t.Fatalf("default talker scope = %q", scoped.Scope)
	}
	for _, talker := range scoped.Talkers {
		if talker.NodeID == "bob" {
			t.Fatalf("default talkers include bob: %+v", scoped.Talkers)
		}
	}
	cleared, err := svc.topTalkers(context.Background(), adaViewer(), rankedIn{
		Start: window.Start, End: window.End, Scope: "all",
	})
	if err != nil {
		t.Fatal(err)
	}
	var sawBob bool
	for _, talker := range cleared.Talkers {
		if talker.NodeID == "bob" {
			sawBob = true
		}
	}
	if cleared.Scope != "all" || !sawBob {
		t.Fatalf("scope=all talkers = %+v", cleared.Talkers)
	}
}

func TestTopPairsAndFlowsBetween(t *testing.T) {
	svc, store, _ := setupMCP(t)
	base := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	if err := store.UpsertNodePairAggregates(context.Background(), database.DefaultTailnetID, []database.NodePairAggregate{
		{Bucket: base.Unix(), SrcNodeID: "ada", DstNodeID: "bob", TrafficType: "virtual", TxBytes: 80, FlowCount: 2, Protocols: "[6]", ProtocolBytes: `{"6":80}`, Ports: `[{"port":443,"proto":6,"bytes":80}]`},
		{Bucket: base.Unix(), SrcNodeID: "ada", DstNodeID: "bob", TrafficType: "physical", TxBytes: 400, FlowCount: 1, Protocols: "[0]", ProtocolBytes: `{"0":400}`, Ports: `[{"port":27,"proto":0,"bytes":400}]`},
	}); err != nil {
		t.Fatal(err)
	}
	window := rankedIn{Start: base.Format(time.RFC3339), End: base.Add(time.Minute).Format(time.RFC3339)}
	pairs, err := svc.topPairs(context.Background(), Viewer{}, window)
	if err != nil {
		t.Fatal(err)
	}
	if len(pairs.Pairs) != 1 || pairs.Pairs[0].TotalBytes != 80 || pairs.Pairs[0].SrcName != "ada-laptop" {
		t.Fatalf("pairs = %+v", pairs.Pairs)
	}

	flows, err := svc.flowsBetween(context.Background(), Viewer{}, flowsBetweenIn{
		Start: window.Start, End: window.End, A: "ada-laptop", B: "100.64.0.9",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(flows.Flows) != 1 || flows.Flows[0].TxBytes != 80 || len(flows.Flows[0].Ports) != 1 || flows.Flows[0].Ports[0].Port != 443 {
		t.Fatalf("flows = %+v", flows.Flows)
	}
	if _, err := svc.flowsBetween(context.Background(), adaViewer(), flowsBetweenIn{
		Start: window.Start, End: window.End, A: "ada", B: "bob",
	}); err == nil {
		t.Fatal("default scope queried bob")
	}
	cleared, err := svc.flowsBetween(context.Background(), adaViewer(), flowsBetweenIn{
		Start: window.Start, End: window.End, A: "ada", B: "bob", Scope: "all",
	})
	if err != nil || cleared.Scope != "all" || len(cleared.Flows) != 1 || cleared.Flows[0].TxBytes != 80 {
		t.Fatalf("scope=all flows = %+v err=%v", cleared, err)
	}
	if _, err := svc.topTalkers(context.Background(), Viewer{}, rankedIn{TrafficTypes: []string{"nope"}}); err == nil {
		t.Fatal("invalid traffic type was accepted")
	}
}

func TestDevicePeersTimelineAndNewConnections(t *testing.T) {
	svc, store, _ := setupMCP(t)
	base := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	ctx := context.Background()
	if err := store.UpsertNodePairAggregates(ctx, database.DefaultTailnetID, []database.NodePairAggregate{
		{Bucket: base.Add(-30 * time.Minute).Unix(), SrcNodeID: "ada", DstNodeID: "bob", TrafficType: "virtual", TxBytes: 5, FlowCount: 1, Protocols: "[6]", ProtocolBytes: `{"6":5}`},
		{Bucket: base.Unix(), SrcNodeID: "ada", DstNodeID: "bob", TrafficType: "virtual", TxBytes: 10, FlowCount: 1, Protocols: "[6]", ProtocolBytes: `{"6":10}`},
		{Bucket: base.Unix(), SrcNodeID: "ada", DstNodeID: "carol", TrafficType: "virtual", TxBytes: 30, FlowCount: 1, Protocols: "[17]", ProtocolBytes: `{"17":30}`},
		{Bucket: base.Unix(), SrcNodeID: "ada", DstNodeID: "bob", TrafficType: "physical", TxBytes: 500, FlowCount: 1, Protocols: "[0]", ProtocolBytes: `{"0":500}`},
		{Bucket: base.Unix(), SrcNodeID: "ada", DstNodeID: "127.3.3.40", TrafficType: "physical", TxBytes: 9, FlowCount: 1, Protocols: "[0]", ProtocolBytes: `{"0":9}`},
	}); err != nil {
		t.Fatal(err)
	}
	start := base.Format(time.RFC3339)
	end := base.Add(time.Minute).Format(time.RFC3339)

	peers, err := svc.devicePeers(ctx, Viewer{}, deviceIn{Device: "ada", Start: start, End: end})
	if err != nil {
		t.Fatal(err)
	}
	if len(peers.Peers) == 0 || peers.Peers[0].NodeID != "carol" || peers.Peers[0].TotalBytes != 30 {
		t.Fatalf("peers = %+v", peers.Peers)
	}
	for _, peer := range peers.Peers {
		if peer.TotalBytes >= 500 {
			t.Fatalf("physical bytes leaked into peers: %+v", peers.Peers)
		}
	}

	timeline, err := svc.deviceTimeline(ctx, Viewer{}, timelineIn{Device: "100.64.0.8", Start: start, End: end})
	if err != nil {
		t.Fatal(err)
	}
	if len(timeline.Buckets) != 1 || timeline.Buckets[0].BytesByType["virtual"] != 40 {
		t.Fatalf("timeline = %+v", timeline.Buckets)
	}
	if _, ok := timeline.Buckets[0].BytesByType["physical"]; ok {
		t.Fatalf("physical series present by default: %+v", timeline.Buckets[0].BytesByType)
	}
	withPhysical, err := svc.deviceTimeline(ctx, Viewer{}, timelineIn{
		Device: "ada", Start: start, End: end, TrafficTypes: []string{"physical"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if withPhysical.Buckets[0].BytesByType["physical"] != 509 {
		t.Fatalf("physical timeline = %+v", withPhysical.Buckets)
	}

	fresh, err := svc.newConnections(ctx, Viewer{}, newConnectionsIn{Start: start, End: end, Lookback: "1h"})
	if err != nil {
		t.Fatal(err)
	}
	if len(fresh.Pairs) != 1 || fresh.Pairs[0].DstNodeID != "carol" {
		t.Fatalf("new pairs = %+v", fresh.Pairs)
	}
	derp, err := svc.newConnections(ctx, Viewer{}, newConnectionsIn{
		Start: start, End: end, Lookback: "1h", TrafficTypes: []string{"physical"},
	})
	if err != nil {
		t.Fatal(err)
	}
	var sawDERP bool
	for _, pair := range derp.Pairs {
		if pair.DstName == "DERP relay" {
			sawDERP = true
		}
	}
	if !sawDERP {
		t.Fatalf("physical new pairs = %+v", derp.Pairs)
	}

	hidden, err := svc.newConnections(ctx, adaViewer(), newConnectionsIn{Start: start, End: end, Lookback: "1h"})
	if err != nil {
		t.Fatal(err)
	}
	for _, pair := range hidden.Pairs {
		if pair.DstNodeID == "bob" || pair.SrcNodeID == "bob" {
			t.Fatalf("scoped new pairs include bob: %+v", hidden.Pairs)
		}
	}
}

func TestStatsOverviewMatchesStoredTotals(t *testing.T) {
	svc, store, _ := setupMCP(t)
	base := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	seedPair(t, store, database.DefaultTailnetID, base, "ada", "bob", "virtual", 80)
	seedPair(t, store, database.DefaultTailnetID, base, "ada", "127.3.3.40", "physical", 200)
	got, err := svc.statsOverview(context.Background(), Viewer{}, statsOverviewIn{
		Start: base.Format(time.RFC3339), End: base.Add(time.Minute).Format(time.RFC3339),
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Summary.VirtualBytes != 80 || got.Summary.PhysicalBytes != 200 || got.Summary.OtherProtoBytes != 0 {
		t.Fatalf("summary = %+v, want virtual bytes separate from physical protocol totals", got.Summary)
	}
}

func TestListTailnetsHonorsGrant(t *testing.T) {
	store := newStore(t)
	reg, err := services.NewRegistry(context.Background(), []config.TailnetSpec{
		{ID: "alpha", Name: "Alpha", APIURL: "https://example.test", APIKey: "k"},
		{ID: "beta", Name: "Beta", APIURL: "https://example.test", APIKey: "k"},
	}, store, services.DefaultPollerConfig())
	if err != nil {
		t.Fatal(err)
	}
	alpha, _ := reg.Get("alpha")
	alpha.Poller.GetDeviceCache().Update([]services.Device{{
		ID: "ada", Hostname: "ada-laptop", User: "ada@example.com", Addresses: []string{"100.64.0.8"},
	}})
	beta, _ := reg.Get("beta")
	beta.Poller.GetDeviceCache().Update([]services.Device{{
		ID: "secret", Hostname: "secret-node", User: "ada@example.com", Addresses: []string{"100.64.1.1"},
	}})
	h := handlers.NewHandlers(nil, store, nil, "test")
	h.UseRegistry(reg)
	svc := New(h, "test")
	var allow access.Allow
	allow.Add(config.Grant{Tailnets: []string{"alpha"}})
	viewer := Viewer{Restricted: true, Ident: access.Identity{Login: "ada@example.com", Allow: allow}}

	listed, err := svc.listTailnets(viewer, listTailnetsIn{})
	if err != nil {
		t.Fatal(err)
	}
	if len(listed.Tailnets) != 1 || listed.Tailnets[0].ID != "alpha" {
		t.Fatalf("tailnets = %+v", listed.Tailnets)
	}
	if _, err := svc.searchDevices(context.Background(), viewer, searchDevicesIn{}); err == nil {
		t.Fatal("missing tailnet was accepted")
	}
	if _, err := svc.searchDevices(context.Background(), viewer, searchDevicesIn{Tailnet: "beta", Scope: "all"}); err == nil {
		t.Fatal("beta tailnet was visible with scope=all")
	}
	found, err := svc.searchDevices(context.Background(), viewer, searchDevicesIn{Tailnet: "alpha"})
	if err != nil {
		t.Fatal(err)
	}
	if found.Count != 1 || found.Devices[0].ID != "ada" {
		t.Fatalf("alpha devices = %+v", found.Devices)
	}
}

func setupMCP(t *testing.T) (*Service, *database.SQLiteStore, *services.Poller) {
	t.Helper()
	store := newStore(t)
	poller := services.NewPoller(nil, store, services.DefaultPollerConfig())
	poller.GetDeviceCache().Update([]services.Device{
		{ID: "ada", Hostname: "ada-laptop", Name: "ada-laptop.example.com", User: "ada@example.com", Addresses: []string{"100.64.0.8"}, Tags: []string{"tag:eng"}, OS: "linux"},
		{ID: "bob", Hostname: "bob-phone", Name: "bob-phone.example.com", User: "bob@example.com", Addresses: []string{"100.64.0.9"}, Tags: []string{"tag:ops"}, OS: "ios"},
	})
	return New(handlers.NewHandlers(nil, store, poller, "test"), "test"), store, poller
}

func newStore(t *testing.T) *database.SQLiteStore {
	t.Helper()
	store, err := database.NewSQLiteStore(filepath.Join(t.TempDir(), "tsflow.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	if err := store.Init(context.Background()); err != nil {
		t.Fatal(err)
	}
	return store
}

func adaViewer() Viewer {
	return Viewer{
		Restricted: true,
		Ident: access.Identity{
			Login:       "ada@example.com",
			Allow:       access.Allow{All: true},
			DeviceScope: &access.DeviceScope{Owners: []string{"ada@example.com"}, Tags: []string{}},
		},
	}
}

func seedPair(t *testing.T, store *database.SQLiteStore, tailnet string, bucket time.Time, src, dst, traffic string, tx int64) {
	t.Helper()
	protocol := "6"
	if traffic == "physical" {
		protocol = "0"
	}
	if err := store.UpsertNodePairAggregates(context.Background(), tailnet, []database.NodePairAggregate{{
		Bucket: bucket.Unix(), SrcNodeID: src, DstNodeID: dst, TrafficType: traffic,
		TxBytes: tx, FlowCount: 1, Protocols: "[" + protocol + "]", ProtocolBytes: `{"` + protocol + `":` + itoa(tx) + `}`,
	}}); err != nil {
		t.Fatal(err)
	}
}

func itoa(n int64) string {
	return strconv.FormatInt(n, 10)
}

func TestNewConnectionsLeaveOutSelfPairs(t *testing.T) {
	svc, store, _ := setupMCP(t)
	base := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	ctx := context.Background()
	row := func(src, dst string) database.NodePairAggregate {
		return database.NodePairAggregate{Bucket: base.Unix(), SrcNodeID: src, DstNodeID: dst, TrafficType: "virtual", TxBytes: 10, FlowCount: 1, Protocols: "[6]", ProtocolBytes: `{"6":10}`}
	}
	if err := store.UpsertNodePairAggregates(ctx, database.DefaultTailnetID, []database.NodePairAggregate{
		row("ada", "ada"),
		row("100.64.0.8", "ada"), // ada's address and ada's id are one device
		row("ada", "carol"),
	}); err != nil {
		t.Fatal(err)
	}
	fresh, err := svc.newConnections(ctx, Viewer{}, newConnectionsIn{
		Start: base.Format(time.RFC3339), End: base.Add(time.Minute).Format(time.RFC3339), Lookback: "1h",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(fresh.Pairs) != 1 || fresh.Pairs[0].SrcNodeID != "ada" || fresh.Pairs[0].DstNodeID != "carol" {
		t.Fatalf("new pairs = %+v, want only ada -> carol", fresh.Pairs)
	}
}
