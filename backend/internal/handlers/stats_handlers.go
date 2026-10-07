package handlers

import (
	"context"
	"log"
	"net/http"
	"sort"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/rajsinghtech/tsflow/backend/internal/database"
)

// GetStatsOverview returns network-wide statistics for a time range
func (h *Handlers) GetStatsOverview(c *gin.Context) {
	tn, ok := h.bindTailnet(c)
	if !ok {
		return
	}
	if h.store == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "Database not configured"})
		return
	}

	startTime, endTime, err := h.parseTimeRange(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	var buckets []database.TrafficStats
	source := "database"
	trafficTypes, trafficTypesErr := parseBandwidthTrafficTypes(c.Query("trafficTypes"))
	if trafficTypesErr != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": trafficTypesErr.Error()})
		return
	}

	// Try rolling cache first for recent data
	duration := endTime.Sub(startTime)
	if tn.poller != nil && duration <= time.Hour && len(trafficTypes) == 0 {
		cache := tn.poller.GetRollingCache()
		if cache.HasTrafficStatsDataFor(startTime, endTime) {
			buckets = cache.GetTrafficStats(startTime, endTime)
			source = "cache"
		}
	}

	// Fall back to database
	if len(buckets) == 0 {
		ctx, cancel := context.WithTimeout(c.Request.Context(), AggregationQueryTimeout)
		defer cancel()

		if len(trafficTypes) > 0 {
			buckets, err = h.store.GetTrafficStatsFromNodePairsByTrafficTypes(ctx, tn.id, startTime, endTime, trafficTypes)
		} else {
			buckets, err = h.store.GetTrafficStats(ctx, tn.id, startTime, endTime)
		}
		if err != nil {
			if writeContextError(c, err) {
				return
			}
			log.Printf("ERROR GetStatsOverview: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{
				"error": "Failed to fetch traffic stats",
			})
			return
		}
		source = "database"

		// traffic_stats is authoritative where it already has a bucket. Derive
		// the missing buckets from node_pairs instead of scanning the window
		// again. The unique-pair recount inside GetTrafficStats stays, so two
		// polls that share a minute still use MAX(recount, stored).
		if len(trafficTypes) == 0 {
			var derivedBuckets []database.TrafficStats
			derivedBuckets, err = h.store.FillMissingTrafficStats(ctx, tn.id, startTime, endTime, buckets)
			if err != nil {
				if writeContextError(c, err) {
					return
				}
				log.Printf("ERROR GetStatsOverview (derived): %v", err)
				c.JSON(http.StatusInternalServerError, gin.H{
					"error": "Failed to fetch supplemental traffic stats",
				})
				return
			}
			if len(buckets) == 0 {
				source = "database (derived)"
			}
			buckets = mergeTrafficStatsBuckets(buckets, derivedBuckets)
		}
	}

	// Aggregate buckets into summary
	var tcpBytes, udpBytes, otherProtoBytes int64
	var virtualBytes, exitBytes, subnetBytes, physicalBytes int64
	var totalFlows, maxUniquePairs int64
	for _, b := range buckets {
		tcpBytes += b.TCPBytes
		udpBytes += b.UDPBytes
		otherProtoBytes += b.OtherProtoBytes
		virtualBytes += b.VirtualBytes
		exitBytes += b.ExitBytes
		subnetBytes += b.SubnetBytes
		physicalBytes += b.PhysicalBytes
		totalFlows += b.TotalFlows
		if b.UniquePairs > maxUniquePairs {
			maxUniquePairs = b.UniquePairs
		}
	}

	// Per-bucket uniquePairs is the busiest minute or hour, which undercounts
	// a window. Count distinct endpoints across the whole range instead, using
	// hourly rollups where they already cover the middle of the window.
	countCtx, countCancel := context.WithTimeout(c.Request.Context(), AggregationQueryTimeout)
	defer countCancel()
	distinctPairs, err := h.store.CountDistinctPairs(countCtx, tn.id, startTime, endTime, trafficTypes)
	if err != nil {
		if writeContextError(c, err) {
			return
		}
		log.Printf("ERROR GetStatsOverview unique pairs: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "Failed to count unique pairs",
		})
		return
	}
	if distinctPairs == 0 {
		distinctPairs = maxUniquePairs
	}

	c.JSON(http.StatusOK, gin.H{
		"summary": gin.H{
			"tcpBytes":        tcpBytes,
			"udpBytes":        udpBytes,
			"otherProtoBytes": otherProtoBytes,
			"virtualBytes":    virtualBytes,
			"exitBytes":       exitBytes,
			"subnetBytes":     subnetBytes,
			"physicalBytes":   physicalBytes,
			"totalFlows":      totalFlows,
			"uniquePairs":     distinctPairs,
		},
		"buckets": buckets,
		"metadata": gin.H{
			"start":        startTime,
			"end":          endTime,
			"bucketCount":  len(buckets),
			"source":       source,
			"trafficTypes": trafficTypes,
		},
	})
}

