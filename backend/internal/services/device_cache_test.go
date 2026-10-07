package services

import (
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
	if byLegacy == nil || byStable == nil || byLegacy.ID != "nAliceLaptop1CNTRL" || byLegacy != byStable {
		t.Fatalf("legacy and stable ids should resolve to one entry: legacy=%v stable=%v", byLegacy, byStable)
	}
	ids := cache.EquivalentIDs("5973675649221043")
	if len(ids) != 2 || ids[0] != "nAliceLaptop1CNTRL" || ids[1] != "5973675649221043" {
		t.Fatalf("equivalent ids = %v", ids)
	}
}

func TestDeviceCache_MergesFlowMetadataByTailscaleAddress(t *testing.T) {
	cache := NewDeviceCache()
	cache.Update([]Device{{
		ID:        "nAliceLaptop1CNTRL",
		LegacyID:  "5973675649221043",
		Name:      "laptop.example.ts.net",
		Hostname:  "laptop",
		User:      "alice@example.com",
		Addresses: []string{"100.64.0.8"},
	}})

	cache.UpsertFromFlowLogMetadata(map[string]any{
		"srcNode": map[string]any{
			"nodeId":    "nOtherSeen11CNTRL",
			"name":      "laptop.example.ts.net",
			"addresses": []any{"100.64.0.8", "fd7a:115c:a1e0::8"},
			"user":      "alice@example.com",
		},
	})

	if len(cache.Devices()) != 1 {
		t.Fatalf("address fallback should merge, got %+v", cache.Devices())
	}
	if got := cache.GetDevice("nOtherSeen11CNTRL"); got == nil || got.ID != "nAliceLaptop1CNTRL" {
		t.Fatalf("flow id should alias the API device, got %+v", got)
	}
	if cache.ResolveIP("fd7a:115c:a1e0::8") != "nAliceLaptop1CNTRL" {
		t.Fatalf("merged tailscale address should resolve to the stable id")
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
