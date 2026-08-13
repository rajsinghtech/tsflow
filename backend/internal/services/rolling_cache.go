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

	// Unique node pairs contributing to network-wide traffic stats by bucket.
	// Keeping the set avoids undercounting when separate polls or traffic types
	// contain disjoint pairs.
	trafficStatPairs map[int64]map[string]struct{}

	// Total bandwidth by minute bucket
	bandwidth map[int64]*database.BandwidthBucket

	// Per-node bandwidth by (bucket, nodeID)
	nodeBandwidth map[int64]map[string]*database.NodeBandwidth

	// Network-wide traffic stats by minute bucket
	trafficStats map[int64]*database.TrafficStats

	// Maximum age of cached data (default 1 hour)
	maxAge time.Duration
}

func NewRollingWindowCache(maxAge time.Duration) *RollingWindowCache {
	return &RollingWindowCache{
		nodePairs:        make(map[int64][]database.NodePairAggregate),
		trafficStatPairs: make(map[int64]map[string]struct{}),
		bandwidth:        make(map[int64]*database.BandwidthBucket),
		nodeBandwidth:    make(map[int64]map[string]*database.NodeBandwidth),
		trafficStats:     make(map[int64]*database.TrafficStats),
		maxAge:           maxAge,
	}
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

	// Add node pairs by bucket, deduplicating by (src, dst, trafficType)
	for _, np := range nodePairs {
		if c.trafficStatPairs[np.Bucket] == nil {
			c.trafficStatPairs[np.Bucket] = make(map[string]struct{})
		}
		c.trafficStatPairs[np.Bucket][np.SrcNodeID+"|"+np.DstNodeID] = struct{}{}

		existing := c.nodePairs[np.Bucket]
		found := false
		for i := range existing {
			if existing[i].SrcNodeID == np.SrcNodeID && existing[i].DstNodeID == np.DstNodeID && existing[i].TrafficType == np.TrafficType {
				existing[i].TxBytes += np.TxBytes
				existing[i].RxBytes += np.RxBytes
				existing[i].TxPkts += np.TxPkts
				existing[i].RxPkts += np.RxPkts
				existing[i].FlowCount += np.FlowCount
				existing[i].ProtocolBytes = mergeProtocolByteJSON(
					existing[i].ProtocolBytes, np.ProtocolBytes,
					existing[i].Protocols, np.Protocols,
					existing[i].TxBytes+existing[i].RxBytes-np.TxBytes-np.RxBytes,
					np.TxBytes+np.RxBytes,
				)
				existing[i].Protocols = protocolJSONFromBytes(existing[i].ProtocolBytes, existing[i].Protocols, np.Protocols)
				existing[i].Ports = mergePortJSON(existing[i].Ports, np.Ports)
				found = true
				break
			}
		}
		if !found {
			c.nodePairs[np.Bucket] = append(c.nodePairs[np.Bucket], np)
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

// prune removes data older than maxAge
func (c *RollingWindowCache) prune() {
	cutoff := time.Now().Add(-c.maxAge).Unix()

	for bucket := range c.nodePairs {
		if bucket < cutoff {
			delete(c.nodePairs, bucket)
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

func cacheWindowCovers(now time.Time, maxAge time.Duration, start, end time.Time, buckets map[int64]struct{}) bool {
	if !end.After(start) || len(buckets) == 0 {
		return false
	}
	if start.Before(now.Add(-maxAge)) || end.After(now.Add(time.Minute)) {
		return false
	}
	firstBucket := (start.Unix() / 60) * 60
	lastBucket := ((end.Unix() - 1) / 60) * 60
	if lastBucket < firstBucket {
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
	return cacheWindowCovers(time.Now(), c.maxAge, start, end, buckets)
}

func (c *RollingWindowCache) HasBandwidthDataFor(start, end time.Time) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	buckets := make(map[int64]struct{}, len(c.bandwidth))
	for bucket := range c.bandwidth {
		buckets[bucket] = struct{}{}
	}
	return cacheWindowCovers(time.Now(), c.maxAge, start, end, buckets)
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
	return cacheWindowCovers(time.Now(), c.maxAge, start, end, buckets)
}

func (c *RollingWindowCache) HasTrafficStatsDataFor(start, end time.Time) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	buckets := make(map[int64]struct{}, len(c.trafficStats))
	for bucket := range c.trafficStats {
		buckets[bucket] = struct{}{}
	}
	return cacheWindowCovers(time.Now(), c.maxAge, start, end, buckets)
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
