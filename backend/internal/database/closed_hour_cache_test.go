package database

import (
	"context"
	"database/sql"
	"reflect"
	"testing"
	"time"
)

func fixtureGroups(t *testing.T) map[pairGroupKey]*pairGroup {
	t.Helper()
	str := func(v any) sql.NullString {
		if v == nil {
			return sql.NullString{}
		}
		return sql.NullString{String: v.(string), Valid: true}
	}
	n := func(v int64) sql.NullInt64 { return sql.NullInt64{Int64: v, Valid: true} }
	templates := append(manyHourTemplates(), rawNodePair{src: "scalar", dst: "scalar", traffic: "virtual",
		protocols: "[6]", protocolBytes: "6", ports: "[6]", txPorts: "[]", rxPorts: "[]", txProto: "6", rxProto: "null"})
	grouped := map[pairGroupKey]*pairGroup{}
	for i, r := range templates {
		g := newPairGroup()
		grouped[pairGroupKey{src: r.src, dst: r.dst, traffic: r.traffic}] = g
		if err := g.add(n(manyHourBase+int64(60*i)), n(int64(100+i)), n(int64(i)), n(1), n(0), n(1), n(int64(r.directional)),
			str(r.protocolBytes), str(r.ports), str(r.txProto), str(r.rxProto), str(r.txPorts), str(r.rxPorts)); err != nil {
			t.Fatal(err)
		}
	}
	// All-NULL sums and a missing bucket must survive packing too.
	nulls := newPairGroup()
	if err := nulls.add(sql.NullInt64{}, sql.NullInt64{}, sql.NullInt64{}, sql.NullInt64{}, sql.NullInt64{}, sql.NullInt64{}, sql.NullInt64{},
		sql.NullString{}, sql.NullString{}, sql.NullString{}, sql.NullString{}, sql.NullString{}, sql.NullString{}); err != nil {
		t.Fatal(err)
	}
	grouped[pairGroupKey{src: "null", dst: "null", traffic: "virtual"}] = nulls
	return grouped
}

func aggregateAll(grouped map[pairGroupKey]*pairGroup) map[pairGroupKey]any {
	out := map[pairGroupKey]any{}
	for key, g := range grouped {
		agg, err := g.aggregate(key)
		if err != nil {
			out[key] = err.Error()
		} else {
			out[key] = agg
		}
	}
	return out
}

func TestPackedHourMergesLikeTheRows(t *testing.T) {
	grouped := fixtureGroups(t)
	entry := packHour(DefaultTailnetID, manyHourBase, grouped)
	if entry == nil {
		t.Fatal("fixture hour should pack")
	}
	got := map[pairGroupKey]*pairGroup{}
	entry.mergeInto(got)
	if !reflect.DeepEqual(aggregateAll(grouped), aggregateAll(got)) {
		t.Fatalf("packed hour differs\nwant %+v\ngot  %+v", aggregateAll(grouped), aggregateAll(got))
	}
	// Merging into groups that already hold rows equals merging the groups.
	base := fixtureGroups(t)
	want := fixtureGroups(t)
	mergePairGroupMaps(want, fixtureGroups(t))
	entry.mergeInto(base)
	if !reflect.DeepEqual(aggregateAll(want), aggregateAll(base)) {
		t.Fatal("packed merge into existing groups differs from a group merge")
	}
	if entry.size <= 0 {
		t.Fatal("entry size should be estimated")
	}
}

func TestPackHourRefusesValuesThatDoNotFit(t *testing.T) {
	key := pairGroupKey{src: "a", dst: "b", traffic: "virtual"}
	for name, mutate := range map[string]func(*pairGroup){
		"bucket outside the hour":  func(g *pairGroup) { g.bucket = manyHourBase + hourSeconds },
		"direction other than 0/1": func(g *pairGroup) { g.directional = 2 },
		"huge protocol key":        func(g *pairGroup) { g.protocols.add(1<<30, false, 1, true) },
		"negative huge proto key":  func(g *pairGroup) { g.txProto.add(-(1<<29)-1, false, 1, true) },
		"huge port":                func(g *pairGroup) { g.ports.add(portKey{port: 1 << 40, proto: 6}, 1, true) },
		"huge port proto":          func(g *pairGroup) { g.rxPorts.add(portKey{port: 1, proto: 1 << 20}, 1, true) },
	} {
		g := newPairGroup()
		g.bucket, g.hasBucket, g.hasDir = manyHourBase, true, true
		g.tx.add(1, true)
		mutate(g)
		if packHour(DefaultTailnetID, manyHourBase, map[pairGroupKey]*pairGroup{key: g}) != nil {
			t.Errorf("%s: packHour should refuse", name)
		}
	}
	// Edge values that do fit round-trip.
	g := newPairGroup()
	g.bucket, g.hasBucket, g.hasDir = manyHourBase+hourSeconds-60, true, true
	g.protocols.add((1<<29)-1, false, 3, true)
	g.protocols.add(-(1 << 29), false, 4, false)
	g.ports.add(portKey{port: -1, proto: -32768, portNull: true}, 5, true)
	g.ports.add(portKey{port: 1<<31 - 1, proto: 32767, protoNull: true}, 6, false)
	grouped := map[pairGroupKey]*pairGroup{key: g}
	entry := packHour(DefaultTailnetID, manyHourBase, grouped)
	if entry == nil {
		t.Fatal("edge values should pack")
	}
	got := map[pairGroupKey]*pairGroup{}
	entry.mergeInto(got)
	if formatProtocols(got[key].protocols) != formatProtocols(g.protocols) || formatProtocolBytes(got[key].protocols) != formatProtocolBytes(g.protocols) ||
		formatPortsLimit(got[key].ports, 0) != formatPortsLimit(g.ports, 0) || got[key].bucket != g.bucket {
		t.Fatalf("edge values changed: %+v vs %+v", got[key], g)
	}
}

