package handlers

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/rajsinghtech/tsflow/backend/internal/database"
	"github.com/rajsinghtech/tsflow/backend/internal/services"
)

const (
	// ChunkThreshold is the duration above which log queries are chunked
	ChunkThreshold = 7 * 24 * time.Hour
	// ChunkSize is the size of each chunk for large log queries
	ChunkSize = 24 * time.Hour
	// MaxParallelChunks limits concurrent chunk fetches
	MaxParallelChunks = 2
	// MaxLogsInMemory limits logs held in memory during chunked queries
	MaxLogsInMemory = 10000
	// MaxLogsInResponse limits logs returned in a single response
	MaxLogsInResponse = 50000
	// MaxBuckets limits the number of time-series buckets returned
	MaxBuckets = 5000
	// MaxAggregates limits the number of aggregated flow entries returned
	MaxAggregates = 10000
	// MinQueryRange prevents degenerate zero-duration queries
	MinQueryRange = time.Second
	// DefaultQueryTimeout is the default timeout for database queries
	DefaultQueryTimeout = 30 * time.Second
	// ShortQueryTimeout is the timeout for quick database queries
	ShortQueryTimeout = 10 * time.Second
	// AggregationQueryTimeout is the timeout for heavy aggregation queries
	AggregationQueryTimeout = 60 * time.Second
)

type Handlers struct {
	tailscaleService *services.TailscaleService
	store            database.Store
	poller           *services.Poller
}

func NewHandlers(tailscaleService *services.TailscaleService, store database.Store, poller *services.Poller) *Handlers {
	return &Handlers{
		tailscaleService: tailscaleService,
		store:            store,
		poller:           poller,
	}
}

func (h *Handlers) HealthCheck(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"status":    "healthy",
		"timestamp": time.Now().UTC(),
		"service":   "tsflow-backend",
	})
}

// parseTimeRange extracts and validates start/end query params.
// Defaults: start = 1 hour ago, end = now.
func (h *Handlers) parseTimeRange(c *gin.Context) (time.Time, time.Time, error) {
	var startTime, endTime time.Time
	var err error

	if s := c.Query("start"); s != "" {
		startTime, err = time.Parse(time.RFC3339, s)
		if err != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("invalid start time: %w", err)
		}
	} else {
		startTime = time.Now().Add(-1 * time.Hour)
	}

	if e := c.Query("end"); e != "" {
		endTime, err = time.Parse(time.RFC3339, e)
		if err != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("invalid end time: %w", err)
		}
	} else {
		endTime = time.Now()
	}

	// Clamp future end times to now — no data can exist beyond the present
	now := time.Now()
	if endTime.After(now) {
		endTime = now
	}

	if endTime.Before(startTime) {
		return time.Time{}, time.Time{}, fmt.Errorf("end time before start time")
	}

	duration := endTime.Sub(startTime)
	if duration < MinQueryRange {
		return time.Time{}, time.Time{}, fmt.Errorf("time range too small, minimum is %s", MinQueryRange)
	}

	const maxQueryRange = 90 * 24 * time.Hour
	if duration > maxQueryRange {
		return time.Time{}, time.Time{}, fmt.Errorf("time range too large, maximum is 90 days")
	}

	return startTime, endTime, nil
}

// parseLimitParam safely parses a "limit" query param with a default and max value.
// Returns defaultLimit if not provided or invalid, clamps to maxLimit.
func (h *Handlers) parseLimitParam(c *gin.Context, defaultLimit, maxLimit int) int {
	l := c.Query("limit")
	if l == "" {
		return defaultLimit
	}
	v, err := strconv.Atoi(l)
	if err != nil || v <= 0 {
		return defaultLimit
	}
	if v > maxLimit {
		return maxLimit
	}
	return v
}

// resolveNodeName returns a human-readable name for a node ID or IP using the device cache.
func (h *Handlers) resolveNodeName(nodeIDOrIP string) string {
	if h.poller == nil {
		return ""
	}
	cache := h.poller.GetDeviceCache()

	// Try by device ID first, then by IP
	var entry *services.DeviceCacheEntry
	if entry = cache.GetDevice(nodeIDOrIP); entry == nil {
		entry = cache.GetDeviceByIP(nodeIDOrIP)
	}
	if entry == nil {
		return ""
	}

	// Prefer hostname but skip generic ones like "localhost"
	if entry.Hostname != "" && entry.Hostname != "localhost" {
		return entry.Hostname
	}
	// Fall back to tailnet name, strip domain suffix for readability
	name := entry.Name
	if idx := strings.Index(name, "."); idx > 0 {
		name = name[:idx]
	}
	return name
}

// resolveNodeID normalizes a node identifier (could be IP or device ID) to a
// consistent device ID. Returns the original value if unresolvable.
func (h *Handlers) resolveNodeID(nodeIDOrIP string) string {
	if h.poller == nil {
		return nodeIDOrIP
	}
	cache := h.poller.GetDeviceCache()

	// Already a device ID?
	if entry := cache.GetDevice(nodeIDOrIP); entry != nil {
		return entry.ID
	}

	// IP address -> device ID
	if entry := cache.GetDeviceByIP(nodeIDOrIP); entry != nil {
		return entry.ID
	}

	return nodeIDOrIP
}

// resolveNodeOwner returns the owner email for a node ID or IP using the device cache.
// Returns an empty string if the node is unknown or has no owner.
func (h *Handlers) resolveNodeOwner(nodeIDOrIP string) string {
	if h.poller == nil {
		return ""
	}
	cache := h.poller.GetDeviceCache()

	var entry *services.DeviceCacheEntry
	if entry = cache.GetDevice(nodeIDOrIP); entry == nil {
		entry = cache.GetDeviceByIP(nodeIDOrIP)
	}
	if entry == nil {
		return ""
	}
	return entry.Owner
}
