package services

import (
	"fmt"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/rajsinghtech/tsflow/backend/internal/database"
)

func TestDeviceCache_UpdateAndResolve(t *testing.T) {
	cache := NewDeviceCache()

	devices := []Device{
		{
			ID:        "device1",
			Name:      "laptop.example.ts.net",
			Hostname:  "laptop",
			Addresses: []string{"100.1.1.1", "fd7a:115c:a1e0::1"},
		},
		{
			ID:        "device2",
			Name:      "server.example.ts.net",
			Hostname:  "server",
			Addresses: []string{"100.1.1.2"},
		},
	}

	cache.Update(devices)

	// Resolve known IPs
	if id := cache.ResolveIP("100.1.1.1"); id != "device1" {
		t.Errorf("expected device1, got %s", id)
	}
	if id := cache.ResolveIP("100.1.1.2"); id != "device2" {
		t.Errorf("expected device2, got %s", id)
	}
	// IPv6
	if id := cache.ResolveIP("fd7a:115c:a1e0::1"); id != "device1" {
		t.Errorf("expected device1 for IPv6, got %s", id)
	}

	// Unknown IP returns itself
	if id := cache.ResolveIP("192.168.1.1"); id != "192.168.1.1" {
		t.Errorf("expected 192.168.1.1, got %s", id)
	}
}

func TestDeviceCache_GetDevice(t *testing.T) {
	cache := NewDeviceCache()
	cache.Update([]Device{
		{ID: "d1", Name: "test.ts.net", Hostname: "test", Addresses: []string{"100.1.1.1"}},
	})

	entry := cache.GetDevice("d1")
	if entry == nil {
		t.Fatal("expected device entry, got nil")
	}
	if entry.Hostname != "test" {
		t.Errorf("expected hostname=test, got %s", entry.Hostname)
	}

	if entry := cache.GetDevice("unknown"); entry != nil {
		t.Errorf("expected nil for unknown device, got %v", entry)
	}
}

func TestDeviceCache_NeedsRefresh(t *testing.T) {
	cache := NewDeviceCache()

	// Fresh cache with no data should need refresh
	if !cache.NeedsRefresh(5 * time.Minute) {
		t.Error("empty cache should need refresh")
	}

	cache.Update([]Device{
		{ID: "d1", Addresses: []string{"100.1.1.1"}},
	})

	// Just updated - should not need refresh
	if cache.NeedsRefresh(5 * time.Minute) {
		t.Error("just-updated cache should not need refresh")
	}
}

func TestDeviceCache_Owner(t *testing.T) {
	cache := NewDeviceCache()
	cache.Update([]Device{
		{
			ID:        "device1",
			Name:      "laptop.example.ts.net",
			Hostname:  "laptop",
			User:      "user@example.com",
			Addresses: []string{"100.1.1.1"},
		},
		{
			ID:        "device2",
			Name:      "server.example.ts.net",
			Hostname:  "server",
			User:      "",
			Addresses: []string{"100.1.1.2"},
		},
	})

	entry := cache.GetDevice("device1")
	if entry == nil {
		t.Fatal("expected device1 entry, got nil")
	}
	if entry.Owner != "user@example.com" {
		t.Errorf("expected owner=user@example.com, got %q", entry.Owner)
	}

	entry2 := cache.GetDevice("device2")
	if entry2 == nil {
		t.Fatal("expected device2 entry, got nil")
	}
	if entry2.Owner != "" {
		t.Errorf("expected empty owner, got %q", entry2.Owner)
	}
}

