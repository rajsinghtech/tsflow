package services

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/rajsinghtech/tsflow/backend/internal/database"
)

func TestDeviceCacheDevicesNeverHaveNullLists(t *testing.T) {
	cache := NewDeviceCache()
	cache.Update([]Device{{ID: "nBare0001CNTRL", Name: "bare.example.ts.net", Addresses: []string{}}})
	cache.UpsertNodeMetadata([]database.NodeMetadata{{NodeID: "nGone0001CNTRL", Name: "gone.example.ts.net"}})
	body, err := json.Marshal(cache.Devices())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "null") {
		t.Fatalf("devices have a null list: %s", body)
	}
}
