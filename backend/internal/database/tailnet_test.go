package database

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

const preTailnetSchema = `
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
	tx_ports TEXT DEFAULT '[]',
	rx_ports TEXT DEFAULT '[]',
	tx_protocol_bytes TEXT DEFAULT '{}',
	rx_protocol_bytes TEXT DEFAULT '{}',
	directional_ports INTEGER NOT NULL DEFAULT 0,
	PRIMARY KEY (bucket, src_node_id, dst_node_id, traffic_type)
);
CREATE INDEX idx_node_pairs_bucket ON node_pairs(bucket);
CREATE INDEX idx_node_pairs_src ON node_pairs(src_node_id, bucket);
CREATE INDEX idx_node_pairs_dst ON node_pairs(dst_node_id, bucket);
INSERT INTO node_pairs (
	bucket, src_node_id, dst_node_id, traffic_type, tx_bytes, rx_bytes,
	tx_pkts, rx_pkts, flow_count, protocols, protocol_bytes, ports,
	tx_ports, rx_ports, tx_protocol_bytes, rx_protocol_bytes, directional_ports
) VALUES (
	1700000000, 'node-a', 'node-b', 'virtual', 100, 40,
	2, 1, 1, '[6]', '{"6":140}', '[{"port":443,"proto":6,"bytes":140}]',
	'[]', '[]', '{}', '{}', 0
);

CREATE TABLE bandwidth (
	bucket INTEGER PRIMARY KEY,
	tx_bytes INTEGER DEFAULT 0,
	rx_bytes INTEGER DEFAULT 0
);
INSERT INTO bandwidth (bucket, tx_bytes, rx_bytes) VALUES (1700000000, 100, 40);

CREATE TABLE bandwidth_by_node (
	bucket INTEGER NOT NULL,
	node_id TEXT NOT NULL,
	tx_bytes INTEGER DEFAULT 0,
	rx_bytes INTEGER DEFAULT 0,
	PRIMARY KEY (bucket, node_id)
);
INSERT INTO bandwidth_by_node (bucket, node_id, tx_bytes, rx_bytes) VALUES (1700000000, 'node-a', 100, 40);

CREATE TABLE traffic_stats (
	bucket INTEGER PRIMARY KEY,
	tcp_bytes INTEGER DEFAULT 0,
	udp_bytes INTEGER DEFAULT 0,
	other_proto_bytes INTEGER DEFAULT 0,
	virtual_bytes INTEGER DEFAULT 0,
	exit_bytes INTEGER DEFAULT 0,
	subnet_bytes INTEGER DEFAULT 0,
	physical_bytes INTEGER DEFAULT 0,
	total_flows INTEGER DEFAULT 0,
	unique_pairs INTEGER DEFAULT 0,
	top_ports TEXT DEFAULT '[]'
);
INSERT INTO traffic_stats (
	bucket, tcp_bytes, virtual_bytes, total_flows, unique_pairs, top_ports
) VALUES (1700000000, 140, 140, 1, 1, '[{"port":443,"proto":6,"bytes":140}]');

CREATE TABLE poll_state (
	id INTEGER PRIMARY KEY CHECK (id = 1),
	last_poll_end DATETIME,
	updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
);
INSERT INTO poll_state (id, last_poll_end, updated_at)
VALUES (1, '2026-03-01 12:00:00', '2026-03-01 12:00:01');

CREATE TABLE ingested_objects (
	object_key TEXT PRIMARY KEY,
	last_modified DATETIME,
	size_bytes INTEGER DEFAULT 0,
	flow_count INTEGER DEFAULT 0,
	ingested_at DATETIME DEFAULT CURRENT_TIMESTAMP,
	metadata_hydrated INTEGER NOT NULL DEFAULT 0
);
INSERT INTO ingested_objects (
	object_key, last_modified, size_bytes, flow_count, ingested_at, metadata_hydrated
) VALUES ('network/old.ndjson', '2026-03-01 12:00:00', 42, 1, '2026-03-01 12:00:02', 1);

CREATE TABLE node_metadata (
	node_id TEXT PRIMARY KEY,
	name TEXT DEFAULT '',
	hostname TEXT DEFAULT '',
	owner TEXT DEFAULT '',
	ips TEXT DEFAULT '[]',
	tags TEXT DEFAULT '[]',
	updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
);
INSERT INTO node_metadata (node_id, name, hostname, owner, ips, tags, updated_at)
VALUES ('node-a', 'a.example', 'a', 'ops@example.com', '["100.64.0.1"]', '["tag:a"]', '2026-03-01 12:00:03');

CREATE TABLE object_metadata_nodes (
	object_key TEXT NOT NULL,
	node_id TEXT NOT NULL,
	PRIMARY KEY (object_key, node_id)
);
INSERT INTO object_metadata_nodes (object_key, node_id) VALUES ('network/old.ndjson', 'node-a');
`

