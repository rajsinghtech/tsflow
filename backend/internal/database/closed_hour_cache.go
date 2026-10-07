package database

import (
	"container/list"
	"math"
	"sync"
	"sync/atomic"
	"unsafe"
)

// DefaultClosedHourCacheBytes caps the closed-hour cache. A 1k-node tailnet
// uses well under 1MB per cached hour; a 20k-node one about 8MB.
const DefaultClosedHourCacheBytes int64 = 256 << 20

// closedHourCache keeps the merged node-pair rows of closed hours in memory,
// LRU and bounded by an estimated byte size.
//
// A closed hour only changes through a late write or retention. Every write
// path reports the hours it touched after its commit lands, which bumps an
// epoch and records it on those hours. A reader takes the epoch before it
// starts reading, and its rows are only stored if no touch was recorded for
// that hour after that point. So a fill that raced a commit is dropped, never
// stored stale.
type closedHourCache struct {
	mu      sync.Mutex
	budget  int64
	used    int64
	lru     *list.List // front is most recent; values are *hourEntry
	entries map[hourCacheKey]*list.Element
	epoch   uint64
	touched map[hourCacheKey]uint64
	floors  map[string]pruneFloor

	hits, misses, uncacheable atomic.Int64
}

type hourCacheKey struct {
	tailnet string
	hour    int64
}

// pruneFloor marks every hour below `below` as touched at `epoch`.
type pruneFloor struct {
	below int64
	epoch uint64
}

func newClosedHourCache(budget int64) *closedHourCache {
	if budget <= 0 {
		return nil
	}
	return &closedHourCache{
		budget:  budget,
		lru:     list.New(),
		entries: make(map[hourCacheKey]*list.Element),
		touched: make(map[hourCacheKey]uint64),
		floors:  make(map[string]pruneFloor),
	}
}

