package services

import (
	"encoding/json"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/rajsinghtech/tsflow/backend/internal/database"
)

// RollingWindowCache provides fast in-memory access to recent aggregates
// This enables instant responses for live view queries without DB access
type RollingWindowCache struct {
	mu sync.RWMutex

	// Node pair aggregates by minute bucket
	nodePairs map[int64][]database.NodePairAggregate

	// Index of nodePairs[bucket] by (src, dst, traffic type). Positions stay
	// valid because pairs are only appended or updated in place.
	nodePairIndex map[int64]map[nodePairCacheKey]int

	// Unique node pairs contributing to network-wide traffic stats by bucket.
	// Keeping the set avoids undercounting when separate polls or traffic types
	// contain disjoint pairs. The pair value is kept as two fields so endpoint
	// IDs containing a delimiter cannot collide.
	trafficStatPairs map[int64]map[trafficPairKey]struct{}

	// Total bandwidth by minute bucket
	bandwidth map[int64]*database.BandwidthBucket

	// Per-node bandwidth by (bucket, nodeID)
	nodeBandwidth map[int64]map[string]*database.NodeBandwidth

	// Network-wide traffic stats by minute bucket
	trafficStats map[int64]*database.TrafficStats

	// Maximum age of cached data (default 1 hour)
	maxAge time.Duration

	// completeFrom is the first bucket this cache holds in full. The
	// database can hold earlier parts of older buckets, written by a previous
	// process before a restart, which the cache never saw. Zero means no
	// floor has been set.
	completeFrom int64
}

func NewRollingWindowCache(maxAge time.Duration) *RollingWindowCache {
	return &RollingWindowCache{
		nodePairs:        make(map[int64][]database.NodePairAggregate),
		nodePairIndex:    make(map[int64]map[nodePairCacheKey]int),
		trafficStatPairs: make(map[int64]map[trafficPairKey]struct{}),
		bandwidth:        make(map[int64]*database.BandwidthBucket),
		nodeBandwidth:    make(map[int64]map[string]*database.NodeBandwidth),
		trafficStats:     make(map[int64]*database.TrafficStats),
		maxAge:           maxAge,
	}
}

// nodePairCacheKey identifies one aggregate row inside a minute bucket.
// Traffic type is part of the key: the same endpoints can carry virtual and
// subnet rows side by side.
type nodePairCacheKey struct {
	src         string
	dst         string
	trafficType string
}

// CoverFrom records that every write from t onward goes through this cache.
// The first call wins: the bucket containing t, and anything before it, may
// already be partly in the database, so coverage checks start at the next
// whole minute. Later calls are ignored.
func (c *RollingWindowCache) CoverFrom(t time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.completeFrom != 0 {
		return
	}
	floor := t.Unix()
	if rem := floor % 60; rem != 0 {
		floor += 60 - rem
	}
	if floor <= 0 {
		floor = 1
	}
	c.completeFrom = floor
}

// Update adds new aggregates to the cache and prunes old data
func (c *RollingWindowCache) Update(
	nodePairs []database.NodePairAggregate,
	bandwidth []database.BandwidthBucket,
	nodeBandwidth []database.NodeBandwidth,
	trafficStats []database.TrafficStats,
) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.applyUpdate(nodePairs, bandwidth, nodeBandwidth, trafficStats, true)
}

// updateLinear applies the same merge with a scan of the bucket. Tests and
// benchmarks use it to check the indexed path against the previous one.
func (c *RollingWindowCache) updateLinear(
	nodePairs []database.NodePairAggregate,
	bandwidth []database.BandwidthBucket,
	nodeBandwidth []database.NodeBandwidth,
	trafficStats []database.TrafficStats,
) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.applyUpdate(nodePairs, bandwidth, nodeBandwidth, trafficStats, false)
}

