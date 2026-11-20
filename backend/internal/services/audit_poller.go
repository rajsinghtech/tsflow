package services

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"log"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/rajsinghtech/tsflow/backend/internal/audit"
	"github.com/rajsinghtech/tsflow/backend/internal/config"
)

const (
	// auditObjectOverlap re-lists objects this far behind the cursor, so an
	// object uploaded late (Tailscale batches every few minutes) is not
	// missed. Objects already read are skipped by key.
	auditObjectOverlap = 15 * time.Minute
	// auditAPIOverlap re-reads this much of the API window. Duplicates are
	// dropped by hash.
	auditAPIOverlap = 10 * time.Minute
	// auditAPIWindow is one API request. The endpoint has no paging, so a
	// day keeps each response small.
	auditAPIWindow = 24 * time.Hour
	// auditAPIWindowsPerPoll caps one poll so a first backfill of 90 days
	// still yields between requests.
	auditAPIWindowsPerPoll = 31
	// auditCleanupInterval is how often old audit rows are deleted.
	auditCleanupInterval = time.Hour
)

// auditFetcher is the part of TailscaleService the API source uses.
type auditFetcher interface {
	GetConfigurationAuditLogs(ctx context.Context, start, end time.Time) ([]byte, error)
}

// AuditPollerStatus is one tailnet's audit ingest state. It holds no
// credentials.
type AuditPollerStatus struct {
	TailnetID       string    `json:"tailnet"`
	Backend         string    `json:"backend"`
	Bucket          string    `json:"bucket,omitempty"`
	Prefix          string    `json:"prefix,omitempty"`
	Running         bool      `json:"running"`
	PollInterval    string    `json:"pollInterval,omitempty"`
	LastPoll        time.Time `json:"lastPoll,omitzero"`
	LastSuccess     time.Time `json:"lastSuccess,omitzero"`
	LastError       string    `json:"lastError,omitempty"`
	LastErrorTime   time.Time `json:"lastErrorTime,omitzero"`
	Polls           int64     `json:"polls"`
	EventsIngested  int64     `json:"eventsIngested"`
	ObjectsIngested int64     `json:"objectsIngested"`
}

// AuditPoller reads one tailnet's configuration audit logs into the audit
// store, from streamed objects (GCS or S3) or from the Tailscale API.
type AuditPoller struct {
	tailnetID  string
	source     config.AuditSource
	store      *audit.Store
	blobs      blobClient
	api        auditFetcher
	interval   time.Duration
	backfill   time.Duration
	maxObjects int
	now        func() time.Time

	mu       sync.Mutex
	status   AuditPollerStatus
	failures map[string]int
	cancel   context.CancelFunc
	done     chan struct{}
}

func newAuditPoller(tailnetID string, src config.AuditSource, store *audit.Store, cfg config.Audit) *AuditPoller {
	p := &AuditPoller{
		tailnetID:  tailnetID,
		source:     src,
		store:      store,
		interval:   cfg.PollInterval,
		backfill:   cfg.Backfill,
		maxObjects: cfg.MaxObjects,
		now:        time.Now,
		failures:   map[string]int{},
	}
	if p.interval <= 0 {
		p.interval = config.DefaultAuditPollInterval
	}
	if p.maxObjects <= 0 {
		p.maxObjects = config.DefaultAuditMaxObjects
	}
	p.status = AuditPollerStatus{TailnetID: tailnetID, Backend: src.Backend, PollInterval: p.interval.String()}
	if src.Backend == config.AuditBackendGCS || src.Backend == config.AuditBackendS3 {
		p.status.Bucket = src.Bucket
		p.status.Prefix = src.Prefix
	}
	return p
}

// Status returns a copy of the poller state.
func (p *AuditPoller) Status() AuditPollerStatus {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.status
}

func (p *AuditPoller) enabled() bool {
	return p.blobs != nil || p.api != nil
}

