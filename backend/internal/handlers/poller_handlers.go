package handlers

import (
	"context"
	"errors"
	"log"
	"net/http"

	"github.com/gin-gonic/gin"
)

// GetPollerStatus returns the current status of the background poller
func (h *Handlers) GetPollerStatus(c *gin.Context) {
	if h.poller == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"error": "Poller not configured",
		})
		return
	}

	stats := h.poller.Stats()

	// Add database stats if available
	if h.store != nil {
		ctx, cancel := context.WithTimeout(c.Request.Context(), ShortQueryTimeout)
		defer cancel()

		dbStats, err := h.store.GetStats(ctx)
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(c.Request.Context().Err(), context.Canceled) {
				c.Status(499)
				return
			}
			if errors.Is(err, context.DeadlineExceeded) || errors.Is(c.Request.Context().Err(), context.DeadlineExceeded) {
				c.Status(http.StatusGatewayTimeout)
				return
			}
			log.Printf("ERROR GetPollerStatus database stats: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{
				"error": "Failed to fetch database stats",
			})
			return
		}
		stats["database"] = dbStats
	}

	c.JSON(http.StatusOK, stats)
}

// TriggerPoll manually triggers an immediate poll (for testing/debugging)
func (h *Handlers) TriggerPoll(c *gin.Context) {
	if h.poller == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"error": "Poller not configured",
		})
		return
	}

	h.poller.TriggerPoll()
	stats := h.poller.Stats()
	c.JSON(http.StatusOK, gin.H{
		"message": "Poll triggered",
		"stats":   stats,
	})
}