// mergeTrafficStatsBuckets adds derived buckets that are absent from the
// primary traffic_stats result. Primary buckets always win on overlap.
func mergeTrafficStatsBuckets(primary, derived []database.TrafficStats) []database.TrafficStats {
	if len(primary) == 0 && len(derived) == 0 {
		return nil
	}

	byBucket := make(map[int64]database.TrafficStats, len(primary)+len(derived))
	for _, bucket := range primary {
		byBucket[bucket.Bucket] = bucket
	}
	for _, bucket := range derived {
		if _, exists := byBucket[bucket.Bucket]; !exists {
			byBucket[bucket.Bucket] = bucket
		}
	}

	merged := make([]database.TrafficStats, 0, len(byBucket))
	for _, bucket := range byBucket {
		merged = append(merged, bucket)
	}
	sort.Slice(merged, func(i, j int) bool {
		return merged[i].Bucket < merged[j].Bucket
	})
	return merged
}

// GetTopTalkers returns the top N nodes by total traffic
func (h *Handlers) GetTopTalkers(c *gin.Context) {
	tn, ok := h.bindTailnet(c)
	if !ok {
		return
	}
	if h.store == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "Database not configured"})
		return
	}

	startTime, endTime, err := h.parseTimeRange(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	limit := h.parseLimitParam(c, 10, 100)
	trafficTypes, trafficTypesErr := parseBandwidthTrafficTypes(c.Query("trafficTypes"))
	if trafficTypesErr != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": trafficTypesErr.Error()})
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), DefaultQueryTimeout)
	defer cancel()

	// Fetch more rows than requested to have enough after filtering unresolvable entries
	var talkers []database.TopTalker
	if len(trafficTypes) > 0 {
		talkers, err = h.store.GetTopTalkersByTrafficTypes(ctx, tn.id, startTime, endTime, trafficTypes, limit*10)
	} else {
		talkers, err = h.store.GetTopTalkers(ctx, tn.id, startTime, endTime, limit*10)
	}
	if err != nil {
		if writeContextError(c, err) {
			return
		}
		log.Printf("ERROR GetTopTalkers: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "Failed to fetch top talkers",
		})
		return
	}

	// Enrich with device names, merge duplicates after ID normalization,
	// and filter to known devices only
	type talkerAccum struct {
		displayName string
		owner       string
		txBytes     int64
		rxBytes     int64
		totalBytes  int64
	}
	merged := make(map[string]*talkerAccum)
	for _, t := range talkers {
		resolvedID := h.resolveNodeID(tn.poller, t.NodeID)
		name := h.resolveNodeName(tn.poller, resolvedID)
		if name == "" {
			name = resolvedID
		}
		if existing, ok := merged[resolvedID]; ok {
			existing.txBytes += t.TxBytes
			existing.rxBytes += t.RxBytes
			existing.totalBytes += t.TotalBytes
		} else {
			merged[resolvedID] = &talkerAccum{
				displayName: name,
				owner:       h.resolveNodeOwner(tn.poller, resolvedID),
				txBytes:     t.TxBytes,
				rxBytes:     t.RxBytes,
				totalBytes:  t.TotalBytes,
			}
		}
	}

	enriched := make([]gin.H, 0, len(merged))
	for id, acc := range merged {
		enriched = append(enriched, gin.H{
			"nodeId":      id,
			"displayName": acc.displayName,
			"owner":       acc.owner,
			"txBytes":     acc.txBytes,
			"rxBytes":     acc.rxBytes,
			"totalBytes":  acc.totalBytes,
		})
	}

	// Sort by totalBytes descending and cap to requested limit
	sort.Slice(enriched, func(i, j int) bool {
		left, right := enriched[i]["totalBytes"].(int64), enriched[j]["totalBytes"].(int64)
		if left != right {
			return left > right
		}
		return enriched[i]["nodeId"].(string) < enriched[j]["nodeId"].(string)
	})
	if len(enriched) > limit {
		enriched = enriched[:limit]
	}

	c.JSON(http.StatusOK, gin.H{
		"talkers": enriched,
		"metadata": gin.H{
			"start":        startTime,
			"end":          endTime,
			"limit":        limit,
			"count":        len(enriched),
			"trafficTypes": trafficTypes,
		},
	})
}