// Start runs PollOnce now and then every interval until Stop.
func (p *AuditPoller) Start(parent context.Context) {
	if !p.enabled() {
		return
	}
	ctx, cancel := context.WithCancel(parent)
	p.mu.Lock()
	p.cancel = cancel
	p.done = make(chan struct{})
	p.status.Running = true
	done := p.done
	p.mu.Unlock()
	go func() {
		defer close(done)
		ticker := time.NewTicker(p.interval)
		defer ticker.Stop()
		for {
			if err := p.PollOnce(ctx); err != nil && ctx.Err() == nil {
				log.Printf("Warning: audit poll for %s: %v", p.tailnetID, err)
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
}

// Stop stops the loop and waits for it.
func (p *AuditPoller) Stop() {
	p.mu.Lock()
	cancel, done := p.cancel, p.done
	p.status.Running = false
	p.mu.Unlock()
	if cancel != nil {
		cancel()
		<-done
	}
}

// PollOnce reads everything new since the stored cursor.
func (p *AuditPoller) PollOnce(ctx context.Context) error {
	started := p.now()
	var events, objects int
	var err error
	switch {
	case p.blobs != nil:
		events, objects, err = p.pollObjects(ctx)
	case p.api != nil:
		events, err = p.pollAPI(ctx)
	default:
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.status.Polls++
	p.status.LastPoll = started
	p.status.EventsIngested += int64(events)
	p.status.ObjectsIngested += int64(objects)
	if err != nil {
		p.status.LastError = err.Error()
		p.status.LastErrorTime = p.now()
		return err
	}
	p.status.LastSuccess = started
	p.status.LastError = ""
	p.status.LastErrorTime = time.Time{}
	if events > 0 || objects > 0 {
		log.Printf("Audit %s: %d new events from %d objects in %s", p.tailnetID, events, objects, p.now().Sub(started).Round(time.Millisecond))
	}
	return nil
}

func (p *AuditPoller) startCursor(ctx context.Context) (time.Time, error) {
	cursor, err := p.store.Cursor(ctx, p.tailnetID)
	if err != nil {
		return time.Time{}, err
	}
	if cursor.IsZero() {
		cursor = p.now().Add(-p.backfill)
	}
	return cursor, nil
}

type auditObject struct {
	key     string
	size    int64
	logTime time.Time
}

func (p *AuditPoller) listObjects(ctx context.Context, start, end time.Time) ([]auditObject, error) {
	var out []auditObject
	for _, prefix := range dayPrefixes(p.source.Prefix, start, end) {
		listed, err := p.blobs.List(ctx, prefix)
		if err != nil {
			return nil, fmt.Errorf("failed to list %s: %w", prefix, err)
		}
		for _, item := range listed {
			// Streamed objects are named by time. Fall back to the upload
			// time for a key that is not, rather than skipping it.
			logTime, ok := auditObjectTime(item.Key)
			if !ok {
				logTime = item.LastModified.UTC()
			}
			if logTime.Before(start) {
				continue
			}
			out = append(out, auditObject{key: item.Key, size: item.Size, logTime: logTime})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].logTime.Equal(out[j].logTime) {
			return out[i].key < out[j].key
		}
		return out[i].logTime.Before(out[j].logTime)
	})
	return out, nil
}

// auditObjectTime reads the time from a streamed object name. It accepts
// the same names as flow objects plus a .json extension.
func auditObjectTime(key string) (time.Time, bool) {
	for _, ext := range []string{".gz", ".gzip", ".zst", ".zstd"} {
		key = strings.TrimSuffix(key, ext)
	}
	return objectTime(strings.TrimSuffix(key, ".json"))
}

func (p *AuditPoller) pollObjects(ctx context.Context) (int, int, error) {
	cursor, err := p.startCursor(ctx)
	if err != nil {
		return 0, 0, err
	}
	now := p.now().UTC()
	listed, err := p.listObjects(ctx, cursor.Add(-auditObjectOverlap), now)
	if err != nil {
		return 0, 0, err
	}
	var pending []auditObject
	for _, obj := range listed {
		seen, err := p.store.ObjectIngested(ctx, p.tailnetID, obj.key)
		if err != nil {
			return 0, 0, err
		}
		if !seen {
			pending = append(pending, obj)
		}
	}
	capped := len(pending) > p.maxObjects
	if capped {
		pending = pending[:p.maxObjects]
	}
	newCursor := now
	if capped && len(pending) > 0 {
		newCursor = pending[len(pending)-1].logTime
	}
	var firstErr error
	events, objects := 0, 0
	for _, obj := range pending {
		parsed, badLines, err := p.readObject(ctx, obj.key)
		if err != nil {
			if ctx.Err() != nil {
				return events, objects, ctx.Err()
			}
			if permanentReadError(err) {
				p.failures[obj.key]++
				if p.failures[obj.key] >= maxObjectReadAttempts {
					log.Printf("Warning: giving up on audit object %s after %d failed reads: %v", obj.key, p.failures[obj.key], err)
					if _, err := p.store.Insert(ctx, p.tailnetID, obj.key, nil, &audit.ObjectRecord{Key: obj.key, Size: obj.size, LogTime: obj.logTime}); err != nil {
						return events, objects, err
					}
					delete(p.failures, obj.key)
					continue
				}
			}
			if firstErr == nil {
				firstErr = fmt.Errorf("failed to read %s: %w", obj.key, err)
			}
			if obj.logTime.Before(newCursor) {
				newCursor = obj.logTime
			}
			continue
		}
		delete(p.failures, obj.key)
		if badLines > 0 {
			log.Printf("Warning: audit object %s had %d lines that were not configuration records", obj.key, badLines)
		}
		n, err := p.store.Insert(ctx, p.tailnetID, obj.key, parsed, &audit.ObjectRecord{Key: obj.key, Size: obj.size, LogTime: obj.logTime})
		if err != nil {
			return events, objects, err
		}
		events += n
		objects++
	}
	if newCursor.After(cursor) || cursor.IsZero() {
		if err := p.store.SetCursor(ctx, p.tailnetID, newCursor); err != nil {
			return events, objects, err
		}
	}
	return events, objects, firstErr
}

func (p *AuditPoller) readObject(ctx context.Context, key string) ([]audit.Event, int, error) {
	body, err := p.blobs.Open(ctx, key)
	if err != nil {
		return nil, 0, err
	}
	defer body.Close()
	reader, closeReader, err := decodeObjectBody(ctx, key, body)
	if err != nil {
		return nil, 0, err
	}
	defer closeReader()
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 0, 64*1024), 32*1024*1024)
	var out []audit.Event
	bad := 0
	for scanner.Scan() {
		events, err := audit.ParseLine(scanner.Bytes())
		if err != nil {
			bad++
			continue
		}
		out = append(out, events...)
	}
	if err := scanner.Err(); err != nil {
		if ctx.Err() != nil {
			return nil, 0, ctx.Err()
		}
		return nil, 0, objectContentError{err}
	}
	return out, bad, nil
}

func (p *AuditPoller) pollAPI(ctx context.Context) (int, error) {
	cursor, err := p.startCursor(ctx)
	if err != nil {
		return 0, err
	}
	now := p.now().UTC()
	start := cursor.Add(-auditAPIOverlap)
	events := 0
	for i := 0; i < auditAPIWindowsPerPoll && start.Before(now); i++ {
		end := start.Add(auditAPIWindow)
		if end.After(now) {
			end = now
		}
		body, err := p.api.GetConfigurationAuditLogs(ctx, start, end)
		if err != nil {
			return events, fmt.Errorf("configuration logs %s to %s: %w", start.Format(time.RFC3339), end.Format(time.RFC3339), err)
		}
		parsed, err := audit.ParseLine(body)
		if err != nil {
			return events, fmt.Errorf("configuration logs response: %w", err)
		}
		n, err := p.store.Insert(ctx, p.tailnetID, "api", parsed, nil)
		if err != nil {
			return events, err
		}
		events += n
		if err := p.store.SetCursor(ctx, p.tailnetID, end); err != nil {
			return events, err
		}
		start = end
	}
	return events, nil
}

// AuditManager owns the audit store and one poller per tailnet.
type AuditManager struct {
	store     *audit.Store
	retention time.Duration
	order     []string
	pollers   map[string]*AuditPoller
	now       func() time.Time

	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// NewAuditManager builds a poller for each tailnet. A tailnet whose source
// resolves to off gets a poller that never runs, so status still lists it.
func NewAuditManager(ctx context.Context, cfg config.Audit, specs []config.TailnetSpec, reg *Registry, store *audit.Store) (*AuditManager, error) {
	if store == nil {
		return nil, errors.New("audit store is required")
	}
	m := &AuditManager{store: store, retention: cfg.Retention, pollers: map[string]*AuditPoller{}, now: time.Now}
	only := len(specs) == 1
	for _, spec := range specs {
		src := spec.ResolveAudit(cfg.Default, only)
		p := newAuditPoller(spec.ID, src, store, cfg)
		switch src.Backend {
		case config.AuditBackendGCS, config.AuditBackendS3:
			if src.Bucket == "" {
				return nil, fmt.Errorf("tailnet %q audit: bucket is required for backend %s", spec.ID, src.Backend)
			}
			auth := src.Auth
			if src.Backend == config.AuditBackendGCS {
				auth = config.ObjectStoreAuthGCSADC
			}
			source, err := NewObjectStoreSource(ctx, ObjectStoreConfig{
				Bucket:               src.Bucket,
				Prefix:               src.Prefix,
				Endpoint:             src.Endpoint,
				Region:               src.Region,
				AuthMode:             auth,
				RoleARN:              src.RoleARN,
				WebIdentityTokenFile: src.WebIdentityTokenFile,
				MaxObjects:           cfg.MaxObjects,
			})
			if err != nil {
				return nil, fmt.Errorf("tailnet %q audit object store: %w", spec.ID, err)
			}
			p.blobs = source.blobs
		case config.AuditBackendAPI:
			entry, ok := reg.Get(spec.ID)
			if !ok || entry.Service == nil || !entry.Service.HasCredentials() {
				return nil, fmt.Errorf("tailnet %q audit: backend api needs API credentials with logs:configuration:read", spec.ID)
			}
			p.api = entry.Service
		}
		m.order = append(m.order, spec.ID)
		m.pollers[spec.ID] = p
	}
	return m, nil
}

// Store is the audit database.
func (m *AuditManager) Store() *audit.Store {
	if m == nil {
		return nil
	}
	return m.store
}

// Retention is how long audit events are kept.
func (m *AuditManager) Retention() time.Duration {
	if m == nil {
		return 0
	}
	return m.retention
}

// Status returns the poller state for one tailnet.
func (m *AuditManager) Status(tailnetID string) (AuditPollerStatus, bool) {
	if m == nil {
		return AuditPollerStatus{}, false
	}
	p, ok := m.pollers[tailnetID]
	if !ok {
		return AuditPollerStatus{}, false
	}
	return p.Status(), true
}

// TailnetIDs lists tailnets in configuration order.
func (m *AuditManager) TailnetIDs() []string {
	if m == nil {
		return nil
	}
	return append([]string(nil), m.order...)
}

// Start starts every enabled poller and the retention loop.
func (m *AuditManager) Start(parent context.Context) {
	ctx, cancel := context.WithCancel(parent)
	m.cancel = cancel
	for _, id := range m.order {
		m.pollers[id].Start(ctx)
	}
	if m.retention <= 0 {
		return
	}
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		ticker := time.NewTicker(auditCleanupInterval)
		defer ticker.Stop()
		for {
			m.cleanup(ctx)
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
}

func (m *AuditManager) cleanup(ctx context.Context) {
	n, err := m.store.Cleanup(ctx, m.now().Add(-m.retention))
	if err != nil {
		if ctx.Err() == nil {
			log.Printf("Warning: audit cleanup: %v", err)
		}
		return
	}
	if n > 0 {
		log.Printf("Audit cleanup removed %d events older than %s", n, m.retention)
	}
}

// Stop stops every poller and the retention loop.
func (m *AuditManager) Stop() {
	if m == nil {
		return
	}
	for _, id := range m.order {
		m.pollers[id].Stop()
	}
	if m.cancel != nil {
		m.cancel()
	}
	m.wg.Wait()
}
