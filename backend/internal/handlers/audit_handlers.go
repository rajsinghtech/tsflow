package handlers

import (
	"bufio"
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/rajsinghtech/tsflow/backend/internal/access"
	"github.com/rajsinghtech/tsflow/backend/internal/audit"
	"github.com/rajsinghtech/tsflow/backend/internal/config"
	"github.com/rajsinghtech/tsflow/backend/internal/services"
)

// DefaultAuditWindow is the search window when start is not given.
const DefaultAuditWindow = 30 * 24 * time.Hour

// AuditGate decides who may read audit data. Audit logs carry policy diffs,
// admin actions and logins, so they are off unless the operator turns them
// on, and with access control on only the listed viewers may read them.
type AuditGate struct {
	enabled bool
	viewers map[string]struct{}
}

// NewAuditGate builds a gate. viewers are logins or group names; a group
// may be written with or without the group: prefix.
func NewAuditGate(enabled bool, viewers []string) AuditGate {
	g := AuditGate{enabled: enabled, viewers: map[string]struct{}{}}
	for _, v := range viewers {
		v = strings.ToLower(strings.TrimSpace(v))
		if v == "" {
			continue
		}
		g.viewers[v] = struct{}{}
		g.viewers[strings.TrimPrefix(v, "group:")] = struct{}{}
	}
	return g
}

// Enabled reports whether audit support is on at all.
func (g AuditGate) Enabled() bool { return g.enabled }

// Allows reports whether the viewer may read audit data. With access control
// off (restricted false) the operator has opened the whole server, so an
// enabled gate allows everyone.
func (g AuditGate) Allows(restricted bool, ident access.Identity) bool {
	if !g.enabled {
		return false
	}
	if !restricted {
		return true
	}
	if login := strings.ToLower(strings.TrimSpace(ident.Login)); login != "" {
		if _, ok := g.viewers[login]; ok {
			return true
		}
	}
	for _, group := range ident.Groups {
		group = strings.ToLower(strings.TrimSpace(group))
		if group == "" {
			continue
		}
		if _, ok := g.viewers[group]; ok {
			return true
		}
		if _, ok := g.viewers[strings.TrimPrefix(group, "group:")]; ok {
			return true
		}
	}
	return false
}

// UseAudit turns on the audit routes.
func (h *Handlers) UseAudit(m *services.AuditManager, gate AuditGate) {
	if h == nil {
		return
	}
	h.audit = m
	h.auditGate = gate
}

// AuditAllowed reports whether audit data is available to this viewer.
func (h *Handlers) AuditAllowed(restricted bool, ident access.Identity) bool {
	return h != nil && h.audit != nil && h.auditGate.Allows(restricted, ident)
}

func (h *Handlers) auditAllowedFor(c *gin.Context) bool {
	ident, restricted := access.FromGin(c)
	return h.AuditAllowed(restricted, ident)
}

// auditScope checks the gate and binds the tailnet. A false result means the
// response has been written.
func (h *Handlers) auditScope(c *gin.Context) (tailnetScope, *audit.Store, bool) {
	if h.audit == nil || !h.auditGate.Enabled() {
		c.JSON(http.StatusNotFound, gin.H{"error": "audit logs are not enabled"})
		return tailnetScope{}, nil, false
	}
	if !h.auditAllowedFor(c) {
		c.JSON(http.StatusForbidden, gin.H{"error": "audit logs are not permitted for this viewer"})
		return tailnetScope{}, nil, false
	}
	tn, ok := h.bindTailnet(c)
	if !ok {
		return tailnetScope{}, nil, false
	}
	return tn, h.audit.Store(), true
}

// AuditTailnetStatus is one tailnet in GET /api/audit/status.
type AuditTailnetStatus struct {
	ID          string                     `json:"id"`
	DisplayName string                     `json:"displayName"`
	Source      services.AuditPollerStatus `json:"source"`
	Stored      audit.TailnetStats         `json:"stored"`
}

