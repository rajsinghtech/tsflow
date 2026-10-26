package middleware

import (
	"log"
	"net/netip"
	"strings"
	"sync/atomic"

	"github.com/gin-gonic/gin"
)

// WarnUntrustedForwarded logs once when a request carries X-Forwarded-For or
// X-Real-IP from a peer that is not a trusted proxy. The header is ignored in
// that case, so behind a reverse proxy every user shares the proxy's address
// for rate limiting and logs. The warning names the setting that fixes it.
func WarnUntrustedForwarded(trusted []string) gin.HandlerFunc {
	return warnUntrustedForwarded(trusted, log.Printf)
}

func warnUntrustedForwarded(trusted []string, logf func(string, ...any)) gin.HandlerFunc {
	var prefixes []netip.Prefix
	for _, raw := range trusted {
		raw = strings.TrimSpace(raw)
		if p, err := netip.ParsePrefix(raw); err == nil {
			prefixes = append(prefixes, p.Masked())
		} else if a, err := netip.ParseAddr(raw); err == nil {
			prefixes = append(prefixes, netip.PrefixFrom(a.Unmap(), a.Unmap().BitLen()))
		}
	}
	var warned atomic.Bool
	return func(c *gin.Context) {
		if warned.Load() || (c.GetHeader("X-Forwarded-For") == "" && c.GetHeader("X-Real-IP") == "") {
			c.Next()
			return
		}
		peer, err := netip.ParseAddr(c.RemoteIP())
		if err != nil {
			c.Next()
			return
		}
		peer = peer.Unmap()
		for _, p := range prefixes {
			if p.Contains(peer) {
				c.Next()
				return
			}
		}
		if warned.CompareAndSwap(false, true) {
			logf("Warning: ignoring X-Forwarded-For/X-Real-IP from %s because it is not a trusted proxy. If tsflow is behind a reverse proxy, set TSFLOW_TRUSTED_PROXIES to its address so clients get their own rate limit and show up in logs.", peer)
		}
		c.Next()
	}
}