func TestTailnetMigrationPreservesRowsAndIsIdempotent(t *testing.T) {
	store := openRawStore(t)
	ctx := context.Background()
	if _, err := store.db.ExecContext(ctx, preTailnetSchema); err != nil {
		t.Fatalf("create pre-tailnet schema: %v", err)
	}
	if err := store.Init(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	assertDefaultTailnetData(t, store)
	schemaBefore := schemaSnapshot(t, store)
	countsBefore := tableCounts(t, store)

	if err := store.Init(ctx); err != nil {
		t.Fatalf("second init: %v", err)
	}
	if schemaAfter := schemaSnapshot(t, store); schemaAfter != schemaBefore {
		t.Fatalf("second init changed schema\nbefore:\n%s\nafter:\n%s", schemaBefore, schemaAfter)
	}
	countsAfter := tableCounts(t, store)
	for table, count := range countsBefore {
		if countsAfter[table] != count {
			t.Fatalf("%s count changed on second init: %d -> %d", table, count, countsAfter[table])
		}
	}
	assertDefaultTailnetData(t, store)

	var leftovers int
	if err := store.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM sqlite_master
		WHERE name LIKE '%__tailnet_new%' OR name = 'poll_state' AND sql LIKE '%CHECK (id = 1)%'
	`).Scan(&leftovers); err != nil {
		t.Fatal(err)
	}
	if leftovers != 0 {
		t.Fatalf("migration left %d temporary or singleton poll_state objects", leftovers)
	}
}

func TestTailnetMigrationRollsBackWhenInterrupted(t *testing.T) {
	store := openRawStore(t)
	ctx := context.Background()
	if _, err := store.db.ExecContext(ctx, `
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
		INSERT INTO node_pairs (
			bucket, src_node_id, dst_node_id, traffic_type, tx_bytes, rx_bytes, flow_count, protocols, ports
		) VALUES (1700000000, 'keep-me', 'peer', 'virtual', 7, 1, 1, '[6]', '[]');
		CREATE TABLE poll_state (
			id INTEGER PRIMARY KEY CHECK (id = 1),
			last_poll_end DATETIME,
			updated_at DATETIME
		);
		INSERT INTO poll_state (id, last_poll_end, updated_at)
		VALUES (1, '2026-03-01 12:00:00', '2026-03-01 12:00:01');
	`); err != nil {
		t.Fatal(err)
	}

	store.migrateFailAfter = "node_pairs"
	err := store.Init(ctx)
	if err == nil || !strings.Contains(err.Error(), "aborted after dropping node_pairs") {
		t.Fatalf("Init error = %v, want aborted migration", err)
	}

	hasTailnet, err := store.columnExists(ctx, "node_pairs", "tailnet_id")
	if err != nil {
		t.Fatal(err)
	}
	if hasTailnet {
		t.Fatal("interrupted migration left node_pairs with tailnet_id")
	}
	var src string
	var txBytes int64
	if err := store.db.QueryRowContext(ctx, "SELECT src_node_id, tx_bytes FROM node_pairs").Scan(&src, &txBytes); err != nil {
		t.Fatalf("pre-tailnet row missing after rollback: %v", err)
	}
	if src != "keep-me" || txBytes != 7 {
		t.Fatalf("rolled back row = %s %d", src, txBytes)
	}
	var tempTables int
	if err := store.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM sqlite_master WHERE name LIKE '%__tailnet_new%'
	`).Scan(&tempTables); err != nil {
		t.Fatal(err)
	}
	if tempTables != 0 {
		t.Fatalf("rollback left %d temporary tables", tempTables)
	}

	store.migrateFailAfter = ""
	if err := store.Init(ctx); err != nil {
		t.Fatalf("retry after aborted migration: %v", err)
	}
	var tailnetID string
	if err := store.db.QueryRowContext(ctx, `
		SELECT tailnet_id, src_node_id, tx_bytes FROM node_pairs
	`).Scan(&tailnetID, &src, &txBytes); err != nil {
		t.Fatal(err)
	}
	if tailnetID != DefaultTailnetID || src != "keep-me" || txBytes != 7 {
		t.Fatalf("retried row = %s %s %d", tailnetID, src, txBytes)
	}
}

