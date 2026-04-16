package handlers

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/rajsinghtech/tsflow/backend/internal/database"
	tailscale "tailscale.com/client/tailscale/v2"
)

// parseDominantProtocol extracts the most common protocol from a JSON array like "[6]" or "[6,17]"
func parseDominantProtocol(protocolsJSON string) int {
	if protocolsJSON == "" || protocolsJSON == "[]" {
		return 0
	}
	var protos []int
	if err := json.Unmarshal([]byte(protocolsJSON), &protos); err != nil || len(protos) == 0 {
		return 0
	}
	return protos[0] // first element is the most common (sorted by aggregator)
}

func (h *Handlers) GetNetworkLogs(c *gin.Context) {
	start := c.Query("start")
	end := c.Query("end")

	if start == "" || end == "" {
		now := time.Now()
		start = now.Add(-5 * time.Minute).Format(time.RFC3339)
		end = now.Format(time.RFC3339)
	}

	st, err := time.Parse(time.RFC3339, start)
	if err != nil {
		log.Printf("ERROR GetNetworkLogs: invalid start time %s: %v", start, err)
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "invalid start time format, expected RFC3339",
		})
		return
	}

	et, err := time.Parse(time.RFC3339, end)
	if err != nil {
		log.Printf("ERROR GetNetworkLogs: invalid end time %s: %v", end, err)
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid end time format, expected RFC3339"})
		return
	}

	if et.Before(st) {
		log.Printf("ERROR GetNetworkLogs: end time before start time: %s < %s", end, start)
		c.JSON(http.StatusBadRequest, gin.H{"error": "end time before start time"})
		return
	}

	now := time.Now()
	if st.After(now) {
		log.Printf("ERROR GetNetworkLogs: future start time not allowed: %s", start)
		c.JSON(http.StatusBadRequest, gin.H{"error": "future start time not allowed"})
		return
	}

	// Clamp future end times to now
	if et.After(now) {
		et = now
		end = et.Format(time.RFC3339)
	}

	duration := et.Sub(st)

	// Cap maximum query range to 90 days
	const maxQueryRange = 90 * 24 * time.Hour
	if duration > maxQueryRange {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "time range too large",
			"hint":  "maximum query range is 90 days",
		})
		return
	}
	// Use chunking for queries longer than threshold to prevent response size issues
	if duration > ChunkThreshold {
		chunks, err := h.tailscaleService.GetNetworkLogsChunkedParallel(start, end, ChunkSize, MaxParallelChunks)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{
				"error": "Failed to fetch network logs",
				"hint":  "Try selecting a smaller time range",
			})
			return
		}

		var allLogs []any

	chunkLoop:
		for _, chunk := range chunks {
			if logsArray, ok := chunk.([]any); ok {
				if len(allLogs)+len(logsArray) > MaxLogsInMemory {
					remaining := MaxLogsInMemory - len(allLogs)
					if remaining > 0 {
						allLogs = append(allLogs, logsArray[:remaining]...)
					}
					break
				}
				allLogs = append(allLogs, logsArray...)
			} else if logsMap, ok := chunk.(map[string]any); ok {
				if logs, exists := logsMap["logs"]; exists {
					if logsArray, ok := logs.([]any); ok {
						if len(allLogs)+len(logsArray) > MaxLogsInMemory {
							remaining := MaxLogsInMemory - len(allLogs)
							if remaining > 0 {
								allLogs = append(allLogs, logsArray[:remaining]...)
							}
							break
						}
						allLogs = append(allLogs, logsArray...)
					} else if logsArray, ok := logs.([]tailscale.NetworkFlowLog); ok {
						for _, log := range logsArray {
							if len(allLogs) >= MaxLogsInMemory {
								break chunkLoop
							}
							allLogs = append(allLogs, log)
						}
					}
				}
			}
		}

		// Sample logs if too many to prevent response size issues
		finalLogs := allLogs
		if len(allLogs) > MaxLogsInResponse {
			sampleRate := max(len(allLogs)/MaxLogsInResponse, 1)
			sampledLogs := make([]any, 0, MaxLogsInResponse)
			for i := 0; i < len(allLogs); i += sampleRate {
				sampledLogs = append(sampledLogs, allLogs[i])
			}
			finalLogs = sampledLogs
		}

		sampleRate := 1
		if len(finalLogs) > 0 && len(allLogs) >= len(finalLogs) {
			sampleRate = len(allLogs) / len(finalLogs)
		}
		c.JSON(http.StatusOK, gin.H{
			"logs": finalLogs,
			"metadata": gin.H{
				"chunked":    true,
				"chunks":     len(chunks),
				"duration":   duration.String(),
				"totalLogs":  len(allLogs),
				"sampled":    len(finalLogs) < len(allLogs),
				"sampleRate": sampleRate,
			},
		})
		return
	}

	logs, err := h.tailscaleService.GetNetworkLogs(start, end)
	if err != nil {
		log.Printf("ERROR GetNetworkLogs: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "Failed to fetch network logs",
		})
		return
	}

	c.JSON(http.StatusOK, logs)
}

func (h *Handlers) GetStoredFlowLogs(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"logs": []any{}})
}

