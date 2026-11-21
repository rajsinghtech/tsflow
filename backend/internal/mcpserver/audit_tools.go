package mcpserver

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sort"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rajsinghtech/tsflow/backend/internal/audit"
	"github.com/rajsinghtech/tsflow/backend/internal/handlers"
)

const (
	auditDefaultWindow = 30 * 24 * time.Hour
	auditDefaultLimit  = 50
	auditMaxLimit      = 200
)

type auditSearchIn struct {
	Tailnet    string   `json:"tailnet,omitempty" jsonschema:"Tailnet id. Omit when one tailnet is configured, or when the id default is permitted."`
	Start      string   `json:"start,omitempty" jsonschema:"Window start. RFC3339, or relative such as now-7d or 30d. Defaults to 30 days before end."`
	End        string   `json:"end,omitempty" jsonschema:"Window end. Defaults to now."`
	Actor      []string `json:"actor,omitempty" jsonschema:"Who made the change: a login or actor id, or ~text to match part of a login or display name."`
	Target     []string `json:"target,omitempty" jsonschema:"What was changed: a target id, or part of a target name such as a device name or login."`
	Event      []string `json:"event,omitempty" jsonschema:"Event names in TARGET.ACTION[.PROPERTY] form such as NODE.LOGIN, ADMIN_CONSOLE.LOGIN, TAILNET.UPDATE.ACL or NODE.UPDATE.ACL_TAGS. A trailing * matches a prefix, such as NODE.*."`
	Category   []string `json:"category,omitempty" jsonschema:"Event categories: logins, policy, devices, users, keys, settings, dns, sharing, integrations, tailnet_lock, billing, pam, failed_requests, other."`
	ActorType  []string `json:"actorType,omitempty" jsonschema:"Actor types such as USER, NODE, AUTOMATED_WORKER (the Tailscale service), OAUTH_CLIENT, SCIM."`
	Ephemeral  bool     `json:"ephemeral,omitempty" jsonschema:"Only events about ephemeral nodes."`
	FailedOnly bool     `json:"failedOnly,omitempty" jsonschema:"Only failed changes and denied API requests."`
	Query      string   `json:"query,omitempty" jsonschema:"Free text matched against actor, target, event name and details."`
	Limit      int      `json:"limit,omitempty" jsonschema:"Maximum events. Defaults to 50, clamped to 200."`
	Cursor     string   `json:"cursor,omitempty" jsonschema:"nextCursor from a previous call, to read the next (older) page."`
}

type auditEventOut struct {
	ID           int64  `json:"id"`
	Time         string `json:"time"`
	Event        string `json:"event"`
	Label        string `json:"label"`
	Category     string `json:"category"`
	Origin       string `json:"origin,omitempty"`
	Actor        string `json:"actor"`
	ActorType    string `json:"actorType,omitempty"`
	Target       string `json:"target"`
	TargetID     string `json:"targetId,omitempty"`
	Old          string `json:"old,omitempty"`
	New          string `json:"new,omitempty"`
	Details      string `json:"details,omitempty"`
	Error        string `json:"error,omitempty"`
	EventGroupID string `json:"eventGroupID,omitempty"`
}

type auditSearchOut struct {
	Tailnet    string          `json:"tailnet"`
	Start      string          `json:"start"`
	End        string          `json:"end"`
	Events     []auditEventOut `json:"events"`
	NextCursor string          `json:"nextCursor,omitempty"`
}

type userLastLoginIn struct {
	Tailnet string `json:"tailnet,omitempty" jsonschema:"Tailnet id. Omit when one tailnet is configured, or when the id default is permitted."`
	Login   string `json:"login" jsonschema:"The user's login name, such as alice@example.com."`
	Live    *bool  `json:"live,omitempty" jsonschema:"Also ask the Tailscale users and devices APIs for lastSeen. Defaults to true."`
}

type userLastLoginOut struct {
	Tailnet      string             `json:"tailnet"`
	Login        string             `json:"login"`
	Summary      audit.UserSummary  `json:"summary"`
	Live         handlers.AuditLive `json:"live"`
	Explanation  string             `json:"explanation"`
	RecentLogins []auditEventOut    `json:"recentLogins"`
}

