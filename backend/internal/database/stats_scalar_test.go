package database

import (
	"context"
	"testing"
	"time"
)

// A minute row whose protocol_bytes is a bare number (an older writer) has a
// null protocol key. Stats must skip it like the hour path does, not fail on
// it, on both the minute path and the rollup path.
func TestStatsSkipScalarProtocolBytesInMinuteRows(t *testing.T) {
	ctx := context.Background()
	store := setupTestDB(t)
	base := manyHourBase
	row := func(tailnet string, bucket int64, src string, tx int64, protocols, pb string) rawNodePair {
		return rawNodePair{tailnet: tailnet, bucket: bucket, src: src, dst: "b", traffic: "virtual",
			tx: tx, txPkts: 1, flows: 1, protocols: protocols, protocolBytes: pb, ports: "[]",
			txPorts: "[]", rxPorts: "[]", txProto: "{}", rxProto: "{}"}
	}
	insertRawNodePairs(t, store, []rawNodePair{
		row(DefaultTailnetID, base+60, "a", 30, "[6]", `{"6":30}`),
		row(DefaultTailnetID, base+120, "a", 12, "[6]", "12"),
		row(DefaultTailnetID, base+180, "c", 5, "[17]", `{"17":5}`),
		row(DefaultTailnetID, base+2*hourSeconds+60, "a", 4, "[6]", "4"),
		row(DefaultTailnetID, base+2*hourSeconds+60, "c", 1, "[1]", `{"1":1}`),
		row("other", base+60, "a", 7, "[6]", "7"),
	})

	sums := func(start, end int64) (tcp, udp, other, virtual int64) {
		t.Helper()
		stats, err := store.GetTrafficStatsFromNodePairsByTrafficTypes(ctx, DefaultTailnetID,
			time.Unix(start, 0), time.Unix(end, 0), nil)
		if err != nil {
			t.Fatalf("stats [%d,%d): %v", start-base, end-base, err)
		}
		for _, st := range stats {
			tcp, udp, other, virtual = tcp+st.TCPBytes, udp+st.UDPBytes, other+st.OtherProtoBytes, virtual+st.VirtualBytes
		}
		return
	}
	check := func(label string, start, end int64, want [4]int64) {
		t.Helper()
		tcp, udp, other, virtual := sums(start, end)
		if got := [4]int64{tcp, udp, other, virtual}; got != want {
			t.Fatalf("%s: tcp/udp/other/virtual = %v, want %v", label, got, want)
		}
	}

	// Minute path (window up to 2h), before and after the first hour rolls up.
	check("minutes", base, base+hourSeconds, [4]int64{30, 5, 0, 47})
	check("rollup path, nothing rolled", base, base+3*hourSeconds, [4]int64{30, 5, 1, 52})
	rollFixtureTo(t, store, DefaultTailnetID, base+hourSeconds+2*minuteSeconds)
	check("minutes after roll", base, base+hourSeconds, [4]int64{30, 5, 0, 47})
	check("rollup path", base, base+3*hourSeconds, [4]int64{30, 5, 1, 52})
	check("scalar minute after the mark only", base+2*hourSeconds, base+2*hourSeconds+600, [4]int64{0, 0, 1, 5})
}
