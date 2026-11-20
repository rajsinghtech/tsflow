package mcpserver

import (
	"context"
	"errors"
	"fmt"
	"log"
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
	FailedOnly bool     `json:"failedOnly,omitempty" jsonschema:"Only events that carry an error."`
	Query      string   `json:"query,omitempty" jsonschema:"Free text matched against actor, target, event name and details."`
	Limit      int      `json:"limit,omitempty" jsonschema:"Maximum events. Defaults to 50, clamped to 200."`
	Cursor     string   `json:"cursor,omitempty" jsonschema:"nextCursor from a previous call, to read the next (older) page."`
}

type auditEventOut struct {
	ID           int64  `json:"id"`
	Time         string `json:"time"`
	Event        string `json:"event"`
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

var (
	auditSearchInSchema   = inputSchema[auditSearchIn]()
	userLastLoginInSchema = inputSchema[userLastLoginIn]()
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
