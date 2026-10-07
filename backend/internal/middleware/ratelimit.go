package middleware

import (
	"math"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

// RateLimitConfig holds rate limiter configuration
type RateLimitConfig struct {
	RequestsPerMinute int
	CleanupInterval   time.Duration
	StaleAfter        time.Duration
}

// DefaultRateLimitConfig returns sane defaults: 100 req/min per IP
func DefaultRateLimitConfig() RateLimitConfig {
	return RateLimitConfig{
		RequestsPerMinute: 100,
		CleanupInterval:   5 * time.Minute,
		StaleAfter:        10 * time.Minute,
	}
}

type bucket struct {
	tokens   float64
	lastSeen time.Time
}

type rateLimiter struct {
	mu       sync.Mutex
	buckets  map[string]*bucket
	rate     float64 // tokens per second
	capacity float64 // max burst
	stopChan chan struct{}
}

// normalize fills unset or invalid fields from the defaults. A zero
// CleanupInterval used to panic in time.NewTicker, and a zero
// RequestsPerMinute rejected every request with a nonsense Retry-After.
// Buckets refill completely in one minute, so dropping one sooner than that
// would forgive a client that is still over its limit.
func (cfg RateLimitConfig) normalize() RateLimitConfig {
	def := DefaultRateLimitConfig()
	if cfg.RequestsPerMinute <= 0 {
		cfg.RequestsPerMinute = def.RequestsPerMinute
	}
	if cfg.CleanupInterval <= 0 {
		cfg.CleanupInterval = def.CleanupInterval
	}
	if cfg.StaleAfter <= 0 {
		cfg.StaleAfter = def.StaleAfter
	}
	if cfg.StaleAfter < time.Minute {
		cfg.StaleAfter = time.Minute
	}
	return cfg
}

func newRateLimiter(cfg RateLimitConfig) *rateLimiter {
	cfg = cfg.normalize()
	rl := &rateLimiter{
		buckets:  make(map[string]*bucket),
		rate:     float64(cfg.RequestsPerMinute) / 60.0,
		capacity: float64(cfg.RequestsPerMinute),
		stopChan: make(chan struct{}),
	}

	go rl.cleanup(cfg.CleanupInterval, cfg.StaleAfter)
	return rl
}

// allow checks if the given key has tokens available
func (rl *rateLimiter) allow(key string) (bool, time.Duration) {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	b, ok := rl.buckets[key]
	if !ok {
		b = &bucket{tokens: rl.capacity, lastSeen: now}
		rl.buckets[key] = b
	}

	// Refill tokens based on elapsed time
	elapsed := now.Sub(b.lastSeen).Seconds()
	b.tokens = math.Min(rl.capacity, b.tokens+elapsed*rl.rate)
	b.lastSeen = now

	if b.tokens >= 1 {
		b.tokens--
		return true, 0
	}

	// Calculate retry delay: time until 1 token is available
	retryAfter := time.Duration((1-b.tokens)/rl.rate*1000) * time.Millisecond
	return false, retryAfter
}

// cleanup periodically removes stale entries
func (rl *rateLimiter) cleanup(interval, staleAfter time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-rl.stopChan:
			return
		case <-ticker.C:
			rl.mu.Lock()
			cutoff := time.Now().Add(-staleAfter)
			for key, b := range rl.buckets {
				if b.lastSeen.Before(cutoff) {
					delete(rl.buckets, key)
				}
			}
			rl.mu.Unlock()
		}
	}
}

// RateLimitMiddleware returns a gin middleware that rate limits by client IP
func RateLimitMiddleware(cfg RateLimitConfig) gin.HandlerFunc {
	rl := newRateLimiter(cfg)

	return func(c *gin.Context) {
		ip := c.ClientIP()
		allowed, retryAfter := rl.allow(ip)
		if !allowed {
			c.Header("Retry-After", strconv.Itoa(int(math.Ceil(retryAfter.Seconds()))))
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{
				"error":   "rate limit exceeded",
				"message": "too many requests, please try again later",
			})
			return
		}
		c.Next()
	}
}