func TestFreshSchemaIsTailnetScoped(t *testing.T) {
	store := setupTestDB(t)
	ctx := context.Background()
	for _, table := range []string{
		"node_pairs", "bandwidth", "bandwidth_by_node", "traffic_stats",
		"poll_state", "ingested_objects", "node_metadata", "object_metadata_nodes",
	} {
		ok, err := store.columnExists(ctx, table, "tailnet_id")
		if err != nil {
			t.Fatal(err)
		}
		if !ok {
			t.Fatalf("fresh %s has no tailnet_id", table)
		}
	}
	var pollSQL string
	if err := store.db.QueryRowContext(ctx, `
		SELECT sql FROM sqlite_master WHERE type = 'table' AND name = 'poll_state'
	`).Scan(&pollSQL); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(pollSQL, "CHECK (id = 1)") {
		t.Fatalf("fresh poll_state is still a singleton: %s", pollSQL)
	}
	var pairSQL string
	if err := store.db.QueryRowContext(ctx, `
		SELECT sql FROM sqlite_master WHERE type = 'table' AND name = 'node_pairs'
	`).Scan(&pairSQL); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(pairSQL, "WITHOUT ROWID") || !strings.Contains(pairSQL, "tailnet_id") {
		t.Fatalf("fresh node_pairs schema = %s", pairSQL)
	}
	if _, err := os.Stat(migrationBackupPath(store.dbPath)); !os.IsNotExist(err) {
		t.Fatalf("fresh database wrote a migration backup: %v", err)
	}
}