func testEntry(tailnet string, hour, size int64) *hourEntry {
	return &hourEntry{tailnet: tailnet, hour: hour, size: size}
}

func TestClosedHourCacheLRUAndBudget(t *testing.T) {
	c := newClosedHourCache(300)
	e := c.start()
	c.put(testEntry("t", 0, 100), e)
	c.put(testEntry("t", 3600, 100), e)
	c.put(testEntry("t", 7200, 100), e)
	c.get("t", 0) // most recent now
	c.put(testEntry("t", 10800, 100), e)
	if c.get("t", 3600) != nil {
		t.Fatal("least recently used hour should be evicted")
	}
	if c.get("t", 0) == nil || c.get("t", 10800) == nil {
		t.Fatal("recent hours should stay")
	}
	if st := c.stats(); st.Bytes > 300 || st.Entries != 3 {
		t.Fatalf("stats = %+v", st)
	}
	c.put(testEntry("t", 14400, 301), e)
	if c.get("t", 14400) != nil || c.stats().Entries != 3 {
		t.Fatal("an entry larger than the budget must not be stored or evict others")
	}
	if newClosedHourCache(0) != nil {
		t.Fatal("a zero budget disables the cache")
	}
	var off *closedHourCache
	off.put(testEntry("t", 0, 1), off.start())
	off.invalidate("t", hourTouches{0: {}})
	off.invalidateBelow("t", 1)
	if off.get("t", 0) != nil {
		t.Fatal("disabled cache returned an entry")
	}
}

// A read that started before a commit must not store its rows, even if the
// invalidation for that commit ran before the read finished.
func TestClosedHourCacheDropsAFillThatRacedACommit(t *testing.T) {
	c := newClosedHourCache(1 << 20)
	before := c.start()
	c.invalidate("t", hourTouches{3600: {}})
	c.put(testEntry("t", 3600, 10), before)
	if c.get("t", 3600) != nil {
		t.Fatal("stale fill was stored")
	}
	c.put(testEntry("t", 7200, 10), before) // a different hour is fine
	if c.get("t", 7200) == nil {
		t.Fatal("untouched hour should be stored")
	}
	after := c.start()
	c.put(testEntry("t", 3600, 10), after)
	if c.get("t", 3600) == nil {
		t.Fatal("a read that started after the commit should be stored")
	}
	c.invalidate("other", hourTouches{3600: {}})
	if c.get("t", 3600) == nil {
		t.Fatal("another tailnet's touch must not drop this one")
	}
	// Retention drops every hour below the floor and blocks older fills.
	old := c.start()
	c.invalidateBelow("t", 7200+3600)
	if c.get("t", 3600) != nil || c.get("t", 7200) != nil {
		t.Fatal("pruned hours should be dropped")
	}
	c.put(testEntry("t", 3600, 10), old)
	c.put(testEntry("t", 10800, 10), old)
	if c.get("t", 3600) != nil {
		t.Fatal("a fill from before the prune was stored")
	}
	if c.get("t", 10800) == nil {
		t.Fatal("hours above the floor are unaffected")
	}
}

func enableCache(t *testing.T, store *SQLiteStore) {
	t.Helper()
	prev := maxClosedHourReaders
	maxClosedHourReaders = 4
	t.Cleanup(func() { maxClosedHourReaders = prev })
	store.SetClosedHourCacheBytes(64 << 20)
}