func (c *RollingWindowCache) applyUpdate(
	nodePairs []database.NodePairAggregate,
	bandwidth []database.BandwidthBucket,
	nodeBandwidth []database.NodeBandwidth,
	trafficStats []database.TrafficStats,
	indexed bool,
) {
	// Add node pairs by bucket, deduplicating by (src, dst, trafficType)
	for _, np := range nodePairs {
		if c.trafficStatPairs[np.Bucket] == nil {
			c.trafficStatPairs[np.Bucket] = make(map[trafficPairKey]struct{})
		}
		c.trafficStatPairs[np.Bucket][trafficPairKey{
			srcNodeID: np.SrcNodeID,
			dstNodeID: np.DstNodeID,
		}] = struct{}{}

		if indexed {
			c.upsertNodePairIndexed(np)
		} else {
			c.upsertNodePairLinear(np)
		}
	}

	// Add bandwidth by bucket
	for _, b := range bandwidth {
		bucket := b.Time.Unix()
		if existing, ok := c.bandwidth[bucket]; ok {
			existing.TxBytes += b.TxBytes
			existing.RxBytes += b.RxBytes
		} else {
			c.bandwidth[bucket] = &database.BandwidthBucket{
				Time:    b.Time,
				TxBytes: b.TxBytes,
				RxBytes: b.RxBytes,
			}
		}
	}

	// Add node bandwidth by bucket and node
	for _, nb := range nodeBandwidth {
		if c.nodeBandwidth[nb.Bucket] == nil {
			c.nodeBandwidth[nb.Bucket] = make(map[string]*database.NodeBandwidth)
		}
		nodeMap := c.nodeBandwidth[nb.Bucket]
		if existing, ok := nodeMap[nb.NodeID]; ok {
			existing.TxBytes += nb.TxBytes
			existing.RxBytes += nb.RxBytes
		} else {
			nodeMap[nb.NodeID] = &database.NodeBandwidth{
				Bucket:  nb.Bucket,
				NodeID:  nb.NodeID,
				TxBytes: nb.TxBytes,
				RxBytes: nb.RxBytes,
			}
		}
	}

	// Add traffic stats by bucket. Port counters are additive because multiple
	// polls can contribute to the same minute bucket.
	for _, ts := range trafficStats {
		if existing, ok := c.trafficStats[ts.Bucket]; ok {
			existing.TCPBytes += ts.TCPBytes
			existing.UDPBytes += ts.UDPBytes
			existing.OtherProtoBytes += ts.OtherProtoBytes
			existing.VirtualBytes += ts.VirtualBytes
			existing.ExitBytes += ts.ExitBytes
			existing.SubnetBytes += ts.SubnetBytes
			existing.PhysicalBytes += ts.PhysicalBytes
			existing.TotalFlows += ts.TotalFlows
			if pairs := c.trafficStatPairs[ts.Bucket]; len(pairs) > 0 {
				if pairCount := int64(len(pairs)); pairCount > existing.UniquePairs {
					existing.UniquePairs = pairCount
				}
			} else if ts.UniquePairs > existing.UniquePairs {
				existing.UniquePairs = ts.UniquePairs
			}
			existing.TopPorts = mergePortJSON(existing.TopPorts, ts.TopPorts)
		} else {
			copied := ts
			if pairs := c.trafficStatPairs[ts.Bucket]; len(pairs) > 0 {
				if pairCount := int64(len(pairs)); pairCount > copied.UniquePairs {
					copied.UniquePairs = pairCount
				}
			}
			c.trafficStats[ts.Bucket] = &copied
		}
	}

	// Prune old data
	c.prune()
}

func (c *RollingWindowCache) upsertNodePairLinear(np database.NodePairAggregate) {
	existing := c.nodePairs[np.Bucket]
	for i := range existing {
		if existing[i].SrcNodeID == np.SrcNodeID && existing[i].DstNodeID == np.DstNodeID && existing[i].TrafficType == np.TrafficType {
			mergeCachedNodePair(&existing[i], np)
			return
		}
	}
	c.nodePairs[np.Bucket] = append(c.nodePairs[np.Bucket], np)
}

func (c *RollingWindowCache) upsertNodePairIndexed(np database.NodePairAggregate) {
	index := c.nodePairIndex[np.Bucket]
	if index == nil {
		index = make(map[nodePairCacheKey]int)
		c.nodePairIndex[np.Bucket] = index
	}
	key := nodePairCacheKey{src: np.SrcNodeID, dst: np.DstNodeID, trafficType: np.TrafficType}
	if i, ok := index[key]; ok {
		mergeCachedNodePair(&c.nodePairs[np.Bucket][i], np)
		return
	}
	index[key] = len(c.nodePairs[np.Bucket])
	c.nodePairs[np.Bucket] = append(c.nodePairs[np.Bucket], np)
}