// GetAuditStatus reports, for every visible tailnet, where audit logs come
// from, how ingest is going, and what is stored.
func (h *Handlers) GetAuditStatus(c *gin.Context) {
	if h.audit == nil || !h.auditGate.Enabled() {
		c.JSON(http.StatusOK, gin.H{"enabled": false, "permitted": false, "tailnets": []AuditTailnetStatus{}})
		return
	}
	if !h.auditAllowedFor(c) {
		c.JSON(http.StatusOK, gin.H{"enabled": true, "permitted": false, "tailnets": []AuditTailnetStatus{}})
		return
	}
	ident, restricted := access.FromGin(c)
	ctx, cancel := context.WithTimeout(c.Request.Context(), ShortQueryTimeout)
	defer cancel()
	out := []AuditTailnetStatus{}
	for _, summary := range h.VisibleTailnets(restricted, ident.Allow) {
		src, _ := h.audit.Status(summary.ID)
		if src.TailnetID == "" {
			src = services.AuditPollerStatus{TailnetID: summary.ID, Backend: config.AuditBackendOff}
		}
		stats, err := h.audit.Store().Stats(ctx, summary.ID)
		if err != nil {
			if writeContextError(c, err) {
				return
			}
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to read audit status"})
			return
		}
		out = append(out, AuditTailnetStatus{ID: summary.ID, DisplayName: summary.DisplayName, Source: src, Stored: stats})
	}
	c.JSON(http.StatusOK, gin.H{
		"enabled":   true,
		"permitted": true,
		"retention": config.FormatLongDuration(h.audit.Retention()),
		"tailnets":  out,
	})
}

// auditWindow reads start and end (RFC3339) or since (72h, 30d).
func auditWindow(c *gin.Context) (time.Time, time.Time, error) {
	end := time.Now().UTC()
	if raw := strings.TrimSpace(c.Query("end")); raw != "" {
		t, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			return time.Time{}, time.Time{}, errors.New("invalid end time")
		}
		end = t.UTC()
	}
	start := end.Add(-DefaultAuditWindow)
	if raw := strings.TrimSpace(c.Query("since")); raw != "" {
		d, err := config.ParseLongDuration(raw)
		if err != nil {
			return time.Time{}, time.Time{}, errors.New("invalid since")
		}
		start = end.Add(-d)
	}
	if raw := strings.TrimSpace(c.Query("start")); raw != "" {
		t, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			return time.Time{}, time.Time{}, errors.New("invalid start time")
		}
		start = t.UTC()
	}
	if end.Before(start) {
		return time.Time{}, time.Time{}, errors.New("end time before start time")
	}
	return start, end, nil
}

func auditLimit(c *gin.Context) (int, error) {
	raw := strings.TrimSpace(c.Query("limit"))
	if raw == "" {
		return audit.DefaultPageSize, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		return 0, errors.New("limit must be a positive integer")
	}
	if n > audit.MaxPageSize {
		n = audit.MaxPageSize
	}
	return n, nil
}

// auditFilter builds a filter from query parameters. List parameters may
// repeat; repeated values match any of them.
func auditFilter(c *gin.Context, tailnetID string) (audit.Filter, error) {
	start, end, err := auditWindow(c)
	if err != nil {
		return audit.Filter{}, err
	}
	limit, err := auditLimit(c)
	if err != nil {
		return audit.Filter{}, err
	}
	failed, _ := strconv.ParseBool(c.Query("failed"))
	ephemeral, _ := strconv.ParseBool(c.Query("ephemeral"))
	categories := c.QueryArray("category")
	for _, cat := range categories {
		for _, part := range strings.Split(cat, ",") {
			if p := strings.TrimSpace(part); p != "" && !audit.KnownCategory(strings.ToLower(p)) {
				return audit.Filter{}, fmt.Errorf("unknown category %q", p)
			}
		}
	}
	return audit.Filter{
		TailnetID:     tailnetID,
		From:          start,
		To:            end,
		Actors:        c.QueryArray("actor"),
		Targets:       c.QueryArray("target"),
		TargetIDs:     c.QueryArray("targetId"),
		User:          c.Query("user"),
		Events:        c.QueryArray("event"),
		Actions:       c.QueryArray("action"),
		TargetTypes:   c.QueryArray("targetType"),
		Origins:       c.QueryArray("origin"),
		Categories:    categories,
		ActorTypes:    c.QueryArray("actorType"),
		EphemeralOnly: ephemeral,
		FailedOnly:    failed,
		Query:         c.Query("q"),
		Cursor:        c.Query("cursor"),
		Limit:         limit,
	}, nil
}

