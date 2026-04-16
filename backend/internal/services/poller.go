package services

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/rajsinghtech/tsflow/backend/internal/database"
)

// PollerConfig holds configuration for the background poller
type PollerConfig struct {
	// PollInterval is how often to poll for new logs
	PollInterval time.Duration
	// InitialBackfill is how far back to fetch on first run
	InitialBackfill time.Duration
	// Retention is how long to keep flow data
	Retention time.Duration
	// CleanupInterval is how often to run cleanup
	CleanupInterval time.Duration
	// DeviceCacheRefresh is how often to refresh device cache
	DeviceCacheRefresh time.Duration
}

// DefaultPollerConfig returns sensible defaults
func DefaultPollerConfig() PollerConfig {
	return PollerConfig{
		PollInterval:       5 * time.Minute,
		InitialBackfill:    6 * time.Hour,
		Retention:          30 * 24 * time.Hour,
		CleanupInterval:    1 * time.Hour,
		DeviceCacheRefresh: 5 * time.Minute,
	}
}

// Poller fetches network logs from Tailscale API and stores them in the database
type Poller struct {
	tsService    *TailscaleService
	store        database.Store
	config       PollerConfig
	deviceCache  *DeviceCache
	rollingCache *RollingWindowCache

	mu          sync.RWMutex
	running     bool
	stopChan    chan struct{}
	doneChan    chan struct{}
	triggerChan chan struct{}

	// Stats
	lastPollTime       time.Time
	lastPollCount      int
	totalPolled        int64
	pollErrors         int64
	lastPollError      string
	lastPollErrorTime  time.Time
	cacheRefreshErrors int64
}

// NewPoller creates a new background poller
func NewPoller(tsService *TailscaleService, store database.Store, config PollerConfig) *Poller {
	return &Poller{
		tsService:    tsService,
		store:        store,
		config:       config,
		deviceCache:  NewDeviceCache(),
		rollingCache: NewRollingWindowCache(time.Hour), // Keep 1 hour in memory
		stopChan:     make(chan struct{}),
		doneChan:     make(chan struct{}),
		triggerChan:  make(chan struct{}, 1),
	}
}

// Start begins the background polling loop
func (p *Poller) Start(ctx context.Context) error {
	p.mu.Lock()
	if p.running {
		p.mu.Unlock()
		return nil
	}
	p.running = true
	// Re-initialize channels for restart support (they're nil after Stop())
	p.stopChan = make(chan struct{})
	p.doneChan = make(chan struct{})
	p.triggerChan = make(chan struct{}, 1)
	p.mu.Unlock()

	log.Printf("Starting background poller (interval: %v, retention: %v)",
		p.config.PollInterval, p.config.Retention)

	// Initial device cache refresh
	if err := p.refreshDeviceCache(ctx); err != nil {
		log.Printf("Warning: initial device cache refresh failed: %v", err)
	}

	// Start background goroutine (initial poll happens asynchronously
	// so the server can start accepting requests immediately)
	go p.run(ctx)

	return nil
}

// Stop stops the background poller
func (p *Poller) Stop() {
	p.mu.Lock()
	if !p.running {
		p.mu.Unlock()
		return
	}
	p.running = false
	stopChan := p.stopChan
	doneChan := p.doneChan
	p.stopChan = nil
	p.mu.Unlock()

	if stopChan != nil {
		close(stopChan)
	}
	if doneChan != nil {
		<-doneChan
	}
	log.Println("Background poller stopped")
}

// Stats returns current poller statistics
func (p *Poller) Stats() map[string]any {
	p.mu.RLock()
	defer p.mu.RUnlock()

	stats := map[string]any{
		"running":       p.running,
		"lastPollTime":  p.lastPollTime,
		"lastPollCount": p.lastPollCount,
		"totalPolled":   p.totalPolled,
		"pollErrors":    p.pollErrors,
		"pollInterval":  p.config.PollInterval.String(),
	}
	if p.lastPollError != "" {
		stats["lastError"] = p.lastPollError
		stats["lastErrorTime"] = p.lastPollErrorTime
	}
	return stats
}

