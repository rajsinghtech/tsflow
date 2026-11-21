package mcpserver

import (
	"context"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/rajsinghtech/tsflow/backend/internal/audit"
	"github.com/rajsinghtech/tsflow/backend/internal/config"
	"github.com/rajsinghtech/tsflow/backend/internal/handlers"
	"github.com/rajsinghtech/tsflow/backend/internal/services"
)

func auditToolNames(t *testing.T, h *handlers.Handlers) map[string]bool {
	t.Helper()
	router := gin.New()
	Mount(router, Options{Enabled: true, Handlers: h})
	server := httptest.NewServer(router)
	t.Cleanup(server.Close)
	session := connectMCP(t, server, nil)
	t.Cleanup(func() { session.Close() })
	res, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, tool := range res.Tools {
		names[tool.Name] = true
	}
	return names
}

func TestAuditToolsHiddenWhenAuditIsOff(t *testing.T) {
	names := auditToolNames(t, handlers.NewHandlers(nil, nil, nil, "test"))
	if names["audit_search"] || names["user_last_login"] || names["audit_overview"] || names["device_audit"] {
		t.Fatalf("audit tools listed while audit is off: %v", names)
	}
}

func TestAuditToolsListedAndSearch(t *testing.T) {
	store, err := audit.Open(filepath.Join(t.TempDir(), "audit.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	if err := store.Init(context.Background()); err != nil {
		t.Fatal(err)
	}
	at := time.Now().UTC().Add(-2 * time.Hour).Format(time.RFC3339Nano)
	events, err := audit.ParseLine([]byte(`{"eventTime":"` + at + `","type":"CONFIG","origin":"NODE","actor":{"id":"u1","type":"USER","loginName":"ada@example.com"},"target":{"id":"nAAA","name":"ada-laptop","type":"NODE"},"action":"LOGIN"}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Insert(context.Background(), "default", "test", events, nil); err != nil {
		t.Fatal(err)
	}
	mgr, err := services.NewAuditManager(context.Background(), config.Audit{Retention: config.DefaultAuditRetention}, nil, nil, store)
	if err != nil {
		t.Fatal(err)
	}
	h := handlers.NewHandlers(nil, nil, nil, "test")
	h.UseAudit(mgr, handlers.NewAuditGate(true, nil))

	names := auditToolNames(t, h)
	if !names["audit_search"] || !names["user_last_login"] || !names["audit_overview"] || !names["device_audit"] {
		t.Fatalf("audit tools missing: %v", names)
	}

	router := gin.New()
	Mount(router, Options{Enabled: true, Handlers: h})
	server := httptest.NewServer(router)
	defer server.Close()
	session := connectMCP(t, server, nil)
	defer session.Close()
	var out auditSearchOut
	callTool(t, session, "audit_search", map[string]any{"event": []string{"NODE.LOGIN"}}, &out)
	if len(out.Events) != 1 || out.Events[0].Actor != "ada@example.com" || out.Events[0].Event != "NODE.LOGIN" || out.Events[0].Category != "logins" || out.Events[0].Label != "Device login" {
		t.Fatalf("audit_search = %+v", out)
	}
	var byCat auditSearchOut
	callTool(t, session, "audit_search", map[string]any{"category": []string{"keys"}}, &byCat)
	if len(byCat.Events) != 0 {
		t.Fatalf("category filter = %+v", byCat)
	}
	var dev deviceAuditOut
	callTool(t, session, "device_audit", map[string]any{"nodeId": "nAAA"}, &dev)
	if dev.Summary == nil || dev.Summary.LastLogin == nil || len(dev.Events) != 1 {
		t.Fatalf("device_audit = %+v", dev)
	}
	var ov auditOverviewOut
	callTool(t, session, "audit_overview", map[string]any{}, &ov)
	if ov.Totals["logins"] != 1 || len(ov.Busiest) != 1 {
		t.Fatalf("audit_overview = %+v", ov)
	}
}