func TestDeviceCache_UpsertNodeMetadata(t *testing.T) {
	cache := NewDeviceCache()
	cache.Update([]Device{
		{ID: "numeric-id", Name: "api-device.example.ts.net", Hostname: "api-device", Addresses: []string{"100.1.1.1"}},
	})

	cache.UpsertNodeMetadata([]database.NodeMetadata{{
		NodeID:   "n5ZfK4a5pz11CNTRL",
		Name:     "garage.keiretsu.ts.net",
		Hostname: "garage",
		Owner:    "ops@example.com",
		IPs:      []string{"100.64.1.2"},
		Tags:     []string{"tag:storage"},
	}})

	if id := cache.ResolveIP("100.64.1.2"); id != "n5ZfK4a5pz11CNTRL" {
		t.Fatalf("expected raw node ID from metadata IP, got %s", id)
	}
	entry := cache.GetDevice("n5ZfK4a5pz11CNTRL")
	if entry == nil {
		t.Fatal("expected node metadata entry")
	}
	if entry.Hostname != "garage" || entry.Name != "garage.keiretsu.ts.net" {
		t.Fatalf("unexpected node metadata entry: %+v", entry)
	}
}

func TestPreferStableDeviceID(t *testing.T) {
	canonical, alias := preferStableDeviceID("5973675649221043", "nAliceLaptop1CNTRL")
	if canonical != "nAliceLaptop1CNTRL" || alias != "5973675649221043" {
		t.Fatalf("canonical=%q alias=%q", canonical, alias)
	}
	canonical, alias = preferStableDeviceID("device-1", "")
	if canonical != "device-1" || alias != "" {
		t.Fatalf("single id canonical=%q alias=%q", canonical, alias)
	}
}

func TestDeviceCache_MergesFlowMetadataIntoAPIDevice(t *testing.T) {
	cache := NewDeviceCache()
	cache.Update([]Device{{
		ID:        "nAliceLaptop1CNTRL",
		LegacyID:  "5973675649221043",
		Name:      "laptop.example.ts.net",
		Hostname:  "laptop",
		User:      "alice@example.com",
		Addresses: []string{"100.64.0.8", "fd7a:115c:a1e0::8"},
		Tags:      []string{"tag:eng"},
	}})

	cache.UpsertFromFlowLogMetadata(map[string]any{
		"srcNode": map[string]any{
			"nodeId":    "5973675649221043",
			"name":      "laptop.example.ts.net",
			"user":      "alice@example.com",
			"addresses": []any{"100.64.0.8"},
		},
	})
	cache.UpsertNodeMetadata([]database.NodeMetadata{{
		NodeID:   "5973675649221043",
		Name:     "laptop.example.ts.net",
		Hostname: "laptop",
		Owner:    "alice@example.com",
		IPs:      []string{"100.64.0.8"},
	}})

	devices := cache.Devices()
	if len(devices) != 1 {
		t.Fatalf("expected 1 device, got %d: %+v", len(devices), devices)
	}
	if devices[0].ID != "nAliceLaptop1CNTRL" {
		t.Fatalf("expected stable id, got %s", devices[0].ID)
	}
	if devices[0].User != "alice@example.com" {
		t.Fatalf("expected user login to survive the merge, got %q", devices[0].User)
	}
	if cache.ResolveIP("100.64.0.8") != "nAliceLaptop1CNTRL" {
		t.Fatalf("ResolveIP = %s", cache.ResolveIP("100.64.0.8"))
	}
	byLegacy := cache.GetDevice("5973675649221043")
	byStable := cache.GetDevice("nAliceLaptop1CNTRL")
	if byLegacy == nil || byStable == nil || byLegacy.ID != "nAliceLaptop1CNTRL" || byStable.ID != byLegacy.ID {
		t.Fatalf("legacy and stable ids should resolve to one entry: legacy=%v stable=%v", byLegacy, byStable)
	}
	ids := cache.EquivalentIDs("5973675649221043")
	if len(ids) != 2 || ids[0] != "nAliceLaptop1CNTRL" || ids[1] != "5973675649221043" {
		t.Fatalf("equivalent ids = %v", ids)
	}
}

