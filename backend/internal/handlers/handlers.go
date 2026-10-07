package handlers

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/rajsinghtech/tsflow/backend/internal/database"
	"github.com/rajsinghtech/tsflow/backend/internal/services"
)

// writeContextError converts request cancellation and query deadlines into
// transport-level statuses consistently across handlers. A canceled request
// must not be reported as a successful empty response.
func writeContextError(c *gin.Context, err error) bool {
	requestErr := c.Request.Context().Err()
	if errors.Is(err, context.Canceled) || errors.Is(requestErr, context.Canceled) {
		c.Status(499)
		return true
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(requestErr, context.DeadlineExceeded) {
		c.Status(http.StatusGatewayTimeout)
		return true
	}
	return false
}

const (
	// ChunkThreshold is the duration above which log queries are chunked
	ChunkThreshold = 7 * 24 * time.Hour
	// ChunkSize is the size of each chunk for large log queries
	ChunkSize = 24 * time.Hour
	// MaxParallelChunks limits concurrent chunk fetches
	MaxParallelChunks = 2
	// MaxLogsInMemory limits logs held in memory during chunked queries. Keep it
	// above the response cap so the sampling path remains reachable.
	MaxLogsInMemory = 100000
	// MaxLogsInResponse limits logs returned in a single response
	MaxLogsInResponse = 50000
	// MaxBuckets limits the number of time-series buckets returned
	MaxBuckets = 5000
	// MaxNetworkLogWindow is the longest raw /api/network-logs read.
	// Longer ranges return 410 and point at the aggregated flow endpoint.
	// One hour of raw logs is already large enough to exhaust a gateway timeout.
	MaxNetworkLogWindow = 30 * time.Minute
	// DefaultNetworkLogLimit is applied when /api/network-logs has no limit.
	DefaultNetworkLogLimit = 1000
	// MaxNetworkLogLimit is the largest limit /api/network-logs will honor.
	MaxNetworkLogLimit = 5000
	derpRelayIP        = "127.3.3.40"
	derpRelayName      = "DERP relay"
	exitInternetName   = "Internet (exit node)"
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
	registry         *services.Registry
	startTime        time.Time
	version          string
}

func NewHandlers(tailscaleService *services.TailscaleService, store database.Store, poller *services.Poller, version string) *Handlers {
	return &Handlers{
		tailscaleService: tailscaleService,
		store:            store,
		poller:           poller,
		startTime:        time.Now(),
		version:          version,
	}
}

// UseRegistry routes data requests through the tailnet registry. The service
// and poller fields stay as the fallback for callers that do not set one.
func (h *Handlers) UseRegistry(registry *services.Registry) {
	if h == nil {
		return
	}
	h.registry = registry
}

func (h *Handlers) HealthCheck(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"status":    "healthy",
		"timestamp": time.Now().UTC(),
		"startTime": h.startTime.UTC(),
		"uptime":    time.Since(h.startTime).Seconds(),
		"version":   h.version,
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

// derpRelayLabel names Tailscale's DERP pseudo-address. The port on that
// address is a DERP region, not a device.
func derpRelayLabel(nodeIDOrIP string) (string, bool) {
	if nodeIDOrIP == derpRelayIP {
		return derpRelayName, true
	}
	host, _, err := net.SplitHostPort(nodeIDOrIP)
	if err == nil && host == derpRelayIP {
		return derpRelayName, true
	}
	return "", false
}

// PseudoEndpointName labels endpoints that are not devices: the DERP
// pseudo-address and the public side of anonymized exit traffic.
func PseudoEndpointName(nodeIDOrIP string) (string, bool) {
	return pseudoEndpointLabel(nodeIDOrIP)
}

func pseudoEndpointLabel(nodeIDOrIP string) (string, bool) {
	if name, ok := derpRelayLabel(nodeIDOrIP); ok {
		return name, true
	}
	if nodeIDOrIP == services.ExitInternetEndpoint {
		return exitInternetName, true
	}
	return "", false
}

func labelRankedTalkers(talkers []database.RankedTalker) {
	for i := range talkers {
		if name, ok := pseudoEndpointLabel(talkers[i].NodeID); ok {
			talkers[i].Hostname = name
		}
	}
}

func labelRankedPairs(pairs []database.RankedPair) {
	for i := range pairs {
		if name, ok := pseudoEndpointLabel(pairs[i].SrcNodeID); ok {
			pairs[i].SrcHostname = name
		}
		if name, ok := pseudoEndpointLabel(pairs[i].DstNodeID); ok {
			pairs[i].DstHostname = name
		}
	}
}

// resolveNodeName returns a human-readable name for a node ID or IP using the device cache.
func (h *Handlers) resolveNodeName(poller *services.Poller, nodeIDOrIP string) string {
	if name, ok := pseudoEndpointLabel(nodeIDOrIP); ok {
		return name
	}
	if poller == nil {
		return ""
	}
	cache := poller.GetDeviceCache()

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
func (h *Handlers) resolveNodeID(poller *services.Poller, nodeIDOrIP string) string {
	if poller == nil {
		return nodeIDOrIP
	}
	cache := poller.GetDeviceCache()

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
func (h *Handlers) resolveNodeOwner(poller *services.Poller, nodeIDOrIP string) string {
	if poller == nil {
		return ""
	}
	cache := poller.GetDeviceCache()

	var entry *services.DeviceCacheEntry
	if entry = cache.GetDevice(nodeIDOrIP); entry == nil {
		entry = cache.GetDeviceByIP(nodeIDOrIP)
	}
	if entry == nil {
		return ""
	}
	return entry.Owner
}

// ResolveNodeName returns the display name used by the stats routes.
func (h *Handlers) ResolveNodeName(poller *services.Poller, nodeIDOrIP string) string {
	return h.resolveNodeName(poller, nodeIDOrIP)
}

// ResolveNodeID returns the canonical device id for a stored id or address.
func (h *Handlers) ResolveNodeID(poller *services.Poller, nodeIDOrIP string) string {
	return h.resolveNodeID(poller, nodeIDOrIP)
}

// ResolveNodeOwner returns the device owner login, or empty when unknown.
func (h *Handlers) ResolveNodeOwner(poller *services.Poller, nodeIDOrIP string) string {
	return h.resolveNodeOwner(poller, nodeIDOrIP)
}

// Store returns the flow store. It is nil when the process has no database.
func (h *Handlers) Store() database.Store {
	if h == nil {
		return nil
	}
	return h.store
}