const lastLoginExplanation = "lastNodeLogin is the last time the user signed in to or re-authenticated a device; devices only do this when their key expires or the user logs in again, so it can be months old for someone who is online daily. lastConsoleLogin and lastWebLogin are browser sign-ins to the admin console or the web interface. live.user.lastSeen (users API) is the later of the last time any of the user's devices was connected and the last time they authenticated anywhere, which is the best answer to 'last active'. Audit history only goes back as far as stored events (summary.firstSeen and the retention setting)."

type auditOverviewIn struct {
	Tailnet string   `json:"tailnet,omitempty" jsonschema:"Tailnet id. Omit when one tailnet is configured, or when the id default is permitted."`
	Start   string   `json:"start,omitempty" jsonschema:"Window start. RFC3339, or relative such as now-7d. Defaults to 30 days before end."`
	End     string   `json:"end,omitempty" jsonschema:"Window end. Defaults to now."`
	Actor   []string `json:"actor,omitempty" jsonschema:"Limit to changes by these actors (login, actor id, or ~text)."`
}

type auditOverviewOut struct {
	Tailnet  string                `json:"tailnet"`
	Start    string                `json:"start"`
	End      string                `json:"end"`
	Totals   map[string]int64      `json:"totalsByCategory"`
	Failed   int64                 `json:"failed"`
	Busiest  []dayCount            `json:"busiestBuckets"`
	Policy   []audit.PolicyChange  `json:"policyChanges"`
	Keys     []audit.KeyLifecycle  `json:"keys"`
	Settings []audit.SettingChange `json:"settings"`
}

type dayCount struct {
	Start string `json:"start"`
	Count int64  `json:"count"`
}

type deviceAuditIn struct {
	Tailnet string `json:"tailnet,omitempty" jsonschema:"Tailnet id. Omit when one tailnet is configured, or when the id default is permitted."`
	NodeID  string `json:"nodeId" jsonschema:"The device's node id (from search_devices or get_device)."`
	Limit   int    `json:"limit,omitempty" jsonschema:"Recent events to include. Defaults to 20, clamped to 200."`
}

type deviceAuditOut struct {
	Tailnet string             `json:"tailnet"`
	Summary *audit.NodeSummary `json:"summary"`
	Events  []auditEventOut    `json:"events"`
}

var (
	auditSearchInSchema   = inputSchema[auditSearchIn]()
	userLastLoginInSchema = inputSchema[userLastLoginIn]()
	auditOverviewInSchema = inputSchema[auditOverviewIn]()
	deviceAuditInSchema   = inputSchema[deviceAuditIn]()
)

// addAuditTools registers the audit tools when this viewer may read audit
// data. Viewers without the permission never see them listed.
func (s *Service) addAuditTools(server *mcp.Server, v Viewer) {
	if s == nil || s.h == nil || !s.h.AuditAllowed(v.Restricted, v.Ident) {
		return
	}
	mcp.AddTool(server, &mcp.Tool{
		Name:        "audit_search",
		Description: "Search Tailscale configuration audit logs: who changed what and when (logins, device approvals, tag and route changes, policy file edits, keys, settings). Newest first. Events use TARGET.ACTION[.PROPERTY] names. Use nextCursor to page back in time.",
		InputSchema: auditSearchInSchema,
		Annotations: readOnly("Audit log search"),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in auditSearchIn) (*mcp.CallToolResult, auditSearchOut, error) {
		out, err := s.auditSearch(ctx, v, in)
		return nil, out, err
	})
	mcp.AddTool(server, &mcp.Tool{
		Name:        "user_last_login",
		Description: "When did a user last log in? Returns the newest device login, admin console login and web login from the audit log, the live users-API lastSeen, the user's devices with their lastSeen, and an explanation of what each one means.",
		InputSchema: userLastLoginInSchema,
		Annotations: readOnly("User last login"),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in userLastLoginIn) (*mcp.CallToolResult, userLastLoginOut, error) {
		out, err := s.userLastLogin(ctx, v, in)
		return nil, out, err
	})
	mcp.AddTool(server, &mcp.Tool{
		Name:        "audit_overview",
		Description: "Summary of tailnet configuration changes over a window: counts by category, failed changes, the busiest days, every policy file edit with lines added and removed, the lifecycle of each key (API access tokens, auth keys, OAuth clients: created, revoked, expired, tags and flags), and the latest change to each tailnet setting.",
		InputSchema: auditOverviewInSchema,
		Annotations: readOnly("Audit overview"),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in auditOverviewIn) (*mcp.CallToolResult, auditOverviewOut, error) {
		out, err := s.auditOverview(ctx, v, in)
		return nil, out, err
	})
	mcp.AddTool(server, &mcp.Tool{
		Name:        "device_audit",
		Description: "One device's configuration history from the audit log: when it was added and by whom, approval, last login and logout, key expiry (time, disabled, expired), current tags and routes, Tailnet Lock signing, whether it is ephemeral or deleted, plus its newest audit events.",
		InputSchema: deviceAuditInSchema,
		Annotations: readOnly("Device audit history"),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in deviceAuditIn) (*mcp.CallToolResult, deviceAuditOut, error) {
		out, err := s.deviceAudit(ctx, v, in)
		return nil, out, err
	})
}

