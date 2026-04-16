package handlers

import (
	"context"
	"log"
	"math"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/rajsinghtech/tsflow/backend/internal/database"
)

// GetBandwidthAggregated returns aggregated bandwidth data for the chart
func (h *Handlers) GetBandwidthAggregated(c *gin.Context) {
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

	nodeID := c.Query("nodeId")
	// Validate nodeId if provided — must be non-empty after trimming
	if c.Query("nodeId") != "" && strings.TrimSpace(nodeID) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "nodeId must be non-empty if provided"})
		return
	}

	// If nodeId looks like an IP address, resolve it to a device ID via the device cache.
	// The bandwidth tables store data keyed by device ID (e.g. "7911952361817638"),
	// but the frontend may send an IP for VIP/service nodes that lack a device object.
	if nodeID != "" && net.ParseIP(nodeID) != nil && h.poller != nil {
		resolved := h.poller.GetDeviceCache().ResolveIP(nodeID)
		if resolved != nodeID {
			nodeID = resolved
		}
	}

	var buckets []database.BandwidthBucket
	source := "database"

	// Try rolling cache first for recent data (within last hour)
	if h.poller != nil {
		cache := h.poller.GetRollingCache()
		if cache.HasDataFor(startTime, endTime) {
			if nodeID != "" {
				buckets = cache.GetNodeBandwidth(startTime, endTime, nodeID)
			} else {
				buckets = cache.GetBandwidth(startTime, endTime)
			}
			source = "cache"
		}
	}

	// Fall back to database if cache miss or no data
	if len(buckets) == 0 {
		ctx, cancel := context.WithTimeout(c.Request.Context(), DefaultQueryTimeout)
		defer cancel()

		// If nodeId provided, filter by that node
		if nodeID != "" {
			buckets, err = h.store.GetNodeBandwidth(ctx, startTime, endTime, nodeID)
		} else {
			buckets, err = h.store.GetBandwidth(ctx, startTime, endTime)
		}

		if err != nil {
			log.Printf("ERROR GetBandwidthAggregated: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{
				"error": "Failed to fetch bandwidth data",
			})
			return
		}
		source = "database"
	}

	// Ensure non-nil slice so JSON serializes as [] not null
	if buckets == nil {
		buckets = []database.BandwidthBucket{}
	}

	truncated := len(buckets) > MaxBuckets
	if truncated {
		buckets = buckets[len(buckets)-MaxBuckets:] // Keep most recent
	}

	// Compute bucket duration from actual data to avoid mismatch when the DB
	// falls back to a coarser tier than the range heuristic expects.
	var bucketSeconds int64
	if source == "cache" {
		bucketSeconds = 60 // rolling cache uses 1-minute buckets
	} else if len(buckets) >= 2 {
		// Derive from minimum gap between consecutive data points
		minGap := int64(math.MaxInt64)
		for i := 1; i < len(buckets); i++ {
			gap := buckets[i].Time.Unix() - buckets[i-1].Time.Unix()
			if gap > 0 && gap < minGap {
				minGap = gap
			}
		}
		if minGap > 0 && minGap < int64(math.MaxInt64) {
			bucketSeconds = minGap
		} else {
			bucketSeconds = 60
		}
	} else {
		// Fallback heuristic for 0-1 data points
		rangeSeconds := int64(endTime.Sub(startTime).Seconds())
		if rangeSeconds <= 24*3600 {
			bucketSeconds = 60
		} else if rangeSeconds <= 7*24*3600 {
			bucketSeconds = 3600
		} else {
			bucketSeconds = 86400
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"buckets": buckets,
		"metadata": gin.H{
			"count":         len(buckets),
			"start":         startTime,
			"end":           endTime,
			"nodeId":        nodeID,
			"source":        source,
			"truncated":     truncated,
			"bucketSeconds": bucketSeconds,
		},
	})
}

// GetBandwidthByIPs returns bandwidth data filtered by IP addresses
// This is for backwards compatibility - converts IPs to node IDs using device cache
func (h *Handlers) GetBandwidthByIPs(c *gin.Context) {
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

	ipsStr := c.Query("ips") // Comma-separated list of IPs

	// If no IPs provided, return total bandwidth
	if ipsStr == "" {
		redirectURL := "/api/bandwidth?start=" + url.QueryEscape(startTime.Format(time.RFC3339)) + "&end=" + url.QueryEscape(endTime.Format(time.RFC3339))
		c.Redirect(http.StatusTemporaryRedirect, redirectURL)
		return
	}

	// Parse IPs and resolve to node IDs using device cache
	ips := strings.Split(ipsStr, ",")
	for i := range ips {
		ips[i] = strings.TrimSpace(ips[i])
	}

	// Use poller's device cache to resolve IPs to node IDs
	nodeIDs := make(map[string]bool)
	if h.poller != nil {
		cache := h.poller.GetDeviceCache()
		for _, ip := range ips {
			nodeID := cache.ResolveIP(ip)
			nodeIDs[nodeID] = true
		}
	} else {
		// Fallback: use IPs as node IDs (for external IPs)
		for _, ip := range ips {
			nodeIDs[ip] = true
		}
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), DefaultQueryTimeout)
	defer cancel()

	// Aggregate bandwidth for all node IDs
	var allBuckets []database.BandwidthBucket
	bucketMap := make(map[int64]*database.BandwidthBucket)

	for nodeID := range nodeIDs {
		buckets, err := h.store.GetNodeBandwidth(ctx, startTime, endTime, nodeID)
		if err != nil {
			continue // Skip errors for individual nodes
		}
		for _, b := range buckets {
			bucket := b.Time.Unix()
			if existing, ok := bucketMap[bucket]; ok {
				existing.TxBytes += b.TxBytes
				existing.RxBytes += b.RxBytes
			} else {
				bucketMap[bucket] = &database.BandwidthBucket{
					Time:    b.Time,
					TxBytes: b.TxBytes,
					RxBytes: b.RxBytes,
				}
			}
		}
	}

	// Convert map to slice and sort by time
	for _, b := range bucketMap {
		allBuckets = append(allBuckets, *b)
	}
	sort.Slice(allBuckets, func(i, j int) bool {
		return allBuckets[i].Time.Before(allBuckets[j].Time)
	})

	// Derive bucket duration from actual data
	var bucketSeconds int64
	if len(allBuckets) >= 2 {
		minGap := int64(math.MaxInt64)
		for i := 1; i < len(allBuckets); i++ {
			gap := allBuckets[i].Time.Unix() - allBuckets[i-1].Time.Unix()
			if gap > 0 && gap < minGap {
				minGap = gap
			}
		}
		if minGap > 0 && minGap < int64(math.MaxInt64) {
			bucketSeconds = minGap
		} else {
			bucketSeconds = 60
		}
	} else {
		rangeSeconds := int64(endTime.Sub(startTime).Seconds())
		if rangeSeconds <= 24*3600 {
			bucketSeconds = 60
		} else if rangeSeconds <= 7*24*3600 {
			bucketSeconds = 3600
		} else {
			bucketSeconds = 86400
		}
	}

	if allBuckets == nil {
		allBuckets = []database.BandwidthBucket{}
	}

	c.JSON(http.StatusOK, gin.H{
		"buckets": allBuckets,
		"metadata": gin.H{
			"count":         len(allBuckets),
			"start":         startTime,
			"end":           endTime,
			"ips":           ipsStr,
			"bucketSeconds": bucketSeconds,
		},
	})
}
