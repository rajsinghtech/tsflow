package database

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

// legacyNodePairAggregatesSQL is the correlated graph query this read replaced.
// Equivalence tests run it on the same rows as the single-pass read and
// require the same API body.
const legacyNodePairAggregatesSQL = `
		SELECT MIN(bucket), src_node_id, dst_node_id, traffic_type,
		       SUM(tx_bytes), SUM(rx_bytes), SUM(tx_pkts), SUM(rx_pkts),
		       SUM(flow_count),
		       COALESCE((SELECT json_group_array(proto) FROM (
		                    SELECT CAST(j.key AS INTEGER) AS proto,
		                           SUM(CAST(j.value AS INTEGER)) AS bytes
		                    FROM node_pairs sub, json_each(
		                        CASE WHEN json_valid(sub.protocol_bytes)
		                             THEN sub.protocol_bytes ELSE '{}' END) AS j
		                    WHERE sub.src_node_id = main.src_node_id
		                      AND sub.dst_node_id = main.dst_node_id
		                      AND sub.traffic_type = main.traffic_type
		                      AND sub.tailnet_id = ? AND sub.bucket >= ? AND sub.bucket < ?
		                    GROUP BY proto
		                    ORDER BY bytes DESC, proto ASC
		                 )), '[]'),
		       COALESCE((SELECT json_group_object(proto, bytes) FROM (
		                    SELECT CAST(j.key AS INTEGER) AS proto,
		                           SUM(CAST(j.value AS INTEGER)) AS bytes
		                    FROM node_pairs sub, json_each(
		                        CASE WHEN json_valid(sub.protocol_bytes)
		                             THEN sub.protocol_bytes ELSE '{}' END) AS j
		                    WHERE sub.src_node_id = main.src_node_id
		                      AND sub.dst_node_id = main.dst_node_id
		                      AND sub.traffic_type = main.traffic_type
		                      AND sub.tailnet_id = ? AND sub.bucket >= ? AND sub.bucket < ?
		                    GROUP BY proto
		                    ORDER BY proto ASC
		                 )), '{}'),
		       COALESCE((SELECT json_group_array(json_object('port', port, 'proto', proto, 'bytes', bytes)) FROM (
		                    SELECT CAST(json_extract(j.value, '$.port') AS INTEGER) AS port,
		                           CAST(json_extract(j.value, '$.proto') AS INTEGER) AS proto,
		                           SUM(CAST(json_extract(j.value, '$.bytes') AS INTEGER)) AS bytes
		                    FROM node_pairs sub, json_each(
		                        CASE WHEN json_valid(sub.ports)
		                             THEN sub.ports ELSE '[]' END) AS j
		                    WHERE sub.src_node_id = main.src_node_id
		                      AND sub.dst_node_id = main.dst_node_id
		                      AND sub.traffic_type = main.traffic_type
		                      AND sub.tailnet_id = ? AND sub.bucket >= ? AND sub.bucket < ?
		                    GROUP BY proto, port
		                    ORDER BY bytes DESC, proto ASC, port ASC
		                    LIMIT 20
		                 )), '[]'),
		       COALESCE((SELECT json_group_object(proto, bytes) FROM (
		                    SELECT CAST(j.key AS INTEGER) AS proto,
		                           SUM(CAST(j.value AS INTEGER)) AS bytes
		                    FROM node_pairs sub, json_each(
		                        CASE WHEN json_valid(sub.tx_protocol_bytes)
		                             THEN sub.tx_protocol_bytes ELSE '{}' END) AS j
		                    WHERE sub.src_node_id = main.src_node_id
		                      AND sub.dst_node_id = main.dst_node_id
		                      AND sub.traffic_type = main.traffic_type
		                      AND sub.tailnet_id = ? AND sub.bucket >= ? AND sub.bucket < ?
		                    GROUP BY proto
		                    ORDER BY proto ASC
		                 )), '{}'),
		       COALESCE((SELECT json_group_object(proto, bytes) FROM (
		                    SELECT CAST(j.key AS INTEGER) AS proto,
		                           SUM(CAST(j.value AS INTEGER)) AS bytes
		                    FROM node_pairs sub, json_each(
		                        CASE WHEN json_valid(sub.rx_protocol_bytes)
		                             THEN sub.rx_protocol_bytes ELSE '{}' END) AS j
		                    WHERE sub.src_node_id = main.src_node_id
		                      AND sub.dst_node_id = main.dst_node_id
		                      AND sub.traffic_type = main.traffic_type
		                      AND sub.tailnet_id = ? AND sub.bucket >= ? AND sub.bucket < ?
		                    GROUP BY proto
		                    ORDER BY proto ASC
		                 )), '{}'),
		       COALESCE((SELECT json_group_array(json_object('port', port, 'proto', proto, 'bytes', bytes)) FROM (
		                    SELECT CAST(json_extract(j.value, '$.port') AS INTEGER) AS port,
		                           CAST(json_extract(j.value, '$.proto') AS INTEGER) AS proto,
		                           SUM(CAST(json_extract(j.value, '$.bytes') AS INTEGER)) AS bytes
		                    FROM node_pairs sub, json_each(
		                        CASE WHEN json_valid(sub.tx_ports)
		                             THEN sub.tx_ports ELSE '[]' END) AS j
		                    WHERE sub.src_node_id = main.src_node_id
		                      AND sub.dst_node_id = main.dst_node_id
		                      AND sub.traffic_type = main.traffic_type
		                      AND sub.tailnet_id = ? AND sub.bucket >= ? AND sub.bucket < ?
		                    GROUP BY proto, port
		                    ORDER BY bytes DESC, proto ASC, port ASC
		                    LIMIT 20
		                 )), '[]'),
		       COALESCE((SELECT json_group_array(json_object('port', port, 'proto', proto, 'bytes', bytes)) FROM (
		                    SELECT CAST(json_extract(j.value, '$.port') AS INTEGER) AS port,
		                           CAST(json_extract(j.value, '$.proto') AS INTEGER) AS proto,
		                           SUM(CAST(json_extract(j.value, '$.bytes') AS INTEGER)) AS bytes
		                    FROM node_pairs sub, json_each(
		                        CASE WHEN json_valid(sub.rx_ports)
		                             THEN sub.rx_ports ELSE '[]' END) AS j
		                    WHERE sub.src_node_id = main.src_node_id
		                      AND sub.dst_node_id = main.dst_node_id
		                      AND sub.traffic_type = main.traffic_type
		                      AND sub.tailnet_id = ? AND sub.bucket >= ? AND sub.bucket < ?
		                    GROUP BY proto, port
		                    ORDER BY bytes DESC, proto ASC, port ASC
		                    LIMIT 20
		                 )), '[]'),
		       MIN(COALESCE(directional_ports, 0))
		FROM node_pairs main
		WHERE main.tailnet_id = ? AND main.bucket >= ? AND main.bucket < ?
		GROUP BY src_node_id, dst_node_id, traffic_type
		ORDER BY SUM(tx_bytes) + SUM(rx_bytes) DESC,
		         src_node_id ASC, dst_node_id ASC, traffic_type ASC
	`

