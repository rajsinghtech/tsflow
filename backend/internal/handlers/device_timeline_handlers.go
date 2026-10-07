package handlers

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/rajsinghtech/tsflow/backend/internal/database"
)

type deviceTimelineMetadata struct {
	Start         time.Time `json:"start"`
	End           time.Time `json:"end"`
	Tailnet       string    `json:"tailnet"`
	Node          string    `json:"node"`
	Limit         int       `json:"limit"`
	Offset        int       `json:"offset"`
	Count         int       `json:"count"`
	HasMore       bool      `json:"hasMore"`
	BucketSeconds int64     `json:"bucketSeconds"`
	TrafficTypes  []string  `json:"trafficTypes,omitempty"`
}

// GetDeviceTimeline returns one device's bytes over time and a page of peers.
func (h *Handlers) GetDeviceTimeline(c *gin.Context) {
	tn, ok := h.bindTailnet(c)
	if !ok {
		return
	}
	if h.store == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "Database not configured"})
		return
	}
	nodeID := strings.TrimSpace(c.Query("node"))
	if nodeID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "node is required"})
		return
	}
	startTime, endTime, err := h.parseTimeRange(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	query, err := parseTimelineQuery(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	scope, ok := h.viewerScope(c)
	if !ok {
		return
	}
	if scope.active {
		owns, ownsErr := h.store.ViewerOwns(c.Request.Context(), tn.id, scope.login, nodeID)
		if ownsErr != nil {
			if writeContextError(c, ownsErr) {
				return
			}
			log.Printf("ERROR GetDeviceTimeline owner: %v", ownsErr)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch device timeline"})
			return
		}
		if !owns {
			c.JSON(http.StatusNotFound, gin.H{"error": "device is not in the viewer scope"})
			return
		}
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), DefaultQueryTimeout)
	defer cancel()
	timeline, err := h.store.GetDeviceTimeline(ctx, tn.id, nodeID, startTime, endTime, query)
	if err != nil {
		if writeContextError(c, err) {
			return
		}
		log.Printf("ERROR GetDeviceTimeline: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch device timeline"})
		return
	}
	if timeline == nil {
		timeline = &database.DeviceTimeline{NodeID: nodeID, Buckets: []database.TimelineBucket{}, Peers: []database.TimelinePeer{}}
	}
	if timeline.Buckets == nil {
		timeline.Buckets = []database.TimelineBucket{}
	}
	if timeline.Peers == nil {
		timeline.Peers = []database.TimelinePeer{}
	}
	applyTimelineCoverage(timeline.Buckets, startTime, endTime, timeline.BucketSeconds)
	labelTimelinePeers(timeline.Peers)
	c.JSON(http.StatusOK, gin.H{
		"nodeId":        timeline.NodeID,
		"hostname":      timeline.Hostname,
		"bucketSeconds": timeline.BucketSeconds,
		"buckets":       timeline.Buckets,
		"peers":         timeline.Peers,
		"metadata": deviceTimelineMetadata{
			Start:         startTime,
			End:           endTime,
			Tailnet:       tn.id,
			Node:          nodeID,
			Limit:         query.Limit,
			Offset:        query.Offset,
			Count:         len(timeline.Peers),
			HasMore:       timeline.HasMore,
			BucketSeconds: timeline.BucketSeconds,
			TrafficTypes:  query.TrafficTypes,
		},
	})
}

func parseTimelineQuery(c *gin.Context) (database.TimelineQuery, error) {
	query := database.TimelineQuery{Limit: database.RankDefaultLimit}
	if raw := c.Query("limit"); raw != "" {
		limit, err := strconv.Atoi(raw)
		if err != nil || limit <= 0 || limit > database.RankMaxLimit {
			return query, fmt.Errorf("limit must be a positive integer no larger than %d", database.RankMaxLimit)
		}
		query.Limit = limit
	}
	if raw := c.Query("offset"); raw != "" {
		offset, err := strconv.Atoi(raw)
		if err != nil || offset < 0 || offset > database.RankMaxOffset {
			return query, fmt.Errorf("offset must be a non-negative integer no larger than %d", database.RankMaxOffset)
		}
		query.Offset = offset
	}
	trafficTypes, err := parseBandwidthTrafficTypes(c.Query("trafficTypes"))
	if err != nil {
		return query, err
	}
	query.TrafficTypes = trafficTypes
	return query, nil
}

func applyTimelineCoverage(buckets []database.TimelineBucket, start, end time.Time, bucketSeconds int64) {
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

func labelTimelinePeers(peers []database.TimelinePeer) {
	for i := range peers {
		if name, ok := derpRelayLabel(peers[i].PeerID); ok {
			peers[i].Hostname = name
		}
	}
}