// GetAggregatedFlowLogs returns pre-aggregated node-to-node traffic
// This is the scalable endpoint for large networks - uses pre-computed node pairs
func (h *Handlers) GetAggregatedFlowLogs(c *gin.Context) {
	if h.store == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"error": "Database not configured",
		})
		return
	}

	start := c.Query("start")
	end := c.Query("end")

	var startTime, endTime time.Time
	var err error

	if start == "" {
		startTime = time.Now().Add(-1 * time.Hour)
	} else {
		startTime, err = time.Parse(time.RFC3339, start)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid start time"})
			return
		}
	}

	if end == "" {
		endTime = time.Now()
	} else {
		endTime, err = time.Parse(time.RFC3339, end)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid end time"})
			return
		}
	}

	// Clamp future end times to now
	if endTime.After(time.Now()) {
		endTime = time.Now()
	}

	if endTime.Before(startTime) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "end time before start time"})
		return
	}

	// Calculate bucket size based on time range
	duration := endTime.Sub(startTime)
	var bucketSize int64 = 60 // 1 minute
	if duration > 24*time.Hour {
		bucketSize = 3600 // 1 hour
	}
	if duration > 7*24*time.Hour {
		bucketSize = 86400 // 1 day
	}

	var aggregates []database.NodePairAggregate
	source := "database"

	// Try rolling cache first for recent data (within last hour)
	if h.poller != nil && duration <= time.Hour {
		cache := h.poller.GetRollingCache()
		if cache.HasDataFor(startTime, endTime) {
			aggregates = cache.GetNodePairs(startTime, endTime)
			source = "cache"
		}
	}

	// Fall back to database if cache miss or no data
	if len(aggregates) == 0 {
		ctx, cancel := context.WithTimeout(c.Request.Context(), AggregationQueryTimeout)
		defer cancel()

		// Use pre-computed node pair aggregates
		aggregates, err = h.store.GetNodePairAggregates(ctx, startTime, endTime, bucketSize)
		if err != nil {
			log.Printf("ERROR GetAggregatedFlowLogs: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{
				"error": "Failed to fetch aggregated flows",
			})
			return
		}
		source = "database"
	}

	// Cap results to prevent unbounded responses
	truncated := len(aggregates) > MaxAggregates
	if truncated {
		aggregates = aggregates[:MaxAggregates]
	}

	// Optional traffic type filter (comma-separated, e.g. "virtual,subnet")
	trafficFilter := make(map[string]bool)
	if tf := c.Query("trafficTypes"); tf != "" {
		for _, t := range strings.Split(tf, ",") {
			trafficFilter[strings.TrimSpace(t)] = true
		}
	}

	// Normalize node IDs, add display names, merge duplicates after normalization,
	// and filter by traffic type
	type mergeKey struct{ src, dst, ttype string }
	merged := make(map[mergeKey]*gin.H)

	for _, agg := range aggregates {
		// Apply traffic type filter
		if len(trafficFilter) > 0 && !trafficFilter[agg.TrafficType] {
			continue
		}

		srcID := h.resolveNodeID(agg.SrcNodeID)
		dstID := h.resolveNodeID(agg.DstNodeID)
		key := mergeKey{srcID, dstID, agg.TrafficType}

		if existing, ok := merged[key]; ok {
			// Merge into existing entry
			(*existing)["totalTxBytes"] = (*existing)["totalTxBytes"].(int64) + agg.TxBytes
			(*existing)["totalRxBytes"] = (*existing)["totalRxBytes"].(int64) + agg.RxBytes
			(*existing)["totalTxPkts"] = (*existing)["totalTxPkts"].(int64) + agg.TxPkts
			(*existing)["totalRxPkts"] = (*existing)["totalRxPkts"].(int64) + agg.RxPkts
			(*existing)["flowCount"] = (*existing)["flowCount"].(int64) + agg.FlowCount
		} else {
			flow := gin.H{
				"srcNodeId":    srcID,
				"dstNodeId":    dstID,
				"trafficType":  agg.TrafficType,
				"totalTxBytes": agg.TxBytes,
				"totalRxBytes": agg.RxBytes,
				"totalTxPkts":  agg.TxPkts,
				"totalRxPkts":  agg.RxPkts,
				"flowCount":    agg.FlowCount,
				"protocol":     parseDominantProtocol(agg.Protocols),
			}
			if name := h.resolveNodeName(srcID); name != "" {
				flow["srcDisplayName"] = name
			}
			if name := h.resolveNodeName(dstID); name != "" {
				flow["dstDisplayName"] = name
			}
			merged[key] = &flow
		}
	}

	// Only include flows where both endpoints are known devices
	flows := make([]gin.H, 0, len(merged))
	for _, flow := range merged {
		hasSrc := (*flow)["srcDisplayName"] != nil
		hasDst := (*flow)["dstDisplayName"] != nil
		if hasSrc && hasDst {
			flows = append(flows, *flow)
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"flows": flows,
		"metadata": gin.H{
			"count":      len(flows),
			"start":      startTime,
			"end":        endTime,
			"bucketSize": bucketSize,
			"source":     source,
			"truncated":  truncated,
		},
	})
}

// GetDataRange returns the available time range of stored data
func (h *Handlers) GetDataRange(c *gin.Context) {
	if h.store == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"error": "Database not configured",
		})
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), ShortQueryTimeout)
	defer cancel()

	dataRange, err := h.store.GetDataRange(ctx)
	if err != nil {
		log.Printf("ERROR GetDataRange: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "Failed to get data range",
		})
		return
	}

	c.JSON(http.StatusOK, dataRange)
}