func (s *SQLiteStore) legacyNodePairAggregates(ctx context.Context, tailnetID string, start, end time.Time) ([]NodePairAggregate, error) {
	if err := checkTailnetID(tailnetID); err != nil {
		return nil, err
	}
	startUnix, endUnix, err := nodePairBounds(start, end)
	if err != nil {
		return nil, err
	}
	args := make([]any, 0, 24)
	for i := 0; i < 8; i++ {
		args = append(args, tailnetID, startUnix, endUnix)
	}
	return s.queryLegacyNodePairAggregates(ctx, legacyNodePairAggregatesSQL, args)
}

func (s *SQLiteStore) queryLegacyNodePairAggregates(ctx context.Context, query string, args []any) ([]NodePairAggregate, error) {
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query node pairs: %w", err)
	}
	defer rows.Close()
	return scanLegacyNodePairRows(rows)
}

func scanLegacyNodePairRows(rows *sql.Rows) ([]NodePairAggregate, error) {
	var aggregates []NodePairAggregate
	for rows.Next() {
		var agg NodePairAggregate
		if err := rows.Scan(
			&agg.Bucket, &agg.SrcNodeID, &agg.DstNodeID, &agg.TrafficType,
			&agg.TxBytes, &agg.RxBytes, &agg.TxPkts, &agg.RxPkts,
			&agg.FlowCount, &agg.Protocols, &agg.ProtocolBytes, &agg.Ports,
			&agg.TxProtocolBytes, &agg.RxProtocolBytes, &agg.TxPorts, &agg.RxPorts,
			&agg.DirectionalPorts,
		); err != nil {
			return nil, fmt.Errorf("failed to scan node pair: %w", err)
		}
		aggregates = append(aggregates, agg)
	}
	return aggregates, rows.Err()
}

func TestNodePairAggregatePlanIsSinglePass(t *testing.T) {
	store := setupTestDB(t)
	rows, err := store.db.Query("EXPLAIN QUERY PLAN "+nodePairScanSQL, DefaultTailnetID, int64(0), int64(60))
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
	plan := strings.Join(details, "\n")
	if strings.Contains(strings.ToUpper(plan), "CORRELATED") {
		t.Fatalf("graph query is still correlated:\n%s", plan)
	}
	if strings.Count(plan, "node_pairs") != 1 {
		t.Fatalf("graph query should scan node_pairs once:\n%s", plan)
	}
	if !strings.Contains(plan, "PRIMARY KEY") && !strings.Contains(plan, "tailnet_id") {
		t.Fatalf("graph query should use the tailnet primary key:\n%s", plan)
	}
}

