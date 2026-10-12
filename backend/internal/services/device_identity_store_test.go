package services

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rajsinghtech/tsflow/backend/internal/config"
	"github.com/rajsinghtech/tsflow/backend/internal/database"
)

// In API mode the flow logs carry no node metadata, so the device list is the
// only source of owners and tags. A device refresh must store them, or the
// Me tab and the owner columns that read node_metadata come back empty.
func TestDeviceRefreshStoresIdentitiesInAPIMode(t *testing.T) {
	var devices atomic.Value
	devices.Store(`{"devices":[
		{"id":"101","nodeId":"nAlice1CNTRL","name":"laptop.example.ts.net","hostname":"laptop","user":"alice@example.com","addresses":["100.64.0.1"]},
		{"id":"102","nodeId":"nSrv1CNTRL","name":"db.example.ts.net","hostname":"db","user":"bob@example.com","addresses":["100.64.0.2"],"tags":["tag:db"]}
	]}`)
	var deviceCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/devices") {
			deviceCalls.Add(1)
			_, _ = w.Write([]byte(devices.Load().(string)))
			return
		}
		_, _ = w.Write([]byte(`{"logs":[]}`))
	}))
	defer server.Close()
	store, err := database.NewSQLiteStore(t.TempDir() + "/identities.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := store.Init(ctx); err != nil {
		t.Fatal(err)
	}
	service := NewTailscaleService(&config.Config{TailscaleAPIURL: server.URL, TailscaleTailnet: "example.com", TailscaleAPIKey: "test"})
	poller := NewPoller(service, store, DefaultPollerConfig())

	if err := poller.refreshDeviceCache(ctx); err != nil {
		t.Fatal(err)
	}
	if deviceCalls.Load() == 0 {
		t.Fatal("device list was not fetched")
	}
	got := metadataByID(t, store)
	if m := got["nAlice1CNTRL"]; m.Owner != "alice@example.com" || m.Hostname != "laptop" || len(m.IPs) != 1 || m.IPs[0] != "100.64.0.1" {
		t.Fatalf("alice laptop metadata = %+v", m)
	}
	if m := got["nSrv1CNTRL"]; len(m.Tags) != 1 || m.Tags[0] != "tag:db" || m.Owner != "bob@example.com" {
		t.Fatalf("db metadata = %+v", m)
	}

	now := time.Now()
	viewer, err := store.ListViewerDevices(ctx, database.DefaultTailnetID, "alice@example.com", now.Add(-time.Hour), now, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(viewer) != 1 || viewer[0].Hostname != "laptop" {
		t.Fatalf("viewer devices = %+v, want alice's laptop", viewer)
	}

	// A changed owner is written on the next refresh. The literal
	// "tagged-devices" is not a login and keeps the stored owner.
	devices.Store(`{"devices":[
		{"id":"101","nodeId":"nAlice1CNTRL","name":"laptop.example.ts.net","hostname":"laptop","user":"carol@example.com","addresses":["100.64.0.1"]},
		{"id":"102","nodeId":"nSrv1CNTRL","name":"db.example.ts.net","hostname":"db","user":"tagged-devices","addresses":["100.64.0.2"],"tags":["tag:db"]}
	]}`)
	if err := poller.refreshDeviceCache(ctx); err != nil {
		t.Fatal(err)
	}
	got = metadataByID(t, store)
	if got["nAlice1CNTRL"].Owner != "carol@example.com" {
		t.Fatalf("owner after change = %q, want carol", got["nAlice1CNTRL"].Owner)
	}
	if got["nSrv1CNTRL"].Owner != "bob@example.com" {
		t.Fatalf("tagged device owner = %q, want the stored login kept", got["nSrv1CNTRL"].Owner)
	}
}

// Unchanged devices are not written again until the rewrite interval passes.
func TestPersistDeviceIdentitiesWritesOnlyChanges(t *testing.T) {
	store := &countingMetadataStore{}
	poller := &Poller{store: store}
	list := []Device{{ID: "nA", Hostname: "a", User: "alice@example.com"}, {ID: "nB", Hostname: "b"}}
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	if err := poller.persistDeviceIdentities(context.Background(), list, now); err != nil {
		t.Fatal(err)
	}
	if store.rows != 2 {
		t.Fatalf("first write = %d rows, want 2", store.rows)
	}
	store.rows = 0
	list[1].Hostname = "b2"
	if err := poller.persistDeviceIdentities(context.Background(), list, now.Add(5*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if store.rows != 1 {
		t.Fatalf("second write = %d rows, want only the changed one", store.rows)
	}
	store.rows = 0
	if err := poller.persistDeviceIdentities(context.Background(), list, now.Add(identityRewriteInterval)); err != nil {
		t.Fatal(err)
	}
	if store.rows != 2 {
		t.Fatalf("rewrite = %d rows, want all", store.rows)
	}
}

type countingMetadataStore struct {
	database.Store
	rows int
}

func (s *countingMetadataStore) UpsertNodeMetadata(_ context.Context, _ string, nodes []database.NodeMetadata) error {
	s.rows += len(nodes)
	return nil
}

func metadataByID(t *testing.T, store *database.SQLiteStore) map[string]database.NodeMetadata {
	t.Helper()
	rows, err := store.GetNodeMetadata(context.Background(), database.DefaultTailnetID)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]database.NodeMetadata{}
	for _, row := range rows {
		out[row.NodeID] = row
	}
	return out
}
