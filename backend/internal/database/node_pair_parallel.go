package database

import (
	"context"
	"database/sql"
	"fmt"
	"hash/maphash"
	"runtime"
	"sync"
)

// maxClosedHourReaders bounds how many closed hours one store reads at once,
// across all requests. The read pool has 10 connections; leaving two free
// keeps small queries moving while a wide window reads.
var maxClosedHourReaders = func() int {
	n := runtime.GOMAXPROCS(0)
	if n > 8 {
		n = 8
	}
	if n < 1 {
		n = 1
	}
	return n
}()

// splitClosedHours separates hour ranges whose every minute is already in
// the rollup (closed) from the filling hour, which must be read on the same
// snapshot as the mark. spans are plan.hours.
func splitClosedHours(spans [][2]int64, mark int64) (closed, open [][2]int64) {
	if mark < 0 {
		return nil, spans
	}
	rolledUntil := mark + minuteSeconds
	for _, span := range spans {
		cut := span[0]
		for cut+hourSeconds <= span[1] && cut+hourSeconds <= rolledUntil {
			cut += hourSeconds
		}
		if cut > span[0] {
			closed = append(closed, [2]int64{span[0], cut})
		}
		if cut < span[1] {
			open = append(open, [2]int64{cut, span[1]})
		}
	}
	return closed, open
}

// rolledHourSeekSQL finds the next hour that has rollup rows. It is a seek
// on the primary key, which starts with (tailnet_id, bucket).
const rolledHourSeekSQL = `
	SELECT MIN(bucket) FROM node_pair_hours
	WHERE tailnet_id = ? AND bucket >= ? AND bucket < ?
`

// listRolledHours returns the hours in ranges that have rollup rows, one
// seek per hour found. A window that starts long before the data, such as
// one from the Unix epoch, becomes one work item per stored hour rather
// than one per hour since the start.
func listRolledHours(ctx context.Context, q queryRower, tailnetID string, ranges [][2]int64) ([]int64, error) {
	var hours []int64
	for _, span := range ranges {
		lo := span[0]
		for lo < span[1] {
			var next sql.NullInt64
			if err := q.QueryRowContext(ctx, rolledHourSeekSQL, tailnetID, lo, span[1]).Scan(&next); err != nil {
				return nil, fmt.Errorf("failed to find rolled hours: %w", err)
			}
			if !next.Valid {
				break
			}
			hour := (next.Int64 / hourSeconds) * hourSeconds
			hours = append(hours, hour)
			lo = hour + hourSeconds
		}
	}
	return hours, nil
}

// closedMinuteChunk is how many closed minutes one parallel work item reads.
const closedMinuteChunk = 10 * minuteSeconds

// splitClosedMinutes separates minute spans that end at or before the
// rollup mark, which only a late write can change, from spans after the
// mark, which must stay on the snapshot that read the mark. Closed spans
// are cut into chunks so they spread across workers.
func splitClosedMinutes(spans [][2]int64, mark int64) (closed, open [][2]int64) {
	if mark < 0 {
		return nil, spans
	}
	rolledUntil := mark + minuteSeconds
	for _, span := range spans {
		lo, hi := span[0], span[1]
		cut := hi
		if cut > rolledUntil {
			cut = rolledUntil
		}
		for ; lo < cut; lo += closedMinuteChunk {
			end := lo + closedMinuteChunk
			if end > cut {
				end = cut
			}
			closed = append(closed, [2]int64{lo, end})
		}
		openLo := span[0]
		if cut > openLo {
			openLo = cut
		}
		if openLo < hi {
			open = append(open, [2]int64{openLo, hi})
		}
	}
	return closed, open
}

// closedSpan is one parallel read: a closed hour from node_pair_hours, or a
// chunk of closed minutes from node_pairs.
type closedSpan struct {
	lo, hi int64
	hours  bool
}