func mergeCachedNodePair(existing *database.NodePairAggregate, np database.NodePairAggregate) {
	existingDirectional := existing.DirectionalPorts
	incomingDirectional := np.DirectionalPorts
	existing.TxBytes += np.TxBytes
	existing.RxBytes += np.RxBytes
	existing.TxPkts += np.TxPkts
	existing.RxPkts += np.RxPkts
	existing.FlowCount += np.FlowCount
	existing.ProtocolBytes = mergeProtocolByteJSON(
		existing.ProtocolBytes, np.ProtocolBytes,
		existing.Protocols, np.Protocols,
		existing.TxBytes+existing.RxBytes-np.TxBytes-np.RxBytes,
		np.TxBytes+np.RxBytes,
	)
	existing.Protocols = protocolJSONFromBytes(existing.ProtocolBytes, existing.Protocols, np.Protocols)
	existing.Ports = mergePortJSON(existing.Ports, np.Ports)
	if existingDirectional && incomingDirectional {
		existing.TxProtocolBytes = mergeDirectionalProtocolByteJSON(existing.TxProtocolBytes, np.TxProtocolBytes)
		existing.RxProtocolBytes = mergeDirectionalProtocolByteJSON(existing.RxProtocolBytes, np.RxProtocolBytes)
		existing.TxPorts = mergePortJSON(existing.TxPorts, np.TxPorts)
		existing.RxPorts = mergePortJSON(existing.RxPorts, np.RxPorts)
		return
	}
	// A legacy contribution has no reliable direction metadata. Keep
	// the cache response on the legacy path for the whole pair.
	existing.DirectionalPorts = false
	existing.TxProtocolBytes = "{}"
	existing.RxProtocolBytes = "{}"
	existing.TxPorts = "[]"
	existing.RxPorts = "[]"
}

// prune removes data older than maxAge
func (c *RollingWindowCache) prune() {
	cutoff := time.Now().Add(-c.maxAge).Unix()

	for bucket := range c.nodePairs {
		if bucket < cutoff {
			delete(c.nodePairs, bucket)
			delete(c.nodePairIndex, bucket)
		}
	}
	for bucket := range c.nodePairIndex {
		if bucket < cutoff {
			delete(c.nodePairIndex, bucket)
		}
	}

	for bucket := range c.trafficStatPairs {
		if bucket < cutoff {
			delete(c.trafficStatPairs, bucket)
		}
	}

	for bucket := range c.bandwidth {
		if bucket < cutoff {
			delete(c.bandwidth, bucket)
		}
	}

	for bucket := range c.nodeBandwidth {
		if bucket < cutoff {
			delete(c.nodeBandwidth, bucket)
		}
	}

	for bucket := range c.trafficStats {
		if bucket < cutoff {
			delete(c.trafficStats, bucket)
		}
	}
}

// GetNodePairs returns cached node pair aggregates for a time range
func (c *RollingWindowCache) GetNodePairs(start, end time.Time) []database.NodePairAggregate {
	c.mu.RLock()
	defer c.mu.RUnlock()

	startUnix := start.Unix()
	endUnix := end.Unix()
	var result []database.NodePairAggregate

	for bucket, pairs := range c.nodePairs {
		if bucket >= startUnix && bucket < endUnix {
			result = append(result, pairs...)
		}
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Bucket != result[j].Bucket {
			return result[i].Bucket < result[j].Bucket
		}
		if result[i].SrcNodeID != result[j].SrcNodeID {
			return result[i].SrcNodeID < result[j].SrcNodeID
		}
		if result[i].DstNodeID != result[j].DstNodeID {
			return result[i].DstNodeID < result[j].DstNodeID
		}
		return result[i].TrafficType < result[j].TrafficType
	})

	return result
}

// GetBandwidth returns cached bandwidth for a time range (sorted by time)
func (c *RollingWindowCache) GetBandwidth(start, end time.Time) []database.BandwidthBucket {
	c.mu.RLock()
	defer c.mu.RUnlock()

	startUnix := start.Unix()
	endUnix := end.Unix()
	var result []database.BandwidthBucket

	for bucket, bw := range c.bandwidth {
		if bucket >= startUnix && bucket < endUnix {
			result = append(result, *bw)
		}
	}

	// Sort by time ascending
	sort.Slice(result, func(i, j int) bool {
		return result[i].Time.Before(result[j].Time)
	})

	return result
}

