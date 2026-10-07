package database

import (
	"context"
	"math/rand"
	"strconv"
	"testing"
	"time"
)

// manyHourBase is hour-aligned. The fixture covers 30 hours after it.
const manyHourBase int64 = 1_699_999_200

// manyHourTemplates mixes clean directional rows with the odd shapes the
// legacy read has to match: NULL and invalid JSON, scalar JSON, null port
// bytes, ties, more than 20 ports, delimiter characters in node ids, and
// rows whose direction flag differs between minutes, and a scalar
// protocol_bytes value, which gives a null protocol key.
func manyHourTemplates() []rawNodePair {
	var wide [][3]int64
	for port := int64(1); port <= 23; port++ {
		wide = append(wide, [3]int64{port, 6, 300 - port})
	}
	widePorts := formatPortFixtures(wide)
	return []rawNodePair{
		{src: "tag:app", dst: "tag:db", traffic: "virtual", directional: 1,
			protocols: "[6]", protocolBytes: `{"6":140}`,
			ports: `[{"port":5432,"proto":6,"bytes":140}]`, txPorts: `[{"port":5432,"proto":6,"bytes":100}]`,
			rxPorts: `[{"port":5432,"proto":6,"bytes":40}]`, txProto: `{"6":100}`, rxProto: `{"6":40}`},
		{src: "tag:app", dst: "tag:db", traffic: "subnet", directional: 1,
			protocols: "[17,6]", protocolBytes: `{"6":20,"17":40}`,
			ports:   `[{"port":443,"proto":6,"bytes":20},{"port":53,"proto":17,"bytes":40}]`,
			txPorts: `[{"port":443,"proto":6,"bytes":50}]`, rxPorts: `[{"port":53,"proto":17,"bytes":10}]`,
			txProto: `{"6":50}`, rxProto: `{"17":10}`},
		{src: "tag:router", dst: "192.168.10.5", traffic: "subnet", directional: 1,
			protocols: "[6]", protocolBytes: `{"6":1000}`, ports: widePorts, txPorts: widePorts, rxPorts: "[]",
			txProto: `{"6":1000}`, rxProto: `{"6":10}`},
		{src: "n-laptop", dst: "tag:server", traffic: "virtual", directional: 0,
			protocols: "[17]", protocolBytes: `{"17":70}`, ports: `[{"port":53,"proto":17,"bytes":70}]`,
			txPorts: "[]", rxPorts: "[]", txProto: "{}", rxProto: "{}"},
		{src: "legacy-src", dst: "legacy-dst", traffic: "physical", directional: 0,
			protocols: "[6,17]", protocolBytes: "not-json", ports: "not-json", txPorts: "not-json",
			rxPorts: "not-json", txProto: "not-json", rxProto: "not-json"},
		{src: "mix-src", dst: "mix-dst", traffic: "exit", directional: 0,
			protocols: "[6]", protocolBytes: nil, ports: nil, txPorts: nil, rxPorts: nil, txProto: nil, rxProto: nil},
		{src: "tie-src", dst: "tie-dst", traffic: "virtual", directional: 1,
			protocols: "[6,17]", protocolBytes: `{"17":100,"6":100}`,
			ports:   `[{"port":10,"proto":17,"bytes":50},{"port":10,"proto":6,"bytes":50},{"port":9,"proto":6,"bytes":50}]`,
			txPorts: `[{"port":10,"proto":17,"bytes":50},{"port":9,"proto":6,"bytes":null}]`,
			rxPorts: "[]", txProto: `{"6":100,"17":null}`, rxProto: "{}"},
		{src: "odd-src", dst: "odd-dst", traffic: "virtual", directional: 0,
			protocols: "[]", protocolBytes: `{"6": 10, "06": 2, "17": 10.9}`,
			ports: `[{"port": 80, "proto": 6, "bytes": 12, "name": "http"}]`, txPorts: "[]", rxPorts: "[]",
			txProto: "{}", rxProto: "{}"},
		{src: "scalar-src", dst: "scalar-dst", traffic: "virtual", directional: 0,
			protocols: "[6]", protocolBytes: `{"6":6}`, ports: "[6]", txPorts: "[]", rxPorts: "[]", txProto: "6", rxProto: "null"},
		{src: "scalar-pb-src", dst: "scalar-pb-dst", traffic: "virtual", directional: 0,
			protocols: "[6]", protocolBytes: "9", ports: "[]", txPorts: "[]", rxPorts: "[]", txProto: "9", rxProto: "{}"},
		{src: "a|b", dst: "c|d", traffic: "virtual", directional: 0,
			protocols: "[1]", protocolBytes: `{"1":8}`, ports: "[]", txPorts: "[]", rxPorts: "[]", txProto: "{}", rxProto: "{}"},
	}
}