func TestDeviceCache_MergesFlowMetadataByTailscaleAddress(t *testing.T) {
	// The device list reported only the numeric id, so the stable id in the
	// flow log can only be tied to it by the shared Tailscale address.
	cache := NewDeviceCache()
	cache.Update([]Device{{
		ID:        "5973675649221043",
		Name:      "laptop.example.ts.net",
		Hostname:  "laptop",
		User:      "alice@example.com",
		Addresses: []string{"100.64.0.8"},
	}})

	cache.UpsertFromFlowLogMetadata(map[string]any{
		"srcNode": map[string]any{
			"nodeId":    "nAliceLaptop1CNTRL",
			"name":      "laptop.example.ts.net",
			"addresses": []any{"100.64.0.8", "fd7a:115c:a1e0::8"},
			"user":      "alice@example.com",
		},
	})

	if len(cache.Devices()) != 1 {
		t.Fatalf("address fallback should merge, got %+v", cache.Devices())
	}
	if got := cache.GetDevice("5973675649221043"); got == nil || got.ID != "nAliceLaptop1CNTRL" {
		t.Fatalf("numeric id should alias the stable id, got %+v", got)
	}
	if cache.ResolveIP("fd7a:115c:a1e0::8") != "nAliceLaptop1CNTRL" {
		t.Fatalf("merged tailscale address should resolve to the stable id")
	}
}

func TestDeviceCache_ReusedAddressDoesNotMergeDistinctNodes(t *testing.T) {
	// A deleted ephemeral node left metadata behind, and its address was
	// later given to a new tagged node. They are different nodes.
	now := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	cache := NewDeviceCache()
	cache.Update([]Device{{
		ID:        "nNewBuild1CNTRL",
		NodeID:    "nNewBuild1CNTRL",
		LegacyID:  "222",
		Name:      "build.example.ts.net",
		Tags:      []string{"tag:ci"},
		Addresses: []string{"100.64.0.30"},
	}})
	cache.UpsertNodeMetadata([]database.NodeMetadata{
		{NodeID: "nOldEphm1CNTRL", Name: "ci-old.example.ts.net", Owner: "bob@example.com", IPs: []string{"100.64.0.30"}, Updated: now.Add(-48 * time.Hour)},
		{NodeID: "111", Name: "ci-old.example.ts.net", Owner: "bob@example.com", IPs: []string{"100.64.0.30"}, Updated: now.Add(-48 * time.Hour)},
	})

	live := cache.GetDevice("nNewBuild1CNTRL")
	if live == nil || live.Owner != "" {
		t.Fatalf("live tagged device must not inherit the old node's login: %+v", live)
	}
	if got := cache.ResolveIP("100.64.0.30"); got != "nNewBuild1CNTRL" {
		t.Fatalf("reused address should resolve to the live device, got %s", got)
	}
	if ids := cache.EquivalentIDs("nNewBuild1CNTRL"); len(ids) != 2 || ids[1] != "222" {
		t.Fatalf("old node ids must not alias the live device: %v", ids)
	}
	if pruned := cache.PruneFlowOnly(24*time.Hour, now); pruned == 0 {
		t.Fatal("stale rows for the deleted node should be prunable")
	}
	if len(cache.Devices()) != 1 || cache.ResolveIP("100.64.0.30") != "nNewBuild1CNTRL" {
		t.Fatalf("after prune: %+v", cache.Devices())
	}
}

func TestDeviceCache_LargeTailnetMergeAndPruneAreLinear(t *testing.T) {
	// 20k API nodes, each with a numeric flow-log row, plus 20k deleted
	// ephemeral nodes seen first by numeric id and then by stable id.
	// Rekeying or pruning that scans every alias or address per device
	// takes tens of seconds here.
	const n = 20000
	now := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	stale := now.Add(-60 * 24 * time.Hour)
	devices := make([]Device, 0, n)
	metadata := make([]database.NodeMetadata, 0, 3*n)
	for i := 0; i < n; i++ {
		ip := fmt.Sprintf("100.64.%d.%d", i/256, i%256)
		legacy := strconv.Itoa(1000000 + i)
		devices = append(devices, Device{ID: fmt.Sprintf("nApi%07dCNTRL", i), LegacyID: legacy, Addresses: []string{ip}})
		metadata = append(metadata, database.NodeMetadata{NodeID: legacy, IPs: []string{ip}, Updated: now})
	}
	for i := 0; i < n; i++ {
		ip := fmt.Sprintf("100.100.%d.%d", i/256, i%256)
		metadata = append(metadata,
			database.NodeMetadata{NodeID: strconv.Itoa(9000000 + i), IPs: []string{ip}, Updated: stale},
			database.NodeMetadata{NodeID: fmt.Sprintf("nEph%07dCNTRL", i), IPs: []string{ip}, Updated: stale},
		)
	}

	started := time.Now()
	cache := NewDeviceCache()
	cache.Update(devices)
	cache.UpsertNodeMetadata(metadata)
	pruned := cache.PruneFlowOnly(30*24*time.Hour, now)
	elapsed := time.Since(started)

	if pruned != n || len(cache.Devices()) != n {
		t.Fatalf("pruned=%d remaining=%d", pruned, len(cache.Devices()))
	}
	if ids := cache.EquivalentIDs("1000042"); len(ids) != 2 || ids[0] != "nApi0000042CNTRL" {
		t.Fatalf("equivalent ids = %v", ids)
	}
	if elapsed > 10*time.Second {
		t.Fatalf("merge and prune of %d nodes took %v", 2*n, elapsed)
	}
}

