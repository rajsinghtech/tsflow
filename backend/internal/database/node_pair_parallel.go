package database

import (
	"context"
	"fmt"
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

// splitClosedHours separates hour buckets whose every minute is already in
// the rollup (closed) from the filling hour, which must be read on the same
// snapshot as the mark. spans are plan.hours.
func splitClosedHours(spans [][2]int64, mark int64) (closed []int64, open [][2]int64) {
	if mark < 0 {
		return nil, spans
	}
	rolledUntil := mark + minuteSeconds
	for _, span := range spans {
		h := span[0]
		for ; h+hourSeconds <= span[1] && h+hourSeconds <= rolledUntil; h += hourSeconds {
			closed = append(closed, h)
		}
		if h < span[1] {
			open = append(open, [2]int64{h, span[1]})
		}
	}
	return closed, open
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

// readClosedSpans reads closed hours and closed minute chunks with bounded
// parallel workers and merges each worker's partial groups into grouped.
// Sums, minimums and map unions do not depend on order, so the result equals
// one sequential scan.
func (s *SQLiteStore) readClosedSpans(ctx context.Context, tailnetID string, hours []closedSpan, grouped map[pairGroupKey]*pairGroup) error {
	if len(hours) == 0 {
		return nil
	}
	workers := maxClosedHourReaders
	if workers > len(hours) {
		workers = len(hours)
	}
	if workers <= 1 {
		for _, h := range hours {
			if err := s.readClosedSpan(ctx, tailnetID, h, grouped); err != nil {
				return err
			}
		}
		return nil
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	work := make(chan closedSpan)
	partials := make([]map[pairGroupKey]*pairGroup, workers)
	errs := make([]error, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		partials[i] = make(map[pairGroupKey]*pairGroup)
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for h := range work {
				if errs[i] != nil {
					continue
				}
				if err := s.readClosedSpan(ctx, tailnetID, h, partials[i]); err != nil {
					errs[i] = err
					cancel()
				}
			}
		}(i)
	}
feed:
	for _, h := range hours {
		select {
		case work <- h:
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
	if err := ctx.Err(); err != nil {
		return err
	}
	for _, partial := range partials {
		mergePairGroupMaps(grouped, partial)
	}
	return nil
}

func (s *SQLiteStore) readClosedSpan(ctx context.Context, tailnetID string, span closedSpan, grouped map[pairGroupKey]*pairGroup) error {
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
	rows, err := s.db.QueryContext(ctx, query, tailnetID, span.lo, span.hi)
	if err != nil {
		return fmt.Errorf("failed to query node pairs: %w", err)
	}
	return readPairRows(rows, grouped, false)
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
	for key, state := range o.byKey {
		p.add(key, false, state.n, state.numeric)
	}
	if o.hasNull {
		p.add(0, true, o.nullKey.n, o.nullKey.numeric)
	}
}

func (p *portSums) merge(o *portSums) {
	for key, state := range o.byKey {
		p.add(key, state.n, state.numeric)
	}
}
