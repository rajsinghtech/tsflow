package access

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/rajsinghtech/tsflow/backend/internal/config"
	"tailscale.com/client/local"
	"tailscale.com/client/tailscale/apitype"
	"tailscale.com/paths"
)

// LazyWhoIs waits until the tsnet server has a LocalClient.
type LazyWhoIs struct {
	mu     sync.RWMutex
	client WhoIsClient
}

func NewLazyWhoIs() *LazyWhoIs {
	return &LazyWhoIs{}
}

func (l *LazyWhoIs) Set(client WhoIsClient) {
	if l == nil {
		return
	}
	l.mu.Lock()
	l.client = client
	l.mu.Unlock()
}

func (l *LazyWhoIs) WhoIs(ctx context.Context, remoteAddr string) (*apitype.WhoIsResponse, error) {
	if l == nil {
		return nil, errors.New("tailscale identity is not ready")
	}
	l.mu.RLock()
	client := l.client
	l.mu.RUnlock()
	if client == nil {
		return nil, errors.New("tailscale identity is not ready")
	}
	return client.WhoIs(ctx, remoteAddr)
}

// OpenLocalWhoIs dials the local tailscaled socket when header mode
// asked for it. A nil client means header identity should be trusted
// instead. require fails startup when the socket is unreachable.
func OpenLocalWhoIs(cfg config.Access) (*local.Client, error) {
	if !cfg.Enabled || cfg.Mode != config.AccessModeHeader || cfg.LocalWhoIs == "off" {
		return nil, nil
	}
	socket := cfg.TailscaledSocket
	if socket == "" {
		socket = paths.DefaultTailscaledSocket()
	}
	client, err := probeLocalWhoIs(socket, 400*time.Millisecond)
	if err == nil {
		return client, nil
	}
	if cfg.LocalWhoIs == "require" || cfg.TailscaledSocket != "" {
		return nil, fmt.Errorf("local tailscaled socket %s: %w", socket, err)
	}
	return nil, nil
}

func probeLocalWhoIs(socket string, timeout time.Duration) (*local.Client, error) {
	client := &local.Client{Socket: socket, UseSocketOnly: true}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	if _, err := client.StatusWithoutPeers(ctx); err != nil {
		return nil, err
	}
	return client, nil
}