func TestDeviceCache_LookupsDoNotRaceWithMerges(t *testing.T) {
	// Run with -race. Object-store ingest merges into cached entries while
	// handlers read them through GetDevice and GetDeviceByIP.
	cache := NewDeviceCache()
	cache.Update([]Device{{ID: "nTagged1CNTRL", LegacyID: "777", Addresses: []string{"100.64.0.21"}}})
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			cache.UpsertFromFlowLogMetadata(map[string]any{"srcNode": map[string]any{
				"nodeId":    "777",
				"user":      "bob@example.com",
				"addresses": []any{"100.64.0.21", fmt.Sprintf("fd7a:115c:a1e0::%x", i)},
			}})
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			if entry := cache.GetDeviceByIP("100.64.0.21"); entry != nil {
				_ = entry.Owner + entry.ID
				_ = len(entry.IPs)
			}
			if entry := cache.GetDevice("777"); entry != nil {
				_ = entry.Owner
			}
		}
	}()
	wg.Wait()
	if got := cache.GetDevice("777"); got == nil || got.Owner != "bob@example.com" {
		t.Fatalf("merged device = %+v", got)
	}
}

func TestDeviceCache_DoesNotMergeOnNonTailscaleAddress(t *testing.T) {
	cache := NewDeviceCache()
	cache.Update([]Device{{
		ID:        "nAliceLaptop1CNTRL",
		Name:      "laptop.example.ts.net",
		Addresses: []string{"192.168.1.9"},
	}})
	cache.UpsertNodeMetadata([]database.NodeMetadata{{
		NodeID: "424242",
		Name:   "other.example.ts.net",
		IPs:    []string{"192.168.1.9"},
	}})
	if len(cache.Devices()) != 2 {
		t.Fatalf("shared LAN address must not merge devices, got %+v", cache.Devices())
	}
}

func TestDeviceCache_KeepsFlowOnlyDevice(t *testing.T) {
	cache := NewDeviceCache()
	cache.UpsertFromFlowLogMetadata(map[string]any{
		"dstNodes": []any{map[string]any{
			"nodeId":    "5973675649221043",
			"name":      "retired.example.ts.net",
			"user":      "alice@example.com",
			"addresses": []any{"100.64.0.9"},
		}},
	})
	devices := cache.Devices()
	if len(devices) != 1 {
		t.Fatalf("expected the flow-only device, got %+v", devices)
	}
	if devices[0].ID != "5973675649221043" || devices[0].User != "alice@example.com" {
		t.Fatalf("flow-only device = %+v", devices[0])
	}
}

func TestDeviceIsOnline(t *testing.T) {
	now := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	if !deviceIsOnline(true, time.Time{}, now) {
		t.Fatal("a node connected to control is online even with no lastSeen")
	}
	if !deviceIsOnline(false, now.Add(-time.Minute), now) {
		t.Fatal("a node seen a minute ago is online")
	}
	if deviceIsOnline(false, now.Add(-time.Hour), now) {
		t.Fatal("a node last seen an hour ago is offline")
	}
	if deviceIsOnline(false, time.Time{}, now) {
		t.Fatal("a node with no lastSeen and no control connection is offline")
	}
}

