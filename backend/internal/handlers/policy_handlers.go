package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

func (h *Handlers) GetUsers(c *gin.Context) {
	users, err := h.tailscaleService.GetUsersWithContext(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.Data(http.StatusOK, "application/json", users)
}

func (h *Handlers) GetPolicy(c *gin.Context) {
	policy, err := h.tailscaleService.GetPolicyWithContext(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// Return raw JSON — the frontend parses it with jsonc-parser
	c.Data(http.StatusOK, "application/json", policy)
}
