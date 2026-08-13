package database

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestNewSQLiteStore(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	store, err := NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	ctx := context.Background()
	if err := store.Init(ctx); err != nil {
		t.Fatal(err)
	}

	// Verify file was created
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		t.Error("database file was not created")
	}
}

func TestPollState(t *testing.T) {
	store := setupTestDB(t)
	ctx := context.Background()

	// Initial state should have zero time
	state, err := store.GetPollState(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !state.LastPollEnd.IsZero() {
		t.Errorf("expected zero last poll end, got %v", state.LastPollEnd)
	}

	// Update and verify
	now := time.Now().UTC().Truncate(time.Second)
	if err := store.UpdatePollState(ctx, now); err != nil {
		t.Fatal(err)
	}

	state, err = store.GetPollState(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if state.LastPollEnd.Truncate(time.Second) != now {
		t.Errorf("expected %v, got %v", now, state.LastPollEnd)
	}
}

func TestPollStateDoesNotMoveBackward(t *testing.T) {
	store := setupTestDB(t)
	ctx := context.Background()
	future := time.Date(2026, 5, 8, 13, 45, 0, 0, time.UTC)
	past := future.Add(-time.Hour)

	if err := store.UpdatePollState(ctx, future); err != nil {
		t.Fatal(err)
	}
	if err := store.UpdatePollState(ctx, past); err != nil {
		t.Fatal(err)
	}

	state, err := store.GetPollState(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !state.LastPollEnd.Equal(future) {
		t.Fatalf("poll cursor = %v, want %v", state.LastPollEnd, future)
	}
}

func TestGetStatsReturnsDatabaseErrors(t *testing.T) {
	store := setupTestDB(t)
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetStats(context.Background()); err == nil {
		t.Fatal("expected GetStats to return an error for a closed database")
	}
}

func TestGetDataRange_Empty(t *testing.T) {
	store := setupTestDB(t)
	ctx := context.Background()

	dr, err := store.GetDataRange(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if dr.Count != 0 {
		t.Errorf("expected 0 count, got %d", dr.Count)
	}
}

func TestCleanup_EmptyDB(t *testing.T) {
	store := setupTestDB(t)
	ctx := context.Background()

	deleted, err := store.Cleanup(ctx, 24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if deleted != 0 {
		t.Errorf("expected 0 deleted on empty db, got %d", deleted)
	}
}

func setupTestDB(t *testing.T) *SQLiteStore {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	store, err := NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })

	if err := store.Init(context.Background()); err != nil {
		t.Fatal(err)
	}
	return store
}

func TestInit_Migration(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	store, err := NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	ctx := context.Background()

	// Manually create old-style minutely tables to simulate a pre-migration DB
	oldSchema := `
		CREATE TABLE node_pairs_minutely (
			bucket INTEGER NOT NULL,
			src_node_id TEXT NOT NULL,
			dst_node_id TEXT NOT NULL,
			traffic_type TEXT NOT NULL,
			tx_bytes INTEGER DEFAULT 0,
			rx_bytes INTEGER DEFAULT 0,
			tx_pkts INTEGER DEFAULT 0,
			rx_pkts INTEGER DEFAULT 0,
			flow_count INTEGER DEFAULT 0,
			protocols TEXT DEFAULT '[]',
			ports TEXT DEFAULT '[]',
			PRIMARY KEY (bucket, src_node_id, dst_node_id, traffic_type)
		);
		INSERT INTO node_pairs_minutely VALUES (1000, 'a', 'b', 'virtual', 100, 50, 1, 1, 1, '[]', '[]');
		CREATE TABLE bandwidth_minutely (bucket INTEGER PRIMARY KEY, tx_bytes INTEGER DEFAULT 0, rx_bytes INTEGER DEFAULT 0);
		CREATE TABLE bandwidth_by_node_minutely (bucket INTEGER NOT NULL, node_id TEXT NOT NULL, tx_bytes INTEGER DEFAULT 0, rx_bytes INTEGER DEFAULT 0, PRIMARY KEY (bucket, node_id));
		CREATE TABLE traffic_stats_minutely (bucket INTEGER PRIMARY KEY, tcp_bytes INTEGER DEFAULT 0, udp_bytes INTEGER DEFAULT 0, other_proto_bytes INTEGER DEFAULT 0, virtual_bytes INTEGER DEFAULT 0, subnet_bytes INTEGER DEFAULT 0, physical_bytes INTEGER DEFAULT 0, total_flows INTEGER DEFAULT 0, unique_pairs INTEGER DEFAULT 0, top_ports TEXT DEFAULT '[]');
		CREATE TABLE poll_state (id INTEGER PRIMARY KEY CHECK (id = 1), last_poll_end DATETIME, updated_at DATETIME DEFAULT CURRENT_TIMESTAMP);
		INSERT OR IGNORE INTO poll_state VALUES (1, NULL, CURRENT_TIMESTAMP);
		CREATE TABLE node_pairs_hourly (bucket INTEGER, src_node_id TEXT, dst_node_id TEXT, traffic_type TEXT, tx_bytes INTEGER DEFAULT 0, rx_bytes INTEGER DEFAULT 0, tx_pkts INTEGER DEFAULT 0, rx_pkts INTEGER DEFAULT 0, flow_count INTEGER DEFAULT 0, protocols TEXT DEFAULT '[]', ports TEXT DEFAULT '[]', PRIMARY KEY (bucket, src_node_id, dst_node_id, traffic_type));
		CREATE TABLE node_pairs_daily  (bucket INTEGER, src_node_id TEXT, dst_node_id TEXT, traffic_type TEXT, tx_bytes INTEGER DEFAULT 0, rx_bytes INTEGER DEFAULT 0, tx_pkts INTEGER DEFAULT 0, rx_pkts INTEGER DEFAULT 0, flow_count INTEGER DEFAULT 0, protocols TEXT DEFAULT '[]', ports TEXT DEFAULT '[]', PRIMARY KEY (bucket, src_node_id, dst_node_id, traffic_type));
		CREATE TABLE flow_logs_current (id INTEGER PRIMARY KEY AUTOINCREMENT, logged_at DATETIME NOT NULL, node_id TEXT NOT NULL, traffic_type TEXT NOT NULL, protocol INTEGER DEFAULT 0, src_ip TEXT NOT NULL, src_port INTEGER DEFAULT 0, dst_ip TEXT NOT NULL, dst_port INTEGER DEFAULT 0, tx_bytes INTEGER DEFAULT 0, rx_bytes INTEGER DEFAULT 0, tx_pkts INTEGER DEFAULT 0, rx_pkts INTEGER DEFAULT 0);
	`
	if _, err := store.db.ExecContext(ctx, oldSchema); err != nil {
		t.Fatalf("failed to create old schema: %v", err)
	}

	// Run Init — this should migrate
	if err := store.Init(ctx); err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	// Verify new table exists and old data was preserved
	var count int64
	if err := store.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM node_pairs").Scan(&count); err != nil {
		t.Fatalf("node_pairs table missing after migration: %v", err)
	}
	if count != 1 {
		t.Errorf("expected 1 row in node_pairs after migration, got %d", count)
	}
	var exitColumn int
	if err := store.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM pragma_table_info('traffic_stats') WHERE name = 'exit_bytes'").Scan(&exitColumn); err != nil {
		t.Fatalf("failed to inspect migrated traffic_stats schema: %v", err)
	}
	if exitColumn != 1 {
		t.Fatal("traffic_stats.exit_bytes was not added during migration")
	}

	// Verify old tables were dropped
	for _, table := range []string{"node_pairs_minutely", "node_pairs_hourly", "node_pairs_daily", "flow_logs_current"} {
		var name string
		err := store.db.QueryRowContext(ctx,
			"SELECT name FROM sqlite_master WHERE type='table' AND name=?", table,
		).Scan(&name)
		if err == nil {
			t.Errorf("old table %q still exists after migration", table)
		}
	}
}

func TestInit_Migration_LegacyFlatTables(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	store, err := NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	ctx := context.Background()
	const oldSchema = `
		CREATE TABLE node_pairs (
			bucket INTEGER NOT NULL,
			src_node_id TEXT NOT NULL,
			dst_node_id TEXT NOT NULL,
			traffic_type TEXT NOT NULL,
			tx_bytes INTEGER DEFAULT 0,
			rx_bytes INTEGER DEFAULT 0,
			tx_pkts INTEGER DEFAULT 0,
			rx_pkts INTEGER DEFAULT 0,
			flow_count INTEGER DEFAULT 0,
			protocols TEXT DEFAULT '[]',
			ports TEXT DEFAULT '[]',
			PRIMARY KEY (bucket, src_node_id, dst_node_id, traffic_type)
		);
		INSERT INTO node_pairs
			(bucket, src_node_id, dst_node_id, traffic_type, tx_bytes, rx_bytes,
			 tx_pkts, rx_pkts, flow_count, protocols, ports)
		VALUES (120, 'legacy-a', 'legacy-b', 'virtual', 100, 50, 2, 1, 1, '[6,17]', '[]');

		CREATE TABLE traffic_stats (
			bucket INTEGER PRIMARY KEY,
			tcp_bytes INTEGER DEFAULT 0,
			udp_bytes INTEGER DEFAULT 0,
			other_proto_bytes INTEGER DEFAULT 0,
			virtual_bytes INTEGER DEFAULT 0,
			subnet_bytes INTEGER DEFAULT 0,
			physical_bytes INTEGER DEFAULT 0,
			total_flows INTEGER DEFAULT 0,
			unique_pairs INTEGER DEFAULT 0,
			top_ports TEXT DEFAULT '[]'
		);
		INSERT INTO traffic_stats
			(bucket, tcp_bytes, virtual_bytes, total_flows, unique_pairs, top_ports)
		VALUES (120, 10, 10, 1, 1, '[]');

		CREATE TABLE ingested_objects (
			object_key TEXT PRIMARY KEY,
			last_modified DATETIME,
			size_bytes INTEGER DEFAULT 0,
			flow_count INTEGER DEFAULT 0,
			ingested_at DATETIME DEFAULT CURRENT_TIMESTAMP
		);
		INSERT INTO ingested_objects (object_key, last_modified, size_bytes, flow_count)
		VALUES ('legacy-object.ndjson', '2026-08-13 00:00:00', 42, 1);
	`
	if _, err := store.db.ExecContext(ctx, oldSchema); err != nil {
		t.Fatalf("failed to create legacy schema: %v", err)
	}

	if err := store.Init(ctx); err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	for _, column := range []string{
		"protocol_bytes", "tx_ports", "rx_ports", "tx_protocol_bytes",
		"rx_protocol_bytes", "directional_ports",
	} {
		exists, err := store.columnExists(ctx, "node_pairs", column)
		if err != nil {
			t.Fatalf("failed to inspect node_pairs.%s: %v", column, err)
		}
		if !exists {
			t.Errorf("node_pairs.%s was not added during migration", column)
		}
	}

	pairs, err := store.GetNodePairAggregates(ctx, time.Unix(0, 0).UTC(), time.Unix(600, 0).UTC())
	if err != nil {
		t.Fatalf("querying migrated node pairs failed: %v", err)
	}
	if len(pairs) != 1 {
		t.Fatalf("expected one migrated node pair, got %d", len(pairs))
	}
	if pairs[0].TxBytes != 100 || pairs[0].RxBytes != 50 || pairs[0].FlowCount != 1 {
		t.Fatalf("unexpected migrated node pair: %+v", pairs[0])
	}
	if pairs[0].DirectionalPorts {
		t.Fatal("legacy node pair should not be marked as directional")
	}
	assertProtocolByteMap(t, pairs[0].ProtocolBytes, map[string]int64{"6": 75, "17": 75})

	if err := store.UpsertNodePairAggregates(ctx, []NodePairAggregate{{
		Bucket:        120,
		SrcNodeID:     "legacy-a",
		DstNodeID:     "legacy-b",
		TrafficType:   "virtual",
		TxBytes:       20,
		RxBytes:       5,
		TxPkts:        1,
		RxPkts:        1,
		FlowCount:     1,
		Protocols:     "[6]",
		ProtocolBytes: `{"6":25}`,
		Ports:         "[]",
	}}); err != nil {
		t.Fatalf("upserting into migrated node_pairs failed: %v", err)
	}

	pairs, err = store.GetNodePairAggregates(ctx, time.Unix(0, 0).UTC(), time.Unix(600, 0).UTC())
	if err != nil {
		t.Fatalf("querying upserted node pair failed: %v", err)
	}
	if len(pairs) != 1 {
		t.Fatalf("expected one upserted node pair, got %d", len(pairs))
	}
	if pairs[0].TxBytes != 120 || pairs[0].RxBytes != 55 || pairs[0].FlowCount != 2 {
		t.Fatalf("unexpected upserted node pair: %+v", pairs[0])
	}
	assertProtocolByteMap(t, pairs[0].ProtocolBytes, map[string]int64{"6": 100, "17": 75})

	stats, err := store.GetTrafficStats(ctx, time.Unix(0, 0).UTC(), time.Unix(600, 0).UTC())
	if err != nil {
		t.Fatalf("querying migrated traffic stats failed: %v", err)
	}
	if len(stats) != 1 || stats[0].TCPBytes != 10 || stats[0].ExitBytes != 0 {
		t.Fatalf("unexpected migrated traffic stats: %+v", stats)
	}
	if err := store.UpsertTrafficStats(ctx, []TrafficStats{{
		Bucket:      120,
		TCPBytes:    5,
		ExitBytes:   7,
		TotalFlows:  1,
		UniquePairs: 1,
		TopPorts:    "[]",
	}}); err != nil {
		t.Fatalf("upserting into migrated traffic_stats failed: %v", err)
	}
	stats, err = store.GetTrafficStats(ctx, time.Unix(0, 0).UTC(), time.Unix(600, 0).UTC())
	if err != nil {
		t.Fatalf("querying upserted traffic stats failed: %v", err)
	}
	if len(stats) != 1 || stats[0].TCPBytes != 15 || stats[0].ExitBytes != 7 {
		t.Fatalf("unexpected upserted traffic stats: %+v", stats)
	}

	keys, err := store.GetObjectsNeedingMetadata(ctx, 10)
	if err != nil {
		t.Fatalf("querying migrated ingested objects failed: %v", err)
	}
	if len(keys) != 1 || keys[0] != "legacy-object.ndjson" {
		t.Fatalf("migrated object metadata queue = %v", keys)
	}
	if err := store.UpsertNodeMetadata(ctx, []NodeMetadata{{NodeID: "legacy-a"}}); err != nil {
		t.Fatalf("seeding migrated object node metadata failed: %v", err)
	}
	if err := store.MarkObjectMetadataHydrated(ctx, keys[0], []string{"legacy-a"}); err != nil {
		t.Fatalf("marking migrated object metadata hydrated failed: %v", err)
	}
	keys, err = store.GetObjectsNeedingMetadata(ctx, 10)
	if err != nil {
		t.Fatalf("querying hydrated ingested objects failed: %v", err)
	}
	if len(keys) != 0 {
		t.Fatalf("hydrated migrated objects still queued: %v", keys)
	}
}

func TestInit_Migration_MalformedProtocolJSON(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	store, err := NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	ctx := context.Background()
	const oldSchema = `
		CREATE TABLE node_pairs (
			bucket INTEGER NOT NULL,
			src_node_id TEXT NOT NULL,
			dst_node_id TEXT NOT NULL,
			traffic_type TEXT NOT NULL,
			tx_bytes INTEGER DEFAULT 0,
			rx_bytes INTEGER DEFAULT 0,
			tx_pkts INTEGER DEFAULT 0,
			rx_pkts INTEGER DEFAULT 0,
			flow_count INTEGER DEFAULT 0,
			protocols TEXT DEFAULT '[]',
			protocol_bytes TEXT DEFAULT '{}',
			ports TEXT DEFAULT '[]',
			PRIMARY KEY (bucket, src_node_id, dst_node_id, traffic_type)
		);
		INSERT INTO node_pairs
			(bucket, src_node_id, dst_node_id, traffic_type, tx_bytes, rx_bytes,
			 flow_count, protocols, protocol_bytes, ports)
		VALUES (120, 'valid-protocols', 'peer', 'virtual', 90, 30, 1, '[6,17]', '{broken', '[]');
		INSERT INTO node_pairs
			(bucket, src_node_id, dst_node_id, traffic_type, tx_bytes, rx_bytes,
			 flow_count, protocols, protocol_bytes, ports)
		VALUES (180, 'invalid-protocols', 'peer', 'virtual', 10, 2, 1, '[6', 'not-json', '[]');
	`
	if _, err := store.db.ExecContext(ctx, oldSchema); err != nil {
		t.Fatalf("failed to create legacy schema: %v", err)
	}

	if err := store.Init(ctx); err != nil {
		t.Fatalf("Init failed for malformed protocol JSON: %v", err)
	}

	var validRaw, invalidRaw string
	if err := store.db.QueryRowContext(ctx,
		"SELECT protocol_bytes FROM node_pairs WHERE src_node_id = ?", "valid-protocols",
	).Scan(&validRaw); err != nil {
		t.Fatalf("failed to read repaired protocol bytes: %v", err)
	}
	if err := store.db.QueryRowContext(ctx,
		"SELECT protocol_bytes FROM node_pairs WHERE src_node_id = ?", "invalid-protocols",
	).Scan(&invalidRaw); err != nil {
		t.Fatalf("failed to read repaired invalid protocol bytes: %v", err)
	}
	assertProtocolByteMap(t, validRaw, map[string]int64{"6": 60, "17": 60})
	assertProtocolByteMap(t, invalidRaw, map[string]int64{})

	pairs, err := store.GetNodePairAggregates(ctx, time.Unix(0, 0).UTC(), time.Unix(600, 0).UTC())
	if err != nil {
		t.Fatalf("querying rows with malformed protocol JSON failed: %v", err)
	}
	if len(pairs) != 2 {
		t.Fatalf("expected two migrated node pairs, got %d", len(pairs))
	}
	for _, pair := range pairs {
		switch pair.SrcNodeID {
		case "valid-protocols":
			assertProtocolByteMap(t, pair.ProtocolBytes, map[string]int64{"6": 60, "17": 60})
		case "invalid-protocols":
			assertProtocolByteMap(t, pair.ProtocolBytes, map[string]int64{})
		default:
			t.Errorf("unexpected migrated node pair: %+v", pair)
		}
	}
}

func assertProtocolByteMap(t *testing.T, raw string, want map[string]int64) {
	t.Helper()
	var got map[string]int64
	if err := json.Unmarshal([]byte(raw), &got); err != nil {
		t.Fatalf("invalid protocol byte JSON %q: %v", raw, err)
	}
	if len(got) != len(want) {
		t.Fatalf("protocol byte map = %v, want %v", got, want)
	}
	for protocol, wantBytes := range want {
		if got[protocol] != wantBytes {
			t.Errorf("protocol %s bytes = %d, want %d", protocol, got[protocol], wantBytes)
		}
	}
}

func TestInit_Idempotent(t *testing.T) {
	store := setupTestDB(t)
	ctx := context.Background()

	// Running Init a second time should not fail
	if err := store.Init(ctx); err != nil {
		t.Fatalf("second Init() failed: %v", err)
	}
}

func TestResolveBucketSize(t *testing.T) {
	cases := []struct {
		rangeSeconds int64
		want         int64
	}{
		{30 * 60, 60},
		{2 * 3600, 60},
		{2*3600 + 1, 3600},
		{48 * 3600, 3600},
		{48*3600 + 1, 86400},
		{30 * 24 * 3600, 86400},
	}
	for _, tc := range cases {
		got := resolveBucketSize(tc.rangeSeconds)
		if got != tc.want {
			t.Errorf("resolveBucketSize(%d) = %d, want %d", tc.rangeSeconds, got, tc.want)
		}
	}
}

func TestCommitObjectIngest_Idempotent(t *testing.T) {
	store := setupTestDB(t)
	ctx := context.Background()
	pollEnd := time.Unix(120, 0).UTC()

	result := ObjectIngestResult{
		Key:          "network/2026/03/31/2026-03-31-00-01-00.ndjson.zst",
		LastModified: pollEnd,
		Size:         123,
		FlowCount:    1,
		NodeMetadata: []NodeMetadata{{
			NodeID:   "n5ZfK4a5pz11CNTRL",
			Name:     "garage.keiretsu.ts.net",
			Hostname: "garage",
			Owner:    "ops@example.com",
			IPs:      []string{"100.64.1.2"},
			Tags:     []string{"tag:storage"},
		}},
		NodePairs: []NodePairAggregate{{
			Bucket:      120,
			SrcNodeID:   "node-a",
			DstNodeID:   "node-b",
			TrafficType: "virtual",
			TxBytes:     100,
			RxBytes:     50,
			TxPkts:      2,
			RxPkts:      1,
			FlowCount:   1,
			Protocols:   "[6]",
			Ports:       "[]",
		}},
		Bandwidth: []BandwidthBucket{{
			Time:    pollEnd,
			TxBytes: 100,
			RxBytes: 50,
		}},
		TrafficStats: []TrafficStats{{
			Bucket:       120,
			TCPBytes:     150,
			VirtualBytes: 150,
			TotalFlows:   1,
			UniquePairs:  1,
			TopPorts:     "[]",
		}},
		PollEnd: pollEnd,
	}

	if err := store.CommitObjectIngest(ctx, result); err != nil {
		t.Fatal(err)
	}
	if err := store.CommitObjectIngest(ctx, result); err != nil {
		t.Fatal(err)
	}

	seen, err := store.IsObjectIngested(ctx, result.Key)
	if err != nil {
		t.Fatal(err)
	}
	if !seen {
		t.Fatal("expected object to be marked ingested")
	}

	pairs, err := store.GetNodePairAggregates(ctx, pollEnd.Add(-time.Minute), pollEnd.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if len(pairs) != 1 {
		t.Fatalf("expected one node pair, got %d", len(pairs))
	}
	if pairs[0].TxBytes != 100 || pairs[0].RxBytes != 50 || pairs[0].FlowCount != 1 {
		t.Fatalf("object ingest was double counted: %+v", pairs[0])
	}

	nodes, err := store.GetNodeMetadata(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 1 {
		t.Fatalf("expected one node metadata row, got %d", len(nodes))
	}
	if nodes[0].NodeID != "n5ZfK4a5pz11CNTRL" || nodes[0].Hostname != "garage" {
		t.Fatalf("unexpected node metadata: %+v", nodes[0])
	}
	if len(nodes[0].IPs) != 1 || nodes[0].IPs[0] != "100.64.1.2" {
		t.Fatalf("unexpected node metadata IPs: %+v", nodes[0].IPs)
	}
}

func TestObjectMetadataHydrationStateRepairsMissingNodes(t *testing.T) {
	store := setupTestDB(t)
	ctx := context.Background()
	result := ObjectIngestResult{
		Key:          "network/test.ndjson",
		NodeMetadata: []NodeMetadata{{NodeID: "node-a"}, {NodeID: "node-b"}},
		PollEnd:      time.Unix(60, 0).UTC(),
	}
	if err := store.CommitObjectIngest(ctx, result); err != nil {
		t.Fatal(err)
	}
	keys, err := store.GetObjectsNeedingMetadata(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 0 {
		t.Fatalf("newly ingested object needs metadata hydration: %v", keys)
	}
	if _, err := store.db.ExecContext(ctx, "DELETE FROM node_metadata WHERE node_id = ?", "node-b"); err != nil {
		t.Fatal(err)
	}
	keys, err = store.GetObjectsNeedingMetadata(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 1 || keys[0] != result.Key {
		t.Fatalf("objects needing repair = %v, want %q", keys, result.Key)
	}
	if err := store.UpsertNodeMetadata(ctx, []NodeMetadata{{NodeID: "node-b"}}); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkObjectMetadataHydrated(ctx, result.Key, []string{"node-a", "node-b"}); err != nil {
		t.Fatal(err)
	}
	keys, err = store.GetObjectsNeedingMetadata(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 0 {
		t.Fatalf("repaired object still needs metadata: %v", keys)
	}
}

func TestGetBandwidth_Bucketing(t *testing.T) {
	store := setupTestDB(t)
	ctx := context.Background()

	// Insert three 1-minute bandwidth rows
	base := int64(1000000)
	base = (base / 60) * 60
	for i := int64(0); i < 3; i++ {
		_, err := store.db.ExecContext(ctx,
			"INSERT INTO bandwidth (bucket, tx_bytes, rx_bytes) VALUES (?, ?, ?)",
			base+i*60, 100, 50,
		)
		if err != nil {
			t.Fatal(err)
		}
	}

	start := time.Unix(base, 0)
	end := time.Unix(base+3*60, 0)

	// Small range (≤2h) → should return 3 individual 1-min buckets
	buckets, err := store.GetBandwidth(ctx, start, end)
	if err != nil {
		t.Fatal(err)
	}
	if len(buckets) != 3 {
		t.Errorf("expected 3 buckets for small range, got %d", len(buckets))
	}

	total := int64(0)
	for _, b := range buckets {
		total += b.TxBytes
	}
	if total != 300 {
		t.Errorf("expected total tx=300, got %d", total)
	}
}