func TestTailnetIsolation(t *testing.T) {
	store := setupTestDB(t)
	ctx := context.Background()
	base := time.Unix((1700000000/60)*60, 0).UTC()
	end := base.Add(2 * time.Minute)
	for _, tailnetID := range []string{"alpha", "beta"} {
		txBytes := int64(10)
		if tailnetID == "beta" {
			txBytes = 999
		}
		pair := NodePairAggregate{
			Bucket: base.Unix(), SrcNodeID: "node-a", DstNodeID: "node-b", TrafficType: "virtual",
			TxBytes: txBytes, RxBytes: 4, TxPkts: 1, RxPkts: 1, FlowCount: 1,
			Protocols: "[6]", ProtocolBytes: `{"6":14}`, Ports: "[]",
		}
		if tailnetID == "beta" {
			pair.ProtocolBytes = `{"6":1003}`
		}
		if err := store.UpsertNodePairAggregates(ctx, tailnetID, []NodePairAggregate{pair}); err != nil {
			t.Fatal(err)
		}
		if err := store.UpsertBandwidth(ctx, tailnetID, []BandwidthBucket{{Time: base, TxBytes: txBytes, RxBytes: 4}}); err != nil {
			t.Fatal(err)
		}
		if err := store.UpsertNodeBandwidth(ctx, tailnetID, []NodeBandwidth{{Bucket: base.Unix(), NodeID: "node-a", TxBytes: txBytes, RxBytes: 4}}); err != nil {
			t.Fatal(err)
		}
		if err := store.UpsertTrafficStats(ctx, tailnetID, []TrafficStats{{
			Bucket: base.Unix(), TCPBytes: txBytes, VirtualBytes: txBytes, TotalFlows: 1, UniquePairs: 1, TopPorts: "[]",
		}}); err != nil {
			t.Fatal(err)
		}
		if err := store.CommitObjectIngest(ctx, tailnetID, ObjectIngestResult{
			Key: "shared.ndjson", LastModified: base, Size: txBytes, FlowCount: 1,
			NodeMetadata: []NodeMetadata{{NodeID: "node-a", Name: tailnetID, IPs: []string{"100.64.0.1"}, Tags: []string{"tag:n"}}},
			NodePairs:    []NodePairAggregate{pair},
			PollEnd:      base.Add(time.Duration(txBytes) * time.Second),
		}); err != nil {
			t.Fatal(err)
		}
	}

	alphaPairs, err := store.GetNodePairAggregates(ctx, "alpha", base, end)
	if err != nil {
		t.Fatal(err)
	}
	// The direct upsert and the object ingest both add the alpha pair.
	if len(alphaPairs) != 1 || alphaPairs[0].TxBytes != 20 {
		t.Fatalf("alpha pairs = %+v, want tx 20", alphaPairs)
	}
	betaPairs, err := store.GetNodePairAggregates(ctx, "beta", base, end)
	if err != nil {
		t.Fatal(err)
	}
	if len(betaPairs) != 1 || betaPairs[0].TxBytes != 1998 {
		t.Fatalf("beta pairs = %+v, want tx 1998", betaPairs)
	}

	alphaBW, err := store.GetBandwidth(ctx, "alpha", base, end)
	if err != nil {
		t.Fatal(err)
	}
	if len(alphaBW) != 1 || alphaBW[0].TxBytes != 10 {
		t.Fatalf("alpha bandwidth = %+v", alphaBW)
	}
	filtered, err := store.GetBandwidthByTrafficTypes(ctx, "alpha", base, end, []string{"virtual"})
	if err != nil {
		t.Fatal(err)
	}
	if len(filtered) != 1 || filtered[0].TxBytes != 28 {
		t.Fatalf("alpha filtered bandwidth = %+v", filtered)
	}
	nodeBW, err := store.GetNodeBandwidth(ctx, "beta", base, end, "node-a")
	if err != nil {
		t.Fatal(err)
	}
	if len(nodeBW) != 1 || nodeBW[0].TxBytes != 1998 {
		t.Fatalf("beta node bandwidth = %+v", nodeBW)
	}

	stats, err := store.GetTrafficStats(ctx, "alpha", base, end)
	if err != nil {
		t.Fatal(err)
	}
	if len(stats) != 1 || stats[0].TCPBytes != 10 {
		t.Fatalf("alpha traffic stats = %+v", stats)
	}
	derived, err := store.GetTrafficStatsFromNodePairs(ctx, "beta", base, end)
	if err != nil {
		t.Fatal(err)
	}
	if len(derived) != 1 || derived[0].VirtualBytes != 2006 {
		t.Fatalf("beta derived stats = %+v", derived)
	}
	talkers, err := store.GetTopTalkers(ctx, "alpha", base, end, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(talkers) != 2 || talkers[0].TotalBytes+talkers[1].TotalBytes != 56 {
		t.Fatalf("alpha talkers = %+v", talkers)
	}
	pairs, err := store.GetTopPairs(ctx, "beta", base, end, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(pairs) != 1 || pairs[0].TotalBytes != 2006 {
		t.Fatalf("beta pairs = %+v", pairs)
	}
	nodeStats, err := store.GetNodeStats(ctx, "alpha", "node-a", base, end)
	if err != nil {
		t.Fatal(err)
	}
	if nodeStats.TotalTx != 20 {
		t.Fatalf("alpha node stats = %+v", nodeStats)
	}

	alphaSeen, err := store.IsObjectIngested(ctx, "alpha", "shared.ndjson")
	if err != nil {
		t.Fatal(err)
	}
	betaSeen, err := store.IsObjectIngested(ctx, "beta", "shared.ndjson")
	if err != nil {
		t.Fatal(err)
	}
	if !alphaSeen || !betaSeen {
		t.Fatalf("shared object seen alpha=%v beta=%v", alphaSeen, betaSeen)
	}
	if err := store.CommitObjectIngest(ctx, "alpha", ObjectIngestResult{
		Key: "shared.ndjson", NodePairs: []NodePairAggregate{{
			Bucket: base.Unix(), SrcNodeID: "node-a", DstNodeID: "node-b", TrafficType: "virtual", TxBytes: 50,
		}},
	}); err != nil {
		t.Fatal(err)
	}
	alphaPairs, err = store.GetNodePairAggregates(ctx, "alpha", base, end)
	if err != nil {
		t.Fatal(err)
	}
	if alphaPairs[0].TxBytes != 20 {
		t.Fatalf("duplicate alpha object changed tx to %d", alphaPairs[0].TxBytes)
	}

	alphaNodes, err := store.GetNodeMetadata(ctx, "alpha")
	if err != nil {
		t.Fatal(err)
	}
	betaNodes, err := store.GetNodeMetadata(ctx, "beta")
	if err != nil {
		t.Fatal(err)
	}
	if len(alphaNodes) != 1 || alphaNodes[0].Name != "alpha" || len(betaNodes) != 1 || betaNodes[0].Name != "beta" {
		t.Fatalf("metadata alpha=%+v beta=%+v", alphaNodes, betaNodes)
	}

	alphaState, err := store.GetPollState(ctx, "alpha")
	if err != nil {
		t.Fatal(err)
	}
	betaState, err := store.GetPollState(ctx, "beta")
	if err != nil {
		t.Fatal(err)
	}
	if alphaState.LastPollEnd.Equal(betaState.LastPollEnd) {
		t.Fatalf("poll cursors were not isolated: %v", alphaState.LastPollEnd)
	}
	if err := store.UpdatePollState(ctx, "alpha", alphaState.LastPollEnd.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	alphaStateAfter, err := store.GetPollState(ctx, "alpha")
	if err != nil {
		t.Fatal(err)
	}
	if !alphaStateAfter.LastPollEnd.Equal(alphaState.LastPollEnd) {
		t.Fatalf("alpha cursor moved backward to %v", alphaStateAfter.LastPollEnd)
	}

	alphaRange, err := store.GetDataRange(ctx, "alpha")
	if err != nil {
		t.Fatal(err)
	}
	betaRange, err := store.GetDataRange(ctx, "beta")
	if err != nil {
		t.Fatal(err)
	}
	if alphaRange.Count == 0 || betaRange.Count == 0 || alphaRange.Count != betaRange.Count {
		t.Fatalf("ranges alpha=%+v beta=%+v", alphaRange, betaRange)
	}

	alphaStats, err := store.GetStats(ctx, "alpha")
	if err != nil {
		t.Fatal(err)
	}
	if alphaStats["tableCounts"].(map[string]int64)["node_pairs"] != 1 {
		t.Fatalf("alpha stats = %+v", alphaStats["tableCounts"])
	}

	if _, err := store.db.ExecContext(ctx, `
		UPDATE node_pairs SET bucket = 1 WHERE tailnet_id = 'alpha';
		UPDATE bandwidth SET bucket = 1 WHERE tailnet_id = 'alpha';
		UPDATE bandwidth_by_node SET bucket = 1 WHERE tailnet_id = 'alpha';
		UPDATE traffic_stats SET bucket = 1 WHERE tailnet_id = 'alpha';
		UPDATE ingested_objects SET ingested_at = '2000-01-01 00:00:00' WHERE tailnet_id = 'alpha';
	`); err != nil {
		t.Fatal(err)
	}
	deleted, err := store.Cleanup(ctx, "alpha", 24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if deleted == 0 {
		t.Fatal("cleanup deleted nothing from alpha")
	}
	betaPairs, err = store.GetNodePairAggregates(ctx, "beta", base, end)
	if err != nil {
		t.Fatal(err)
	}
	if len(betaPairs) != 1 || betaPairs[0].TxBytes != 1998 {
		t.Fatalf("cleanup touched beta pairs: %+v", betaPairs)
	}
	betaSeen, err = store.IsObjectIngested(ctx, "beta", "shared.ndjson")
	if err != nil {
		t.Fatal(err)
	}
	if !betaSeen {
		t.Fatal("cleanup removed beta ingested object")
	}
	alphaSeen, err = store.IsObjectIngested(ctx, "alpha", "shared.ndjson")
	if err != nil {
		t.Fatal(err)
	}
	if alphaSeen {
		t.Fatal("cleanup left the expired alpha object")
	}

	if _, err := store.GetNodePairAggregates(ctx, "", base, end); err == nil {
		t.Fatal("empty tailnet id was accepted")
	}
}

func TestTailnetQueryPlansUseIndexes(t *testing.T) {
	store := setupTestDB(t)
	ctx := context.Background()
	const nodes = 400
	const minutes = 30
	base := int64(1700000000)
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO node_pairs (
			tailnet_id, bucket, src_node_id, dst_node_id, traffic_type, tx_bytes, rx_bytes, flow_count, protocols, ports
		) VALUES (?, ?, ?, ?, 'virtual', 1, 1, 1, '[6]', '[]')
	`)
	if err != nil {
		t.Fatal(err)
	}
	for minute := int64(0); minute < minutes; minute++ {
		for node := 0; node < nodes; node++ {
			src := "n" + itoa(node)
			dst := "n" + itoa((node+1)%nodes)
			for _, tailnetID := range []string{DefaultTailnetID, "other"} {
				if _, err := stmt.ExecContext(ctx, tailnetID, base+minute*60, src, dst); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	stmt.Close()
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.ExecContext(ctx, "ANALYZE"); err != nil {
		t.Fatal(err)
	}

	narrowStart := base + 5*60
	narrowEnd := narrowStart + 60
	plans := []struct {
		name  string
		query string
		args  []any
		want  string
	}{
		{
			name: "time range",
			query: `EXPLAIN QUERY PLAN
				SELECT SUM(tx_bytes) FROM node_pairs
				WHERE tailnet_id = ? AND bucket >= ? AND bucket < ?`,
			args: []any{DefaultTailnetID, narrowStart, narrowEnd},
			want: "PRIMARY KEY",
		},
		{
			name: "source node",
			query: `EXPLAIN QUERY PLAN
				SELECT SUM(tx_bytes) FROM node_pairs
				WHERE tailnet_id = ? AND src_node_id = ? AND bucket >= ? AND bucket < ?`,
			args: []any{DefaultTailnetID, "n10", base, base + minutes*60},
			want: "idx_node_pairs_src",
		},
		{
			name: "dest node",
			query: `EXPLAIN QUERY PLAN
				SELECT SUM(tx_bytes) FROM node_pairs
				WHERE tailnet_id = ? AND dst_node_id = ? AND bucket >= ? AND bucket < ?`,
			args: []any{DefaultTailnetID, "n11", base, base + minutes*60},
			want: "idx_node_pairs_dst",
		},
		{
			name: "pair",
			query: `EXPLAIN QUERY PLAN
				SELECT SUM(tx_bytes) FROM node_pairs
				WHERE tailnet_id = ? AND src_node_id = ? AND dst_node_id = ? AND traffic_type = ?
				  AND bucket >= ? AND bucket < ?`,
			args: []any{DefaultTailnetID, "n10", "n11", "virtual", base, base + minutes*60},
			want: "idx_node_pairs_endpoints",
		},
	}
	for _, plan := range plans {
		t.Run(plan.name, func(t *testing.T) {
			detail := explain(t, store, plan.query, plan.args...)
			if !strings.Contains(detail, plan.want) || !strings.Contains(detail, "tailnet_id") {
				t.Fatalf("plan %q\nwant index %s with tailnet_id", detail, plan.want)
			}
		})
	}
}

func assertDefaultTailnetData(t *testing.T, store *SQLiteStore) {
	t.Helper()
	ctx := context.Background()
	start := time.Unix(1700000000, 0).UTC()
	end := start.Add(time.Minute)
	pairs, err := store.GetNodePairAggregates(ctx, DefaultTailnetID, start, end)
	if err != nil {
		t.Fatal(err)
	}
	if len(pairs) != 1 || pairs[0].SrcNodeID != "node-a" || pairs[0].TxBytes != 100 || pairs[0].RxBytes != 40 || pairs[0].FlowCount != 1 {
		t.Fatalf("migrated pairs = %+v", pairs)
	}
	if pairs[0].ProtocolBytes != `{"6":140}` {
		t.Fatalf("migrated protocol bytes = %s", pairs[0].ProtocolBytes)
	}
	bandwidth, err := store.GetBandwidth(ctx, DefaultTailnetID, start, end)
	if err != nil {
		t.Fatal(err)
	}
	if len(bandwidth) != 1 || bandwidth[0].TxBytes != 100 || bandwidth[0].RxBytes != 40 {
		t.Fatalf("migrated bandwidth = %+v", bandwidth)
	}
	stats, err := store.GetTrafficStats(ctx, DefaultTailnetID, start, end)
	if err != nil {
		t.Fatal(err)
	}
	if len(stats) != 1 || stats[0].TCPBytes != 140 || stats[0].VirtualBytes != 140 || stats[0].TotalFlows != 1 {
		t.Fatalf("migrated stats = %+v", stats)
	}
	state, err := store.GetPollState(ctx, DefaultTailnetID)
	if err != nil {
		t.Fatal(err)
	}
	wantCursor := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	if !state.LastPollEnd.Equal(wantCursor) {
		t.Fatalf("migrated poll cursor = %v, want %v", state.LastPollEnd, wantCursor)
	}
	seen, err := store.IsObjectIngested(ctx, DefaultTailnetID, "network/old.ndjson")
	if err != nil {
		t.Fatal(err)
	}
	if !seen {
		t.Fatal("migrated object key was not found")
	}
	otherSeen, err := store.IsObjectIngested(ctx, "other", "network/old.ndjson")
	if err != nil {
		t.Fatal(err)
	}
	if otherSeen {
		t.Fatal("migrated object key is visible to another tailnet")
	}
	nodes, err := store.GetNodeMetadata(ctx, DefaultTailnetID)
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 1 || nodes[0].NodeID != "node-a" || nodes[0].Hostname != "a" || len(nodes[0].IPs) != 1 || nodes[0].IPs[0] != "100.64.0.1" {
		t.Fatalf("migrated metadata = %+v", nodes)
	}
	otherNodes, err := store.GetNodeMetadata(ctx, "other")
	if err != nil {
		t.Fatal(err)
	}
	if len(otherNodes) != 0 {
		t.Fatalf("other tailnet metadata = %+v", otherNodes)
	}
	keys, err := store.GetObjectsNeedingMetadata(ctx, DefaultTailnetID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 0 {
		t.Fatalf("hydrated migrated object was queued: %v", keys)
	}
}

func openRawStore(t *testing.T) *SQLiteStore {
	t.Helper()
	dir := t.TempDir()
	store, err := NewSQLiteStore(dir + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}

func schemaSnapshot(t *testing.T, store *SQLiteStore) string {
	t.Helper()
	rows, err := store.db.QueryContext(context.Background(), `
		SELECT type, name, sql FROM sqlite_master
		WHERE sql IS NOT NULL AND name NOT LIKE 'sqlite_%'
		ORDER BY type, name
	`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var b strings.Builder
	for rows.Next() {
		var typ, name, sql string
		if err := rows.Scan(&typ, &name, &sql); err != nil {
			t.Fatal(err)
		}
		b.WriteString(typ)
		b.WriteByte(' ')
		b.WriteString(name)
		b.WriteByte(' ')
		b.WriteString(sql)
		b.WriteByte('\n')
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return b.String()
}

func tableCounts(t *testing.T, store *SQLiteStore) map[string]int64 {
	t.Helper()
	counts := map[string]int64{}
	for _, table := range []string{
		"node_pairs", "bandwidth", "bandwidth_by_node", "traffic_stats",
		"poll_state", "ingested_objects", "node_metadata", "object_metadata_nodes",
	} {
		var n int64
		if err := store.db.QueryRowContext(context.Background(), "SELECT COUNT(*) FROM "+table).Scan(&n); err != nil {
			t.Fatal(err)
		}
		counts[table] = n
	}
	return counts
}

func explain(t *testing.T, store *SQLiteStore, query string, args ...any) string {
	t.Helper()
	rows, err := store.db.QueryContext(context.Background(), query, args...)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var details []string
	for rows.Next() {
		var id, parent, notused int
		var detail string
		if err := rows.Scan(&id, &parent, &notused, &detail); err != nil {
			t.Fatal(err)
		}
		details = append(details, detail)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return strings.Join(details, "\n")
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var digits [16]byte
	i := len(digits)
	for n > 0 {
		i--
		digits[i] = byte('0' + n%10)
		n /= 10
	}
	return string(digits[i:])
}
