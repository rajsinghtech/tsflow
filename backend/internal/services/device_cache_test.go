package services

import (
	"testing"
	"time"
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
