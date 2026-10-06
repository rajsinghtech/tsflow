package handlers

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/rajsinghtech/tsflow/backend/internal/database"
	_ "modernc.org/sqlite"
)

// preTailnetHandlerSchema is the aggregate schema from before tailnet ids.
const preTailnetHandlerSchema = `
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
INSERT INTO node_pairs (
	bucket, src_node_id, dst_node_id, traffic_type,
	tx_bytes, rx_bytes, tx_pkts, rx_pkts, flow_count,
	protocols, protocol_bytes, ports,
	tx_ports, rx_ports, tx_protocol_bytes, rx_protocol_bytes, directional_ports
) VALUES
	(1772445600, 'node-a', 'node-b', 'virtual', 100, 40, 2, 1, 1, '[6]', '{"6":140}', '[{"port":443,"proto":6,"bytes":140}]', '[]', '[]', '{}', '{}', 0),
	(1772445660, 'node-a', 'node-c', 'subnet', 25, 5, 1, 1, 1, '[17]', '{"17":30}', '[{"port":53,"proto":17,"bytes":30}]', '[]', '[]', '{}', '{}', 0);

CREATE TABLE bandwidth (
	bucket INTEGER PRIMARY KEY,
	tx_bytes INTEGER DEFAULT 0,
	rx_bytes INTEGER DEFAULT 0
);
INSERT INTO bandwidth (bucket, tx_bytes, rx_bytes) VALUES
	(1772445600, 100, 40),
	(1772445660, 25, 5);

CREATE TABLE bandwidth_by_node (
	bucket INTEGER NOT NULL,
	node_id TEXT NOT NULL,
	tx_bytes INTEGER DEFAULT 0,
	rx_bytes INTEGER DEFAULT 0,
	PRIMARY KEY (bucket, node_id)
);
INSERT INTO bandwidth_by_node (bucket, node_id, tx_bytes, rx_bytes)
VALUES (1772445600, 'node-a', 100, 40);

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
	bucket, tcp_bytes, udp_bytes, virtual_bytes, subnet_bytes, total_flows, unique_pairs, top_ports
) VALUES
	(1772445600, 140, 0, 140, 0, 1, 1, '[{"port":443,"proto":6,"bytes":140}]'),
	(1772445660, 0, 30, 0, 30, 1, 1, '[{"port":53,"proto":17,"bytes":30}]');

CREATE TABLE poll_state (
	id INTEGER PRIMARY KEY CHECK (id = 1),
	last_poll_end DATETIME,
	updated_at DATETIME
);
INSERT INTO poll_state (id, last_poll_end, updated_at)
VALUES (1, '2026-03-01 12:02:00', '2026-03-01 12:02:00');
`

