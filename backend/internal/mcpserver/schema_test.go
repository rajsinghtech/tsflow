package mcpserver

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rajsinghtech/tsflow/backend/internal/handlers"
)

// scope, sort, and trafficTypes only accept fixed values, so the advertised
// schema says so and a client can pick from them instead of guessing.
func TestToolSchemasListEnums(t *testing.T) {
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
	type prop struct {
		Enum  []string `json:"enum"`
		Items *struct {
			Enum []string `json:"enum"`
		} `json:"items"`
	}
	checked := map[string]bool{}
	for _, tool := range res.Tools {
		raw, _ := json.Marshal(tool.InputSchema)
		var schema struct {
			Properties map[string]prop `json:"properties"`
		}
		if err := json.Unmarshal(raw, &schema); err != nil {
			t.Fatal(err)
		}
		if p, ok := schema.Properties["scope"]; ok {
			checked["scope"] = true
			if !slices.Equal(p.Enum, []string{"mine", "all"}) {
				t.Errorf("%s scope enum = %v", tool.Name, p.Enum)
			}
		}
		if p, ok := schema.Properties["sort"]; ok {
			checked["sort"] = true
			if !slices.Equal(p.Enum, []string{"bytes", "flows"}) {
				t.Errorf("%s sort enum = %v", tool.Name, p.Enum)
			}
		}
		if p, ok := schema.Properties["trafficTypes"]; ok {
			checked["trafficTypes"] = true
			if p.Items == nil || !slices.Equal(p.Items.Enum, []string{"virtual", "subnet", "exit", "physical"}) {
				t.Errorf("%s trafficTypes items = %+v", tool.Name, p.Items)
			}
		}
	}
	for _, name := range []string{"scope", "sort", "trafficTypes"} {
		if !checked[name] {
			t.Errorf("no tool has a %s argument", name)
		}
	}

	// A value outside the enum is still a tool error, not a crash.
	call, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "top_talkers", Arguments: map[string]any{"sort": "packets"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !call.IsError {
		t.Fatal("sort=packets accepted")
	}
	text := call.Content[0].(*mcp.TextContent).Text
	if !strings.Contains(text, "sort") {
		t.Fatalf("error = %q", text)
	}
}