func TestGetNodePairAggregatesMatchesLegacyAPI(t *testing.T) {
	store := setupTestDB(t)
	ctx := context.Background()
	const base int64 = 1_700_000_040
	if err := store.UpsertNodeMetadata(ctx, DefaultTailnetID, []NodeMetadata{{
		NodeID: "tag:app",
		Name:   "app",
		Tags:   []string{"tag:app", "tag:prod"},
	}}); err != nil {
		t.Fatal(err)
	}

	minute1Ports := make([][3]int64, 0, 21)
	for port := int64(1); port <= 21; port++ {
		minute1Ports = append(minute1Ports, [3]int64{port, 6, 500 - port})
	}
	subnetPorts1 := formatPortFixtures(minute1Ports)
	subnetPorts2 := formatPortFixtures([][3]int64{{21, 6, 1000}})

	rows := []rawNodePair{
		// Self pair on a tagged node, directional, merged across two minutes.
		{
			tailnet: DefaultTailnetID, bucket: base, src: "tag:app", dst: "tag:app", traffic: "virtual",
			tx: 100, rx: 40, txPkts: 2, rxPkts: 1, flows: 1, directional: 1,
			protocols: "[6]", protocolBytes: `{"6":140}`,
			ports:   `[{"port":443,"proto":6,"bytes":140}]`,
			txPorts: `[{"port":443,"proto":6,"bytes":100}]`,
			rxPorts: `[{"port":53,"proto":17,"bytes":40}]`,
			txProto: `{"6":100}`, rxProto: `{"17":40}`,
		},
		{
			tailnet: DefaultTailnetID, bucket: base + 60, src: "tag:app", dst: "tag:app", traffic: "virtual",
			tx: 50, rx: 10, txPkts: 1, rxPkts: 1, flows: 1, directional: 1,
			protocols: "[17,6]", protocolBytes: `{"6":20,"17":40}`,
			ports:   `[{"port":443,"proto":6,"bytes":20},{"port":53,"proto":17,"bytes":40}]`,
			txPorts: `[{"port":443,"proto":6,"bytes":50}]`,
			rxPorts: `[{"port":53,"proto":17,"bytes":10}]`,
			txProto: `{"6":50}`, rxProto: `{"17":10}`,
		},
		// Subnet route from a tagged router to an off-tailnet address.
		// Minute two pushes port 21 into the top 20 and drops port 20.
		{
			tailnet: DefaultTailnetID, bucket: base, src: "tag:subnet-router", dst: "192.168.10.5", traffic: "subnet",
			tx: 1000, rx: 10, txPkts: 21, rxPkts: 1, flows: 21, directional: 1,
			protocols: "[6]", protocolBytes: `{"6":1000}`,
			ports: subnetPorts1, txPorts: subnetPorts1, rxPorts: "[]",
			txProto: `{"6":1000}`, rxProto: `{"6":10}`,
		},
		{
			tailnet: DefaultTailnetID, bucket: base + 60, src: "tag:subnet-router", dst: "192.168.10.5", traffic: "subnet",
			tx: 1000, rx: 10, txPkts: 1, rxPkts: 1, flows: 1, directional: 1,
			protocols: "[6]", protocolBytes: `{"6":1000}`,
			ports: subnetPorts2, txPorts: subnetPorts2, rxPorts: "[]",
			txProto: `{"6":1000}`, rxProto: `{"6":10}`,
		},
		// Same endpoints, second traffic type, so the group key includes traffic type.
		{
			tailnet: DefaultTailnetID, bucket: base, src: "tag:subnet-router", dst: "192.168.10.5", traffic: "virtual",
			tx: 8, rx: 1, txPkts: 1, rxPkts: 0, flows: 1, directional: 0,
			protocols: "[]", protocolBytes: `{"17":9}`,
			ports: "[]", txPorts: "[]", rxPorts: "[]", txProto: "{}", rxProto: "{}",
		},
		// One directional minute and one legacy minute. Direction clears.
		{
			tailnet: DefaultTailnetID, bucket: base, src: "tag:server", dst: "n-laptop", traffic: "virtual",
			tx: 30, rx: 5, txPkts: 1, rxPkts: 1, flows: 1, directional: 1,
			protocols: "[6]", protocolBytes: `{"6":35}`,
			ports:   `[{"port":22,"proto":6,"bytes":35}]`,
			txPorts: `[{"port":22,"proto":6,"bytes":30}]`,
			rxPorts: `[{"port":22,"proto":6,"bytes":5}]`,
			txProto: `{"6":30}`, rxProto: `{"6":5}`,
		},
		{
			tailnet: DefaultTailnetID, bucket: base + 60, src: "tag:server", dst: "n-laptop", traffic: "virtual",
			tx: 70, rx: 0, txPkts: 1, rxPkts: 0, flows: 1, directional: 0,
			protocols: "[17]", protocolBytes: `{"17":70}`,
			ports:   `[{"port":53,"proto":17,"bytes":70}]`,
			txPorts: "[]", rxPorts: "[]", txProto: "{}", rxProto: "{}",
		},
		// Stored protocols list with empty protocol_bytes. The read rebuilds
		// protocols from protocol_bytes, so the list does not leak through.
		{
			tailnet: DefaultTailnetID, bucket: base, src: "proto-col-only", dst: "proto-col-only", traffic: "physical",
			tx: 80, rx: 0, txPkts: 1, rxPkts: 0, flows: 1, directional: 0,
			protocols: "[6,17]", protocolBytes: "{}",
			ports: "[]", txPorts: "[]", rxPorts: "[]", txProto: "{}", rxProto: "{}",
		},
		// Invalid protocol_bytes falls back to an empty object.
		{
			tailnet: DefaultTailnetID, bucket: base, src: "legacy-src", dst: "legacy-dst", traffic: "physical",
			tx: 100, rx: 50, txPkts: 3, rxPkts: 1, flows: 2, directional: 0,
			protocols: "[6,17]", protocolBytes: "not-json",
			ports: "not-json", txPorts: "not-json", rxPorts: "not-json",
			txProto: "not-json", rxProto: "not-json",
		},
		// Empty and null protocol blobs contribute nothing. A later valid blob does.
		{
			tailnet: DefaultTailnetID, bucket: base, src: "mix-src", dst: "mix-dst", traffic: "exit",
			tx: 10, rx: 0, txPkts: 1, rxPkts: 0, flows: 1, directional: 0,
			protocols: "[6]", protocolBytes: nil,
			ports: nil, txPorts: nil, rxPorts: nil, txProto: nil, rxProto: nil,
		},
		{
			tailnet: DefaultTailnetID, bucket: base + 60, src: "mix-src", dst: "mix-dst", traffic: "exit",
			tx: 10, rx: 0, txPkts: 1, rxPkts: 0, flows: 1, directional: 0,
			protocols: "", protocolBytes: "",
			ports: "", txPorts: "", rxPorts: "", txProto: "", rxProto: "",
		},
		{
			tailnet: DefaultTailnetID, bucket: base + 120, src: "mix-src", dst: "mix-dst", traffic: "exit",
			tx: 25, rx: 0, txPkts: 1, rxPkts: 0, flows: 1, directional: 1,
			protocols: "[17]", protocolBytes: `{"17":25}`,
			ports:   `[{"port":41641,"proto":17,"bytes":25}]`,
			txPorts: `[{"port":41641,"proto":17,"bytes":25}]`,
			rxPorts: "[]", txProto: `{"17":25}`, rxProto: "{}",
		},
		// Equal protocol bytes. Lower protocol number wins the tie.
		{
			tailnet: DefaultTailnetID, bucket: base, src: "tie-src", dst: "tie-dst", traffic: "virtual",
			tx: 200, rx: 0, txPkts: 2, rxPkts: 0, flows: 2, directional: 1,
			protocols: "[6,17]", protocolBytes: `{"17":100,"6":100}`,
			ports:   `[{"port":10,"proto":17,"bytes":50},{"port":10,"proto":6,"bytes":50},{"port":9,"proto":6,"bytes":50}]`,
			txPorts: `[{"port":10,"proto":17,"bytes":50},{"port":10,"proto":6,"bytes":50},{"port":9,"proto":6,"bytes":50}]`,
			rxPorts: "[]", txProto: `{"6":100,"17":100}`, rxProto: "{}",
		},
		// Whitespace, duplicate numeric keys, and a non-integer byte value.
		{
			tailnet: DefaultTailnetID, bucket: base, src: "odd-src", dst: "odd-dst", traffic: "virtual",
			tx: 12, rx: 1, txPkts: 1, rxPkts: 1, flows: 1, directional: 0,
			protocols: "[]", protocolBytes: `{"6": 10, "06": 2, "17": 10.9}`,
			ports:   `[{"port": 80, "proto": 6, "bytes": 12, "name": "http"}]`,
			txPorts: "[]", rxPorts: "[]", txProto: "{}", rxProto: "{}",
		},
		// Scalar JSON is expanded by json_each the same way as an object.
		{
			tailnet: DefaultTailnetID, bucket: base, src: "scalar-src", dst: "scalar-dst", traffic: "virtual",
			tx: 6, rx: 0, txPkts: 1, rxPkts: 0, flows: 1, directional: 0,
			protocols: "[6]", protocolBytes: "6",
			ports: "[6]", txPorts: "[]", rxPorts: "[]", txProto: "6", rxProto: "null",
		},
		// Node ids that contain a character we must not use as a join key.
		{
			tailnet: DefaultTailnetID, bucket: base, src: "a|b", dst: "c|d", traffic: "virtual",
			tx: 4, rx: 4, txPkts: 1, rxPkts: 1, flows: 1, directional: 0,
			protocols: "[1]", protocolBytes: `{"1":8}`,
			ports: "[]", txPorts: "[]", rxPorts: "[]", txProto: "{}", rxProto: "{}",
		},
		// IP self pair with empty metadata.
		{
			tailnet: DefaultTailnetID, bucket: base + 120, src: "10.0.0.1", dst: "10.0.0.1", traffic: "physical",
			tx: 5, rx: 0, txPkts: 1, rxPkts: 0, flows: 1, directional: 0,
			protocols: "[]", protocolBytes: "{}",
			ports: "[]", txPorts: "[]", rxPorts: "[]", txProto: "{}", rxProto: "{}",
		},
		// Outside the half-open window.
		{
			tailnet: DefaultTailnetID, bucket: base - 60, src: "early-src", dst: "early-dst", traffic: "virtual",
			tx: 12345, rx: 0, txPkts: 1, rxPkts: 0, flows: 1, directional: 0,
			protocols: "[6]", protocolBytes: `{"6":12345}`,
			ports: "[]", txPorts: "[]", rxPorts: "[]", txProto: "{}", rxProto: "{}",
		},
		{
			tailnet: DefaultTailnetID, bucket: base + 180, src: "boundary-src", dst: "boundary-dst", traffic: "virtual",
			tx: 12345, rx: 0, txPkts: 1, rxPkts: 0, flows: 1, directional: 0,
			protocols: "[6]", protocolBytes: `{"6":12345}`,
			ports: "[]", txPorts: "[]", rxPorts: "[]", txProto: "{}", rxProto: "{}",
		},
		// Another tailnet reuses default's keys with different totals.
		{
			tailnet: "other", bucket: base, src: "tag:app", dst: "tag:app", traffic: "virtual",
			tx: 99999, rx: 1, txPkts: 1, rxPkts: 1, flows: 1, directional: 1,
			protocols: "[17]", protocolBytes: `{"17":99999}`,
			ports:   `[{"port":9,"proto":17,"bytes":99999}]`,
			txPorts: `[{"port":9,"proto":17,"bytes":99999}]`,
			rxPorts: "[]", txProto: `{"17":99999}`, rxProto: "{}",
		},
		{
			tailnet: "other", bucket: base, src: "tag:subnet-router", dst: "192.168.10.5", traffic: "subnet",
			tx: 50000, rx: 0, txPkts: 1, rxPkts: 0, flows: 1, directional: 0,
			protocols: "[6]", protocolBytes: `{"6":50000}`,
			ports:   `[{"port":9,"proto":6,"bytes":50000}]`,
			txPorts: "[]", rxPorts: "[]", txProto: "{}", rxProto: "{}",
		},
		{
			tailnet: "other", bucket: base + 60, src: "only-other", dst: "only-other", traffic: "subnet",
			tx: 7, rx: 0, txPkts: 1, rxPkts: 0, flows: 1, directional: 0,
			protocols: "[6]", protocolBytes: `{"6":7}`,
			ports: "[]", txPorts: "[]", rxPorts: "[]", txProto: "{}", rxProto: "{}",
		},
	}
	insertRawNodePairs(t, store, rows)

	start := time.Unix(base, 0).UTC()
	end := time.Unix(base+180, 0).UTC()
	assertSamePairAPI(t, store, DefaultTailnetID, start, end)
	assertSamePairAPI(t, store, "other", start, end)
	assertSamePairAPI(t, store, "lab", start, end.Add(time.Minute))

	emptyStart := time.Unix(base+86400, 0).UTC()
	emptyEnd := emptyStart.Add(time.Minute)
	assertSamePairAPI(t, store, DefaultTailnetID, emptyStart, emptyEnd)

	for _, tc := range []struct{ start, end time.Time }{
		{start, start},
		{end, start},
	} {
		_, legacyErr := store.legacyNodePairAggregates(ctx, DefaultTailnetID, tc.start, tc.end)
		_, currentErr := store.GetNodePairAggregates(ctx, DefaultTailnetID, tc.start, tc.end)
		if legacyErr == nil || currentErr == nil || legacyErr.Error() != currentErr.Error() {
			t.Fatalf("range errors legacy=%v current=%v", legacyErr, currentErr)
		}
	}
	_, legacyErr := store.legacyNodePairAggregates(ctx, "", start, end)
	_, currentErr := store.GetNodePairAggregates(ctx, "", start, end)
	if legacyErr == nil || currentErr == nil || legacyErr.Error() != currentErr.Error() {
		t.Fatalf("tailnet errors legacy=%v current=%v", legacyErr, currentErr)
	}

	current, err := store.GetNodePairAggregates(ctx, DefaultTailnetID, start, end)
	if err != nil {
		t.Fatal(err)
	}
	self, ok := findPair(current, "tag:app", "tag:app", "virtual")
	if !ok {
		t.Fatal("missing self pair")
	}
	if self.Bucket != base || self.TxBytes != 150 || self.RxBytes != 50 || self.TxPkts != 3 || self.RxPkts != 2 || self.FlowCount != 2 {
		t.Fatalf("self pair totals = %+v", self)
	}
	if self.ProtocolBytes != `{"6":160,"17":40}` || self.Protocols != "[6,17]" {
		t.Fatalf("self pair protocols = %s %s", self.Protocols, self.ProtocolBytes)
	}
	if !self.DirectionalPorts || self.TxProtocolBytes != `{"6":150}` || self.RxProtocolBytes != `{"17":50}` {
		t.Fatalf("self pair directional = %+v", self)
	}
	if self.Ports != `[{"port":443,"proto":6,"bytes":160},{"port":53,"proto":17,"bytes":40}]` {
		t.Fatalf("self pair ports = %s", self.Ports)
	}

	subnet, ok := findPair(current, "tag:subnet-router", "192.168.10.5", "subnet")
	if !ok || subnet.TxBytes != 2000 {
		t.Fatalf("subnet route = %+v", subnet)
	}
	var subnetPorts []PortStat
	if err := json.Unmarshal([]byte(subnet.Ports), &subnetPorts); err != nil {
		t.Fatal(err)
	}
	if len(subnetPorts) != 20 || subnetPorts[0].Port != 21 || subnetPorts[0].Bytes != 1479 {
		t.Fatalf("subnet top ports = %+v", subnetPorts)
	}
	for _, port := range subnetPorts {
		if port.Port == 20 {
			t.Fatalf("port 20 should have fallen out of the top 20: %+v", subnetPorts)
		}
	}

	legacyOnly, ok := findPair(current, "proto-col-only", "proto-col-only", "physical")
	if !ok || legacyOnly.Protocols != "[]" || legacyOnly.ProtocolBytes != "{}" {
		t.Fatalf("empty protocol_bytes fallback = %+v", legacyOnly)
	}
	invalid, ok := findPair(current, "legacy-src", "legacy-dst", "physical")
	if !ok || invalid.Protocols != "[]" || invalid.ProtocolBytes != "{}" || invalid.Ports != "[]" || invalid.DirectionalPorts {
		t.Fatalf("invalid JSON fallback = %+v", invalid)
	}
	mixed, ok := findPair(current, "mix-src", "mix-dst", "exit")
	if !ok || mixed.TxBytes != 45 || mixed.Protocols != "[17]" || mixed.ProtocolBytes != `{"17":25}` || mixed.DirectionalPorts {
		t.Fatalf("mixed legacy metadata = %+v", mixed)
	}
	tie, ok := findPair(current, "tie-src", "tie-dst", "virtual")
	if !ok || tie.Protocols != "[6,17]" || tie.ProtocolBytes != `{"6":100,"17":100}` {
		t.Fatalf("protocol tie = %s %s", tie.Protocols, tie.ProtocolBytes)
	}
	var tiePorts []PortStat
	if err := json.Unmarshal([]byte(tie.Ports), &tiePorts); err != nil {
		t.Fatal(err)
	}
	if len(tiePorts) != 3 || tiePorts[0].Port != 9 || tiePorts[0].Proto != 6 || tiePorts[1].Port != 10 || tiePorts[1].Proto != 6 || tiePorts[2].Proto != 17 {
		t.Fatalf("port tie order = %+v", tiePorts)
	}
	if _, ok := findPair(current, "only-other", "only-other", "subnet"); ok {
		t.Fatal("default read included another tailnet's pair")
	}
	if _, ok := findPair(current, "early-src", "early-dst", "virtual"); ok {
		t.Fatal("window included the bucket before start")
	}
	if _, ok := findPair(current, "boundary-src", "boundary-dst", "virtual"); ok {
		t.Fatal("window included the bucket at end")
	}

	other, err := store.GetNodePairAggregates(ctx, "other", start, end)
	if err != nil {
		t.Fatal(err)
	}
	otherSelf, ok := findPair(other, "tag:app", "tag:app", "virtual")
	if !ok || otherSelf.TxBytes != 99999 {
		t.Fatalf("other tailnet self pair = %+v", otherSelf)
	}
	if _, ok := findPair(other, "a|b", "c|d", "virtual"); ok {
		t.Fatal("other tailnet read included default rows")
	}

	api := aggregatedFlowAPIBody(current, start, end)
	var parsed struct {
		Flows []apiFlow `json:"flows"`
	}
	if err := json.Unmarshal([]byte(api), &parsed); err != nil {
		t.Fatal(err)
	}
	selfFlow, ok := findFlow(parsed.Flows, "tag:app", "tag:app", "virtual")
	if !ok || selfFlow.TotalTxBytes != 150 || selfFlow.Protocol != 6 || !selfFlow.Directional || selfFlow.TxProtocolBytes[6] != 150 {
		t.Fatalf("self pair API flow = %+v", selfFlow)
	}
	cleared, ok := findFlow(parsed.Flows, "tag:server", "n-laptop", "virtual")
	if !ok || cleared.Directional || cleared.TxProtocolBytes != nil || cleared.TxPorts != nil {
		t.Fatalf("legacy minute did not clear directional API fields: %+v", cleared)
	}
	subnetFlow, ok := findFlow(parsed.Flows, "tag:subnet-router", "192.168.10.5", "subnet")
	if !ok || subnetFlow.TotalTxBytes != 2000 || len(subnetFlow.Ports) != 20 || subnetFlow.Ports[0].Port != 21 {
		t.Fatalf("subnet API flow = %+v", subnetFlow)
	}
}

