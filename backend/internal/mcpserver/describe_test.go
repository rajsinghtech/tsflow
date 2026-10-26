package mcpserver

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/rajsinghtech/tsflow/backend/internal/handlers"
)

// MCP includes exit traffic and looks back 24h by default, while the UI shows
// virtual + subnet and the New tab looks back 7d. The defaults stay, but an
// assistant has to be told, or its numbers silently differ from the screen.
func TestToolDescriptionsExplainUIDefaults(t *testing.T) {
	router := gin.New()
	Mount(router, Options{Enabled: true, Handlers: handlers.NewHandlers(nil, nil, nil, "test")})
	server := httptest.NewServer(router)
	defer server.Close()
	session := connectMCP(t, server, nil)
	defer session.Close()

	res, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	seenTypes, seenLookback := 0, false
	for _, tool := range res.Tools {
		raw, _ := json.Marshal(tool.InputSchema)
		var schema struct {
			Properties map[string]struct {
				Description string `json:"description"`
			} `json:"properties"`
		}
		_ = json.Unmarshal(raw, &schema)
		if p, ok := schema.Properties["trafficTypes"]; ok {
			seenTypes++
			if !strings.Contains(p.Description, "Omitted means virtual, subnet, and exit") ||
				!strings.Contains(p.Description, "UI shows virtual and subnet by default") {
				t.Errorf("%s trafficTypes description = %q", tool.Name, p.Description)
			}
		}
		if p, ok := schema.Properties["lookback"]; ok {
			seenLookback = true
			if !strings.Contains(p.Description, "default to 7d") {
				t.Errorf("%s lookback description = %q", tool.Name, p.Description)
			}
		}
	}
	if seenTypes < 7 || !seenLookback {
		t.Fatalf("trafficTypes on %d tools, lookback seen %v", seenTypes, seenLookback)
	}
	init := session.InitializeResult()
	if init == nil || !strings.Contains(init.Instructions, "virtual and subnet") {
		t.Fatalf("instructions do not mention the UI default")
	}
}
