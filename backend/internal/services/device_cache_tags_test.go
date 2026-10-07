package services

import (
	"slices"
	"testing"
	"time"

	"github.com/rajsinghtech/tsflow/backend/internal/database"
)

// The device list is the current state. Flow-log metadata is a snapshot from
// whenever the log was written, and node_metadata keeps it for the whole
// retention window, so it must not add tags or addresses a device no longer
// has.
func TestDeviceCacheAPITagsAndAddressesAreAuthoritative(t *testing.T) {
	cache := NewDeviceCache()
	cache.Update([]Device{{
		ID:        "nBuild0001CNTRL",
		NodeID:    "nBuild0001CNTRL",
		Name:      "build.example.ts.net",
		Hostname:  "build",
		Addresses: []string{"100.64.0.20", "fd7a:115c:a1e0::20"},
		Tags:      []string{"tag:prod"},
	}})
	// Retagged from tag:ci to tag:prod, and an old address since released.
	cache.UpsertNodeMetadata([]database.NodeMetadata{{
		NodeID:  "nBuild0001CNTRL",
		Name:    "build.example.ts.net",
		IPs:     []string{"100.64.0.20", "100.64.0.99"},
		Tags:    []string{"tag:ci"},
		Updated: time.Now().Add(-6 * time.Hour),
	}})
	cache.UpsertFromFlowLogMetadata(map[string]any{
		"srcNode": map[string]any{
			"nodeId":    "nBuild0001CNTRL",
			"name":      "build.example.ts.net",
			"addresses": []any{"100.64.0.20"},
			"tags":      []any{"tag:ci", "tag:old"},
		},
	})

	devices := cache.Devices()
	if len(devices) != 1 {
		t.Fatalf("devices = %+v, want one", devices)
	}
	if got := devices[0].Tags; !slices.Equal(got, []string{"tag:prod"}) {
		t.Fatalf("tags = %v, want the device list's [tag:prod]", got)
	}
	if got := devices[0].Addresses; !slices.Equal(got, []string{"100.64.0.20", "fd7a:115c:a1e0::20"}) {
		t.Fatalf("addresses = %v, want the device list's addresses", got)
	}
	// Flows from before the change still belong to this device.
	if got := cache.ResolveIP("100.64.0.99"); got != "nBuild0001CNTRL" {
		t.Fatalf("old address resolves to %q, want the device", got)
	}
}

// Flow-only devices (deleted, or not in the device list) have nothing better
// than the logs, so their observations still accumulate.
func TestDeviceCacheFlowOnlyTagsStillMerge(t *testing.T) {
	cache := NewDeviceCache()
	cache.UpsertNodeMetadata([]database.NodeMetadata{{NodeID: "nGone0001CNTRL", Name: "gone.example.ts.net", IPs: []string{"100.64.0.50"}, Tags: []string{"tag:a"}}})
	cache.UpsertNodeMetadata([]database.NodeMetadata{{NodeID: "nGone0001CNTRL", Name: "gone.example.ts.net", IPs: []string{"100.64.0.50"}, Tags: []string{"tag:b"}}})
	entry := cache.GetDevice("nGone0001CNTRL")
	if entry == nil || !slices.Equal(entry.Tags, []string{"tag:a", "tag:b"}) {
		t.Fatalf("flow-only entry = %+v, want merged tags", entry)
	}
}
