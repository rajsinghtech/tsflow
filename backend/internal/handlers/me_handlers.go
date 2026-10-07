package handlers

import (
	"context"
	"log"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/rajsinghtech/tsflow/backend/internal/access"
	"github.com/rajsinghtech/tsflow/backend/internal/database"
)

type viewerScope struct {
	active bool
	login  string
}

func (h *Handlers) viewerScope(c *gin.Context) (viewerScope, bool) {
	if strings.TrimSpace(c.Query("me")) != "1" {
		return viewerScope{}, true
	}
	login, ok := viewerLogin(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "viewer identity is required"})
		return viewerScope{}, false
	}
	return viewerScope{active: true, login: login}, true
}

func (h *Handlers) applyViewerScope(c *gin.Context, query *database.RankQuery) bool {
	scope, ok := h.viewerScope(c)
	if !ok {
		return false
	}
	if scope.active {
		query.User = scope.login
		query.ExactUser = true
		query.Tag = ""
		query.Q = ""
	}
	return true
}

func viewerLogin(c *gin.Context) (string, bool) {
	ident, ok := access.FromGin(c)
	if !ok {
		return "", false
	}
	login := strings.TrimSpace(ident.Login)
	if login == "" {
		return "", false
	}
	return login, true
}

func (h *Handlers) requireViewer(c *gin.Context) (string, bool) {
	login, ok := viewerLogin(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "viewer identity is required"})
		return "", false
	}
	return login, true
}

// GetViewerSummary is the signed-in viewer's devices, traffic, peers, and
// new connections. The login comes from the request identity, not a query param.
func (h *Handlers) GetViewerSummary(c *gin.Context) {
	tn, ok := h.bindTailnet(c)
	if !ok {
		return
	}
	if h.store == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "Database not configured"})
		return
	}
	login, ok := h.requireViewer(c)
	if !ok {
		return
	}
	startTime, endTime, err := h.parseTimeRange(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	trafficTypes, err := parseBandwidthTrafficTypes(c.Query("trafficTypes"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	lookback, lookbackLabel, err := parseLookback(c.Query("lookback"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), DefaultQueryTimeout)
	defer cancel()
	devices, err := h.store.ListViewerDevices(ctx, tn.id, login, startTime, endTime, trafficTypes)
	if err != nil {
		if writeContextError(c, err) {
			return
		}
		log.Printf("ERROR GetViewerSummary devices: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch viewer devices"})
		return
	}
	if devices == nil {
		devices = []database.ViewerDevice{}
	}
	h.markViewerOnline(tn, devices)

	pairs, _, err := h.store.ListNewPairs(ctx, tn.id, startTime, endTime, database.NewPairQuery{
		Lookback:     lookback,
		Limit:        20,
		User:         login,
		ExactUser:    true,
		TrafficTypes: trafficTypes,
	})
	if err != nil {
		if writeContextError(c, err) {
			return
		}
		log.Printf("ERROR GetViewerSummary pairs: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch viewer connections"})
		return
	}
	if pairs == nil {
		pairs = []database.NewPair{}
	}
	labelNewPairs(pairs)
	lookbackStart, dataStart, lookbackComplete := h.lookbackCoverage(ctx, tn.id, startTime, lookback)

	var traffic database.TrafficByteTotal
	var flows int64
	timelines := make([]database.DeviceTimeline, 0, len(devices))
	peerTotals := map[string]*database.TimelinePeer{}
	// Top peers are the devices the viewer talks to, not the viewer's own.
	owned := map[string]bool{}
	for _, device := range devices {
		owned[device.NodeID] = true
		for _, id := range device.IDs {
			owned[id] = true
		}
	}
	for _, device := range devices {
		traffic.TxBytes += device.TxBytes
		traffic.RxBytes += device.RxBytes
		flows += device.FlowCount
		if len(timelines) >= 12 {
			continue
		}
		timeline, timelineErr := h.store.GetDeviceTimeline(ctx, tn.id, device.NodeID, startTime, endTime, database.TimelineQuery{
			// Room for peers that are the viewer's own devices, which are skipped.
			Limit:        10,
			TrafficTypes: trafficTypes,
		})
		if timelineErr != nil {
			if writeContextError(c, timelineErr) {
				return
			}
			log.Printf("ERROR GetViewerSummary timeline: %v", timelineErr)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch viewer timeline"})
			return
		}
		if timeline == nil {
			continue
		}
		applyTimelineCoverage(timeline.Buckets, startTime, endTime, timeline.BucketSeconds)
		labelTimelinePeers(timeline.Peers)
		timelines = append(timelines, *timeline)
		for _, peer := range timeline.Peers {
			if owned[peer.PeerID] || peer.TotalBytes == 0 {
				continue
			}
			item := peerTotals[peer.PeerID]
			if item == nil {
				copyPeer := peer
				peerTotals[peer.PeerID] = &copyPeer
				continue
			}
			item.TxBytes += peer.TxBytes
			item.RxBytes += peer.RxBytes
			item.TotalBytes += peer.TotalBytes
			item.FlowCount += peer.FlowCount
		}
	}
	peers := make([]database.TimelinePeer, 0, len(peerTotals))
	for _, peer := range peerTotals {
		peers = append(peers, *peer)
	}
	sortViewerPeers(peers)
	if len(peers) > 8 {
		peers = peers[:8]
	}

	c.JSON(http.StatusOK, gin.H{
		"login":     login,
		"devices":   devices,
		"traffic":   gin.H{"txBytes": traffic.TxBytes, "rxBytes": traffic.RxBytes, "totalBytes": traffic.TxBytes + traffic.RxBytes, "flowCount": flows},
		"timelines": timelines,
		"peers":     peers,
		"newPairs":  pairs,
		"metadata": gin.H{
			"start":        startTime,
			"end":          endTime,
			"tailnet":      tn.id,
			"lookback":     lookbackLabel,
			"trafficTypes": trafficTypes,
			// Same coverage fields as /analytics/new-pairs, for newPairs.
			"lookbackStart":    lookbackStart,
			"dataStart":        dataStart,
			"lookbackComplete": lookbackComplete,
		},
	})
}

func (h *Handlers) markViewerOnline(tn tailnetScope, devices []database.ViewerDevice) {
	if tn.poller == nil {
		return
	}
	cache := tn.poller.GetDeviceCache()
	if cache == nil {
		return
	}
	for i := range devices {
		for _, id := range devices[i].IDs {
			entry := cache.GetDevice(id)
			if entry == nil {
				continue
			}
			if entry.Online {
				devices[i].Online = true
			}
			if devices[i].Hostname == "" && entry.Hostname != "" {
				devices[i].Hostname = entry.Hostname
			}
		}
	}
}

func sortViewerPeers(peers []database.TimelinePeer) {
	for i := 1; i < len(peers); i++ {
		item := peers[i]
		j := i
		for j > 0 && (peers[j-1].TotalBytes < item.TotalBytes || (peers[j-1].TotalBytes == item.TotalBytes && peers[j-1].PeerID > item.PeerID)) {
			peers[j] = peers[j-1]
			j--
		}
		peers[j] = item
	}
}
