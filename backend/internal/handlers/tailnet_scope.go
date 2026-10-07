package handlers

import (
	"errors"
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

// TailnetBinding is the tailnet selected for one request.
type TailnetBinding struct {
	ID      string
	Service *services.TailscaleService
	Poller  *services.Poller
}

// TailnetError is a tailnet selection failure with the same status and
// message the REST routes write.
type TailnetError struct {
	Status   int
	Message  string
	Tailnets []string
}

func (e *TailnetError) Error() string {
	if e == nil {
		return ""
	}
	return e.Message
}

// BindTailnet resolves the optional tailnet id the same way the data routes do.
// restricted is false when access control did not attach a viewer.
func (h *Handlers) BindTailnet(restricted bool, allow access.Allow, raw string) (TailnetBinding, error) {
	raw = strings.TrimSpace(raw)
	if restricted && raw != "" && !allow.Permits(raw) {
		return TailnetBinding{}, tailnetForbidden(raw)
	}
	if h == nil || h.registry == nil {
		if raw != "" && raw != database.DefaultTailnetID {
			return TailnetBinding{}, unknownTailnet(raw)
		}
		service := (*services.TailscaleService)(nil)
		var poller *services.Poller
		if h != nil {
			service = h.tailscaleService
			poller = h.poller
		}
		binding := TailnetBinding{ID: database.DefaultTailnetID, Service: service, Poller: poller}
		if restricted && !allow.Permits(binding.ID) {
			return TailnetBinding{}, tailnetForbidden(binding.ID)
		}
		return binding, nil
	}

	entries := h.registry.List()
	if len(entries) == 1 {
		only := entries[0]
		if raw == "" || raw == only.ID {
			if restricted && !allow.Permits(only.ID) {
				return TailnetBinding{}, tailnetForbidden(only.ID)
			}
			return bindingFrom(only), nil
		}
		return TailnetBinding{}, unknownTailnet(raw)
	}

	if raw == "" {
		if entry, ok := h.registry.Default(); ok && (!restricted || allow.Permits(entry.ID)) {
			return bindingFrom(entry), nil
		}
		ids := tailnetIDs(entries)
		if restricted {
			ids = allow.Filter(ids)
			if len(ids) == 0 {
				return TailnetBinding{}, tailnetForbidden("")
			}
		}
		return TailnetBinding{}, &TailnetError{
			Status:   http.StatusBadRequest,
			Message:  fmt.Sprintf("tailnet query parameter is required. Valid ids: %s", strings.Join(ids, ", ")),
			Tailnets: ids,
		}
	}

	entry, ok := h.registry.Get(raw)
	if !ok || entry == nil {
		return TailnetBinding{}, unknownTailnet(raw)
	}
	if restricted && !allow.Permits(entry.ID) {
		return TailnetBinding{}, tailnetForbidden(entry.ID)
	}
	return bindingFrom(entry), nil
}

func tailnetForbidden(id string) error {
	if id == "" {
		return &TailnetError{Status: http.StatusForbidden, Message: "no permitted tailnets"}
	}
	return &TailnetError{Status: http.StatusForbidden, Message: fmt.Sprintf("tailnet %q is not permitted", id)}
}

func unknownTailnet(id string) error {
	return &TailnetError{Status: http.StatusNotFound, Message: fmt.Sprintf("unknown tailnet %q", id)}
}

// bindTailnet resolves the optional tailnet query parameter.
// A false result means the response has already been written.
// With access control off, the resolution matches the open server.
func (h *Handlers) bindTailnet(c *gin.Context) (tailnetScope, bool) {
	ident, restricted := access.FromGin(c)
	binding, err := h.BindTailnet(restricted, ident.Allow, c.Query("tailnet"))
	if err != nil {
		var te *TailnetError
		if !errors.As(err, &te) {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to resolve tailnet"})
			return tailnetScope{}, false
		}
		body := gin.H{"error": te.Message}
		if te.Tailnets != nil {
			body["tailnets"] = te.Tailnets
		}
		c.JSON(te.Status, body)
		return tailnetScope{}, false
	}
	return tailnetScope{id: binding.ID, service: binding.Service, poller: binding.Poller}, true
}

func scopeFrom(entry *services.TailnetRuntime) tailnetScope {
	binding := bindingFrom(entry)
	return tailnetScope{id: binding.ID, service: binding.Service, poller: binding.Poller}
}

func bindingFrom(entry *services.TailnetRuntime) TailnetBinding {
	if entry == nil {
		return TailnetBinding{}
	}
	return TailnetBinding{ID: entry.ID, Service: entry.Service, Poller: entry.Poller}
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

type TailnetPollerStatus struct {
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

// TailnetSummary is one configured tailnet without credentials.
type TailnetSummary struct {
	ID          string              `json:"id"`
	DisplayName string              `json:"displayName"`
	Poller      TailnetPollerStatus `json:"poller"`
}

// ListTailnets returns id, display name, and poller status for each configured
// tailnet. Credentials are not included.
func (h *Handlers) ListTailnets(c *gin.Context) {
	ident, restricted := access.FromGin(c)
	c.JSON(http.StatusOK, gin.H{"tailnets": h.VisibleTailnets(restricted, ident.Allow)})
}

// VisibleTailnets lists configured tailnets, dropping ids the viewer cannot use.
// restricted is false when access control is off.
func (h *Handlers) VisibleTailnets(restricted bool, allow access.Allow) []TailnetSummary {
	items := h.allTailnets()
	if !restricted {
		return items
	}
	out := make([]TailnetSummary, 0, len(items))
	for _, item := range items {
		if allow.Permits(item.ID) {
			out = append(out, item)
		}
	}
	return out
}

func (h *Handlers) allTailnets() []TailnetSummary {
	if h == nil || h.registry == nil {
		var poller *services.Poller
		if h != nil {
			poller = h.poller
		}
		return []TailnetSummary{{
			ID:          database.DefaultTailnetID,
			DisplayName: "",
			Poller:      pollerStatus(poller),
		}}
	}
	entries := h.registry.List()
	out := make([]TailnetSummary, 0, len(entries))
	for _, entry := range entries {
		if entry == nil {
			continue
		}
		out = append(out, TailnetSummary{
			ID:          entry.ID,
			DisplayName: entry.Name,
			Poller:      pollerStatus(entry.Poller),
		})
	}
	return out
}

func pollerStatus(p *services.Poller) TailnetPollerStatus {
	if p == nil {
		return TailnetPollerStatus{}
	}
	stats := p.Stats()
	status := TailnetPollerStatus{
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