func (s *Service) auditOverview(ctx context.Context, v Viewer, in auditOverviewIn) (auditOverviewOut, error) {
	binding, err := s.bind(v, in.Tailnet)
	if err != nil {
		return auditOverviewOut{}, err
	}
	now := time.Now().UTC()
	end := now
	if strings.TrimSpace(in.End) != "" {
		if end, err = parseTime(in.End, now); err != nil {
			return auditOverviewOut{}, fmt.Errorf("end: %w", err)
		}
	}
	start := end.Add(-auditDefaultWindow)
	if strings.TrimSpace(in.Start) != "" {
		if start, err = parseTime(in.Start, now); err != nil {
			return auditOverviewOut{}, fmt.Errorf("start: %w", err)
		}
	}
	if !start.Before(end) {
		return auditOverviewOut{}, errors.New("start must be before end")
	}
	ins, err := s.h.AuditOverview(ctx, binding, audit.Filter{From: start, To: end, Actors: in.Actor}, 0)
	if err != nil {
		log.Printf("ERROR mcp audit_overview: %v", err)
		return auditOverviewOut{}, errors.New("failed to read audit logs")
	}
	out := auditOverviewOut{Tailnet: binding.ID, Start: start.Format(time.RFC3339), End: end.Format(time.RFC3339),
		Totals: map[string]int64{}, Policy: ins.Policy, Keys: ins.Keys, Settings: ins.Settings, Busiest: []dayCount{}}
	buckets := append([]audit.HistogramBucket(nil), ins.Histogram.Buckets...)
	for _, b := range buckets {
		out.Failed += b.Failed
		for cat, n := range b.Categories {
			out.Totals[string(cat)] += n
		}
	}
	sort.SliceStable(buckets, func(i, j int) bool { return buckets[i].Total > buckets[j].Total })
	for _, b := range buckets {
		if b.Total == 0 || len(out.Busiest) == 5 {
			break
		}
		out.Busiest = append(out.Busiest, dayCount{Start: b.Start.Format(time.RFC3339), Count: b.Total})
	}
	return out, nil
}

func (s *Service) deviceAudit(ctx context.Context, v Viewer, in deviceAuditIn) (deviceAuditOut, error) {
	nodeID := strings.TrimSpace(in.NodeID)
	if nodeID == "" {
		return deviceAuditOut{}, errors.New("nodeId is required")
	}
	binding, err := s.bind(v, in.Tailnet)
	if err != nil {
		return deviceAuditOut{}, err
	}
	limit := in.Limit
	if limit <= 0 {
		limit = 20
	}
	if limit > auditMaxLimit {
		limit = auditMaxLimit
	}
	summary, page, err := s.h.AuditNode(ctx, binding, nodeID, limit)
	if err != nil {
		log.Printf("ERROR mcp device_audit: %v", err)
		return deviceAuditOut{}, errors.New("failed to read audit logs")
	}
	out := deviceAuditOut{Tailnet: binding.ID, Summary: summary, Events: make([]auditEventOut, 0, len(page.Events))}
	for _, e := range page.Events {
		out.Events = append(out.Events, toAuditEventOut(e))
	}
	return out, nil
}

