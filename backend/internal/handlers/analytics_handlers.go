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

type rankMetadata struct {
	Start        time.Time `json:"start"`
	End          time.Time `json:"end"`
	Tailnet      string    `json:"tailnet"`
	Limit        int       `json:"limit"`
	Offset       int       `json:"offset"`
	Count        int       `json:"count"`
	HasMore      bool      `json:"hasMore"`
	Sort         string    `json:"sort"`
	TrafficTypes []string  `json:"trafficTypes,omitempty"`
	Tag          string    `json:"tag,omitempty"`
	User         string    `json:"user,omitempty"`
	Q            string    `json:"q,omitempty"`
}

// GetRankedTalkers returns one page of devices ranked over a time window.
func (h *Handlers) GetRankedTalkers(c *gin.Context) {
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
	query, err := h.parseRankQuery(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if !h.applyViewerScope(c, &query) {
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), DefaultQueryTimeout)
	defer cancel()
	talkers, hasMore, err := h.store.ListRankedTalkers(ctx, tn.id, startTime, endTime, query)
	if err != nil {
		if writeContextError(c, err) {
			return
		}
		log.Printf("ERROR GetRankedTalkers: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch ranked talkers"})
		return
	}
	if talkers == nil {
		talkers = []database.RankedTalker{}
	}
	labelRankedTalkers(talkers)
	c.JSON(http.StatusOK, gin.H{
		"talkers":  talkers,
		"metadata": rankMeta(tn.id, startTime, endTime, query, len(talkers), hasMore),
	})
}

// GetRankedPairs returns one page of src/dst pairs ranked over a time window.
func (h *Handlers) GetRankedPairs(c *gin.Context) {
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
	query, err := h.parseRankQuery(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if !h.applyViewerScope(c, &query) {
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), DefaultQueryTimeout)
	defer cancel()
	pairs, hasMore, err := h.store.ListRankedPairs(ctx, tn.id, startTime, endTime, query)
	if err != nil {
		if writeContextError(c, err) {
			return
		}
		log.Printf("ERROR GetRankedPairs: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch ranked pairs"})
		return
	}
	if pairs == nil {
		pairs = []database.RankedPair{}
	}
	labelRankedPairs(pairs)
	c.JSON(http.StatusOK, gin.H{
		"pairs":    pairs,
		"metadata": rankMeta(tn.id, startTime, endTime, query, len(pairs), hasMore),
	})
}

func (h *Handlers) parseRankQuery(c *gin.Context) (database.RankQuery, error) {
	query := database.RankQuery{
		Limit: h.parseLimitParam(c, database.RankDefaultLimit, database.RankMaxLimit),
		Sort:  database.RankSortBytes,
	}
	if raw := c.Query("offset"); raw != "" {
		offset, err := strconv.Atoi(raw)
		if err != nil || offset < 0 || offset > database.RankMaxOffset {
			return query, fmt.Errorf("offset must be a non-negative integer no larger than %d", database.RankMaxOffset)
		}
		query.Offset = offset
	}
	switch c.Query("sort") {
	case "", database.RankSortBytes:
	case database.RankSortFlows:
		query.Sort = database.RankSortFlows
	default:
		return query, fmt.Errorf("sort must be bytes or flows")
	}
	trafficTypes, err := parseBandwidthTrafficTypes(c.Query("trafficTypes"))
	if err != nil {
		return query, err
	}
	query.TrafficTypes = trafficTypes
	query.Tag = strings.TrimSpace(c.Query("tag"))
	query.User = strings.TrimSpace(c.Query("user"))
	query.Q = strings.TrimSpace(c.Query("q"))
	if _, err := query.Identity(); err != nil {
		return query, err
	}
	return query, nil
}

func rankMeta(tailnet string, start, end time.Time, query database.RankQuery, count int, hasMore bool) rankMetadata {
	identity, _ := query.Identity()
	return rankMetadata{
		Start:        start,
		End:          end,
		Tailnet:      tailnet,
		Limit:        query.Limit,
		Offset:       query.Offset,
		Count:        count,
		HasMore:      hasMore,
		Sort:         query.Sort,
		TrafficTypes: query.TrafficTypes,
		Tag:          identity.Tag,
		User:         identity.User,
		Q:            identity.Q,
	}
}