// GetNodeBandwidth returns cached bandwidth for a specific node (sorted by time)
func (c *RollingWindowCache) GetNodeBandwidth(start, end time.Time, nodeID string) []database.BandwidthBucket {
	c.mu.RLock()
	defer c.mu.RUnlock()

	startUnix := start.Unix()
	endUnix := end.Unix()
	var result []database.BandwidthBucket

	for bucket, nodeMap := range c.nodeBandwidth {
		if bucket >= startUnix && bucket < endUnix {
			if nb, ok := nodeMap[nodeID]; ok {
				result = append(result, database.BandwidthBucket{
					Time:    time.Unix(bucket, 0).UTC(),
					TxBytes: nb.TxBytes,
					RxBytes: nb.RxBytes,
				})
			}
		}
	}

	// Sort by time ascending
	sort.Slice(result, func(i, j int) bool {
		return result[i].Time.Before(result[j].Time)
	})

	return result
}

// GetTrafficStats returns cached traffic stats for a time range (sorted by bucket)
func (c *RollingWindowCache) GetTrafficStats(start, end time.Time) []database.TrafficStats {
	c.mu.RLock()
	defer c.mu.RUnlock()

	startUnix := start.Unix()
	endUnix := end.Unix()
	var result []database.TrafficStats

	for bucket, ts := range c.trafficStats {
		if bucket >= startUnix && bucket < endUnix {
			result = append(result, *ts)
		}
	}

	sort.Slice(result, func(i, j int) bool {
		return result[i].Bucket < result[j].Bucket
	})

	return result
}

// cacheWindowCovers reports whether every minute bucket of [start, end) is
// cached. A window that reaches a bucket before completeFrom is not covered.
func cacheWindowCovers(now time.Time, maxAge time.Duration, completeFrom int64, start, end time.Time, buckets map[int64]struct{}) bool {
	if !end.After(start) || len(buckets) == 0 {
		return false
	}
	if start.Before(now.Add(-maxAge)) || end.After(now.Add(time.Minute)) {
		return false
	}
	// Getters use the exact Unix-second range (`bucket >= start.Unix()`), so a
	// query beginning partway through a minute cannot be covered by the bucket
	// that began before it. Start at the first bucket the getter can return.
	startUnix := start.Unix()
	firstBucket := (startUnix / 60) * 60
	if firstBucket < startUnix {
		firstBucket += 60
	}
	lastBucket := ((end.Unix() - 1) / 60) * 60
	if lastBucket < firstBucket {
		return false
	}
	if firstBucket < completeFrom {
		return false
	}
	for bucket := firstBucket; bucket <= lastBucket; bucket += 60 {
		if _, ok := buckets[bucket]; !ok {
			return false
		}
	}
	return true
}

func (c *RollingWindowCache) HasNodePairDataFor(start, end time.Time) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	buckets := make(map[int64]struct{}, len(c.nodePairs))
	for bucket := range c.nodePairs {
		buckets[bucket] = struct{}{}
	}
	return cacheWindowCovers(time.Now(), c.maxAge, c.completeFrom, start, end, buckets)
}

func (c *RollingWindowCache) HasBandwidthDataFor(start, end time.Time) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	buckets := make(map[int64]struct{}, len(c.bandwidth))
	for bucket := range c.bandwidth {
		buckets[bucket] = struct{}{}
	}
	return cacheWindowCovers(time.Now(), c.maxAge, c.completeFrom, start, end, buckets)
}

func (c *RollingWindowCache) HasNodeBandwidthDataFor(start, end time.Time, nodeID string) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	buckets := make(map[int64]struct{})
	for bucket, nodes := range c.nodeBandwidth {
		if _, ok := nodes[nodeID]; ok {
			buckets[bucket] = struct{}{}
		}
	}
	return cacheWindowCovers(time.Now(), c.maxAge, c.completeFrom, start, end, buckets)
}

func (c *RollingWindowCache) HasTrafficStatsDataFor(start, end time.Time) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	buckets := make(map[int64]struct{}, len(c.trafficStats))
	for bucket := range c.trafficStats {
		buckets[bucket] = struct{}{}
	}
	return cacheWindowCovers(time.Now(), c.maxAge, c.completeFrom, start, end, buckets)
}

