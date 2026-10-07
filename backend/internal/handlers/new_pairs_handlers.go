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

type newPairMetadata struct {
	Start        time.Time `json:"start"`
	End          time.Time `json:"end"`
	Tailnet      string    `json:"tailnet"`
	Lookback     string    `json:"lookback"`
	Limit        int       `json:"limit"`
	Offset       int       `json:"offset"`
	Count        int       `json:"count"`
	HasMore      bool      `json:"hasMore"`
	TrafficTypes []string  `json:"trafficTypes,omitempty"`
	// LookbackStart is start minus the lookback. DataStart is the oldest
	// stored minute. LookbackComplete is false when stored data begins after
	// LookbackStart: a pair seen only before DataStart cannot be ruled out,
	// so some listed pairs may not be new.
	LookbackStart    time.Time  `json:"lookbackStart"`
	DataStart        *time.Time `json:"dataStart,omitempty"`
	LookbackComplete bool       `json:"lookbackComplete"`
}

// GetNewPairs lists src/dst pairs first seen in the selected window.
func (h *Handlers) GetNewPairs(c *gin.Context) {
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
	query, lookbackLabel, err := parseNewPairQuery(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), DefaultQueryTimeout)
	defer cancel()
	pairs, hasMore, err := h.store.ListNewPairs(ctx, tn.id, startTime, endTime, query)
	if err != nil {
		if writeContextError(c, err) {
			return
		}
		log.Printf("ERROR GetNewPairs: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch new pairs"})
		return
	}
	if pairs == nil {
		pairs = []database.NewPair{}
	}
	labelNewPairs(pairs)
	lookbackStart := startTime.Add(-query.Lookback)
	var dataStart *time.Time
	if dataRange, rangeErr := h.store.GetDataRange(ctx, tn.id); rangeErr != nil {
		log.Printf("WARN GetNewPairs data range: %v", rangeErr)
	} else if dataRange != nil && dataRange.Count > 0 {
		earliest := dataRange.Earliest
		dataStart = &earliest
	}
	c.JSON(http.StatusOK, gin.H{
		"pairs": pairs,
		"metadata": newPairMetadata{
			Start:            startTime,
			End:              endTime,
			Tailnet:          tn.id,
			Lookback:         lookbackLabel,
			Limit:            query.Limit,
			Offset:           query.Offset,
			Count:            len(pairs),
			HasMore:          hasMore,
			TrafficTypes:     query.TrafficTypes,
			LookbackStart:    lookbackStart,
			DataStart:        dataStart,
			LookbackComplete: dataStart != nil && !dataStart.After(lookbackStart),
		},
	})
}

func parseNewPairQuery(c *gin.Context) (database.NewPairQuery, string, error) {
	lookback, label, err := parseLookback(c.Query("lookback"))
	if err != nil {
		return database.NewPairQuery{}, "", err
	}
	query := database.NewPairQuery{
		Lookback: lookback,
		Limit:    database.RankDefaultLimit,
	}
	if raw := c.Query("limit"); raw != "" {
		limit, convErr := strconv.Atoi(raw)
		if convErr != nil || limit <= 0 || limit > database.RankMaxLimit {
			return query, "", fmt.Errorf("limit must be a positive integer no larger than %d", database.RankMaxLimit)
		}
		query.Limit = limit
	}
	if raw := c.Query("offset"); raw != "" {
		offset, convErr := strconv.Atoi(raw)
		if convErr != nil || offset < 0 || offset > database.RankMaxOffset {
			return query, "", fmt.Errorf("offset must be a non-negative integer no larger than %d", database.RankMaxOffset)
		}
		query.Offset = offset
	}
	trafficTypes, err := parseBandwidthTrafficTypes(c.Query("trafficTypes"))
	if err != nil {
		return query, "", err
	}
	query.TrafficTypes = trafficTypes
	return query, label, nil
}

func parseLookback(raw string) (time.Duration, string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return database.NewPairDefaultLookback, formatLookback(database.NewPairDefaultLookback), nil
	}
	var lookback time.Duration
	if strings.HasSuffix(raw, "d") {
		days, err := strconv.Atoi(strings.TrimSuffix(raw, "d"))
		if err != nil || days <= 0 {
			return 0, "", fmt.Errorf("lookback must be between %s and %s", database.NewPairMinLookback, database.NewPairMaxLookback)
		}
		lookback = time.Duration(days) * 24 * time.Hour
	} else {
		parsed, err := time.ParseDuration(raw)
		if err != nil {
			return 0, "", fmt.Errorf("lookback must be between %s and %s", database.NewPairMinLookback, database.NewPairMaxLookback)
		}
		lookback = parsed
	}
	if lookback < database.NewPairMinLookback || lookback > database.NewPairMaxLookback {
		return 0, "", fmt.Errorf("lookback must be between %s and %s", database.NewPairMinLookback, database.NewPairMaxLookback)
	}
	return lookback, formatLookback(lookback), nil
}

func formatLookback(lookback time.Duration) string {
	if lookback%(24*time.Hour) == 0 {
		return fmt.Sprintf("%dd", int(lookback/(24*time.Hour)))
	}
	if lookback%time.Hour == 0 {
		return fmt.Sprintf("%dh", int(lookback/time.Hour))
	}
	return lookback.String()
}

func labelNewPairs(pairs []database.NewPair) {
	for i := range pairs {
		if name, ok := derpRelayLabel(pairs[i].SrcNodeID); ok {
			pairs[i].SrcHostname = name
		}
		if name, ok := derpRelayLabel(pairs[i].DstNodeID); ok {
			pairs[i].DstHostname = name
		}
	}
}
