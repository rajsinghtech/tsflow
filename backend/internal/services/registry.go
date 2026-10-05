package services

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/rajsinghtech/tsflow/backend/internal/config"
	"github.com/rajsinghtech/tsflow/backend/internal/database"
)

// TailnetRuntime is the API client and poller for one configured tailnet.
type TailnetRuntime struct {
	ID      string
	Name    string
	Service *TailscaleService
	Poller  *Poller
}

// Registry holds one service and one poller per tailnet. Each poller runs in
// its own goroutine and writes only its tailnet id, so one tailnet's API
// failure or delay does not block the others.
type Registry struct {
	mu      sync.RWMutex
	order   []string
	byID    map[string]*TailnetRuntime
	started bool
}

// PollerConfigFrom builds the process-wide poller settings from config.
// Per-tailnet id and object prefix are applied later by NewRegistry.
func PollerConfigFrom(cfg *config.Config) (PollerConfig, error) {
	if cfg == nil {
		return PollerConfig{}, fmt.Errorf("config is nil")
	}
	pc := DefaultPollerConfig()
	var err error
	if pc.PollInterval, err = time.ParseDuration(cfg.PollInterval); err != nil {
		return PollerConfig{}, fmt.Errorf("TSFLOW_POLL_INTERVAL: %w", err)
	}
	if pc.InitialBackfill, err = time.ParseDuration(cfg.InitialBackfill); err != nil {
		return PollerConfig{}, fmt.Errorf("TSFLOW_INITIAL_BACKFILL: %w", err)
	}
	if cfg.Retention != "" {
		if pc.Retention, err = time.ParseDuration(cfg.Retention); err != nil {
			return PollerConfig{}, fmt.Errorf("TSFLOW_RETENTION: %w", err)
		}
	}
	pc.FlowBackend = cfg.FlowBackend
	if pc.FlowBackend == "" {
		pc.FlowBackend = "api"
		if cfg.FlowObjectStoreEndpoint != "" && cfg.FlowObjectStoreAccessKey != "" && cfg.FlowObjectStoreSecretKey != "" {
			pc.FlowBackend = "s3"
		}
	}
	if pc.FlowBackend == "s3" && cfg.Retention == "" {
		pc.Retention = 0
	}
	// Match the historical startup path: a lookback that is not used by the
	// API backend is ignored when it does not parse. S3 mode is rejected
	// earlier by config validation.
	lookback, _ := time.ParseDuration(cfg.FlowObjectStoreLookback)
	pc.ObjectStore = ObjectStoreConfig{
		Bucket:       cfg.FlowObjectStoreBucket,
		Prefix:       cfg.FlowObjectStorePrefix,
		Endpoint:     cfg.FlowObjectStoreEndpoint,
		Region:       cfg.FlowObjectStoreRegion,
		AccessKey:    cfg.FlowObjectStoreAccessKey,
		SecretKey:    cfg.FlowObjectStoreSecretKey,
		UsePathStyle: cfg.FlowObjectStorePathStyle,
		Lookback:     lookback,
		MaxObjects:   cfg.FlowObjectStoreMaxObjects,
	}
	return pc, nil
}

// NewRegistry builds one Tailscale service and one poller per spec. The
// pollers share store and stamp their own tailnet id on every write.
func NewRegistry(ctx context.Context, specs []config.TailnetSpec, store database.Store, base PollerConfig) (*Registry, error) {
	if len(specs) == 0 {
		return nil, fmt.Errorf("at least one tailnet is required")
	}
	reg := &Registry{
		order: make([]string, 0, len(specs)),
		byID:  make(map[string]*TailnetRuntime, len(specs)),
	}
	for _, spec := range specs {
		if spec.ID == "" {
			return nil, fmt.Errorf("tailnet id is required")
		}
		if _, exists := reg.byID[spec.ID]; exists {
			return nil, fmt.Errorf("duplicate tailnet id %q", spec.ID)
		}
		pollerCfg := base
		pollerCfg.TailnetID = spec.ID
		if spec.S3Prefix != "" {
			pollerCfg.ObjectStore.Prefix = spec.S3Prefix
		}
		service := NewTailscaleService(spec.ServiceConfig(nil))
		if service.baseURL == "" {
			return nil, fmt.Errorf("tailnet %q is missing an API URL", spec.ID)
		}
		poller := NewPoller(service, store, pollerCfg)
		if pollerCfg.FlowBackend == "s3" {
			source, err := NewObjectStoreSource(ctx, pollerCfg.ObjectStore)
			if err != nil {
				return nil, fmt.Errorf("tailnet %q object store: %w", spec.ID, err)
			}
			poller.ConfigureObjectStore(source)
		}
		reg.order = append(reg.order, spec.ID)
		reg.byID[spec.ID] = &TailnetRuntime{
			ID:      spec.ID,
			Name:    spec.Name,
			Service: service,
			Poller:  poller,
		}
	}
	return reg, nil
}

// Get returns the runtime for id.
func (r *Registry) Get(id string) (*TailnetRuntime, bool) {
	if r == nil {
		return nil, false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	entry, ok := r.byID[id]
	return entry, ok
}

// Default returns the runtime for the default tailnet id.
func (r *Registry) Default() (*TailnetRuntime, bool) {
	return r.Get(database.DefaultTailnetID)
}

// List returns the configured tailnets in file order.
func (r *Registry) List() []*TailnetRuntime {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*TailnetRuntime, 0, len(r.order))
	for _, id := range r.order {
		out = append(out, r.byID[id])
	}
	return out
}

// Start starts every poller. Each poller returns as soon as its loop is
// running, so a slow or unauthorized tailnet does not delay the others.
func (r *Registry) Start(ctx context.Context) error {
	if r == nil {
		return fmt.Errorf("tailnet registry is nil")
	}
	r.mu.Lock()
	r.started = true
	entries := make([]*TailnetRuntime, 0, len(r.order))
	for _, id := range r.order {
		entries = append(entries, r.byID[id])
	}
	r.mu.Unlock()

	var errs []error
	for _, entry := range entries {
		if err := entry.Poller.Start(ctx); err != nil {
			errs = append(errs, fmt.Errorf("tailnet %q: %w", entry.ID, err))
		}
	}
	if len(errs) == 0 {
		return nil
	}
	if len(errs) == 1 {
		return errs[0]
	}
	return fmt.Errorf("failed to start %d tailnet pollers: %v", len(errs), errs)
}

// Stop stops every poller. Stop cancels that poller's own work and waits for
// it, so one tailnet cannot keep another poller's loop running.
func (r *Registry) Stop() {
	if r == nil {
		return
	}
	for _, entry := range r.List() {
		if entry.Poller != nil {
			entry.Poller.Stop()
		}
	}
}

// LogConfigured writes the tailnet ids and names without credentials.
func (r *Registry) LogConfigured() {
	for _, entry := range r.List() {
		log.Printf("Tailnet %s (%s)", entry.ID, entry.Name)
	}
}
