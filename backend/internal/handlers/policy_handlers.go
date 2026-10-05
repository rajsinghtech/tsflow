package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

func (h *Handlers) GetUsers(c *gin.Context) {
	tn, ok := h.bindTailnet(c)
	if !ok {
		return
	}
	users, err := tn.service.GetUsersWithContext(c.Request.Context())
	if err != nil {
		if writeContextError(c, err) {
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.Data(http.StatusOK, "application/json", users)
}

func (h *Handlers) GetPolicy(c *gin.Context) {
	tn, ok := h.bindTailnet(c)
	if !ok {
		return
	}
	policy, err := tn.service.GetPolicyWithContext(c.Request.Context())
	if err != nil {
		if writeContextError(c, err) {
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// Return raw JSON — the frontend parses it with jsonc-parser
	c.Data(http.StatusOK, "application/json", policy)
}