func (h *Handlers) writeAuditError(c *gin.Context, err error) {
	if writeContextError(c, err) {
		return
	}
	if strings.Contains(err.Error(), "invalid cursor") {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid cursor"})
		return
	}
	c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to read audit logs"})
}

// GetAuditEvents searches events, newest first, with keyset paging.
func (h *Handlers) GetAuditEvents(c *gin.Context) {
	tn, store, ok := h.auditScope(c)
	if !ok {
		return
	}
	filter, err := auditFilter(c, tn.id)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), DefaultQueryTimeout)
	defer cancel()
	page, err := store.Query(ctx, filter)
	if err != nil {
		h.writeAuditError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"tailnet":    tn.id,
		"start":      filter.From,
		"end":        filter.To,
		"events":     page.Events,
		"nextCursor": page.NextCursor,
	})
}

// GetAuditEvent returns one event and the other events from the same
// operation.
func (h *Handlers) GetAuditEvent(c *gin.Context) {
	tn, store, ok := h.auditScope(c)
	if !ok {
		return
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid event id"})
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), ShortQueryTimeout)
	defer cancel()
	event, err := store.Get(ctx, tn.id, id)
	if errors.Is(err, audit.ErrNotFound) {
		c.JSON(http.StatusNotFound, gin.H{"error": "event not found"})
		return
	}
	if err != nil {
		h.writeAuditError(c, err)
		return
	}
	group, err := store.Group(ctx, tn.id, event.EventGroupID)
	if err != nil {
		h.writeAuditError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"event": event, "group": group})
}

// GetAuditGroup returns every event from one operation.
func (h *Handlers) GetAuditGroup(c *gin.Context) {
	tn, store, ok := h.auditScope(c)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), ShortQueryTimeout)
	defer cancel()
	group, err := store.Group(ctx, tn.id, c.Param("groupId"))
	if err != nil {
		h.writeAuditError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"events": group})
}

// GetAuditFacets counts events in the window for the filter menus.
func (h *Handlers) GetAuditFacets(c *gin.Context) {
	tn, store, ok := h.auditScope(c)
	if !ok {
		return
	}
	filter, err := auditFilter(c, tn.id)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), DefaultQueryTimeout)
	defer cancel()
	facets, err := store.Facets(ctx, filter)
	if err != nil {
		h.writeAuditError(c, err)
		return
	}
	c.JSON(http.StatusOK, facets)
}

// GetAuditNode returns audit history for one device (target id).
func (h *Handlers) GetAuditNode(c *gin.Context) {
	tn, store, ok := h.auditScope(c)
	if !ok {
		return
	}
	filter, err := auditFilter(c, tn.id)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	filter.TargetIDs = []string{c.Param("nodeId")}
	if c.Query("start") == "" && c.Query("since") == "" {
		filter.From = time.Time{}
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), DefaultQueryTimeout)
	defer cancel()
	page, err := store.Query(ctx, filter)
	if err != nil {
		h.writeAuditError(c, err)
		return
	}
	summary, err := store.NodeSummary(ctx, tn.id, c.Param("nodeId"))
	if err != nil {
		h.writeAuditError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"nodeId": c.Param("nodeId"), "summary": summary, "events": page.Events, "nextCursor": page.NextCursor})
}