// GetTopPairs returns the top N node pairs by total traffic
func (h *Handlers) GetTopPairs(c *gin.Context) {
	tn, ok := h.bindTailnet(c)
	if !ok {
		return
	}
	if h.store == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "Database not configured"})
		return
	}

	startTime, endTime, err := h.parseTimeRange(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	limit := h.parseLimitParam(c, 10, 100)
	trafficTypes, trafficTypesErr := parseBandwidthTrafficTypes(c.Query("trafficTypes"))
	if trafficTypesErr != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": trafficTypesErr.Error()})
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), DefaultQueryTimeout)
	defer cancel()

	// Fetch more rows than requested to have enough after filtering unresolvable entries
	var pairs []database.TopPair
	if len(trafficTypes) > 0 {
		pairs, err = h.store.GetTopPairsByTrafficTypes(ctx, tn.id, startTime, endTime, trafficTypes, limit*10)
	} else {
		pairs, err = h.store.GetTopPairs(ctx, tn.id, startTime, endTime, limit*10)
	}
	if err != nil {
		if writeContextError(c, err) {
			return
		}
		log.Printf("ERROR GetTopPairs: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "Failed to fetch top pairs",
		})
		return
	}

	// Enrich with device names, merge duplicates after ID normalization,
	// and filter to pairs where both endpoints are known
	type pairKey struct{ src, dst string }
	type pairAccum struct {
		srcName    string
		srcOwner   string
		dstName    string
		dstOwner   string
		txBytes    int64
		rxBytes    int64
		totalBytes int64
		flowCount  int64
	}
	pairMerged := make(map[pairKey]*pairAccum)
	for _, p := range pairs {
		srcID := h.resolveNodeID(tn.poller, p.SrcNodeID)
		dstID := h.resolveNodeID(tn.poller, p.DstNodeID)
		srcName := h.resolveNodeName(tn.poller, srcID)
		dstName := h.resolveNodeName(tn.poller, dstID)
		if srcName == "" {
			srcName = srcID
		}
		if dstName == "" {
			dstName = dstID
		}
		key := pairKey{srcID, dstID}
		if existing, ok := pairMerged[key]; ok {
			existing.txBytes += p.TxBytes
			existing.rxBytes += p.RxBytes
			existing.totalBytes += p.TotalBytes
			existing.flowCount += p.FlowCount
		} else {
			pairMerged[key] = &pairAccum{
				srcName:    srcName,
				srcOwner:   h.resolveNodeOwner(tn.poller, srcID),
				dstName:    dstName,
				dstOwner:   h.resolveNodeOwner(tn.poller, dstID),
				txBytes:    p.TxBytes,
				rxBytes:    p.RxBytes,
				totalBytes: p.TotalBytes,
				flowCount:  p.FlowCount,
			}
		}
	}

	enriched := make([]gin.H, 0, len(pairMerged))
	for key, acc := range pairMerged {
		enriched = append(enriched, gin.H{
			"srcNodeId":      key.src,
			"srcDisplayName": acc.srcName,
			"srcOwner":       acc.srcOwner,
			"dstNodeId":      key.dst,
			"dstDisplayName": acc.dstName,
			"dstOwner":       acc.dstOwner,
			"txBytes":        acc.txBytes,
			"rxBytes":        acc.rxBytes,
			"totalBytes":     acc.totalBytes,
			"flowCount":      acc.flowCount,
		})
	}

	// Sort by totalBytes descending and cap to requested limit
	sort.Slice(enriched, func(i, j int) bool {
		left, right := enriched[i]["totalBytes"].(int64), enriched[j]["totalBytes"].(int64)
		if left != right {
			return left > right
		}
		if enriched[i]["srcNodeId"].(string) != enriched[j]["srcNodeId"].(string) {
			return enriched[i]["srcNodeId"].(string) < enriched[j]["srcNodeId"].(string)
		}
		return enriched[i]["dstNodeId"].(string) < enriched[j]["dstNodeId"].(string)
	})
	if len(enriched) > limit {
		enriched = enriched[:limit]
	}

	c.JSON(http.StatusOK, gin.H{
		"pairs": enriched,
		"metadata": gin.H{
			"start":        startTime,
			"end":          endTime,
			"limit":        limit,
			"count":        len(enriched),
			"trafficTypes": trafficTypes,
		},
	})
}

// GetNodeDetailStats returns detailed stats for a specific node
func (h *Handlers) GetNodeDetailStats(c *gin.Context) {
	tn, ok := h.bindTailnet(c)
	if !ok {
		return
	}
	if h.store == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "Database not configured"})
		return
	}

	nodeID := c.Param("id")
	if nodeID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "node ID required"})
		return
	}

	startTime, endTime, err := h.parseTimeRange(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), DefaultQueryTimeout)
	defer cancel()

	stats, err := h.store.GetNodeStats(ctx, tn.id, nodeID, startTime, endTime)
	if err != nil {
		if writeContextError(c, err) {
			return
		}
		log.Printf("ERROR GetNodeDetailStats: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "Failed to fetch node stats",
		})
		return
	}

	c.JSON(http.StatusOK, stats)
}