func TestMigratedDatabaseMatchesFreshHandlerResponses(t *testing.T) {
	ctx := context.Background()
	migratedPath := t.TempDir() + "/migrated.db"
	migrated, err := database.NewSQLiteStore(migratedPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { migrated.Close() })
	execSQLite(t, migratedPath, preTailnetHandlerSchema)
	if err := migrated.Init(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if err := migrated.Init(ctx); err != nil {
		t.Fatalf("second init: %v", err)
	}

	freshPath := t.TempDir() + "/fresh.db"
	fresh, err := database.NewSQLiteStore(freshPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { fresh.Close() })
	if err := fresh.Init(ctx); err != nil {
		t.Fatal(err)
	}
	execSQLite(t, freshPath, `
		INSERT INTO node_pairs (
			tailnet_id, bucket, src_node_id, dst_node_id, traffic_type,
			tx_bytes, rx_bytes, tx_pkts, rx_pkts, flow_count,
			protocols, protocol_bytes, ports,
			tx_ports, rx_ports, tx_protocol_bytes, rx_protocol_bytes, directional_ports
		) VALUES
			('default', 1772445600, 'node-a', 'node-b', 'virtual', 100, 40, 2, 1, 1, '[6]', '{"6":140}', '[{"port":443,"proto":6,"bytes":140}]', '[]', '[]', '{}', '{}', 0),
			('default', 1772445660, 'node-a', 'node-c', 'subnet', 25, 5, 1, 1, 1, '[17]', '{"17":30}', '[{"port":53,"proto":17,"bytes":30}]', '[]', '[]', '{}', '{}', 0);
		INSERT INTO bandwidth (tailnet_id, bucket, tx_bytes, rx_bytes) VALUES
			('default', 1772445600, 100, 40),
			('default', 1772445660, 25, 5);
		INSERT INTO traffic_stats (
			tailnet_id, bucket, tcp_bytes, udp_bytes, virtual_bytes, subnet_bytes, total_flows, unique_pairs, top_ports
		) VALUES
			('default', 1772445600, 140, 0, 140, 0, 1, 1, '[{"port":443,"proto":6,"bytes":140}]'),
			('default', 1772445660, 0, 30, 0, 30, 1, 1, '[{"port":53,"proto":17,"bytes":30}]');
	`)

	start := time.Unix(1772445600, 0).UTC().Format(time.RFC3339)
	end := time.Unix(1772445600+180, 0).UTC().Format(time.RFC3339)
	qs := "start=" + start + "&end=" + end
	requests := []struct {
		name    string
		method  func(*Handlers, *gin.Context)
		target  string
		paramID string
		want    string
	}{
		{name: "flows", method: (*Handlers).GetAggregatedFlowLogs, target: "/api/flow-logs/aggregated?" + qs, want: `"srcNodeId":"node-a"`},
		{name: "bandwidth", method: (*Handlers).GetBandwidthAggregated, target: "/api/bandwidth/aggregated?" + qs, want: `"txBytes":100`},
		{name: "node bandwidth", method: (*Handlers).GetBandwidthAggregated, target: "/api/bandwidth/aggregated?" + qs + "&nodeId=node-a", want: `"txBytes":100`},
		{name: "overview", method: (*Handlers).GetStatsOverview, target: "/api/stats/overview?" + qs, want: `"virtualBytes":140`},
		{name: "talkers", method: (*Handlers).GetTopTalkers, target: "/api/stats/top-talkers?" + qs, want: `"nodeId":"node-a"`},
		{name: "pairs", method: (*Handlers).GetTopPairs, target: "/api/stats/top-pairs?" + qs, want: `"dstNodeId":"node-b"`},
		{name: "ranked talkers", method: (*Handlers).GetRankedTalkers, target: "/api/analytics/talkers?" + qs, want: `"nodeId":"node-a"`},
		{name: "ranked pairs", method: (*Handlers).GetRankedPairs, target: "/api/analytics/pairs?" + qs, want: `"dstNodeId":"node-b"`},
		{name: "node", method: (*Handlers).GetNodeDetailStats, target: "/api/stats/nodes/node-a?" + qs, paramID: "node-a", want: `"totalTx":125`},
		{name: "range", method: (*Handlers).GetDataRange, target: "/api/data-range", want: `"count":2`},
	}
	for _, request := range requests {
		t.Run(request.name, func(t *testing.T) {
			migratedBody := handlerBody(t, migrated, request.method, request.target, request.paramID)
			freshBody := handlerBody(t, fresh, request.method, request.target, request.paramID)
			if migratedBody != freshBody {
				t.Fatalf("migrated handler response differs from a fresh database\nmigrated: %s\nfresh:    %s", migratedBody, freshBody)
			}
			if !strings.Contains(migratedBody, request.want) {
				t.Fatalf("response missing %s: %s", request.want, migratedBody)
			}
		})
	}
}

func handlerBody(t *testing.T, store *database.SQLiteStore, method func(*Handlers, *gin.Context), target, paramID string) string {
	t.Helper()
	h := &Handlers{store: store}
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, target, nil)
	if paramID != "" {
		c.Params = gin.Params{{Key: "id", Value: paramID}}
	}
	method(h, c)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	return w.Body.String()
}

func execSQLite(t *testing.T, path, query string) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(query); err != nil {
		t.Fatal(err)
	}
}
