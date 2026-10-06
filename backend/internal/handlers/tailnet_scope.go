package handlers

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/rajsinghtech/tsflow/backend/internal/access"
	"github.com/rajsinghtech/tsflow/backend/internal/database"
	"github.com/rajsinghtech/tsflow/backend/internal/services"
)

// tailnetScope is the tailnet selected for one request. It lives on the stack
// so two requests can use different tailnets without sharing handler state.
type tailnetScope struct {
	id      string
	service *services.TailscaleService
	poller  *services.Poller
}

// bindTailnet resolves the optional tailnet query parameter.
// A false result means the response has already been written.
// With access control off, the resolution matches the open server.
func (h *Handlers) bindTailnet(c *gin.Context) (tailnetScope, bool) {
	ident, restricted := access.FromGin(c)
	raw := strings.TrimSpace(c.Query("tailnet"))
	if restricted && raw != "" && !ident.Allow.Permits(raw) {
		writeTailnetForbidden(c, raw)
		return tailnetScope{}, false
	}
	if h == nil || h.registry == nil {
		if raw != "" && raw != database.DefaultTailnetID {
			writeUnknownTailnet(c, raw)
			return tailnetScope{}, false
		}
		service := (*services.TailscaleService)(nil)
		var poller *services.Poller
		if h != nil {
			service = h.tailscaleService
			poller = h.poller
		}
		scope := tailnetScope{id: database.DefaultTailnetID, service: service, poller: poller}
		if restricted && !ident.Allow.Permits(scope.id) {
			writeTailnetForbidden(c, scope.id)
			return tailnetScope{}, false
		}
		return scope, true
	}

	entries := h.registry.List()
	if len(entries) == 1 {
		only := entries[0]
		if raw == "" || raw == only.ID {
			if restricted && !ident.Allow.Permits(only.ID) {
				writeTailnetForbidden(c, only.ID)
				return tailnetScope{}, false
			}
			return scopeFrom(only), true
		}
		writeUnknownTailnet(c, raw)
		return tailnetScope{}, false
	}

	if raw == "" {
		if entry, ok := h.registry.Default(); ok && (!restricted || ident.Allow.Permits(entry.ID)) {
			return scopeFrom(entry), true
		}
		ids := tailnetIDs(entries)
		if restricted {
			ids = ident.Allow.Filter(ids)
			if len(ids) == 0 {
				writeTailnetForbidden(c, "")
				return tailnetScope{}, false
			}
		}
		writeTailnetRequired(c, ids)
		return tailnetScope{}, false
	}

	entry, ok := h.registry.Get(raw)
	if !ok || entry == nil {
		writeUnknownTailnet(c, raw)
		return tailnetScope{}, false
	}
	if restricted && !ident.Allow.Permits(entry.ID) {
		writeTailnetForbidden(c, entry.ID)
		return tailnetScope{}, false
	}
	return scopeFrom(entry), true
}

func scopeFrom(entry *services.TailnetRuntime) tailnetScope {
	if entry == nil {
		return tailnetScope{}
	}
	return tailnetScope{id: entry.ID, service: entry.Service, poller: entry.Poller}
}

func tailnetIDs(entries []*services.TailnetRuntime) []string {
	ids := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry == nil || entry.ID == "" {
			continue
		}
		ids = append(ids, entry.ID)
	}
	return ids
}

func writeUnknownTailnet(c *gin.Context, id string) {
	c.JSON(http.StatusNotFound, gin.H{
		"error": fmt.Sprintf("unknown tailnet %q", id),
	})
}

func writeTailnetForbidden(c *gin.Context, id string) {
	if id == "" {
		c.JSON(http.StatusForbidden, gin.H{"error": "no permitted tailnets"})
		return
	}
	c.JSON(http.StatusForbidden, gin.H{
		"error": fmt.Sprintf("tailnet %q is not permitted", id),
	})
}

func writeTailnetRequired(c *gin.Context, ids []string) {
	c.JSON(http.StatusBadRequest, gin.H{
		"error":    fmt.Sprintf("tailnet query parameter is required. Valid ids: %s", strings.Join(ids, ", ")),
		"tailnets": ids,
	})
}

type tailnetPollerStatus struct {
	Running       bool      `json:"running"`
	LastPollTime  time.Time `json:"lastPollTime"`
	LastPollCount int       `json:"lastPollCount"`
	TotalPolled   int64     `json:"totalPolled"`
	PollErrors    int64     `json:"pollErrors"`
	PollInterval  string    `json:"pollInterval"`
	FlowBackend   string    `json:"flowBackend"`
	LastError     string    `json:"lastError,omitempty"`
	LastErrorTime time.Time `json:"lastErrorTime,omitempty"`
}

type tailnetSummary struct {
	ID          string              `json:"id"`
	DisplayName string              `json:"displayName"`
	Poller      tailnetPollerStatus `json:"poller"`
}

// ListTailnets returns id, display name, and poller status for each configured
// tailnet. Credentials are not included.
func (h *Handlers) ListTailnets(c *gin.Context) {
	if h == nil || h.registry == nil {
		var poller *services.Poller
		if h != nil {
			poller = h.poller
		}
		c.JSON(http.StatusOK, gin.H{
			"tailnets": visibleTailnets(c, []tailnetSummary{{
				ID:          database.DefaultTailnetID,
				DisplayName: "",
				Poller:      pollerStatus(poller),
			}}),
		})
		return
	}

	entries := h.registry.List()
	out := make([]tailnetSummary, 0, len(entries))
	for _, entry := range entries {
		if entry == nil {
			continue
		}
		out = append(out, tailnetSummary{
			ID:          entry.ID,
			DisplayName: entry.Name,
			Poller:      pollerStatus(entry.Poller),
		})
	}
	c.JSON(http.StatusOK, gin.H{"tailnets": visibleTailnets(c, out)})
}

func visibleTailnets(c *gin.Context, items []tailnetSummary) []tailnetSummary {
	ident, restricted := access.FromGin(c)
	if !restricted {
		return items
	}
	out := make([]tailnetSummary, 0, len(items))
	for _, item := range items {
		if ident.Allow.Permits(item.ID) {
			out = append(out, item)
		}
	}
	return out
}

func pollerStatus(p *services.Poller) tailnetPollerStatus {
	if p == nil {
		return tailnetPollerStatus{}
	}
	stats := p.Stats()
	status := tailnetPollerStatus{
		Running:       boolStat(stats["running"]),
		LastPollTime:  timeStat(stats["lastPollTime"]),
		LastPollCount: intStat(stats["lastPollCount"]),
		TotalPolled:   int64Stat(stats["totalPolled"]),
		PollErrors:    int64Stat(stats["pollErrors"]),
		PollInterval:  stringStat(stats["pollInterval"]),
		FlowBackend:   stringStat(stats["flowBackend"]),
		LastError:     stringStat(stats["lastError"]),
	}
	if t := timeStat(stats["lastErrorTime"]); !t.IsZero() {
		status.LastErrorTime = t
	}
	return status
}

func boolStat(v any) bool {
	b, _ := v.(bool)
	return b
}

func stringStat(v any) string {
	s, _ := v.(string)
	return s
}

func intStat(v any) int {
	switch n := v.(type) {
	case int:
		return n
	case int64:
		return int(n)
	default:
		return 0
	}
}

func int64Stat(v any) int64 {
	switch n := v.(type) {
	case int64:
		return n
	case int:
		return int64(n)
	default:
		return 0
	}
}

func timeStat(v any) time.Time {
	t, _ := v.(time.Time)
	return t
}