// GetDeviceCache returns the device cache for external use
func (p *Poller) GetDeviceCache() *DeviceCache {
	return p.deviceCache
}

// GetRollingCache returns the rolling window cache for fast recent data access
func (p *Poller) GetRollingCache() *RollingWindowCache {
	return p.rollingCache
}

// TriggerPoll signals the background loop to poll immediately.
// Non-blocking: if a trigger is already pending, this is a no-op.
func (p *Poller) TriggerPoll() {
	select {
	case p.triggerChan <- struct{}{}:
	default:
	}
}

func (p *Poller) run(ctx context.Context) {
	// Capture channels at start to avoid race with Stop() setting them to nil
	p.mu.RLock()
	stopChan := p.stopChan
	doneChan := p.doneChan
	p.mu.RUnlock()

	defer close(doneChan)

	// Run cleanup before initial poll to purge any stale raw flow logs from prior runs
	if err := p.cleanup(ctx); err != nil {
		log.Printf("Pre-poll cleanup failed: %v", err)
	}

	// Initial poll (runs asynchronously so the server isn't blocked)
	if err := p.poll(ctx); err != nil {
		log.Printf("Initial poll failed: %v", err)
		p.mu.Lock()
		p.pollErrors++
		p.lastPollError = err.Error()
		p.lastPollErrorTime = time.Now()
		p.mu.Unlock()
	}

	pollTicker := time.NewTicker(p.config.PollInterval)
	defer pollTicker.Stop()

	cleanupTicker := time.NewTicker(p.config.CleanupInterval)
	defer cleanupTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-stopChan:
			return
		case <-p.triggerChan:
			// Manual trigger — same logic as scheduled poll
			if p.deviceCache.NeedsRefresh(p.config.DeviceCacheRefresh) {
				if err := p.refreshDeviceCache(ctx); err != nil {
					log.Printf("Warning: device cache refresh failed: %v", err)
				}
			}
			if err := p.poll(ctx); err != nil {
				log.Printf("Triggered poll failed: %v", err)
				p.mu.Lock()
				p.pollErrors++
				p.lastPollError = err.Error()
				p.lastPollErrorTime = time.Now()
				p.mu.Unlock()
			}
		case <-pollTicker.C:
			// Refresh device cache if stale
			if p.deviceCache.NeedsRefresh(p.config.DeviceCacheRefresh) {
				if err := p.refreshDeviceCache(ctx); err != nil {
					log.Printf("Warning: device cache refresh failed: %v", err)
					p.mu.Lock()
					p.cacheRefreshErrors++
					p.mu.Unlock()
				}
			}

			if err := p.poll(ctx); err != nil {
				log.Printf("Poll failed: %v", err)
				p.mu.Lock()
				p.pollErrors++
				p.lastPollError = err.Error()
				p.lastPollErrorTime = time.Now()
				p.mu.Unlock()
			}
		case <-cleanupTicker.C:
			if err := p.cleanup(ctx); err != nil {
				log.Printf("Cleanup failed: %v", err)
			}
		}
	}
}

func (p *Poller) refreshDeviceCache(_ context.Context) error {
	devicesResp, err := p.tsService.GetDevices()
	if err != nil {
		return err
	}
	p.deviceCache.Update(devicesResp.Devices)
	log.Printf("Device cache refreshed: %d devices", len(devicesResp.Devices))
	return nil
}

// maxPollChunk is the maximum time range for a single API call.
// Larger ranges are split into sequential chunks to avoid HTTP timeouts.
const maxPollChunk = 30 * time.Minute