// HasDataFor is retained for callers that do not identify a dataset. It only
// reports a hit when one complete dataset covers the range.
func (c *RollingWindowCache) HasDataFor(start, end time.Time) bool {
	return c.HasNodePairDataFor(start, end) || c.HasBandwidthDataFor(start, end)
}

func mergeProtocolByteJSON(existing, incoming, existingProtocols, incomingProtocols string, existingTotal, incomingTotal int64) string {
	merged := make(map[int]int64)
	add := func(rawBytes, rawProtocols string, total int64) {
		var values map[string]int64
		if json.Unmarshal([]byte(rawBytes), &values) == nil && len(values) > 0 {
			for rawProtocol, bytes := range values {
				var protocol int
				if _, err := fmt.Sscanf(rawProtocol, "%d", &protocol); err == nil {
					merged[protocol] += bytes
				}
			}
			return
		}
		var protocols []int
		if json.Unmarshal([]byte(rawProtocols), &protocols) != nil || len(protocols) == 0 {
			return
		}
		perProtocol := total / int64(len(protocols))
		remainder := total - perProtocol*int64(len(protocols))
		for i, protocol := range protocols {
			bytes := perProtocol
			if i == 0 {
				bytes += remainder
			}
			merged[protocol] += bytes
		}
	}
	add(existing, existingProtocols, existingTotal)
	add(incoming, incomingProtocols, incomingTotal)
	encoded, err := json.Marshal(merged)
	if err != nil {
		return "{}"
	}
	return string(encoded)
}

func mergeDirectionalProtocolByteJSON(existing, incoming string) string {
	merged := make(map[int]int64)
	for _, raw := range []string{existing, incoming} {
		var values map[string]int64
		if json.Unmarshal([]byte(raw), &values) != nil {
			continue
		}
		for rawProtocol, bytes := range values {
			var protocol int
			if _, err := fmt.Sscanf(rawProtocol, "%d", &protocol); err == nil {
				merged[protocol] += bytes
			}
		}
	}
	encoded, err := json.Marshal(merged)
	if err != nil {
		return "{}"
	}
	return string(encoded)
}

func protocolJSONFromBytes(rawBytes, fallbackExisting, fallbackIncoming string) string {
	var values map[string]int64
	if json.Unmarshal([]byte(rawBytes), &values) != nil || len(values) == 0 {
		return mergeProtocolJSON(fallbackExisting, fallbackIncoming)
	}
	protocols := make([]int, 0, len(values))
	for rawProtocol := range values {
		var protocol int
		if _, err := fmt.Sscanf(rawProtocol, "%d", &protocol); err == nil {
			protocols = append(protocols, protocol)
		}
	}
	sort.Slice(protocols, func(i, j int) bool {
		left, right := values[fmt.Sprint(protocols[i])], values[fmt.Sprint(protocols[j])]
		if left != right {
			return left > right
		}
		return protocols[i] < protocols[j]
	})
	encoded, err := json.Marshal(protocols)
	if err != nil {
		return "[]"
	}
	return string(encoded)
}

func mergeProtocolJSON(existing, incoming string) string {
	var values []int
	seen := make(map[int]struct{})
	for _, raw := range []string{existing, incoming} {
		var protocols []int
		if err := json.Unmarshal([]byte(raw), &protocols); err != nil {
			continue
		}
		for _, protocol := range protocols {
			if _, ok := seen[protocol]; !ok {
				seen[protocol] = struct{}{}
				values = append(values, protocol)
			}
		}
	}
	sort.Ints(values)
	encoded, err := json.Marshal(values)
	if err != nil {
		return "[]"
	}
	return string(encoded)
}

func mergePortJSON(existing, incoming string) string {
	var all []database.PortStat
	for _, raw := range []string{existing, incoming} {
		var ports []database.PortStat
		if err := json.Unmarshal([]byte(raw), &ports); err == nil {
			all = append(all, ports...)
		}
	}
	merged := make(map[[2]int]int64, len(all))
	for _, port := range all {
		merged[[2]int{port.Proto, port.Port}] += port.Bytes
	}
	result := make([]database.PortStat, 0, len(merged))
	for key, bytes := range merged {
		result = append(result, database.PortStat{Proto: key[0], Port: key[1], Bytes: bytes})
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
	encoded, err := json.Marshal(result)
	if err != nil {
		return "[]"
	}
	return string(encoded)
}
