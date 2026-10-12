package middleware

import (
	"crypto/md5"
	"encoding/hex"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
)

// CacheConfig holds cache configuration
type CacheConfig struct {
	MaxAge      time.Duration
	Private     bool
	NoStore     bool
	MustRevalid bool
}

// DefaultCacheConfig returns config for cacheable but frequently changing data
func DefaultCacheConfig() CacheConfig {
	return CacheConfig{
		MaxAge:      30 * time.Second,
		Private:     true,
		MustRevalid: true,
	}
}

// NoCacheConfig returns config for data that should not be cached
func NoCacheConfig() CacheConfig {
	return CacheConfig{
		NoStore: true,
	}
}

// ShortCacheConfig returns config for short-lived cache (live data)
func ShortCacheConfig() CacheConfig {
	return CacheConfig{
		MaxAge:      10 * time.Second,
		Private:     true,
		MustRevalid: true,
	}
}

// LongCacheConfig returns config for longer-lived cache (historical data)
func LongCacheConfig() CacheConfig {
	return CacheConfig{
		MaxAge:      5 * time.Minute,
		Private:     true,
		MustRevalid: true,
	}
}

// CacheMiddleware adds cache control headers based on configuration.
// Headers must be set BEFORE c.Next() because the handler flushes the response.
func CacheMiddleware(config CacheConfig) gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request.Method == "GET" {
			setCacheHeaders(c, config)
		}
		c.Next()
	}
}

// setCacheHeaders sets appropriate cache control headers
func setCacheHeaders(c *gin.Context, config CacheConfig) {
	if config.NoStore {
		c.Header("Cache-Control", "no-store, no-cache, must-revalidate")
		c.Header("Pragma", "no-cache")
		return
	}

	var cacheControl string
	if config.Private {
		cacheControl = "private"
	} else {
		cacheControl = "public"
	}

	if config.MaxAge > 0 {
		cacheControl += ", max-age=" + strconv.Itoa(int(config.MaxAge.Seconds()))
	}

	if config.MustRevalid {
		cacheControl += ", must-revalidate"
	}

	c.Header("Cache-Control", cacheControl)
}

// ETagMiddleware generates and validates ETags for responses
func ETagMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		// Only for GET requests
		if c.Request.Method != "GET" {
			c.Next()
			return
		}

		// Generate ETag from time bucket (5 second resolution) and URL
		// This ensures cache invalidation every 5 seconds for live data
		timeBucket := time.Now().Unix() / 5
		hash := md5.Sum([]byte(strconv.FormatInt(timeBucket, 10) + c.Request.URL.String()))
		etag := `"` + hex.EncodeToString(hash[:8]) + `"`

		// Check If-None-Match BEFORE processing request
		if c.GetHeader("If-None-Match") == etag {
			c.Header("ETag", etag)
			c.AbortWithStatus(304)
			return
		}

		c.Next()

		// Only set ETag for successful responses
		if c.Writer.Status() < 400 {
			c.Header("ETag", etag)
		}
	}
}

// ConditionalCacheHeaders adds time-based cache headers.
// Headers must be set BEFORE c.Next() because the handler flushes the response.
func ConditionalCacheHeaders(isHistorical bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request.Method == "GET" {
			if isHistorical {
				setCacheHeaders(c, LongCacheConfig())
			} else {
				setCacheHeaders(c, ShortCacheConfig())
			}
		}
		c.Next()
	}
}