func (p *Poller) poll(ctx context.Context) error {
	// Get last poll state
	pollState, err := p.store.GetPollState(ctx)
	if err != nil {
		return err
	}

	var start time.Time
	end := time.Now()

	if pollState.LastPollEnd.IsZero() {
		// First poll - backfill
		start = end.Add(-p.config.InitialBackfill)
		log.Printf("First poll, backfilling from %v", start)
	} else {
		// Continue from exactly where we left off - no overlap
		start = pollState.LastPollEnd
	}

	// If the time range is larger than maxPollChunk, split into chunks
	// to avoid HTTP timeouts on large API responses.
	if end.Sub(start) > maxPollChunk {
		return p.pollChunked(ctx, start, end)
	}

	return p.pollRange(ctx, start, end)
}

// pollChunked splits a large time range into sequential chunks, committing
// each chunk independently so that progress is saved on partial failure.
func (p *Poller) pollChunked(ctx context.Context, start, end time.Time) error {
	totalRange := end.Sub(start)
	numChunks := int(totalRange/maxPollChunk) + 1
	log.Printf("Large time range (%v), splitting into %d chunks of %v", totalRange.Round(time.Second), numChunks, maxPollChunk)

	cursor := start
	chunksCompleted := 0
	for cursor.Before(end) {
		chunkEnd := cursor.Add(maxPollChunk)
		if chunkEnd.After(end) {
			chunkEnd = end
		}

		if err := p.pollRange(ctx, cursor, chunkEnd); err != nil {
			log.Printf("Chunk %d/%d failed (%v to %v): %v — stopping backfill, will resume next poll",
				chunksCompleted+1, numChunks,
				cursor.Format(time.RFC3339), chunkEnd.Format(time.RFC3339), err)
			// Return nil so the poller doesn't count this as a full failure;
			// progress up to the last successful chunk is already committed.
			return nil
		}

		chunksCompleted++
		cursor = chunkEnd

		// Check for cancellation between chunks
		select {
		case <-ctx.Done():
			log.Printf("Backfill interrupted after %d/%d chunks", chunksCompleted, numChunks)
			return nil
		default:
		}
	}

	log.Printf("Backfill complete: %d chunks processed", chunksCompleted)
	return nil
}

// pollRange fetches and commits logs for a single time range.
func (p *Poller) pollRange(ctx context.Context, start, end time.Time) error {
	// Fetch logs from Tailscale API
	logsResp, err := p.tsService.GetNetworkLogs(
		start.Format(time.RFC3339),
		end.Format(time.RFC3339),
	)
	if err != nil {
		return err
	}

	// Convert to flow logs
	flowLogs := p.convertLogs(logsResp)
	if len(flowLogs) == 0 {
		// Update poll state even with no logs
		return p.store.UpdatePollState(ctx, end)
	}

	// Pre-aggregate at poll time: node pairs, bandwidth, and traffic stats
	nodePairs, totalBandwidth, nodeBandwidth, trafficStats := p.aggregate(flowLogs)

	// Atomically commit all aggregates + poll state in a single transaction.
	if err := p.store.CommitPollResults(ctx, database.PollResults{
		NodePairs:     nodePairs,
		Bandwidth:     totalBandwidth,
		NodeBandwidth: nodeBandwidth,
		TrafficStats:  trafficStats,
		PollEnd:       end,
	}); err != nil {
		return fmt.Errorf("failed to commit poll results: %w", err)
	}

	// Update rolling cache for fast live view queries
	p.rollingCache.Update(nodePairs, totalBandwidth, nodeBandwidth, trafficStats)

	p.mu.Lock()
	p.lastPollTime = time.Now()
	p.lastPollCount = len(flowLogs)
	p.totalPolled += int64(len(flowLogs))
	p.mu.Unlock()

	if len(flowLogs) > 0 {
		log.Printf("Polled %d flow logs, aggregated %d node pairs, %d bandwidth buckets (%v to %v)",
			len(flowLogs), len(nodePairs), len(totalBandwidth),
			start.Format(time.RFC3339), end.Format(time.RFC3339))
	}

	return nil
}

func (p *Poller) cleanup(ctx context.Context) error {
	deleted, err := p.store.Cleanup(ctx, p.config.Retention)
	if err != nil {
		return err
	}
	if deleted > 0 {
		log.Printf("Cleaned up %d old records", deleted)
	}
	return nil
}
