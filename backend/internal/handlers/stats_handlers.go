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

	// Try rolling cache first for recent data
	duration := endTime.Sub(startTime)
	if h.poller != nil && duration <= time.Hour {
		cache := h.poller.GetRollingCache()
		if cache.HasDataFor(startTime, endTime) {
			buckets = cache.GetTrafficStats(startTime, endTime)
			source = "cache"
		}
	}

	// Fall back to database
	if len(buckets) == 0 {
		ctx, cancel := context.WithTimeout(c.Request.Context(), AggregationQueryTimeout)
		defer cancel()

		buckets, err = h.store.GetTrafficStats(ctx, startTime, endTime)
		if err != nil {
			log.Printf("ERROR GetStatsOverview: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{
				"error": "Failed to fetch traffic stats",
			})
			return
		}
		source = "database"

		// Fallback: synthesize from node_pairs when traffic_stats tables are empty
		if len(buckets) == 0 {
			buckets, err = h.store.GetTrafficStatsFromNodePairs(ctx, startTime, endTime)
			if err != nil {
				log.Printf("ERROR GetStatsOverview (fallback): %v", err)
			} else {
				source = "database (derived)"
			}
		}
	}

	// Aggregate buckets into summary
	var tcpBytes, udpBytes, otherProtoBytes int64
	var virtualBytes, subnetBytes, physicalBytes int64
	var totalFlows, maxUniquePairs int64
	for _, b := range buckets {
		tcpBytes += b.TCPBytes
		udpBytes += b.UDPBytes
		otherProtoBytes += b.OtherProtoBytes
		virtualBytes += b.VirtualBytes
		subnetBytes += b.SubnetBytes
		physicalBytes += b.PhysicalBytes
		totalFlows += b.TotalFlows
		if b.UniquePairs > maxUniquePairs {
			maxUniquePairs = b.UniquePairs
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"summary": gin.H{
			"tcpBytes":        tcpBytes,
			"udpBytes":        udpBytes,
			"otherProtoBytes": otherProtoBytes,
			"virtualBytes":    virtualBytes,
			"subnetBytes":     subnetBytes,
			"physicalBytes":   physicalBytes,
			"totalFlows":      totalFlows,
			"uniquePairs":     maxUniquePairs,
		},
		"buckets": buckets,
		"metadata": gin.H{
			"start":       startTime,
			"end":         endTime,
			"bucketCount": len(buckets),
			"source":      source,
		},
	})
}

// GetTopTalkers returns the top N nodes by total traffic
func (h *Handlers) GetTopTalkers(c *gin.Context) {
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

	ctx, cancel := context.WithTimeout(c.Request.Context(), DefaultQueryTimeout)
	defer cancel()

	// Fetch more rows than requested to have enough after filtering unresolvable entries
	talkers, err := h.store.GetTopTalkers(ctx, startTime, endTime, limit*10)
	if err != nil {
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
		resolvedID := h.resolveNodeID(t.NodeID)
		name := h.resolveNodeName(resolvedID)
		if name == "" {
			continue // skip unknown devices
		}
		if existing, ok := merged[resolvedID]; ok {
			existing.txBytes += t.TxBytes
			existing.rxBytes += t.RxBytes
			existing.totalBytes += t.TotalBytes
		} else {
			merged[resolvedID] = &talkerAccum{
				displayName: name,
				owner:       h.resolveNodeOwner(resolvedID),
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
		return enriched[i]["totalBytes"].(int64) > enriched[j]["totalBytes"].(int64)
	})
	if len(enriched) > limit {
		enriched = enriched[:limit]
	}

	c.JSON(http.StatusOK, gin.H{
		"talkers": enriched,
		"metadata": gin.H{
			"start": startTime,
			"end":   endTime,
			"limit": limit,
			"count": len(enriched),
		},
	})
}

// GetTopPairs returns the top N node pairs by total traffic
func (h *Handlers) GetTopPairs(c *gin.Context) {
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

	ctx, cancel := context.WithTimeout(c.Request.Context(), DefaultQueryTimeout)
	defer cancel()

	// Fetch more rows than requested to have enough after filtering unresolvable entries
	pairs, err := h.store.GetTopPairs(ctx, startTime, endTime, limit*10)
	if err != nil {
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
		srcID := h.resolveNodeID(p.SrcNodeID)
		dstID := h.resolveNodeID(p.DstNodeID)
		srcName := h.resolveNodeName(srcID)
		dstName := h.resolveNodeName(dstID)
		if srcName == "" || dstName == "" {
			continue
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
				srcOwner:   h.resolveNodeOwner(srcID),
				dstName:    dstName,
				dstOwner:   h.resolveNodeOwner(dstID),
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
		return enriched[i]["totalBytes"].(int64) > enriched[j]["totalBytes"].(int64)
	})
	if len(enriched) > limit {
		enriched = enriched[:limit]
	}

	c.JSON(http.StatusOK, gin.H{
		"pairs": enriched,
		"metadata": gin.H{
			"start": startTime,
			"end":   endTime,
			"limit": limit,
			"count": len(enriched),
		},
	})
}

// GetNodeDetailStats returns detailed stats for a specific node
func (h *Handlers) GetNodeDetailStats(c *gin.Context) {
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

	stats, err := h.store.GetNodeStats(ctx, nodeID, startTime, endTime)
	if err != nil {
		log.Printf("ERROR GetNodeDetailStats: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "Failed to fetch node stats",
		})
		return
	}

	c.JSON(http.StatusOK, stats)
}
