package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/rajsinghtech/tsflow/backend/internal/access"
	"github.com/rajsinghtech/tsflow/backend/internal/audit"
	"github.com/rajsinghtech/tsflow/backend/internal/config"
	"github.com/rajsinghtech/tsflow/backend/internal/services"
)

func auditRouter(h *Handlers, ident *access.Identity) *gin.Engine {
	router := gin.New()
	api := router.Group("/api")
	if ident != nil {
		api.Use(func(c *gin.Context) { access.SetIdentity(c, *ident) })
	}
	api.GET("/whoami", h.WhoAmI)
	a := api.Group("/audit")
	a.GET("/status", h.GetAuditStatus)
	a.GET("/events", h.GetAuditEvents)
	a.GET("/events/:id", h.GetAuditEvent)
	a.GET("/groups/:groupId", h.GetAuditGroup)
	a.GET("/facets", h.GetAuditFacets)
	a.GET("/users/:login", h.GetAuditUser)
	a.GET("/nodes/:nodeId", h.GetAuditNode)
	return router
}

func auditTestHandlers(t *testing.T) (*Handlers, *audit.Store) {
	t.Helper()
	server := httptest.NewServer(tailnetHTTP(t, map[string]string{"alpha.example": "alpha-device", "beta.example": "beta-device"}))
	t.Cleanup(server.Close)
	specs := []config.TailnetSpec{
		{ID: "alpha", Name: "alpha.example", APIURL: server.URL, APIKey: "alpha-secret"},
		{ID: "beta", Name: "beta.example", APIURL: server.URL, APIKey: "beta-secret"},
	}
	h := registryHandlers(t, newAPIStore(t), specs)
	store, err := audit.Open(filepath.Join(t.TempDir(), "audit.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	if err := store.Init(context.Background()); err != nil {
		t.Fatal(err)
	}
	mgr, err := services.NewAuditManager(context.Background(), config.Audit{Retention: config.DefaultAuditRetention}, specs, nil, store)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Add(-time.Hour)
	var lines []string
	for i := 0; i < 12; i++ {
		at := now.Add(-time.Duration(i) * time.Hour)
		action, targetType, targetID := "LOGIN", "NODE", "nAAA"
		if i%3 == 1 {
			action, targetType, targetID = "LOGIN", "ADMIN_CONSOLE", ""
		}
		if i%3 == 2 {
			action, targetType, targetID = "UPDATE", "TAILNET", "alpha"
		}
		lines = append(lines, fmt.Sprintf(`{"eventTime":%q,"type":"CONFIG","eventGroupID":"op-%d","origin":"ADMIN_CONSOLE","actor":{"id":"u1","type":"USER","loginName":"ada@example.com","displayName":"Ada"},"target":{"id":%q,"name":"ada-laptop","type":%q,"property":%q},"action":%q,"old":"a","new":"b"}`,
			at.Format(time.RFC3339Nano), i/2, targetID, targetType, map[bool]string{true: "ACL"}[targetType == "TAILNET"], action))
	}
	var events []audit.Event
	for _, l := range lines {
		e, err := audit.ParseLine([]byte(l))
		if err != nil {
			t.Fatal(err)
		}
		events = append(events, e...)
	}
	if _, err := store.Insert(context.Background(), "alpha", "test", events, nil); err != nil {
		t.Fatal(err)
	}
	h.UseAudit(mgr, NewAuditGate(true, []string{"group:auditors", "lead@example.com"}))
	return h, store
}

func decode(t *testing.T, body []byte, v any) {
	t.Helper()
	if err := json.Unmarshal(body, v); err != nil {
		t.Fatalf("decode %s: %v", body, err)
	}
}

func TestAuditRoutesOffByDefault(t *testing.T) {
	h := NewHandlers(nil, nil, nil, "test")
	router := auditRouter(h, nil)
	code, _ := serve(router, http.MethodGet, "/api/audit/events")
	if code != http.StatusNotFound {
		t.Fatalf("events without audit: %d", code)
	}
	code, body := serve(router, http.MethodGet, "/api/audit/status")
	var status struct{ Enabled bool }
	decode(t, body, &status)
	if code != http.StatusOK || status.Enabled {
		t.Fatalf("status without audit: %d %s", code, body)
	}
	_, body = serve(router, http.MethodGet, "/api/whoami")
	if string(body) != `{"autoscope":"off"}` {
		t.Fatalf("whoami must not change when audit is off: %s", body)
	}
}

func TestAuditRoutesOpenServer(t *testing.T) {
	h, _ := auditTestHandlers(t)
	router := auditRouter(h, nil)

	code, body := serve(router, http.MethodGet, "/api/audit/events?tailnet=alpha&limit=5")
	if code != http.StatusOK {
		t.Fatalf("events: %d %s", code, body)
	}
	var page struct {
		Events     []audit.Event `json:"events"`
		NextCursor string        `json:"nextCursor"`
	}
	decode(t, body, &page)
	if len(page.Events) != 5 || page.NextCursor == "" {
		t.Fatalf("first page: %d events cursor=%q", len(page.Events), page.NextCursor)
	}
	code, body = serve(router, http.MethodGet, "/api/audit/events?tailnet=alpha&limit=5&cursor="+page.NextCursor)
	var next struct{ Events []audit.Event }
	decode(t, body, &next)
	if code != http.StatusOK || len(next.Events) != 5 || next.Events[0].ID == page.Events[4].ID {
		t.Fatalf("second page: %d %s", code, body)
	}
	code, _ = serve(router, http.MethodGet, "/api/audit/events?tailnet=alpha&cursor=bad")
	if code != http.StatusBadRequest {
		t.Fatalf("bad cursor: %d", code)
	}
	code, body = serve(router, http.MethodGet, "/api/audit/events?tailnet=alpha&event=ADMIN_CONSOLE.LOGIN")
	decode(t, body, &next)
	if code != http.StatusOK || len(next.Events) != 4 {
		t.Fatalf("event filter: %d %d", code, len(next.Events))
	}
	_, body = serve(router, http.MethodGet, "/api/audit/events?tailnet=beta")
	decode(t, body, &next)
	if len(next.Events) != 0 {
		t.Fatal("beta must not see alpha's events")
	}

	code, body = serve(router, http.MethodGet, fmt.Sprintf("/api/audit/events/%d?tailnet=alpha", page.Events[0].ID))
	var detail struct {
		Event audit.Event   `json:"event"`
		Group []audit.Event `json:"group"`
	}
	decode(t, body, &detail)
	if code != http.StatusOK || detail.Event.ID != page.Events[0].ID || len(detail.Group) != 2 {
		t.Fatalf("detail: %d %s", code, body)
	}
	code, _ = serve(router, http.MethodGet, fmt.Sprintf("/api/audit/events/%d?tailnet=beta", page.Events[0].ID))
	if code != http.StatusNotFound {
		t.Fatalf("event from another tailnet: %d", code)
	}
	code, _ = serve(router, http.MethodGet, "/api/audit/events/abc?tailnet=alpha")
	if code != http.StatusBadRequest {
		t.Fatalf("bad id: %d", code)
	}

	code, body = serve(router, http.MethodGet, "/api/audit/users/ada@example.com?tailnet=alpha&live=false")
	var user AuditUserResponse
	decode(t, body, &user)
	if code != http.StatusOK || user.Summary.LastNodeLogin == nil || user.Summary.LastConsoleLogin == nil || len(user.Timeline.Events) != 12 {
		t.Fatalf("user: %d %s", code, body)
	}

	code, body = serve(router, http.MethodGet, "/api/audit/nodes/nAAA?tailnet=alpha")
	decode(t, body, &next)
	if code != http.StatusOK || len(next.Events) != 4 {
		t.Fatalf("node history: %d %d", code, len(next.Events))
	}

	code, body = serve(router, http.MethodGet, "/api/audit/facets?tailnet=alpha")
	var facets audit.Facets
	decode(t, body, &facets)
	if code != http.StatusOK || facets.Total != 12 || len(facets.Events) != 3 {
		t.Fatalf("facets: %d %s", code, body)
	}
	code, body = serve(router, http.MethodGet, "/api/audit/facets?tailnet=alpha&targetId=nAAA")
	decode(t, body, &facets)
	if code != http.StatusOK || facets.Total != 4 {
		t.Fatalf("facets follow the filter: %d %s", code, body)
	}

	code, body = serve(router, http.MethodGet, "/api/audit/status")
	var status struct {
		Enabled, Permitted bool
		Tailnets           []AuditTailnetStatus
	}
	decode(t, body, &status)
	if code != http.StatusOK || !status.Enabled || !status.Permitted || len(status.Tailnets) != 2 || status.Tailnets[0].Stored.Events != 12 || status.Tailnets[0].Source.Backend != "off" {
		t.Fatalf("status: %d %s", code, body)
	}
	_, body = serve(router, http.MethodGet, "/api/whoami")
	if string(body) != `{"autoscope":"off","audit":true}` {
		t.Fatalf("whoami: %s", body)
	}
}

func TestAuditRoutesNeedViewerPermission(t *testing.T) {
	h, _ := auditTestHandlers(t)
	outsider := access.Identity{Login: "eve@example.com", Allow: access.Allow{All: true}}
	router := auditRouter(h, &outsider)
	code, _ := serve(router, http.MethodGet, "/api/audit/events?tailnet=alpha")
	if code != http.StatusForbidden {
		t.Fatalf("outsider: %d", code)
	}
	_, body := serve(router, http.MethodGet, "/api/audit/status")
	var status struct{ Enabled, Permitted bool }
	decode(t, body, &status)
	if !status.Enabled || status.Permitted {
		t.Fatalf("outsider status: %s", body)
	}

	var limited access.Allow
	limited.Add(config.Grant{Tailnets: []string{"beta"}})
	member := access.Identity{Login: "ada@example.com", Groups: []string{"group:auditors"}, Allow: limited}
	router = auditRouter(h, &member)
	code, _ = serve(router, http.MethodGet, "/api/audit/events?tailnet=beta")
	if code != http.StatusOK {
		t.Fatalf("group member: %d", code)
	}
	code, _ = serve(router, http.MethodGet, "/api/audit/events?tailnet=alpha")
	if code != http.StatusForbidden {
		t.Fatalf("audit permission must not widen tailnet grants: %d", code)
	}
	_, body = serve(router, http.MethodGet, "/api/whoami")
	var who struct{ Audit bool }
	decode(t, body, &who)
	if !who.Audit {
		t.Fatalf("whoami should report audit for a viewer: %s", body)
	}

	lead := access.Identity{Login: "LEAD@example.com", Allow: access.Allow{All: true}}
	code, _ = serve(auditRouter(h, &lead), http.MethodGet, "/api/audit/events?tailnet=alpha")
	if code != http.StatusOK {
		t.Fatalf("listed login: %d", code)
	}
}

func TestAuditGate(t *testing.T) {
	off := NewAuditGate(false, []string{"ada@example.com"})
	if off.Allows(false, access.Identity{}) {
		t.Fatal("a disabled gate allows nobody")
	}
	empty := NewAuditGate(true, nil)
	if !empty.Allows(false, access.Identity{}) {
		t.Fatal("an open server with audit on allows everyone")
	}
	if empty.Allows(true, access.Identity{Login: "ada@example.com"}) {
		t.Fatal("with access control on, an empty viewer list allows nobody")
	}
	g := NewAuditGate(true, []string{"ops"})
	if !g.Allows(true, access.Identity{Groups: []string{"group:ops"}}) {
		t.Fatal("a group may be listed without its prefix")
	}
}
