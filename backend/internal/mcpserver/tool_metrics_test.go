package mcpserver

import (
	"context"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rajsinghtech/tsflow/backend/internal/handlers"
)

type toolCall struct {
	tool   string
	failed bool
}

func TestToolObserverSeesCallsAndFoldsUnknownNames(t *testing.T) {
	var mu sync.Mutex
	var calls []toolCall
	router := gin.New()
	Mount(router, Options{
		Enabled:  true,
		Handlers: handlers.NewHandlers(nil, nil, nil, "test"),
		ToolObserver: func(tool string, d time.Duration, failed bool) {
			mu.Lock()
			calls = append(calls, toolCall{tool, failed})
			mu.Unlock()
		},
	})
	server := httptest.NewServer(router)
	defer server.Close()
	session := connectMCP(t, server, nil)
	defer session.Close()

	var who whoamiOut
	callTool(t, session, "whoami", nil, &who)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "no_such_tool_" + time.Now().Format("150405")}); err == nil {
		t.Fatal("unknown tool did not fail")
	}
	// A registered tool that returns an error is a failed call, not unknown.
	callToolRaw(t, session, "get_device", map[string]any{"device": "x"})

	mu.Lock()
	defer mu.Unlock()
	want := []toolCall{{"whoami", false}, {"unknown", true}, {"get_device", true}}
	if len(calls) != len(want) {
		t.Fatalf("calls = %+v, want %+v", calls, want)
	}
	for i := range want {
		if calls[i] != want[i] {
			t.Fatalf("calls = %+v, want %+v", calls, want)
		}
	}
}

func TestNoToolObserverByDefault(t *testing.T) {
	svc := New(handlers.NewHandlers(nil, nil, nil, "test"), "test")
	if svc.observe != nil {
		t.Fatal("tool observer set without Options.ToolObserver")
	}
}
