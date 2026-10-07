package mcpserver

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rajsinghtech/tsflow/backend/internal/config"
	"github.com/rajsinghtech/tsflow/backend/internal/handlers"
	"github.com/rajsinghtech/tsflow/backend/internal/services"
)

func init() {
	gin.SetMode(gin.TestMode)
}

func TestMCPDisabledByDefault(t *testing.T) {
	t.Setenv("TSFLOW_MCP_ENABLED", "")
	t.Setenv("TAILSCALE_API_KEY", "key")
	if config.Load().MCPEnabled {
		t.Fatal("TSFLOW_MCP_ENABLED defaults to on")
	}

	router := gin.New()
	Mount(router, Options{Enabled: false, Handlers: handlers.NewHandlers(nil, nil, nil, "test")})
	server := httptest.NewServer(router)
	defer server.Close()

	resp, err := server.Client().Post(server.URL+"/mcp", "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("disabled /mcp = %d %s", resp.StatusCode, body)
	}

	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "v1"}, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err = client.Connect(ctx, &mcp.StreamableClientTransport{
		Endpoint:             server.URL + "/mcp",
		HTTPClient:           server.Client(),
		DisableStandaloneSSE: true,
	}, nil)
	if err == nil {
		t.Fatal("MCP client connected while the server was disabled")
	}
}