// GetAuditCatalog lists every documented event with its label and category.
func (h *Handlers) GetAuditCatalog(c *gin.Context) {
	// The catalog is static, so it needs the feature and viewer checks but
	// not a tailnet.
	if h.audit == nil || !h.auditGate.Enabled() {
		c.JSON(http.StatusNotFound, gin.H{"error": "audit logs are not enabled"})
		return
	}
	if !h.auditAllowedFor(c) {
		c.JSON(http.StatusForbidden, gin.H{"error": "audit logs are not permitted for this viewer"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"categories": audit.Categories, "events": audit.Catalog()})
}

// AuditInsights is the overview: activity over time plus the derived views
// (policy edits, key lifecycles, tailnet settings).
type AuditInsights struct {
	Histogram audit.Histogram       `json:"histogram"`
	Policy    []audit.PolicyChange  `json:"policy"`
	Keys      []audit.KeyLifecycle  `json:"keys"`
	Settings  []audit.SettingChange `json:"settings"`
}

// BuildAuditInsights computes the overview for a filter. tzOffset is the
// viewer's offset from UTC in seconds, so day buckets start at local midnight.
func BuildAuditInsights(ctx context.Context, store *audit.Store, filter audit.Filter, tzOffset int) (AuditInsights, error) {
	var out AuditInsights
	var err error
	if out.Histogram, err = store.Histogram(ctx, filter, 0, tzOffset); err != nil {
		return out, err
	}
	// The derived views keep the window and actor but not narrower event
	// filters, which would usually hide them.
	base := audit.Filter{TailnetID: filter.TailnetID, From: filter.From, To: filter.To, Actors: filter.Actors, User: filter.User}
	if out.Policy, err = store.PolicyChanges(ctx, base, 50); err != nil {
		return out, err
	}
	if out.Keys, err = store.Keys(ctx, base, 100); err != nil {
		return out, err
	}
	if out.Settings, err = store.Settings(ctx, base); err != nil {
		return out, err
	}
	return out, nil
}

// GetAuditInsights returns the histogram and derived views for a filter.
func (h *Handlers) GetAuditInsights(c *gin.Context) {
	tn, store, ok := h.auditScope(c)
	if !ok {
		return
	}
	filter, err := auditFilter(c, tn.id)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	tz, _ := strconv.Atoi(c.Query("tzOffset"))
	if tz < -14*3600 || tz > 14*3600 {
		tz = 0
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), DefaultQueryTimeout)
	defer cancel()
	out, err := BuildAuditInsights(ctx, store, filter, tz)
	if err != nil {
		h.writeAuditError(c, err)
		return
	}
	c.JSON(http.StatusOK, out)
}

// MaxAuditExportRows caps one export.
const MaxAuditExportRows = 100000

var auditCSVHeader = []string{"event_time", "event", "label", "category", "origin", "actor_login", "actor_display_name", "actor_type", "actor_id", "actor_tags",
	"target_type", "target_name", "target_id", "target_property", "target_ephemeral", "event_group_id", "deferred_at", "old", "new", "action_details", "error"}