func closedSpans(hours []int64, minutes [][2]int64) []closedSpan {
	spans := make([]closedSpan, 0, len(hours)+len(minutes))
	for _, h := range hours {
		spans = append(spans, closedSpan{lo: h, hi: h + hourSeconds, hours: true})
	}
	for _, m := range minutes {
		spans = append(spans, closedSpan{lo: m[0], hi: m[1]})
	}
	return spans
}

func (s *SQLiteStore) closedHourSlots() chan struct{} {
	s.closedHourOnce.Do(func() {
		s.closedHourSem = make(chan struct{}, maxClosedHourReaders)
	})
	return s.closedHourSem
}

// keyedGroup is one partial group tagged with its key.
type keyedGroup struct {
	key   pairGroupKey
	group *pairGroup
}

// spanResult is what one closed span contributed: a packed hour (from the
// cache or freshly read) or, for minute chunks and hours that do not pack,
// partial groups split by partition.
type spanResult struct {
	entry *hourEntry
	parts [][]keyedGroup
}

var partitionSeed = maphash.MakeSeed()

// keyPartitionHash places a pair key in a merge partition. Packed hours
// store it per pair, so it must not change within a process.
func keyPartitionHash(key pairGroupKey) uint32 {
	var h maphash.Hash
	h.SetSeed(partitionSeed)
	h.WriteString(key.src)
	h.WriteByte(0)
	h.WriteString(key.dst)
	h.WriteByte(0)
	h.WriteString(key.traffic)
	return uint32(h.Sum64())
}

func splitByPartition(grouped map[pairGroupKey]*pairGroup, parts int) [][]keyedGroup {
	out := make([][]keyedGroup, parts)
	for key, group := range grouped {
		p := int(keyPartitionHash(key) % uint32(parts))
		out[p] = append(out[p], keyedGroup{key: key, group: group})
	}
	return out
}

// aggregateWithClosedSpans reads closed spans with bounded parallel
// workers, then merges them with grouped (the snapshot's rows) one key
// partition per worker, so no merge step is sequential. Sums, minimums and
// map unions do not depend on order, and partitions share no keys, so the
// result equals one sequential scan.
func (s *SQLiteStore) aggregateWithClosedSpans(ctx context.Context, tailnetID string, spans []closedSpan, grouped map[pairGroupKey]*pairGroup) ([]NodePairAggregate, error) {
	if len(spans) == 0 {
		return sortedPairAggregates(grouped)
	}
	parts := maxClosedHourReaders
	results := make([]spanResult, len(spans))
	for i := range spans {
		if spans[i].hours {
			results[i].entry = s.hourCache.get(tailnetID, spans[i].lo)
		}
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	if err := runParallel(ctx, cancel, len(spans), parts, func(i int) error {
		if results[i].entry != nil {
			return nil
		}
		return s.readClosedSpan(ctx, tailnetID, spans[i], parts, &results[i])
	}); err != nil {
		return nil, err
	}
	mainParts := splitByPartition(grouped, parts)
	out := make([][]NodePairAggregate, parts)
	if err := runParallel(ctx, cancel, parts, parts, func(p int) error {
		merged := make(map[pairGroupKey]*pairGroup, len(mainParts[p])*2)
		for _, kg := range mainParts[p] {
			merged[kg.key] = kg.group
		}
		for i := range results {
			if results[i].entry != nil {
				results[i].entry.mergePartition(merged, uint32(p), uint32(parts))
				continue
			}
			for _, kg := range results[i].parts[p] {
				if existing := merged[kg.key]; existing != nil {
					existing.merge(kg.group)
				} else {
					merged[kg.key] = kg.group
				}
			}
		}
		aggs := make([]NodePairAggregate, 0, len(merged))
		for key, group := range merged {
			agg, err := group.aggregate(key)
			if err != nil {
				return err
			}
			aggs = append(aggs, agg)
		}
		out[p] = aggs
		return nil
	}); err != nil {
		return nil, err
	}
	total := 0
	for _, aggs := range out {
		total += len(aggs)
	}
	if total == 0 {
		return nil, nil
	}
	all := make([]NodePairAggregate, 0, total)
	for _, aggs := range out {
		all = append(all, aggs...)
	}
	sortPairAggregates(all)
	return all, nil
}

// runParallel calls fn(0..n-1) on up to workers goroutines and returns the
// first error. A failure cancels the rest.
func runParallel(ctx context.Context, cancel context.CancelFunc, n, workers int, fn func(int) error) error {
	if workers > n {
		workers = n
	}
	if workers < 1 {
		workers = 1
	}
	work := make(chan int)
	errs := make([]error, workers)
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := range work {
				if errs[w] != nil {
					continue
				}
				if err := fn(i); err != nil {
					errs[w] = err
					cancel()
				}
			}
		}(w)
	}