// buildManyHourStore writes 30 hours of minute rows for two tailnets, rolls
// minutes up through hour 29 minute 24, and returns that mark.
func buildManyHourStore(t *testing.T) (*SQLiteStore, int64) {
	t.Helper()
	store := setupTestDB(t)
	rng := rand.New(rand.NewSource(7))
	var rows []rawNodePair
	for _, tailnet := range []string{DefaultTailnetID, "other"} {
		for h := int64(0); h < 30; h++ {
			for i, tmpl := range manyHourTemplates() {
				if (h+int64(i))%5 == 4 {
					continue // some pairs skip some hours
				}
				for _, m := range []int64{0, 7, 31, 59} {
					if rng.Intn(3) == 0 {
						continue
					}
					row := tmpl
					row.tailnet = tailnet
					row.bucket = manyHourBase + h*hourSeconds + m*minuteSeconds
					row.tx = int64(10 + rng.Intn(1000))
					row.rx = int64(rng.Intn(500))
					row.txPkts, row.rxPkts, row.flows = int64(1+rng.Intn(5)), int64(rng.Intn(5)), int64(1+rng.Intn(3))
					if i == 3 && m == 31 {
						row.directional = 1 // direction flips inside the pair
					}
					rows = append(rows, row)
				}
			}
		}
	}
	insertRawNodePairs(t, store, rows)
	mark := manyHourBase + 29*hourSeconds + 24*minuteSeconds
	for _, tailnet := range []string{DefaultTailnetID, "other"} {
		rollFixtureTo(t, store, tailnet, mark+minuteSeconds)
	}
	return store, mark
}

func rollFixtureTo(t *testing.T, store *SQLiteStore, tailnet string, closedThrough int64) {
	t.Helper()
	ctx := context.Background()
	unlock := store.lockTailnet(tailnet)
	defer unlock()
	tx, err := store.beginWrite(ctx, tailnet)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err := rollClosedMinutes(ctx, tx, tailnet, closedThrough); err != nil {
		t.Fatal(err)
	}
	if err := store.commitWrite(tx, tailnet); err != nil {
		t.Fatal(err)
	}
	mark, err := readHourMark(ctx, store.db, tailnet)
	if err != nil || mark != closedThrough-minuteSeconds {
		t.Fatalf("fixture mark = %d (%v), want %d", mark, err, closedThrough-minuteSeconds)
	}
}

// lateWrite commits a minute row through the poll path without moving the
// mark, the way a delayed log or a late object lands in a closed hour.
func lateWrite(t *testing.T, store *SQLiteStore, tailnet string, bucket int64, src, dst string, tx int64, port int) {
	t.Helper()
	p := strconv.Itoa(port)
	b := strconv.FormatInt(tx, 10)
	if err := store.CommitPollResults(context.Background(), tailnet, PollResults{NodePairs: []NodePairAggregate{{
		Bucket: bucket, SrcNodeID: src, DstNodeID: dst, TrafficType: "virtual",
		TxBytes: tx, TxPkts: 1, FlowCount: 1, Protocols: "[6]", ProtocolBytes: `{"6":` + b + `}`,
		Ports: `[{"port":` + p + `,"proto":6,"bytes":` + b + `}]`, TxPorts: `[{"port":` + p + `,"proto":6,"bytes":` + b + `}]`,
		RxPorts: "[]", TxProtocolBytes: `{"6":` + b + `}`, RxProtocolBytes: "{}", DirectionalPorts: true,
	}}}); err != nil {
		t.Fatal(err)
	}
}

// fixtureWindows returns windows over the fixture: whole hours, ragged
// edges, unaligned seconds, windows that end at or past the mark, windows
// that start at the Unix epoch, and windows that miss the data.
func fixtureWindows(mark int64, n int) [][2]int64 {
	rng := rand.New(rand.NewSource(11))
	end := manyHourBase + 30*hourSeconds
	windows := [][2]int64{
		{manyHourBase, end},
		{manyHourBase, mark + minuteSeconds},
		{manyHourBase + 5*hourSeconds, manyHourBase + 29*hourSeconds},
		{manyHourBase + 5*hourSeconds + 17*minuteSeconds, mark + 4*minuteSeconds},
		{manyHourBase + 28*hourSeconds, end},
		{manyHourBase + 3*hourSeconds + 59*minuteSeconds, manyHourBase + 6*hourSeconds + 1},
		{manyHourBase - 2*hourSeconds, manyHourBase + hourSeconds},
		{end + hourSeconds, end + 2*hourSeconds},
		{0, end + hourSeconds}, // from the Unix epoch, like a poller readiness check
		{0, mark + 7*minuteSeconds},
	}
	for len(windows) < n {
		a := manyHourBase - hourSeconds + rng.Int63n(32*hourSeconds)
		b := a + 1 + rng.Int63n(26*hourSeconds)
		if rng.Intn(2) == 0 {
			a, b = a/60*60, b/60*60
		}
		if b > a {
			windows = append(windows, [2]int64{a, b})
		}
	}
	return windows
}

func assertFixtureWindows(t *testing.T, store *SQLiteStore, mark int64, n int) {
	t.Helper()
	windows := fixtureWindows(mark, n)
	if raceEnabled {
		// A whole-fixture window, a ragged one through the mark and one from
		// the epoch. The plain run checks every window.
		windows = [][2]int64{windows[0], windows[3], windows[8]}
	}
	for _, tailnet := range []string{DefaultTailnetID, "other"} {
		for _, w := range windows {
			assertSamePairAPI(t, store, tailnet, time.Unix(w[0], 0).UTC(), time.Unix(w[1], 0).UTC())
		}
	}
}