func TestNodePairNullSumsMatchLegacy(t *testing.T) {
	store := setupTestDB(t)
	ctx := context.Background()
	const base int64 = 1_700_000_040
	if _, err := store.db.ExecContext(ctx, `
		INSERT INTO node_pairs (
			tailnet_id, bucket, src_node_id, dst_node_id, traffic_type,
			tx_bytes, rx_bytes, tx_pkts, rx_pkts, flow_count,
			protocols, protocol_bytes, ports, tx_ports, rx_ports,
			tx_protocol_bytes, rx_protocol_bytes, directional_ports
		) VALUES
			('default', ?, 'mix', 'mix', 'virtual', NULL, 1, 1, 1, 1, '[]', '{}', '[]', '[]', '[]', '{}', '{}', 0),
			('default', ?, 'mix', 'mix', 'virtual', 5, NULL, NULL, 2, NULL, '[]', '{}', '[]', '[]', '[]', '{}', '{}', 0)
	`, base, base+60); err != nil {
		t.Fatal(err)
	}
	start := time.Unix(base, 0).UTC()
	end := time.Unix(base+120, 0).UTC()
	assertSamePairAPI(t, store, DefaultTailnetID, start, end)
	rows, err := store.GetNodePairAggregates(ctx, DefaultTailnetID, start, end)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := findPair(rows, "mix", "mix", "virtual")
	if !ok || got.TxBytes != 5 || got.RxBytes != 1 || got.TxPkts != 1 || got.RxPkts != 3 || got.FlowCount != 1 {
		t.Fatalf("null inputs were not summed like SUM(): %+v", got)
	}

	nullStore := setupTestDB(t)
	if _, err := nullStore.db.ExecContext(ctx, `
		INSERT INTO node_pairs (
			tailnet_id, bucket, src_node_id, dst_node_id, traffic_type,
			tx_bytes, rx_bytes, tx_pkts, rx_pkts, flow_count
		) VALUES ('default', ?, 'empty', 'empty', 'virtual', NULL, NULL, NULL, NULL, NULL)
	`, base); err != nil {
		t.Fatal(err)
	}
	_, legacyErr := nullStore.legacyNodePairAggregates(ctx, DefaultTailnetID, start, end)
	_, currentErr := nullStore.GetNodePairAggregates(ctx, DefaultTailnetID, start, end)
	if legacyErr == nil || currentErr == nil {
		t.Fatalf("all-null sums legacy=%v current=%v", legacyErr, currentErr)
	}
}