// GetAuditExport streams matching events as CSV, or as NDJSON holding the
// original Tailscale records (format=ndjson), newest first.
func (h *Handlers) GetAuditExport(c *gin.Context) {
	tn, store, ok := h.auditScope(c)
	if !ok {
		return
	}
	filter, err := auditFilter(c, tn.id)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	format := strings.ToLower(c.DefaultQuery("format", "csv"))
	if format != "csv" && format != "ndjson" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "format must be csv or ndjson"})
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 2*DefaultQueryTimeout)
	defer cancel()
	name := fmt.Sprintf("tsflow-audit-%s-%s.%s", tn.id, time.Now().UTC().Format("20060102-150405"), format)
	c.Header("Content-Disposition", `attachment; filename="`+name+`"`)
	if format == "ndjson" {
		c.Header("Content-Type", "application/x-ndjson")
	} else {
		c.Header("Content-Type", "text/csv; charset=utf-8")
	}
	c.Status(http.StatusOK)
	w := bufio.NewWriter(c.Writer)
	defer w.Flush()
	var cw *csv.Writer
	if format == "csv" {
		cw = csv.NewWriter(w)
		_ = cw.Write(auditCSVHeader)
	}
	err = store.Each(ctx, filter, MaxAuditExportRows, func(e audit.Event, raw []byte) error {
		if cw == nil {
			if _, err := w.Write(raw); err != nil {
				return err
			}
			return w.WriteByte('\n')
		}
		deferred := ""
		if e.DeferredAt != nil {
			deferred = e.DeferredAt.Format(time.RFC3339Nano)
		}
		return cw.Write([]string{
			e.EventTime.Format(time.RFC3339Nano), e.Event, audit.Describe(e.Event).Label, string(e.Category), e.Origin,
			e.Actor.LoginName, e.Actor.DisplayName, e.Actor.Type, e.Actor.ID, strings.Join(e.Actor.Tags, " "),
			e.Target.Type, e.Target.Name, e.Target.ID, e.Target.Property, strconv.FormatBool(e.Target.IsEphemeral),
			e.EventGroupID, deferred, csvValue(e.Old), csvValue(e.New), e.ActionDetails, e.Error,
		})
	})
	if cw != nil {
		cw.Flush()
	}
	if err != nil {
		// Headers are gone; the truncated body is the only signal left.
		log.Printf("audit export for %s stopped: %v", tn.id, err)
	}
}

// csvValue writes a JSON string's text, or compact JSON for other values.
func csvValue(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	return string(raw)
}

// LiveUser is what the Tailscale users API says about a login right now.
type LiveUser struct {
	ID                 string `json:"id,omitempty"`
	DisplayName        string `json:"displayName,omitempty"`
	Role               string `json:"role,omitempty"`
	Status             string `json:"status,omitempty"`
	Created            string `json:"created,omitempty"`
	LastSeen           string `json:"lastSeen,omitempty"`
	CurrentlyConnected bool   `json:"currentlyConnected"`
	DeviceCount        int    `json:"deviceCount"`
}

// LiveDevice is one of the user's devices from the devices API.
type LiveDevice struct {
	ID                 string   `json:"id"`
	Name               string   `json:"name"`
	OS                 string   `json:"os,omitempty"`
	LastSeen           string   `json:"lastSeen,omitempty"`
	ConnectedToControl bool     `json:"connectedToControl"`
	Tags               []string `json:"tags,omitempty"`
}

// AuditLive is the live API view next to the audit summary.
type AuditLive struct {
	Available bool         `json:"available"`
	Error     string       `json:"error,omitempty"`
	User      *LiveUser    `json:"user,omitempty"`
	Devices   []LiveDevice `json:"devices"`
}

// AuditUserResponse is GET /api/audit/users/:login.
type AuditUserResponse struct {
	Tailnet  string            `json:"tailnet"`
	Summary  audit.UserSummary `json:"summary"`
	Live     AuditLive         `json:"live"`
	Timeline audit.Page        `json:"timeline"`
}

// AuditUser builds the per-user view: last logins from the audit log, the
// live users and devices API view when credentials allow, and a page of
// events done by or to the user.
func (h *Handlers) AuditUser(ctx context.Context, binding TailnetBinding, login string, filter audit.Filter, live bool) (AuditUserResponse, error) {
	store := h.audit.Store()
	summary, err := store.UserSummary(ctx, binding.ID, login)
	if err != nil {
		return AuditUserResponse{}, err
	}
	filter.TailnetID = binding.ID
	filter.User = login
	page, err := store.Query(ctx, filter)
	if err != nil {
		return AuditUserResponse{}, err
	}
	out := AuditUserResponse{Tailnet: binding.ID, Summary: summary, Timeline: page, Live: AuditLive{Devices: []LiveDevice{}}}
	if live {
		out.Live = liveUser(ctx, binding.Service, login)
	}
	return out, nil
}

