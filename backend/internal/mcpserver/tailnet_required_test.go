package mcpserver

import (
	"context"
	"strings"
	"testing"

	"github.com/rajsinghtech/tsflow/backend/internal/access"
	"github.com/rajsinghtech/tsflow/backend/internal/config"
	"github.com/rajsinghtech/tsflow/backend/internal/handlers"
	"github.com/rajsinghtech/tsflow/backend/internal/services"
)

func TestMissingTailnetNamesTheToolArgument(t *testing.T) {
	store := newStore(t)
	reg, err := services.NewRegistry(context.Background(), []config.TailnetSpec{
		{ID: "alpha", Name: "Alpha", APIURL: "https://example.test", APIKey: "k"},
		{ID: "beta", Name: "Beta", APIURL: "https://example.test", APIKey: "k"},
	}, store, services.DefaultPollerConfig())
	if err != nil {
		t.Fatal(err)
	}
	h := handlers.NewHandlers(nil, store, nil, "test")
	h.UseRegistry(reg)
	svc := New(h, "test")

	_, err = svc.searchDevices(context.Background(), Viewer{Ident: access.Identity{Allow: access.Allow{All: true}}}, searchDevicesIn{})
	if err == nil {
		t.Fatal("missing tailnet was accepted")
	}
	msg := err.Error()
	if strings.Contains(msg, "query parameter") {
		t.Fatalf("error = %q, mentions a query parameter", msg)
	}
	if !strings.Contains(msg, "tailnet is required") || !strings.Contains(msg, "alpha, beta") {
		t.Fatalf("error = %q, want the argument name and valid ids", msg)
	}
}