func (s *Service) auditSearch(ctx context.Context, v Viewer, in auditSearchIn) (auditSearchOut, error) {
	binding, err := s.bind(v, in.Tailnet)
	if err != nil {
		return auditSearchOut{}, err
	}
	now := time.Now().UTC()
	end := now
	if strings.TrimSpace(in.End) != "" {
		if end, err = parseTime(in.End, now); err != nil {
			return auditSearchOut{}, fmt.Errorf("end: %w", err)
		}
	}
	start := end.Add(-auditDefaultWindow)
	if strings.TrimSpace(in.Start) != "" {
		if start, err = parseTime(in.Start, now); err != nil {
			return auditSearchOut{}, fmt.Errorf("start: %w", err)
		}
	}
	if end.Before(start) {
		return auditSearchOut{}, errors.New("end is before start")
	}
	limit := in.Limit
	if limit <= 0 {
		limit = auditDefaultLimit
	}
	if limit > auditMaxLimit {
		limit = auditMaxLimit
	}
	page, err := s.h.AuditSearch(ctx, binding, audit.Filter{
		From: start, To: end, Actors: in.Actor, Targets: in.Target, Events: in.Event,
		FailedOnly: in.FailedOnly, Query: in.Query, Cursor: in.Cursor, Limit: limit,
		Categories: in.Category, ActorTypes: in.ActorType, EphemeralOnly: in.Ephemeral,
	})
	if err != nil {
		if strings.Contains(err.Error(), "invalid cursor") {
			return auditSearchOut{}, errors.New("invalid cursor")
		}
		log.Printf("ERROR mcp audit_search: %v", err)
		return auditSearchOut{}, errors.New("failed to search audit logs")
	}
	out := auditSearchOut{
		Tailnet: binding.ID, Start: start.Format(time.RFC3339), End: end.Format(time.RFC3339),
		Events: make([]auditEventOut, 0, len(page.Events)), NextCursor: page.NextCursor,
	}
	for _, e := range page.Events {
		out.Events = append(out.Events, toAuditEventOut(e))
	}
	return out, nil
}

func (s *Service) userLastLogin(ctx context.Context, v Viewer, in userLastLoginIn) (userLastLoginOut, error) {
	login := strings.TrimSpace(in.Login)
	if login == "" {
		return userLastLoginOut{}, errors.New("login is required")
	}
	binding, err := s.bind(v, in.Tailnet)
	if err != nil {
		return userLastLoginOut{}, err
	}
	live := in.Live == nil || *in.Live
	resp, err := s.h.AuditUser(ctx, binding, login, audit.Filter{Events: []string{"NODE.LOGIN", "ADMIN_CONSOLE.LOGIN", "WEB_INTERFACE.LOGIN"}, Limit: 10}, live)
	if err != nil {
		log.Printf("ERROR mcp user_last_login: %v", err)
		return userLastLoginOut{}, errors.New("failed to read audit logs")
	}
	out := userLastLoginOut{
		Tailnet: binding.ID, Login: login, Summary: resp.Summary, Live: resp.Live,
		Explanation: lastLoginExplanation, RecentLogins: make([]auditEventOut, 0, len(resp.Timeline.Events)),
	}
	for _, e := range resp.Timeline.Events {
		out.RecentLogins = append(out.RecentLogins, toAuditEventOut(e))
	}
	return out, nil
}

// maxAuditValue keeps a policy file diff from filling an agent's context.
const maxAuditValue = 2000

func toAuditEventOut(e audit.Event) auditEventOut {
	actor := e.Actor.LoginName
	if actor == "" {
		actor = e.Actor.DisplayName
	}
	if actor == "" {
		actor = e.Actor.Type
	}
	target := e.Target.Name
	if target == "" {
		target = e.Target.ID
	}
	return auditEventOut{
		ID: e.ID, Time: e.EventTime.Format(time.RFC3339), Event: e.Event, Origin: e.Origin,
		Label: audit.Describe(e.Event).Label, Category: string(e.Category),
		Actor: actor, ActorType: e.Actor.Type, Target: target, TargetID: e.Target.ID,
		Old: clip(string(e.Old)), New: clip(string(e.New)), Details: e.ActionDetails, Error: e.Error,
		EventGroupID: e.EventGroupID,
	}
}

func clip(s string) string {
	if len(s) <= maxAuditValue {
		return s
	}
	return s[:maxAuditValue] + fmt.Sprintf("… (%d more bytes)", len(s)-maxAuditValue)
}