func liveUser(parent context.Context, svc *services.TailscaleService, login string) AuditLive {
	out := AuditLive{Devices: []LiveDevice{}}
	if svc == nil || !svc.HasCredentials() {
		out.Error = "no API credentials for this tailnet"
		return out
	}
	ctx, cancel := context.WithTimeout(parent, 8*time.Second)
	defer cancel()
	body, err := svc.GetUsersWithContext(ctx)
	if err != nil {
		out.Error = "users API unavailable (needs users:read)"
	} else {
		var resp struct {
			Users []struct {
				LiveUser
				LoginName string `json:"loginName"`
			} `json:"users"`
		}
		if json.Unmarshal(body, &resp) == nil {
			out.Available = true
			for _, u := range resp.Users {
				if strings.EqualFold(u.LoginName, login) {
					user := u.LiveUser
					out.User = &user
					break
				}
			}
		}
	}
	devices, err := svc.GetDevicesWithContext(ctx)
	if err == nil && devices != nil {
		out.Available = true
		for _, d := range devices.Devices {
			if !strings.EqualFold(d.User, login) {
				continue
			}
			id := d.NodeID
			if id == "" {
				id = d.ID
			}
			out.Devices = append(out.Devices, LiveDevice{ID: id, Name: d.Name, OS: d.OS, LastSeen: d.LastSeen, ConnectedToControl: d.ConnectedToControl, Tags: d.Tags})
		}
		sort.Slice(out.Devices, func(i, j int) bool { return out.Devices[i].LastSeen > out.Devices[j].LastSeen })
	}
	return out
}

// GetAuditUser returns the per-user summary and timeline.
func (h *Handlers) GetAuditUser(c *gin.Context) {
	tn, _, ok := h.auditScope(c)
	if !ok {
		return
	}
	login := strings.TrimSpace(c.Param("login"))
	if login == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "login is required"})
		return
	}
	filter, err := auditFilter(c, tn.id)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if c.Query("start") == "" && c.Query("since") == "" {
		filter.From = time.Time{}
	}
	live := c.Query("live") != "false"
	ctx, cancel := context.WithTimeout(c.Request.Context(), DefaultQueryTimeout)
	defer cancel()
	resp, err := h.AuditUser(ctx, TailnetBinding{ID: tn.id, Service: tn.service, Poller: tn.poller}, login, filter, live)
	if err != nil {
		h.writeAuditError(c, err)
		return
	}
	c.JSON(http.StatusOK, resp)
}

// AuditSearch runs a search for the MCP tool.
func (h *Handlers) AuditSearch(ctx context.Context, binding TailnetBinding, filter audit.Filter) (audit.Page, error) {
	if h == nil || h.audit == nil {
		return audit.Page{}, errors.New("audit logs are not enabled")
	}
	filter.TailnetID = binding.ID
	return h.audit.Store().Query(ctx, filter)
}

// AuditOverview returns the insights for MCP, scoped to one tailnet.
func (h *Handlers) AuditOverview(ctx context.Context, binding TailnetBinding, filter audit.Filter, tzOffset int) (AuditInsights, error) {
	if h == nil || h.audit == nil {
		return AuditInsights{}, errors.New("audit logs are not enabled")
	}
	filter.TailnetID = binding.ID
	return BuildAuditInsights(ctx, h.audit.Store(), filter, tzOffset)
}

// AuditNode returns one device's audit summary and newest events for MCP.
func (h *Handlers) AuditNode(ctx context.Context, binding TailnetBinding, nodeID string, limit int) (*audit.NodeSummary, audit.Page, error) {
	if h == nil || h.audit == nil {
		return nil, audit.Page{}, errors.New("audit logs are not enabled")
	}
	store := h.audit.Store()
	summary, err := store.NodeSummary(ctx, binding.ID, nodeID)
	if err != nil {
		return nil, audit.Page{}, err
	}
	page, err := store.Query(ctx, audit.Filter{TailnetID: binding.ID, TargetIDs: []string{nodeID}, Limit: limit})
	return summary, page, err
}
