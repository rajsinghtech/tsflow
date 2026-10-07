package handlers

import (
	"context"
	"fmt"
	"log"
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
	tn, ok := h.bindTailnet(c)
	if !ok {
		return
	}
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
	trafficTypes, trafficTypesErr := parseBandwidthTrafficTypes(c.Query("trafficTypes"))
	if trafficTypesErr != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": trafficTypesErr.Error()})
		return
	}
	// Validate nodeId if provided — must be non-empty after trimming
	if c.Query("nodeId") != "" && strings.TrimSpace(nodeID) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "nodeId must be non-empty if provided"})
		return
	}

	// If nodeId looks like an IP address, resolve it to a device ID via the device cache.
	// The bandwidth tables store data keyed by device ID (e.g. "7911952361817638"),
	// but the frontend may send an IP for VIP/service nodes that lack a device object.
	if nodeID != "" && net.ParseIP(nodeID) != nil && tn.poller != nil {
		resolved := tn.poller.GetDeviceCache().ResolveIP(nodeID)
		if resolved != nodeID {
			nodeID = resolved
		}
	}
	// Historical rows may use either the stable node id or the legacy numeric id.
	queryIDs := []string{nodeID}
	if nodeID != "" && tn.poller != nil {
		if equiv := tn.poller.GetDeviceCache().EquivalentIDs(nodeID); len(equiv) > 1 {
			queryIDs = equiv
		}
	}

	var buckets []database.BandwidthBucket
	source := "database"

	// Try rolling cache first for recent data (within last hour)
	if tn.poller != nil && len(trafficTypes) == 0 {
		cache := tn.poller.GetRollingCache()
		cacheHasData := cache.HasBandwidthDataFor(startTime, endTime)
		if nodeID != "" {
			cacheHasData = cache.HasNodeBandwidthDataFor(startTime, endTime, nodeID)
			if len(queryIDs) > 1 {
				cacheHasData = true
				for _, id := range queryIDs {
					if !cache.HasNodeBandwidthDataFor(startTime, endTime, id) {
						cacheHasData = false
						break
					}
				}
			}
		}
		if cacheHasData {
			if nodeID != "" {
				if len(queryIDs) == 1 {
					buckets = cache.GetNodeBandwidth(startTime, endTime, nodeID)
				} else {
					parts := make([][]database.BandwidthBucket, 0, len(queryIDs))
					for _, id := range queryIDs {
						parts = append(parts, cache.GetNodeBandwidth(startTime, endTime, id))
					}
					buckets = mergeBandwidthBuckets(parts...)
				}
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

		if nodeID != "" {
			nodeBandwidth := func(id string) ([]database.BandwidthBucket, error) {
				if len(trafficTypes) > 0 {
					return h.store.GetNodeBandwidthByTrafficTypes(ctx, tn.id, startTime, endTime, id, trafficTypes)
				}
				return h.store.GetNodeBandwidth(ctx, tn.id, startTime, endTime, id)
			}
			if len(queryIDs) == 1 {
				buckets, err = nodeBandwidth(nodeID)
			} else {
				parts := make([][]database.BandwidthBucket, 0, len(queryIDs))
				for _, id := range queryIDs {
					var part []database.BandwidthBucket
					part, err = nodeBandwidth(id)
					if err != nil {
						break
					}
					parts = append(parts, part)
				}
				if err == nil {
					buckets = mergeBandwidthBuckets(parts...)
				}
			}
		} else if len(trafficTypes) > 0 {
			// Any explicit list, including all four types, is read from node
			// pairs so physical is included only when the caller asked for it.
			// The pre-aggregated bandwidth table omits physical transport.
			buckets, err = h.store.GetBandwidthByTrafficTypes(ctx, tn.id, startTime, endTime, trafficTypes)
		} else {
			buckets, err = h.store.GetBandwidth(ctx, tn.id, startTime, endTime)
		}

		if err != nil {
			if writeContextError(c, err) {
				return
			}
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

	// The store and the rolling cache both group by bucketSizeForRange, so
	// the bucket width is known from the window. Deriving it from the gaps
	// between returned buckets overstates it whenever some buckets have no
	// traffic, which divides every rate by the gap instead of the bucket.
	bucketSeconds := bucketSizeForRange(startTime, endTime)

	applyBucketCoverage(buckets, startTime, endTime, bucketSeconds)

	c.JSON(http.StatusOK, gin.H{
		"buckets": buckets,
		"metadata": gin.H{
			"count":         len(buckets),
			"start":         startTime,
			"end":           endTime,
			"nodeId":        nodeID,
			"trafficTypes":  trafficTypes,
			"source":        source,
			"truncated":     truncated,
			"bucketSeconds": bucketSeconds,
		},
	})
}

// applyBucketCoverage records how many seconds of each bucket sit inside the
// query window. Dividing a partial first or last bucket by the full bucket
// size understates its throughput.
func applyBucketCoverage(buckets []database.BandwidthBucket, start, end time.Time, bucketSeconds int64) {
	if bucketSeconds < 1 {
		bucketSeconds = 60
	}
	startUnix := start.UTC().Unix()
	endUnix := end.UTC().Unix()
	for i := range buckets {
		bucketStart := buckets[i].Time.UTC().Unix()
		bucketEnd := bucketStart + bucketSeconds
		covStart := bucketStart
		if startUnix > covStart {
			covStart = startUnix
		}
		covEnd := bucketEnd
		if endUnix < covEnd {
			covEnd = endUnix
		}
		seconds := covEnd - covStart
		if seconds < 1 {
			seconds = 1
		}
		if seconds > bucketSeconds {
			seconds = bucketSeconds
		}
		buckets[i].Seconds = seconds
	}
}

func mergeBandwidthBuckets(parts ...[]database.BandwidthBucket) []database.BandwidthBucket {
	totals := make(map[int64]*database.BandwidthBucket)
	order := make([]int64, 0)
	for _, part := range parts {
		for _, bucket := range part {
			key := bucket.Time.UTC().Unix()
			existing, ok := totals[key]
			if !ok {
				copyBucket := bucket
				copyBucket.Time = bucket.Time.UTC()
				totals[key] = &copyBucket
				order = append(order, key)
				continue
			}
			existing.TxBytes += bucket.TxBytes
			existing.RxBytes += bucket.RxBytes
		}
	}
	sort.Slice(order, func(i, j int) bool { return order[i] < order[j] })
	merged := make([]database.BandwidthBucket, 0, len(order))
	for _, key := range order {
		merged = append(merged, *totals[key])
	}
	return merged
}

// ParseTrafficTypes validates a list of traffic type names.
// An empty list means the caller did not choose, which leaves physical out.
// The allowed names are virtual, subnet, exit, and physical.
func ParseTrafficTypes(values []string) ([]string, error) {
	if len(values) == 0 {
		return nil, nil
	}
	return parseBandwidthTrafficTypes(strings.Join(values, ","))
}

func parseBandwidthTrafficTypes(raw string) ([]string, error) {
	if raw == "" {
		return nil, nil
	}
	allowed := map[string]bool{
		"virtual":  true,
		"exit":     true,
		"subnet":   true,
		"physical": true,
	}
	seen := make(map[string]bool)
	var result []string
	for _, value := range strings.Split(raw, ",") {
		value = strings.TrimSpace(value)
		if value == "" || !allowed[value] {
			return nil, fmt.Errorf("trafficTypes contains invalid value %q", value)
		}
		if seen[value] {
			continue
		}
		seen[value] = true
		result = append(result, value)
	}
	return result, nil
}

// GetBandwidthByIPs returns bandwidth data filtered by IP addresses
// This is for backwards compatibility - converts IPs to node IDs using device cache
func (h *Handlers) GetBandwidthByIPs(c *gin.Context) {
	tn, ok := h.bindTailnet(c)
	if !ok {
		return
	}
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
		if c.Query("tailnet") != "" {
			redirectURL += "&tailnet=" + url.QueryEscape(tn.id)
		}
		c.Redirect(http.StatusTemporaryRedirect, redirectURL)
		return
	}

	// Parse IPs and resolve to node IDs using device cache
	ips := strings.Split(ipsStr, ",")
	for i := range ips {
		ips[i] = strings.TrimSpace(ips[i])
		if ips[i] == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "ips must not contain empty entries"})
			return
		}
	}

	// Use poller's device cache to resolve IPs to node IDs
	nodeIDs := make(map[string]bool)
	if tn.poller != nil {
		cache := tn.poller.GetDeviceCache()
		for _, ip := range ips {
			nodeID := cache.ResolveIP(ip)
			for _, id := range cache.EquivalentIDs(nodeID) {
				nodeIDs[id] = true
			}
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
		buckets, err := h.store.GetNodeBandwidth(ctx, tn.id, startTime, endTime, nodeID)
		if err != nil {
			if writeContextError(c, err) {
				return
			}
			log.Printf("ERROR GetBandwidthByIPs for node %q: %v", nodeID, err)
			c.JSON(http.StatusInternalServerError, gin.H{
				"error": "Failed to fetch bandwidth data",
			})
			return
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

	bucketSeconds := bucketSizeForRange(startTime, endTime)

	if allBuckets == nil {
		allBuckets = []database.BandwidthBucket{}
	}
	applyBucketCoverage(allBuckets, startTime, endTime, bucketSeconds)

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
