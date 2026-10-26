package database

import (
	"context"
	"encoding/json"
	"testing"
)

// A row written by an old build can hold a bare number in its protocol bytes
// columns. Merging a new poll into that row used to drop the number.
func TestUpsertKeepsBareNumberProtocolBytes(t *testing.T) {
	ctx := context.Background()
	store := setupTestDB(t)
	bucket := manyHourBase + 60
	insertRawNodePairs(t, store, []rawNodePair{{
		tailnet: DefaultTailnetID, bucket: bucket, src: "a", dst: "b", traffic: "virtual",
		tx: 12, txPkts: 1, flows: 1, protocols: "[6]", protocolBytes: "12", ports: "[]",
		txPorts: "[]", rxPorts: "[]", txProto: "12", rxProto: "{}",
	}})
	err := store.UpsertNodePairAggregates(ctx, DefaultTailnetID, []NodePairAggregate{{
		Bucket: bucket, SrcNodeID: "a", DstNodeID: "b", TrafficType: "virtual",
		TxBytes: 30, FlowCount: 1, Protocols: "[6]", ProtocolBytes: `{"6":30}`, Ports: "[]",
		TxPorts: "[]", RxPorts: "[]", TxProtocolBytes: `{"6":30}`, RxProtocolBytes: "{}",
	}})
	if err != nil {
		t.Fatal(err)
	}
	var pb, txp string
	var tx int64
	if err := store.db.QueryRow(`SELECT protocol_bytes, tx_protocol_bytes, tx_bytes FROM node_pairs WHERE src_node_id = 'a'`).Scan(&pb, &txp, &tx); err != nil {
		t.Fatal(err)
	}
	if tx != 42 {
		t.Fatalf("tx_bytes = %d, want 42", tx)
	}
	for name, raw := range map[string]string{"protocol_bytes": pb, "tx_protocol_bytes": txp} {
		var m map[string]int64
		if err := json.Unmarshal([]byte(raw), &m); err != nil {
			t.Fatalf("%s = %q: %v", name, raw, err)
		}
		var sum int64
		for _, v := range m {
			sum += v
		}
		if sum != 42 || m["6"] != 30 || m["0"] != 12 {
			t.Fatalf("%s = %s, want 30 on protocol 6 and 12 on protocol 0", name, raw)
		}
	}
}