// start returns the epoch a reader must pass to put. Take it before the read.
func (c *closedHourCache) start() uint64 {
	if c == nil {
		return 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.epoch
}

func (c *closedHourCache) get(tailnet string, hour int64) *hourEntry {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	el := c.entries[hourCacheKey{tailnet, hour}]
	if el == nil {
		c.misses.Add(1)
		return nil
	}
	c.hits.Add(1)
	c.lru.MoveToFront(el)
	return el.Value.(*hourEntry)
}

func (c *closedHourCache) touchedAt(key hourCacheKey) uint64 {
	e := c.touched[key]
	if f, ok := c.floors[key.tailnet]; ok && key.hour < f.below && f.epoch > e {
		e = f.epoch
	}
	return e
}

func (c *closedHourCache) put(entry *hourEntry, readEpoch uint64) {
	if c == nil || entry == nil || entry.size > c.budget {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	key := hourCacheKey{entry.tailnet, entry.hour}
	if c.touchedAt(key) > readEpoch {
		return
	}
	if el := c.entries[key]; el != nil {
		c.removeLocked(el)
	}
	c.entries[key] = c.lru.PushFront(entry)
	c.used += entry.size
	for c.used > c.budget {
		c.removeLocked(c.lru.Back())
	}
}

func (c *closedHourCache) removeLocked(el *list.Element) {
	entry := el.Value.(*hourEntry)
	c.lru.Remove(el)
	delete(c.entries, hourCacheKey{entry.tailnet, entry.hour})
	c.used -= entry.size
}

// invalidate drops the given hours. Call it after the commit that changed
// them has landed.
func (c *closedHourCache) invalidate(tailnet string, hours hourTouches) {
	if c == nil || len(hours) == 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.epoch++
	for hour := range hours {
		key := hourCacheKey{tailnet, hour}
		c.touched[key] = c.epoch
		if el := c.entries[key]; el != nil {
			c.removeLocked(el)
		}
	}
}

// invalidateBelow drops every hour of a tailnet that starts before below.
func (c *closedHourCache) invalidateBelow(tailnet string, below int64) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.epoch++
	f := c.floors[tailnet]
	if below > f.below {
		f.below = below
	}
	f.epoch = c.epoch
	c.floors[tailnet] = f
	for key, el := range c.entries {
		if key.tailnet == tailnet && key.hour < f.below {
			c.removeLocked(el)
		}
	}
	for key := range c.touched {
		if key.tailnet == tailnet && key.hour < f.below {
			delete(c.touched, key)
		}
	}
}

// ClosedHourCacheStats reports the cache size and counters.
type ClosedHourCacheStats struct {
	Entries     int
	Bytes       int64
	Budget      int64
	Hits        int64
	Misses      int64
	Uncacheable int64
}

func (c *closedHourCache) stats() ClosedHourCacheStats {
	if c == nil {
		return ClosedHourCacheStats{}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return ClosedHourCacheStats{
		Entries: len(c.entries), Bytes: c.used, Budget: c.budget,
		Hits: c.hits.Load(), Misses: c.misses.Load(), Uncacheable: c.uncacheable.Load(),
	}
}

// hourTouches is the set of hour buckets a write transaction changed.
type hourTouches map[int64]struct{}

func (t hourTouches) add(bucket int64) {
	if t != nil {
		t[(bucket/hourSeconds)*hourSeconds] = struct{}{}
	}
}

// hourEntry is one closed hour's rows, packed. It is immutable once built
// and shared by concurrent readers.
type hourEntry struct {
	tailnet string
	hour    int64
	size    int64
	strs    []string
	pairs   []packedPair
	protoK  []uint32 // see packProtoKey
	protoN  []int64
	portK   []uint64 // port int32 | proto int16 | flags
	portN   []int64
}

type packedPair struct {
	tx, rx, txPkts, rxPkts, flows int64
	src, dst, traffic             uint32
	protoOff, portOff             uint32
	nProto                        [3]uint16
	nPort                         [3]uint16
	bucketOff                     uint16
	flags                         uint16
	part                          uint32 // keyPartitionHash of the key
}

const (
	pfBucket uint16 = 1 << iota
	pfTx
	pfRx
	pfTxPkts
	pfRxPkts
	pfFlows
	pfDir
	pfDirSet
)

const (
	itemNumeric = 1 << iota
	itemKeyNull
	itemProtoNull
)

func sumFlag(s sumState, f uint16) uint16 {
	if s.numeric {
		return f
	}
	return 0
}

// packHour builds an entry from one hour's groups. It returns nil when a
// value does not fit the packed form; that hour is then read from the
// database each time.
func packHour(tailnet string, hour int64, grouped map[pairGroupKey]*pairGroup) *hourEntry {
	e := &hourEntry{tailnet: tailnet, hour: hour, pairs: make([]packedPair, 0, len(grouped))}
	ids := make(map[string]uint32)
	intern := func(s string) uint32 {
		if id, ok := ids[s]; ok {
			return id
		}
		id := uint32(len(e.strs))
		ids[s] = id
		e.strs = append(e.strs, s)
		return id
	}
	for key, g := range grouped {
		p := packedPair{
			tx: g.tx.n, rx: g.rx.n, txPkts: g.txPkts.n, rxPkts: g.rxPkts.n, flows: g.flows.n,
			src: intern(key.src), dst: intern(key.dst), traffic: intern(key.traffic), part: keyPartitionHash(key),
			flags: sumFlag(g.tx, pfTx) | sumFlag(g.rx, pfRx) | sumFlag(g.txPkts, pfTxPkts) |
				sumFlag(g.rxPkts, pfRxPkts) | sumFlag(g.flows, pfFlows),
		}
		if g.hasBucket {
			off := g.bucket - hour
			if off < 0 || off >= hourSeconds {
				return nil
			}
			p.bucketOff = uint16(off)
			p.flags |= pfBucket
		}
		if g.hasDir {
			if g.directional != 0 && g.directional != 1 {
				return nil
			}
			p.flags |= pfDirSet
			if g.directional == 1 {
				p.flags |= pfDir
			}
		}
		p.protoOff = uint32(len(e.protoK))
		for i, sums := range []*protoSums{&g.protocols, &g.txProto, &g.rxProto} {
			n, ok := e.packProtos(sums)
			if !ok {
				return nil
			}
			p.nProto[i] = n
		}
		p.portOff = uint32(len(e.portK))
		for i, sums := range []*portSums{&g.ports, &g.txPorts, &g.rxPorts} {
			n, ok := e.packPorts(sums)
			if !ok {
				return nil
			}
			p.nPort[i] = n
		}
		e.pairs = append(e.pairs, p)
	}
	e.size = e.estimateSize()
	return e
}

func (e *hourEntry) packProtos(p *protoSums) (uint16, bool) {
	n := len(p.items)
	if p.hasNull {
		n++
	}
	if n > math.MaxUint16 {
		return 0, false
	}
	for _, item := range p.items {
		key, st := item.key, item.st
		if key < -(1<<29) || key >= 1<<29 {
			return 0, false
		}
		e.protoK = append(e.protoK, packProtoKey(key, st.numeric, false))
		e.protoN = append(e.protoN, st.n)
	}
	if p.hasNull {
		e.protoK = append(e.protoK, packProtoKey(0, p.nullKey.numeric, true))
		e.protoN = append(e.protoN, p.nullKey.n)
	}
	return uint16(n), true
}

// packProtoKey stores a protocol key in the low 30 bits (signed) and the
// numeric and null-key flags in the top two. packProtos rejects keys that do
// not fit.
func packProtoKey(key int, numeric, keyNull bool) uint32 {
	v := uint32(int32(key)) & 0x3fffffff
	if numeric {
		v |= 1 << 30
	}
	if keyNull {
		v |= 1 << 31
	}
	return v
}

func unpackProtoKey(v uint32) (key int, numeric, keyNull bool) {
	raw := v & 0x3fffffff
	if raw&0x20000000 != 0 { // sign-extend 30 bits
		raw |= 0xc0000000
	}
	return int(int32(raw)), v&(1<<30) != 0, v&(1<<31) != 0
}

func (e *hourEntry) packPorts(p *portSums) (uint16, bool) {
	if len(p.items) > math.MaxUint16 {
		return 0, false
	}
	for _, item := range p.items {
		key, st := item.key, item.st
		if key.port < math.MinInt32 || key.port > math.MaxInt32 || key.proto < math.MinInt16 || key.proto > math.MaxInt16 {
			return 0, false
		}
		var flags uint64
		if st.numeric {
			flags |= itemNumeric
		}
		if key.portNull {
			flags |= itemKeyNull
		}
		if key.protoNull {
			flags |= itemProtoNull
		}
		e.portK = append(e.portK, uint64(uint32(int32(key.port)))|uint64(uint16(int16(key.proto)))<<32|flags<<48)
		e.portN = append(e.portN, st.n)
	}
	return uint16(len(p.items)), true
}

func (e *hourEntry) estimateSize() int64 {
	size := int64(unsafe.Sizeof(*e)) + 128 // struct plus list/map bookkeeping
	size += int64(cap(e.pairs)) * int64(unsafe.Sizeof(packedPair{}))
	size += int64(cap(e.protoK))*4 + int64(cap(e.protoN))*8
	size += int64(cap(e.portK))*8 + int64(cap(e.portN))*8
	for _, s := range e.strs {
		size += 16 + int64(len(s))
	}
	return size
}

// mergePartition adds the entry's pairs whose key falls in partition part
// of parts to grouped, as readPairRows would have.
func (e *hourEntry) mergePartition(grouped map[pairGroupKey]*pairGroup, part, parts uint32) {
	for i := range e.pairs {
		p := &e.pairs[i]
		if parts > 1 && p.part%parts != part {
			continue
		}
		key := pairGroupKey{src: e.strs[p.src], dst: e.strs[p.dst], traffic: e.strs[p.traffic]}
		g := grouped[key]
		if g == nil {
			g = newPairGroup()
			grouped[key] = g
		}
		if p.flags&pfBucket != 0 {
			b := e.hour + int64(p.bucketOff)
			if !g.hasBucket || b < g.bucket {
				g.bucket = b
				g.hasBucket = true
			}
		}
		g.tx.add(p.tx, p.flags&pfTx != 0)
		g.rx.add(p.rx, p.flags&pfRx != 0)
		g.txPkts.add(p.txPkts, p.flags&pfTxPkts != 0)
		g.rxPkts.add(p.rxPkts, p.flags&pfRxPkts != 0)
		g.flows.add(p.flows, p.flags&pfFlows != 0)
		if p.flags&pfDirSet != 0 {
			dir := 0
			if p.flags&pfDir != 0 {
				dir = 1
			}
			if !g.hasDir || dir < g.directional {
				g.directional = dir
				g.hasDir = true
			}
		}
		off := int(p.protoOff)
		for j, dst := range []*protoSums{&g.protocols, &g.txProto, &g.rxProto} {
			for k := 0; k < int(p.nProto[j]); k++ {
				key, numeric, keyNull := unpackProtoKey(e.protoK[off])
				dst.add(key, keyNull, e.protoN[off], numeric)
				off++
			}
		}
		off = int(p.portOff)
		for j, dst := range []*portSums{&g.ports, &g.txPorts, &g.rxPorts} {
			for k := 0; k < int(p.nPort[j]); k++ {
				v := e.portK[off]
				flags := v >> 48
				dst.add(portKey{
					port:      int(int32(uint32(v))),
					proto:     int(int16(uint16(v >> 32))),
					portNull:  flags&itemKeyNull != 0,
					protoNull: flags&itemProtoNull != 0,
				}, e.portN[off], flags&itemNumeric != 0)
				off++
			}
		}
	}
}

// SetClosedHourCacheBytes sets the closed-hour cache budget and empties the
// cache. Zero or less disables it. Call it before the store serves reads.
func (s *SQLiteStore) SetClosedHourCacheBytes(n int64) {
	s.hourCache = newClosedHourCache(n)
}

// ClosedHourCacheStats reports the closed-hour cache size and counters.
func (s *SQLiteStore) ClosedHourCacheStats() ClosedHourCacheStats {
	return s.hourCache.stats()
}