// Cold, warm, after late writes through every write path, and after
// retention: every window must equal the plain minute scan.
func TestClosedHourCacheMatchesMinuteScan(t *testing.T) {
	ctx := context.Background()
	store, mark := buildManyHourStore(t)
	enableCache(t, store)

	assertFixtureWindows(t, store, mark, 60)
	cold := store.ClosedHourCacheStats()
	if cold.Entries == 0 || cold.Bytes <= 0 {
		t.Fatalf("cache should fill: %+v", cold)
	}
	assertFixtureWindows(t, store, mark, 60)
	warm := store.ClosedHourCacheStats()
	if warm.Hits <= cold.Hits {
		t.Fatalf("second pass should hit the cache: cold %+v warm %+v", cold, warm)
	}

	// Late writes into cached hours, through the poll, object and direct
	// upsert paths.
	lateWrite(t, store, DefaultTailnetID, manyHourBase+3*hourSeconds+7*minuteSeconds, "tag:app", "tag:db", 777, 8443)
	lateWrite(t, store, "other", manyHourBase+20*hourSeconds, "late-new", "late-peer", 55, 22)
	if err := store.CommitObjectIngest(ctx, DefaultTailnetID, ObjectIngestResult{
		Key: "late-object", LastModified: time.Unix(manyHourBase, 0), NodePairs: []NodePairAggregate{{
			Bucket: manyHourBase + 12*hourSeconds + 59*minuteSeconds, SrcNodeID: "a|b", DstNodeID: "c|d", TrafficType: "virtual",
			TxBytes: 31, TxPkts: 1, FlowCount: 1, Protocols: "[17]", ProtocolBytes: `{"17":31}`,
			Ports: `[{"port":53,"proto":17,"bytes":31}]`, TxPorts: "[]", RxPorts: "[]", TxProtocolBytes: "{}", RxProtocolBytes: "{}",
		}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertNodePairAggregates(ctx, DefaultTailnetID, []NodePairAggregate{{
		Bucket: manyHourBase + 25*hourSeconds + 30*minuteSeconds, SrcNodeID: "tie-src", DstNodeID: "tie-dst", TrafficType: "virtual",
		TxBytes: 13, TxPkts: 1, FlowCount: 1, Protocols: "[6]", ProtocolBytes: `{"6":13}`,
		Ports: "[]", TxPorts: "[]", RxPorts: "[]", TxProtocolBytes: "{}", RxProtocolBytes: "{}",
	}}); err != nil {
		t.Fatal(err)
	}
	assertFixtureWindows(t, store, mark, 60)

	// Retention cuts the fixture at hour 10, minute 30.
	cutoff := manyHourBase + 10*hourSeconds + 30*minuteSeconds
	if _, err := store.Cleanup(ctx, DefaultTailnetID, time.Since(time.Unix(cutoff, 0))); err != nil {
		t.Fatal(err)
	}
	assertFixtureWindows(t, store, mark, 60)
	if st := store.ClosedHourCacheStats(); st.Uncacheable != 0 {
		t.Fatalf("fixture hours should all pack: %+v", st)
	}
}

// The cache must not keep rows that a commit between the snapshot and the
// closed reads changed.
func TestClosedHourCacheAcrossAConcurrentLateWrite(t *testing.T) {
	ctx := context.Background()
	store, mark := buildManyHourStore(t)
	enableCache(t, store)
	start := time.Unix(manyHourBase+2*hourSeconds, 0).UTC()
	end := time.Unix(mark, 0).UTC()
	if _, err := store.GetNodePairAggregates(ctx, DefaultTailnetID, start, end); err != nil {
		t.Fatal(err)
	}
	fired := false
	store.closedReadHook = func() {
		if !fired {
			fired = true
			lateWrite(t, store, DefaultTailnetID, manyHourBase+5*hourSeconds+31*minuteSeconds, "tag:app", "tag:db", 4242, 9000)
		}
	}
	got, err := store.GetNodePairAggregates(ctx, DefaultTailnetID, start, end)
	store.closedReadHook = nil
	if err != nil || !fired {
		t.Fatalf("err=%v fired=%v", err, fired)
	}
	want, err := store.legacyNodePairAggregates(ctx, DefaultTailnetID, start, end)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(want, got) {
		t.Fatal("a cached hour hid a late write committed before the closed reads")
	}
	assertSamePairAPI(t, store, DefaultTailnetID, start, end)
}

// mergeInto adds every pair of the entry to grouped.
func (e *hourEntry) mergeInto(grouped map[pairGroupKey]*pairGroup) {
	e.mergePartition(grouped, 0, 1)
}

func mergePairGroupMaps(dst, src map[pairGroupKey]*pairGroup) {
	for key, group := range src {
		if existing := dst[key]; existing != nil {
			existing.merge(group)
		} else {
			dst[key] = group
		}
	}
}