func TestMCPClientEnforcesTailnetAndDeviceScope(t *testing.T) {
	store := newStore(t)
	reg, err := services.NewRegistry(context.Background(), []config.TailnetSpec{
		{ID: "alpha", Name: "Alpha", APIURL: "https://example.test", APIKey: "k"},
		{ID: "beta", Name: "Beta", APIURL: "https://example.test", APIKey: "k"},
	}, store, services.DefaultPollerConfig())
	if err != nil {
		t.Fatal(err)
	}
	alpha, _ := reg.Get("alpha")
	alpha.Poller.GetDeviceCache().Update([]services.Device{
		{ID: "ada", Hostname: "ada-laptop", Name: "ada-laptop.alpha.example", User: "ada@example.com", Addresses: []string{"100.64.0.8"}, Tags: []string{"tag:eng"}},
		{ID: "bob", Hostname: "bob-phone", Name: "bob-phone.alpha.example", User: "bob@example.com", Addresses: []string{"100.64.0.9"}, Tags: []string{"tag:ops"}},
	})
	beta, _ := reg.Get("beta")
	beta.Poller.GetDeviceCache().Update([]services.Device{{
		ID: "beta-node", Hostname: "beta-node", User: "ada@example.com", Addresses: []string{"100.64.1.8"},
	}})
	base := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	seedPair(t, store, "alpha", base, "ada", "bob", "virtual", 10)
	seedPair(t, store, "alpha", base, "bob", "ada", "virtual", 90)
	seedPair(t, store, "beta", base, "beta-node", "ada", "virtual", 1000)

	h := handlers.NewHandlers(nil, store, nil, "test")
	h.UseRegistry(reg)
	router := gin.New()
	Mount(router, Options{
		Enabled:  true,
		Handlers: h,
		Version:  "test",
		Access: config.Access{
			Enabled:         true,
			Mode:            config.AccessModeHeader,
			Grants:          config.AccessGrantsRequired,
			TrustedPrefixes: []netip.Prefix{netip.MustParsePrefix("127.0.0.1/32"), netip.MustParsePrefix("::1/128")},
			GroupsHeader:    "X-Tsflow-Groups",
			GroupGrants: map[string]config.Grant{
				"group:eng": {Tailnets: []string{"alpha"}},
			},
			Autoscope: config.AccessAutoscopeUser,
		},
	})
	server := httptest.NewServer(router)
	defer server.Close()

	denied, err := server.Client().Post(server.URL+"/mcp", "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	defer denied.Body.Close()
	if denied.StatusCode != http.StatusForbidden {
		body, _ := io.ReadAll(denied.Body)
		t.Fatalf("unauthenticated /mcp = %d %s", denied.StatusCode, body)
	}

	session := connectMCP(t, server, map[string]string{
		"Tailscale-User-Login": "ada@example.com",
		"Tailscale-User-Name":  "Ada",
		"X-Tsflow-Groups":      "group:eng",
	})
	defer session.Close()

	tools, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, tool := range tools.Tools {
		names[tool.Name] = true
		if tool.Annotations == nil || !tool.Annotations.ReadOnlyHint {
			t.Fatalf("tool %s is not marked read-only", tool.Name)
		}
	}
	for _, name := range []string{
		"list_tailnets", "search_devices", "get_device", "top_talkers", "top_pairs",
		"flows_between", "device_peers", "device_timeline", "new_connections", "stats_overview",
	} {
		if !names[name] {
			t.Fatalf("missing tool %s in %+v", name, names)
		}
	}

	var tailnets listTailnetsOut
	callTool(t, session, "list_tailnets", map[string]any{}, &tailnets)
	if len(tailnets.Tailnets) != 1 || tailnets.Tailnets[0].ID != "alpha" {
		t.Fatalf("tailnets = %+v", tailnets.Tailnets)
	}

	var devices searchDevicesOut
	callTool(t, session, "search_devices", map[string]any{"tailnet": "alpha", "query": ""}, &devices)
	if devices.Scope != "mine" || devices.Count != 1 || devices.Devices[0].ID != "ada" {
		t.Fatalf("default devices = %+v", devices)
	}
	var everyone searchDevicesOut
	callTool(t, session, "search_devices", map[string]any{"tailnet": "alpha", "scope": "all"}, &everyone)
	if everyone.Scope != "all" || everyone.Count != 2 {
		t.Fatalf("scope=all devices = %+v", everyone)
	}
	saw := map[string]bool{}
	for _, device := range everyone.Devices {
		saw[device.ID] = true
		if device.ID == "beta-node" {
			t.Fatal("scope=all returned a device from another tailnet")
		}
	}
	if !saw["ada"] || !saw["bob"] {
		t.Fatalf("scope=all devices = %+v", everyone.Devices)
	}

	if _, isErr, _ := callToolRaw(t, session, "get_device", map[string]any{"tailnet": "alpha", "device": "bob-phone"}); !isErr {
		t.Fatal("default get_device returned bob's phone")
	}
	var bob getDeviceOut
	callTool(t, session, "get_device", map[string]any{"tailnet": "alpha", "device": "bob-phone", "scope": "all"}, &bob)
	if bob.Device.ID != "bob" || bob.Scope != "all" {
		t.Fatalf("scope=all bob = %+v", bob)
	}
	if _, isErr, _ := callToolRaw(t, session, "search_devices", map[string]any{"tailnet": "beta", "scope": "all"}); !isErr {
		t.Fatal("beta tailnet was accepted with scope=all")
	}

	var talkers talkersOut
	callTool(t, session, "top_talkers", map[string]any{
		"tailnet": "alpha",
		"start":   base.Format(time.RFC3339),
		"end":     base.Add(time.Minute).Format(time.RFC3339),
	}, &talkers)
	for _, talker := range talkers.Talkers {
		if talker.NodeID == "bob" || talker.NodeID == "beta-node" {
			t.Fatalf("talkers include out-of-scope device %+v", talkers.Talkers)
		}
	}
	if len(talkers.Talkers) != 1 || talkers.Talkers[0].NodeID != "ada" {
		t.Fatalf("talkers = %+v", talkers.Talkers)
	}
}

func connectMCP(t *testing.T, server *httptest.Server, headers map[string]string) *mcp.ClientSession {
	t.Helper()
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "v1"}, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{
		Endpoint:             server.URL + "/mcp",
		HTTPClient:           &http.Client{Transport: headerTransport{base: server.Client().Transport, header: headers}},
		DisableStandaloneSSE: true,
	}, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	return session
}

type headerTransport struct {
	base   http.RoundTripper
	header map[string]string
}

func (h headerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	clone := req.Clone(req.Context())
	for key, value := range h.header {
		clone.Header.Set(key, value)
	}
	base := h.base
	if base == nil {
		base = http.DefaultTransport
	}
	return base.RoundTrip(clone)
}

func callTool(t *testing.T, session *mcp.ClientSession, name string, args map[string]any, dest any) {
	t.Helper()
	res, isErr, text := callToolRaw(t, session, name, args)
	if isErr {
		t.Fatalf("%s error: %s", name, text)
	}
	raw, err := json.Marshal(res.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, dest); err != nil {
		t.Fatalf("decode %s: %v body=%s", name, err, raw)
	}
}

func callToolRaw(t *testing.T, session *mcp.ClientSession, name string, args map[string]any) (*mcp.CallToolResult, bool, string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	res, err := session.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s call: %v", name, err)
	}
	var text string
	for _, content := range res.Content {
		if item, ok := content.(*mcp.TextContent); ok {
			text += item.Text
		}
	}
	return res, res.IsError, text
}
