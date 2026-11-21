package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/rajsinghtech/tsflow/backend/internal/access"
	"github.com/rajsinghtech/tsflow/backend/internal/config"
)

type whoAmIResponse struct {
	Login       string              `json:"login,omitempty"`
	Name        string              `json:"name,omitempty"`
	Node        string              `json:"node,omitempty"`
	Groups      []string            `json:"groups,omitempty"`
	Tailnets    []string            `json:"tailnets,omitempty"`
	Autoscope   string              `json:"autoscope"`
	DeviceScope *access.DeviceScope `json:"deviceScope,omitempty"`
	// Audit is true when this viewer may open the audit log pages.
	Audit bool `json:"audit,omitempty"`
}

// WhoAmI reports the viewer when access control identified one.
// With access control off, or before a grant is applied, the body is
// only {"autoscope":"off"} so the UI keeps the unfiltered view.
func (h *Handlers) WhoAmI(c *gin.Context) {
	ident, ok := access.FromGin(c)
	if !ok {
		c.JSON(http.StatusOK, whoAmIResponse{Autoscope: config.AccessAutoscopeOff, Audit: h.AuditAllowed(false, access.Identity{})})
		return
	}
	autoscope := ident.Autoscope
	if autoscope == "" {
		autoscope = config.AccessAutoscopeOff
	}
	c.JSON(http.StatusOK, whoAmIResponse{
		Login:       ident.Login,
		Name:        ident.Name,
		Node:        ident.Node,
		Groups:      ident.Groups,
		Tailnets:    ident.Allow.TailnetIDs(),
		Autoscope:   autoscope,
		DeviceScope: ident.DeviceScope,
		Audit:       h.AuditAllowed(true, ident),
	})
}
