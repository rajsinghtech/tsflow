package middleware

import "github.com/gin-gonic/gin"

// ConfigureClientIP sets which peers may supply the client address through
// X-Forwarded-For or X-Real-IP. That address keys the API rate limiter and is
// the IP in request logs. Gin trusts every peer by default, which lets any
// client choose its own address. With no trusted proxies the peer address is
// used.
func ConfigureClientIP(router *gin.Engine, trusted []string) error {
	if len(trusted) == 0 {
		trusted = nil
	}
	return router.SetTrustedProxies(trusted)
}
