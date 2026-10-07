package database

import (
	"context"
	"reflect"
	"testing"
	"time"
)

// A protocol_bytes value that is a bare JSON scalar gives the minute read a
// protocol with a null key, which shows up as null in the protocol list.
// Rolling the minute into its hour must keep that key, its bytes and its
// place in the list.
func TestHourRollupKeepsNullProtocolKey(t *testing.T) {
	ctx := context.Background()
	store := setupTestDB(t)
	const h = hourSeconds
	base := manyHourBase
	row := func(bucket int64, src, dst string, tx int64, pb, txProto any) rawNodePair {
		return rawNodePair{tailnet: DefaultTailnetID, bucket: bucket, src: src, dst: dst, traffic: "virtual",
			tx: tx, txPkts: 1, flows: 1, protocols: "[6]", protocolBytes: pb, ports: "[]",
			txPorts: "[]", rxPorts: "[]", txProto: txProto, rxProto: "{}"}
	}
	rows := []rawNodePair{
		// Scalar only: the null key is the pair's only protocol.
		row(base+60, "scalar-src", "scalar-dst", 10, "6", "{}"),
		row(base+120, "scalar-src", "scalar-dst", 20, "40", "6"),
		// Mixed: keyed minutes and scalar minutes in one pair. The null key's
		// bytes decide its place among the keyed protocols.
		row(base+60, "mixed-src", "mixed-dst", 30, `{"6":30,"17":5}`, `{"6":30}`),
		row(base+120, "mixed-src", "mixed-dst", 8, "8", "7"), // in the first roll step
		row(base+180, "mixed-src", "mixed-dst", 40, "12", "{}"),
		row(base+240, "mixed-src", "mixed-dst", 50, "null", "{}"),
		// A JSON null scalar alone keeps a null key with a null sum.
		row(base+300, "nullsum-src", "nullsum-dst", 7, "null", "{}"),
		// A literal "null" key in an object is protocol 0 on the minute read.
		row(base+60, "literal-src", "literal-dst", 9, `{"null":9,"6":1}`, "{}"),
		// Next hour, so the first one closes.
		row(base+h+60, "scalar-src", "scalar-dst", 1, "3", "{}"),
	}
	insertRawNodePairs(t, store, rows)
	// Roll in two steps, so the second merges into the stored hour row.
	rollFixtureTo(t, store, DefaultTailnetID, base+3*minuteSeconds)
	rollFixtureTo(t, store, DefaultTailnetID, base+h+2*minuteSeconds)

	windows := [][2]int64{
		{base, base + h},             // the closed hour alone, read from its rollup row
		{base, base + h + 3*60},      // closed hour plus minutes
		{base - h, base + 2*h},       // around everything
		{base + 2*60, base + h + 60}, // ragged edges
	}
	check := func(stage string) {
		t.Helper()
		for _, w := range windows {
			start, end := time.Unix(w[0], 0).UTC(), time.Unix(w[1], 0).UTC()
			want, err := store.legacyNodePairAggregates(ctx, DefaultTailnetID, start, end)
			if err != nil {
				t.Fatal(err)
			}
			got, err := store.GetNodePairAggregates(ctx, DefaultTailnetID, start, end)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(want, got) {
				t.Fatalf("%s window %v\nwant %s\ngot  %s", stage, w, pairRowsJSON(want), pairRowsJSON(got))
			}
		}
	}
	check("after rollup")

	// A late scalar write into the closed hour goes through the hour delta.
	lateScalar := []NodePairAggregate{{
		Bucket: base + 7*60, SrcNodeID: "mixed-src", DstNodeID: "mixed-dst", TrafficType: "virtual",
		TxBytes: 99, TxPkts: 1, FlowCount: 1, Protocols: "[6]", ProtocolBytes: "99",
		Ports: "[]", TxPorts: "[]", RxPorts: "[]", TxProtocolBytes: "{}", RxProtocolBytes: "{}",
	}, {
		Bucket: base + 8*60, SrcNodeID: "late-src", DstNodeID: "late-dst", TrafficType: "virtual",
		TxBytes: 5, TxPkts: 1, FlowCount: 1, Protocols: "[6]", ProtocolBytes: "5",
		Ports: "[]", TxPorts: "[]", RxPorts: "[]", TxProtocolBytes: "{}", RxProtocolBytes: "{}",
	}}
	if err := store.CommitPollResults(ctx, DefaultTailnetID, PollResults{NodePairs: lateScalar}); err != nil {
		t.Fatal(err)
	}
	check("after a late write")

	// Retention inside the hour rebuilds its rollup row from what is left.
	if _, err := store.Cleanup(ctx, DefaultTailnetID, time.Since(time.Unix(base+150, 0))); err != nil {
		t.Fatal(err)
	}
	check("after retention")
}

// The stats protocol totals read rolled hours with SQL. The null key has no
// protocol number there, so it must not turn into protocol 0.
func TestHourRollupNullKeyStaysOutOfStatsProtocols(t *testing.T) {
	ctx := context.Background()
	store := setupTestDB(t)
	base := manyHourBase
	insertRawNodePairs(t, store, []rawNodePair{
		{tailnet: DefaultTailnetID, bucket: base + 60, src: "a", dst: "b", traffic: "virtual",
			tx: 30, txPkts: 1, flows: 1, protocols: "[6]", protocolBytes: `{"6":30}`, ports: "[]",
			txPorts: "[]", rxPorts: "[]", txProto: "{}", rxProto: "{}"},
		{tailnet: DefaultTailnetID, bucket: base + 120, src: "a", dst: "b", traffic: "virtual",
			tx: 12, txPkts: 1, flows: 1, protocols: "[6]", protocolBytes: "12", ports: "[]",
			txPorts: "[]", rxPorts: "[]", txProto: "{}", rxProto: "{}"},
		{tailnet: DefaultTailnetID, bucket: base + hourSeconds + 60, src: "a", dst: "b", traffic: "virtual",
			tx: 1, txPkts: 1, flows: 1, protocols: "[6]", protocolBytes: `{"6":1}`, ports: "[]",
			txPorts: "[]", rxPorts: "[]", txProto: "{}", rxProto: "{}"},
	})
	rollFixtureTo(t, store, DefaultTailnetID, base+hourSeconds+2*minuteSeconds)
	stats, err := store.GetTrafficStatsFromNodePairsByTrafficTypes(ctx, DefaultTailnetID,
		time.Unix(base, 0), time.Unix(base+3*hourSeconds, 0), nil)
	if err != nil {
		t.Fatal(err)
	}
	var tcp, udp, other int64
	for _, st := range stats {
		tcp, udp, other = tcp+st.TCPBytes, udp+st.UDPBytes, other+st.OtherProtoBytes
	}
	if tcp != 31 || udp != 0 || other != 0 {
		t.Fatalf("stats over the rolled hour: tcp=%d udp=%d other=%d, want 31/0/0", tcp, udp, other)
	}
}
