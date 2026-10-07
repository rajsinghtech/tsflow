package access

import (
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/rajsinghtech/tsflow/backend/internal/config"
)

const (
	// denyLogWindow is how often one peer and reason pair is logged
	// when debug logging is off.
	denyLogWindow = time.Minute
	// denyLogMaxKeys bounds the limiter so a scan from many peers
	// cannot grow it without limit.
	denyLogMaxKeys = 1024
	// denyLogMaxField bounds forwarded header values in the log line.
	denyLogMaxField = 256
)

// denyLimiter allows one log line per key per window.
type denyLimiter struct {
	mu     sync.Mutex
	window time.Duration
	now    func() time.Time
	last   map[string]time.Time
}

func newDenyLimiter(window time.Duration) *denyLimiter {
	return &denyLimiter{window: window, now: time.Now, last: make(map[string]time.Time)}
}

func (l *denyLimiter) allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	if last, ok := l.last[key]; ok && now.Sub(last) < l.window {
		return false
	}
	if len(l.last) >= denyLogMaxKeys {
		for k, t := range l.last {
			if now.Sub(t) >= l.window {
				delete(l.last, k)
			}
		}
		if len(l.last) >= denyLogMaxKeys {
			return false
		}
	}
	l.last[key] = now
	return true
}

// logDenied records why a request was refused and which TCP peer sent
// it. The peer is r.RemoteAddr, the address trust decisions use. It is
// not the client IP a proxy reports in X-Forwarded-For, which is logged
// next to it so the two can be compared. Identity headers are never
// logged. With TSFLOW_LOG_LEVEL=debug every denial is logged; otherwise
// one line per peer and reason per minute.
func logDenied(cfg config.Access, limiter *denyLimiter, r *http.Request, err error) {
	if r == nil || err == nil {
		return
	}
	peer := "-"
	if addr, ok := remoteAddr(r); ok {
		peer = addr.String()
	} else if strings.TrimSpace(r.RemoteAddr) != "" {
		peer = r.RemoteAddr
	}
	reason := publicError(err)
	if !cfg.Debug && limiter != nil && !limiter.allow(peer+"|"+reason) {
		return
	}
	line := "access denied: reason=%q mode=%s method=%s path=%q peer=%s x_forwarded_for=%q"
	args := []any{reason, cfg.Mode, r.Method, clip(r.URL.Path), peer, clip(r.Header.Get(headerForwarded))}
	if cfg.Mode == config.AccessModeHeader {
		line += " trusted_proxy=%t"
		args = append(args, fromTrustedProxy(r, cfg.TrustedPrefixes))
	}
	log.Printf(line, args...)
}

func clip(value string) string {
	if len(value) > denyLogMaxField {
		return value[:denyLogMaxField] + "..."
	}
	return value
}