feed:
	for i := 0; i < n; i++ {
		select {
		case work <- i:
		case <-ctx.Done():
			break feed
		}
	}
	close(work)
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			return err
		}
	}
	return ctx.Err()
}

func (s *SQLiteStore) readClosedSpan(ctx context.Context, tailnetID string, span closedSpan, parts int, result *spanResult) error {
	slots := s.closedHourSlots()
	select {
	case slots <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	}
	defer func() { <-slots }()
	query := nodePairScanSQL
	if span.hours {
		query = nodePairHourScanSQL
	}
	cacheable := span.hours && s.hourCache != nil
	var epoch uint64
	if cacheable {
		// Take the epoch before the read starts, so a commit that lands
		// during the read keeps these rows out of the cache.
		epoch = s.hourCache.start()
	}
	rows, err := s.db.QueryContext(ctx, query, tailnetID, span.lo, span.hi)
	if err != nil {
		return fmt.Errorf("failed to query node pairs: %w", err)
	}
	grouped := make(map[pairGroupKey]*pairGroup)
	if err := readPairRows(rows, grouped, false); err != nil {
		return err
	}
	if cacheable {
		if entry := packHour(tailnetID, span.lo, grouped); entry != nil {
			s.hourCache.put(entry, epoch)
			result.entry = entry
			return nil
		}
		s.hourCache.uncacheable.Add(1)
	}
	result.parts = splitByPartition(grouped, parts)
	return nil
}

// merge adds another partial group, as if its rows had been added here.
func (g *pairGroup) merge(o *pairGroup) {
	if o.hasBucket && (!g.hasBucket || o.bucket < g.bucket) {
		g.bucket = o.bucket
		g.hasBucket = true
	}
	g.tx.add(o.tx.n, o.tx.numeric)
	g.rx.add(o.rx.n, o.rx.numeric)
	g.txPkts.add(o.txPkts.n, o.txPkts.numeric)
	g.rxPkts.add(o.rxPkts.n, o.rxPkts.numeric)
	g.flows.add(o.flows.n, o.flows.numeric)
	if o.hasDir && (!g.hasDir || o.directional < g.directional) {
		g.directional = o.directional
		g.hasDir = true
	}
	g.protocols.merge(&o.protocols)
	g.txProto.merge(&o.txProto)
	g.rxProto.merge(&o.rxProto)
	g.ports.merge(&o.ports)
	g.txPorts.merge(&o.txPorts)
	g.rxPorts.merge(&o.rxPorts)
	g.dirty = g.dirty || o.dirty
}

func (p *protoSums) merge(o *protoSums) {
	for _, item := range o.items {
		p.add(item.key, false, item.st.n, item.st.numeric)
	}
	if o.hasNull {
		p.add(0, true, o.nullKey.n, o.nullKey.numeric)
	}
}

func (p *portSums) merge(o *portSums) {
	for _, item := range o.items {
		p.add(item.key, item.st.n, item.st.numeric)
	}
}