func TestGetNodePairAggregatesDoesNotBlockPollCommit(t *testing.T) {
	store := setupTestDB(t)
	ctx := context.Background()
	entered := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	store.nodePairReadHook = func() {
		once.Do(func() { close(entered) })
		<-release
	}
	defer func() {
		store.nodePairReadHook = nil
		select {
		case <-release:
		default:
			close(release)
		}
	}()

	start := time.Unix(1_700_000_040, 0).UTC()
	end := start.Add(time.Minute)
	queryDone := make(chan error, 1)
	go func() {
		_, err := store.GetNodePairAggregates(ctx, DefaultTailnetID, start, end)
		queryDone <- err
	}()
	select {
	case <-entered:
	case err := <-queryDone:
		t.Fatalf("query finished before the read hook: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("graph read did not reach the query")
	}

	commitDone := make(chan error, 1)
	go func() {
		commitDone <- store.CommitPollResults(ctx, DefaultTailnetID, PollResults{PollEnd: end})
	}()
	select {
	case err := <-commitDone:
		if err != nil {
			t.Fatalf("poll commit: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("poll commit blocked while the graph read was in progress")
	}
	close(release)
	if err := <-queryDone; err != nil {
		t.Fatal(err)
	}
}

type rawNodePair struct {
	tailnet                            string
	bucket                             int64
	src, dst, traffic                  string
	tx, rx, txPkts, rxPkts, flows      int64
	protocols, protocolBytes, ports    any
	txPorts, rxPorts, txProto, rxProto any
	directional                        int
}

func insertRawNodePairs(t *testing.T, store *SQLiteStore, rows []rawNodePair) {
	t.Helper()
	tx, err := store.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	stmt, err := tx.Prepare(`
		INSERT INTO node_pairs (
			tailnet_id, bucket, src_node_id, dst_node_id, traffic_type,
			tx_bytes, rx_bytes, tx_pkts, rx_pkts, flow_count,
			protocols, protocol_bytes, ports,
			tx_ports, rx_ports, tx_protocol_bytes, rx_protocol_bytes,
			directional_ports
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	for _, row := range rows {
		if _, err := stmt.Exec(
			row.tailnet, row.bucket, row.src, row.dst, row.traffic,
			row.tx, row.rx, row.txPkts, row.rxPkts, row.flows,
			row.protocols, row.protocolBytes, row.ports,
			row.txPorts, row.rxPorts, row.txProto, row.rxProto,
			row.directional,
		); err != nil {
			t.Fatalf("insert %s %s->%s: %v", row.tailnet, row.src, row.dst, err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func formatPortFixtures(specs [][3]int64) string {
	parts := make([]string, len(specs))
	for i, spec := range specs {
		parts[i] = fmt.Sprintf(`{"port":%d,"proto":%d,"bytes":%d}`, spec[0], spec[1], spec[2])
	}
	return "[" + strings.Join(parts, ",") + "]"
}

func assertSamePairAPI(t *testing.T, store *SQLiteStore, tailnetID string, start, end time.Time) {
	t.Helper()
	ctx := context.Background()
	legacy, legacyErr := store.legacyNodePairAggregates(ctx, tailnetID, start, end)
	current, currentErr := store.GetNodePairAggregates(ctx, tailnetID, start, end)
	if legacyErr != nil || currentErr != nil {
		t.Fatalf("tailnet %s query errors legacy=%v current=%v", tailnetID, legacyErr, currentErr)
	}
	legacyAPI := aggregatedFlowAPIBody(legacy, start, end)
	currentAPI := aggregatedFlowAPIBody(current, start, end)
	if legacyAPI != currentAPI {
		t.Fatalf("tailnet %s API output mismatch\nlegacy:\n%s\ncurrent:\n%s", tailnetID, legacyAPI, currentAPI)
	}
	if !reflect.DeepEqual(legacy, current) {
		t.Fatalf("tailnet %s rows differ\nlegacy:\n%s\ncurrent:\n%s", tailnetID, pairRowsJSON(legacy), pairRowsJSON(current))
	}
}

func findPair(rows []NodePairAggregate, src, dst, traffic string) (NodePairAggregate, bool) {
	for _, row := range rows {
		if row.SrcNodeID == src && row.DstNodeID == dst && row.TrafficType == traffic {
			return row, true
		}
	}
	return NodePairAggregate{}, false
}

type pairRowView struct {
	Bucket           int64  `json:"bucket"`
	SrcNodeID        string `json:"srcNodeId"`
	DstNodeID        string `json:"dstNodeId"`
	TrafficType      string `json:"trafficType"`
	TxBytes          int64  `json:"txBytes"`
	RxBytes          int64  `json:"rxBytes"`
	TxPkts           int64  `json:"txPkts"`
	RxPkts           int64  `json:"rxPkts"`
	FlowCount        int64  `json:"flowCount"`
	Protocols        string `json:"protocols"`
	ProtocolBytes    string `json:"protocolBytes"`
	Ports            string `json:"ports"`
	TxPorts          string `json:"txPorts"`
	RxPorts          string `json:"rxPorts"`
	TxProtocolBytes  string `json:"txProtocolBytes"`
	RxProtocolBytes  string `json:"rxProtocolBytes"`
	DirectionalPorts bool   `json:"directionalPorts"`
}

func pairRowsJSON(rows []NodePairAggregate) string {
	views := make([]pairRowView, len(rows))
	for i, row := range rows {
		views[i] = pairRowView{
			Bucket: row.Bucket, SrcNodeID: row.SrcNodeID, DstNodeID: row.DstNodeID, TrafficType: row.TrafficType,
			TxBytes: row.TxBytes, RxBytes: row.RxBytes, TxPkts: row.TxPkts, RxPkts: row.RxPkts, FlowCount: row.FlowCount,
			Protocols: row.Protocols, ProtocolBytes: row.ProtocolBytes, Ports: row.Ports,
			TxPorts: row.TxPorts, RxPorts: row.RxPorts, TxProtocolBytes: row.TxProtocolBytes, RxProtocolBytes: row.RxProtocolBytes,
			DirectionalPorts: row.DirectionalPorts,
		}
	}
	encoded, err := json.MarshalIndent(views, "", "  ")
	if err != nil {
		return err.Error()
	}
	return string(encoded)
}

// apiFlow matches the aggregated-flow response object.
type apiFlow struct {
	SrcNodeID       string        `json:"srcNodeId"`
	DstNodeID       string        `json:"dstNodeId"`
	SrcDisplayName  string        `json:"srcDisplayName,omitempty"`
	DstDisplayName  string        `json:"dstDisplayName,omitempty"`
	TrafficType     string        `json:"trafficType"`
	TotalTxBytes    int64         `json:"totalTxBytes"`
	TotalRxBytes    int64         `json:"totalRxBytes"`
	TotalTxPkts     int64         `json:"totalTxPkts"`
	TotalRxPkts     int64         `json:"totalRxPkts"`
	FlowCount       int64         `json:"flowCount"`
	Protocol        int           `json:"protocol"`
	Ports           []PortStat    `json:"ports,omitempty"`
	Directional     bool          `json:"directional"`
	TxProtocolBytes map[int]int64 `json:"txProtocolBytes,omitempty"`
	RxProtocolBytes map[int]int64 `json:"rxProtocolBytes,omitempty"`
	TxPorts         []PortStat    `json:"txPorts,omitempty"`
	RxPorts         []PortStat    `json:"rxPorts,omitempty"`
	protocolBytes   map[int]int64
}

func findFlow(flows []apiFlow, src, dst, traffic string) (apiFlow, bool) {
	for _, flow := range flows {
		if flow.SrcNodeID == src && flow.DstNodeID == dst && flow.TrafficType == traffic {
			return flow, true
		}
	}
	return apiFlow{}, false
}

// aggregatedFlowAPIBody is the JSON body GetAggregatedFlowLogs returns when
// there is no device cache and no traffic-type filter.
func aggregatedFlowAPIBody(aggregates []NodePairAggregate, start, end time.Time) string {
	type mergeKey struct{ src, dst, traffic string }
	merged := make(map[mergeKey]*apiFlow)
	for _, agg := range aggregates {
		key := mergeKey{agg.SrcNodeID, agg.DstNodeID, agg.TrafficType}
		ports := parseAPIPortStats(agg.Ports)
		protocolBytes := parseAPIProtocolBytes(agg.ProtocolBytes, agg.Protocols, agg.TxBytes+agg.RxBytes)
		var txProtocolBytes, rxProtocolBytes map[int]int64
		var txPorts, rxPorts []PortStat
		if agg.DirectionalPorts {
			txProtocolBytes = parseAPIProtocolBytes(agg.TxProtocolBytes, "", 0)
			rxProtocolBytes = parseAPIProtocolBytes(agg.RxProtocolBytes, "", 0)
			txPorts = parseAPIPortStats(agg.TxPorts)
			rxPorts = parseAPIPortStats(agg.RxPorts)
		}
		if existing, ok := merged[key]; ok {
			existing.TotalTxBytes += agg.TxBytes
			existing.TotalRxBytes += agg.RxBytes
			existing.TotalTxPkts += agg.TxPkts
			existing.TotalRxPkts += agg.RxPkts
			existing.FlowCount += agg.FlowCount
			for protocol, bytes := range protocolBytes {
				existing.protocolBytes[protocol] += bytes
			}
			existing.Ports = mergeAPIPortStats(existing.Ports, ports)
			existing.Protocol = dominantAPIProtocolFromMap(existing.protocolBytes, existing.Ports, agg.Protocols)
			if existing.Directional && agg.DirectionalPorts {
				existing.TxProtocolBytes = mergeAPIProtocolByteStats(existing.TxProtocolBytes, txProtocolBytes)
				existing.RxProtocolBytes = mergeAPIProtocolByteStats(existing.RxProtocolBytes, rxProtocolBytes)
				existing.TxPorts = mergeAPIPortStats(existing.TxPorts, txPorts)
				existing.RxPorts = mergeAPIPortStats(existing.RxPorts, rxPorts)
			} else {
				existing.Directional = false
				existing.TxProtocolBytes = nil
				existing.RxProtocolBytes = nil
				existing.TxPorts = nil
				existing.RxPorts = nil
			}
		} else {
			merged[key] = &apiFlow{
				SrcNodeID:       agg.SrcNodeID,
				DstNodeID:       agg.DstNodeID,
				TrafficType:     agg.TrafficType,
				TotalTxBytes:    agg.TxBytes,
				TotalRxBytes:    agg.RxBytes,
				TotalTxPkts:     agg.TxPkts,
				TotalRxPkts:     agg.RxPkts,
				FlowCount:       agg.FlowCount,
				Protocol:        dominantAPIProtocolFromBytes(agg.ProtocolBytes, agg.Protocols, agg.TxBytes+agg.RxBytes, ports),
				Ports:           ports,
				Directional:     agg.DirectionalPorts,
				TxProtocolBytes: txProtocolBytes,
				RxProtocolBytes: rxProtocolBytes,
				TxPorts:         txPorts,
				RxPorts:         rxPorts,
				protocolBytes:   protocolBytes,
			}
		}
	}
	flows := make([]apiFlow, 0, len(merged))
	for _, flow := range merged {
		flows = append(flows, *flow)
	}
	sort.Slice(flows, func(i, j int) bool {
		left := flows[i].TotalTxBytes + flows[i].TotalRxBytes
		right := flows[j].TotalTxBytes + flows[j].TotalRxBytes
		if left != right {
			return left > right
		}
		if flows[i].SrcNodeID != flows[j].SrcNodeID {
			return flows[i].SrcNodeID < flows[j].SrcNodeID
		}
		if flows[i].DstNodeID != flows[j].DstNodeID {
			return flows[i].DstNodeID < flows[j].DstNodeID
		}
		return flows[i].TrafficType < flows[j].TrafficType
	})
	seconds := end.UTC().Unix() - start.UTC().Unix()
	bucketSize := int64(86400)
	if seconds <= 2*3600 {
		bucketSize = 60
	} else if seconds <= 48*3600 {
		bucketSize = 3600
	}
	body := map[string]any{
		"flows": flows,
		"metadata": map[string]any{
			"count":      len(flows),
			"start":      start,
			"end":        end,
			"bucketSize": bucketSize,
			"source":     "database",
			"truncated":  false,
		},
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		return err.Error()
	}
	return string(encoded)
}

func parseAPIPortStats(portsJSON string) []PortStat {
	if portsJSON == "" || portsJSON == "[]" {
		return nil
	}
	var ports []PortStat
	if err := json.Unmarshal([]byte(portsJSON), &ports); err != nil {
		return nil
	}
	sort.SliceStable(ports, func(i, j int) bool {
		if ports[i].Bytes == ports[j].Bytes {
			if ports[i].Proto == ports[j].Proto {
				return ports[i].Port < ports[j].Port
			}
			return ports[i].Proto < ports[j].Proto
		}
		return ports[i].Bytes > ports[j].Bytes
	})
	return ports
}

func mergeAPIPortStats(existing, incoming []PortStat) []PortStat {
	merged := make(map[[2]int]int64, len(existing)+len(incoming))
	for _, stat := range existing {
		merged[[2]int{stat.Proto, stat.Port}] += stat.Bytes
	}
	for _, stat := range incoming {
		merged[[2]int{stat.Proto, stat.Port}] += stat.Bytes
	}
	result := make([]PortStat, 0, len(merged))
	for key, bytes := range merged {
		result = append(result, PortStat{Proto: key[0], Port: key[1], Bytes: bytes})
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Bytes != result[j].Bytes {
			return result[i].Bytes > result[j].Bytes
		}
		if result[i].Proto != result[j].Proto {
			return result[i].Proto < result[j].Proto
		}
		return result[i].Port < result[j].Port
	})
	if len(result) > 20 {
		result = result[:20]
	}
	return result
}

func mergeAPIProtocolByteStats(existing, incoming map[int]int64) map[int]int64 {
	if len(existing) == 0 && len(incoming) == 0 {
		return nil
	}
	if existing == nil {
		existing = make(map[int]int64, len(incoming))
	}
	for protocol, bytes := range incoming {
		existing[protocol] += bytes
	}
	return existing
}

func parseAPIDominantProtocol(protocolsJSON string) int {
	if protocolsJSON == "" || protocolsJSON == "[]" {
		return 0
	}
	var protos []int
	if err := json.Unmarshal([]byte(protocolsJSON), &protos); err != nil || len(protos) == 0 {
		return 0
	}
	return protos[0]
}

func dominantAPIProtocol(protocolsJSON string, ports []PortStat) int {
	if protocol := parseAPIDominantProtocol(protocolsJSON); protocol != 0 {
		return protocol
	}
	if len(ports) > 0 {
		return ports[0].Proto
	}
	return 0
}

func parseAPIProtocolBytes(protocolBytesJSON, protocolsJSON string, totalBytes int64) map[int]int64 {
	result := make(map[int]int64)
	var raw map[string]int64
	if json.Unmarshal([]byte(protocolBytesJSON), &raw) == nil && len(raw) > 0 {
		for key, bytes := range raw {
			var protocol int
			if _, err := fmt.Sscanf(key, "%d", &protocol); err == nil {
				result[protocol] += bytes
			}
		}
		return result
	}
	var protocols []int
	if json.Unmarshal([]byte(protocolsJSON), &protocols) != nil || len(protocols) == 0 {
		return result
	}
	perProtocol := totalBytes / int64(len(protocols))
	remainder := totalBytes - perProtocol*int64(len(protocols))
	for i, protocol := range protocols {
		bytes := perProtocol
		if i == 0 {
			bytes += remainder
		}
		result[protocol] += bytes
	}
	return result
}

func dominantAPIProtocolFromBytes(protocolBytesJSON, protocolsJSON string, totalBytes int64, ports []PortStat) int {
	bytesByProtocol := parseAPIProtocolBytes(protocolBytesJSON, protocolsJSON, totalBytes)
	var dominant, dominantBytes int64
	for protocol, bytes := range bytesByProtocol {
		if bytes > dominantBytes || (bytes == dominantBytes && int64(protocol) < dominant) {
			dominant = int64(protocol)
			dominantBytes = bytes
		}
	}
	if dominantBytes > 0 || len(bytesByProtocol) > 0 {
		return int(dominant)
	}
	return dominantAPIProtocol(protocolsJSON, ports)
}

func dominantAPIProtocolFromMap(bytesByProtocol map[int]int64, ports []PortStat, fallbackProtocols string) int {
	var dominant, dominantBytes int64
	for protocol, bytes := range bytesByProtocol {
		if bytes > dominantBytes || (bytes == dominantBytes && int64(protocol) < dominant) {
			dominant = int64(protocol)
			dominantBytes = bytes
		}
	}
	if dominantBytes > 0 || len(bytesByProtocol) > 0 {
		return int(dominant)
	}
	return dominantAPIProtocol(fallbackProtocols, ports)
}