func TestDeviceCache_TaggedDeviceKeepsCreatorLogin(t *testing.T) {
	cache := NewDeviceCache()
	cache.Update([]Device{{
		ID:                 "nTaggedBuild1CNTRL",
		NodeID:             "nTaggedBuild1CNTRL",
		LegacyID:           "5973675649221043",
		Name:               "build.example.ts.net",
		Hostname:           "build",
		User:               "",
		OS:                 "linux",
		Addresses:          []string{"100.64.0.21"},
		Online:             true,
		ConnectedToControl: true,
		Created:            "2026-01-02T03:04:05Z",
		ClientVersion:      "1.84.0",
		Tags:               []string{"tag:ci"},
		Authorized:         true,
	}})
	cache.UpsertFromFlowLogMetadata(map[string]any{
		"srcNode": map[string]any{
			"nodeId":    "5973675649221043",
			"name":      "build.example.ts.net",
			"user":      "alice@example.com",
			"tags":      []any{"tag:ci"},
			"addresses": []any{"100.64.0.21"},
		},
	})

	devices := cache.Devices()
	if len(devices) != 1 {
		t.Fatalf("expected one merged device, got %+v", devices)
	}
	got := devices[0]
	if got.ID != "nTaggedBuild1CNTRL" || got.NodeID != "nTaggedBuild1CNTRL" {
		t.Fatalf("id=%s nodeId=%s", got.ID, got.NodeID)
	}
	if got.User != "alice@example.com" {
		t.Fatalf("creator login was not copied onto the tagged device: %+v", got)
	}
	if got.OS != "linux" || !got.Online || !got.ConnectedToControl || got.LastSeen != "" ||
		got.Created != "2026-01-02T03:04:05Z" || got.ClientVersion != "1.84.0" {
		t.Fatalf("API detail fields were dropped: %+v", got)
	}
	if cache.GetDevice("5973675649221043") == nil || cache.GetDevice("5973675649221043").Owner != "alice@example.com" {
		t.Fatal("numeric id should resolve to the merged device and its creator login")
	}
}

func TestDeviceCache_PruneStaleFlowOnlyDevices(t *testing.T) {
	cache := NewDeviceCache()
	now := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	cache.Update([]Device{{
		ID:        "nLive1CNTRL",
		Name:      "live.example.ts.net",
		User:      "alice@example.com",
		OS:        "macOS",
		Online:    true,
		Addresses: []string{"100.64.0.1"},
	}})
	cache.UpsertNodeMetadata([]database.NodeMetadata{
		{
			NodeID:  "111",
			Name:    "gone.example.ts.net",
			Owner:   "bob@example.com",
			IPs:     []string{"100.64.0.50"},
			Updated: now.Add(-48 * time.Hour),
		},
		{
			NodeID:  "222",
			Name:    "recent.example.ts.net",
			Owner:   "carol@example.com",
			IPs:     []string{"100.64.0.51"},
			Updated: now.Add(-time.Hour),
		},
	})

	if pruned := cache.PruneFlowOnly(0, now); pruned != 0 {
		t.Fatalf("non-positive retention pruned %d devices", pruned)
	}
	pruned := cache.PruneFlowOnly(24*time.Hour, now)
	if pruned != 1 {
		t.Fatalf("pruned %d devices, want 1", pruned)
	}
	if cache.GetDevice("111") != nil {
		t.Fatal("flow-only device outside retention should be removed")
	}
	if cache.ResolveIP("100.64.0.50") != "100.64.0.50" {
		t.Fatal("pruned device address should no longer resolve")
	}
	if cache.GetDevice("222") == nil || cache.GetDevice("222").Owner != "carol@example.com" {
		t.Fatal("flow-only device inside retention should stay")
	}
	live := cache.GetDevice("nLive1CNTRL")
	if live == nil || live.OS != "macOS" || !live.Online || live.Owner != "alice@example.com" {
		t.Fatalf("API device should stay with its detail fields: %+v", live)
	}
	if len(cache.Devices()) != 2 {
		t.Fatalf("device list = %+v", cache.Devices())
	}
}
