package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sort"
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

func parsePortStats(portsJSON string) []database.PortStat {
	if portsJSON == "" || portsJSON == "[]" {
		return nil
	}
	var ports []database.PortStat
	if err := json.Unmarshal([]byte(portsJSON), &ports); err != nil {
		return nil
	}
	sort.SliceStable(ports, func(i, j int) bool {
		if ports[i].Bytes == ports[j].Bytes {
			if ports[i].Proto == ports[j].Proto {
				return ports[i].Port < ports[j].Port
			}
			return ports[i].Proto < ports[j].Proto
		}
		return ports[i].Bytes > ports[j].Bytes
	})
	return ports
}

func mergePortStats(existing, incoming []database.PortStat) []database.PortStat {
	merged := make(map[[2]int]int64, len(existing)+len(incoming))
	for _, stat := range existing {
		merged[[2]int{stat.Proto, stat.Port}] += stat.Bytes
	}
	for _, stat := range incoming {
		merged[[2]int{stat.Proto, stat.Port}] += stat.Bytes
	}

	result := make([]database.PortStat, 0, len(merged))
	for key, bytes := range merged {
		result = append(result, database.PortStat{
			Proto: key[0],
			Port:  key[1],
			Bytes: bytes,
		})
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
	return result
}

func dominantProtocol(protocolsJSON string, ports []database.PortStat) int {
	if protocol := parseDominantProtocol(protocolsJSON); protocol != 0 {
		return protocol
	}
	if len(ports) > 0 {
		return ports[0].Proto
	}
	return 0
}

func parseProtocolBytes(protocolBytesJSON, protocolsJSON string, totalBytes int64) map[int]int64 {
	result := make(map[int]int64)
	var raw map[string]int64
	if json.Unmarshal([]byte(protocolBytesJSON), &raw) == nil && len(raw) > 0 {
		for key, bytes := range raw {
			var protocol int
			if _, err := fmt.Sscanf(key, "%d", &protocol); err == nil {
				result[protocol] += bytes
			}
		}
		return result
	}

	var protocols []int
	if json.Unmarshal([]byte(protocolsJSON), &protocols) != nil || len(protocols) == 0 {
		return result
	}
	perProtocol := totalBytes / int64(len(protocols))
	remainder := totalBytes - perProtocol*int64(len(protocols))
	for i, protocol := range protocols {
		bytes := perProtocol
		if i == 0 {
			bytes += remainder
		}
		result[protocol] += bytes
	}
	return result
}

func dominantProtocolFromBytes(protocolBytesJSON, protocolsJSON string, totalBytes int64, ports []database.PortStat) int {
	bytesByProtocol := parseProtocolBytes(protocolBytesJSON, protocolsJSON, totalBytes)
	var dominant, dominantBytes int64
	for protocol, bytes := range bytesByProtocol {
		if bytes > dominantBytes || (bytes == dominantBytes && protocol < int(dominant)) {
			dominant = int64(protocol)
			dominantBytes = bytes
		}
	}
	if dominantBytes > 0 || len(bytesByProtocol) > 0 {
		return int(dominant)
	}
	return dominantProtocol(protocolsJSON, ports)
}

func (h *Handlers) GetNetworkLogs(c *gin.Context) {
	st, et, err := h.parseTimeRange(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	start := st.Format(time.RFC3339)
	end := et.Format(time.RFC3339)
	duration := et.Sub(st)
	// Use chunking for queries longer than threshold to prevent response size issues
	if duration > ChunkThreshold {
		chunks, err := h.tailscaleService.GetNetworkLogsChunkedParallelWithContext(c.Request.Context(), start, end, ChunkSize, MaxParallelChunks)
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

		// Sample logs if too many to prevent response size issues. The interval
		// uses ceiling division so the response can never exceed the cap.
		finalLogs, sampleRate := sampleLogs(allLogs, MaxLogsInResponse)
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

	logs, err := h.tailscaleService.GetNetworkLogsWithContext(c.Request.Context(), start, end)
	if err != nil {
		log.Printf("ERROR GetNetworkLogs: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "Failed to fetch network logs",
		})
		return
	}

	c.JSON(http.StatusOK, logs)
}

func sampleLogs(logs []any, maxCount int) ([]any, int) {
	if maxCount <= 0 || len(logs) <= maxCount {
		return logs, 1
	}
	interval := (len(logs) + maxCount - 1) / maxCount
	sampled := make([]any, 0, (len(logs)+interval-1)/interval)
	for i := 0; i < len(logs); i += interval {
		sampled = append(sampled, logs[i])
	}
	return sampled, interval
}

func (h *Handlers) GetStoredFlowLogs(c *gin.Context) {
	c.Header("Deprecation", "true")
	c.JSON(http.StatusGone, gin.H{
		"error":       "raw flow logs are no longer stored",
		"replacement": "/api/flow-logs/aggregated",
	})
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

	startTime, endTime, err := h.parseTimeRange(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
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
		if cache.HasNodePairDataFor(startTime, endTime) {
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
	type mergedFlow struct {
		SrcNodeID      string              `json:"srcNodeId"`
		DstNodeID      string              `json:"dstNodeId"`
		SrcDisplayName string              `json:"srcDisplayName,omitempty"`
		DstDisplayName string              `json:"dstDisplayName,omitempty"`
		TrafficType    string              `json:"trafficType"`
		TotalTxBytes   int64               `json:"totalTxBytes"`
		TotalRxBytes   int64               `json:"totalRxBytes"`
		TotalTxPkts    int64               `json:"totalTxPkts"`
		TotalRxPkts    int64               `json:"totalRxPkts"`
		FlowCount      int64               `json:"flowCount"`
		Protocol       int                 `json:"protocol"`
		Ports          []database.PortStat `json:"ports,omitempty"`
		protocolBytes  map[int]int64
	}
	merged := make(map[mergeKey]*mergedFlow)

	for _, agg := range aggregates {
		// Apply traffic type filter
		if len(trafficFilter) > 0 && !trafficFilter[agg.TrafficType] {
			continue
		}

		srcID := h.resolveNodeID(agg.SrcNodeID)
		dstID := h.resolveNodeID(agg.DstNodeID)
		key := mergeKey{srcID, dstID, agg.TrafficType}
		ports := parsePortStats(agg.Ports)
		protocolBytes := parseProtocolBytes(agg.ProtocolBytes, agg.Protocols, agg.TxBytes+agg.RxBytes)

		if existing, ok := merged[key]; ok {
			// Merge into existing entry
			existing.TotalTxBytes += agg.TxBytes
			existing.TotalRxBytes += agg.RxBytes
			existing.TotalTxPkts += agg.TxPkts
			existing.TotalRxPkts += agg.RxPkts
			existing.FlowCount += agg.FlowCount
			for protocol, bytes := range protocolBytes {
				existing.protocolBytes[protocol] += bytes
			}
			existing.Ports = mergePortStats(existing.Ports, ports)
			existing.Protocol = dominantProtocolFromMap(existing.protocolBytes, existing.Ports, agg.Protocols)
		} else {
			flow := &mergedFlow{
				SrcNodeID:     srcID,
				DstNodeID:     dstID,
				TrafficType:   agg.TrafficType,
				TotalTxBytes:  agg.TxBytes,
				TotalRxBytes:  agg.RxBytes,
				TotalTxPkts:   agg.TxPkts,
				TotalRxPkts:   agg.RxPkts,
				FlowCount:     agg.FlowCount,
				Protocol:      dominantProtocolFromBytes(agg.ProtocolBytes, agg.Protocols, agg.TxBytes+agg.RxBytes, ports),
				Ports:         ports,
				protocolBytes: protocolBytes,
			}
			if name := h.resolveNodeName(srcID); name != "" {
				flow.SrcDisplayName = name
			}
			if name := h.resolveNodeName(dstID); name != "" {
				flow.DstDisplayName = name
			}
			merged[key] = flow
		}
	}

	flows := make([]mergedFlow, 0, len(merged))
	for _, flow := range merged {
		flows = append(flows, *flow)
	}
	sort.Slice(flows, func(i, j int) bool {
		left := flows[i].TotalTxBytes + flows[i].TotalRxBytes
		right := flows[j].TotalTxBytes + flows[j].TotalRxBytes
		if left != right {
			return left > right
		}
		if flows[i].SrcNodeID != flows[j].SrcNodeID {
			return flows[i].SrcNodeID < flows[j].SrcNodeID
		}
		if flows[i].DstNodeID != flows[j].DstNodeID {
			return flows[i].DstNodeID < flows[j].DstNodeID
		}
		return flows[i].TrafficType < flows[j].TrafficType
	})

	c.JSON(http.StatusOK, gin.H{
		"flows": flows,
		"metadata": gin.H{
			"count":      len(flows),
			"start":      startTime,
			"end":        endTime,
			"bucketSize": bucketSize,
			"source":     source,
			"truncated":  false,
		},
	})
}

func dominantProtocolFromMap(bytesByProtocol map[int]int64, ports []database.PortStat, fallbackProtocols string) int {
	var dominant, dominantBytes int64
	for protocol, bytes := range bytesByProtocol {
		if bytes > dominantBytes || (bytes == dominantBytes && protocol < int(dominant)) {
			dominant = int64(protocol)
			dominantBytes = bytes
		}
	}
	if dominantBytes > 0 || len(bytesByProtocol) > 0 {
		return int(dominant)
	}
	return dominantProtocol(fallbackProtocols, ports)
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
