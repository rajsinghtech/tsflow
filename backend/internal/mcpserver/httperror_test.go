package mcpserver

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/rajsinghtech/tsflow/backend/internal/config"
	"github.com/rajsinghtech/tsflow/backend/internal/handlers"
	"github.com/rajsinghtech/tsflow/backend/internal/middleware"
)

type rpcErrResp struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Error   struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Data    struct {
			HTTPStatus int `json:"httpStatus"`
		} `json:"data"`
	} `json:"error"`
}

func postMCP(t *testing.T, url, body string, headers map[string]string) (*http.Response, rpcErrResp, string) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, url+"/mcp", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var out rpcErrResp
	_ = json.Unmarshal(raw, &out)
	return resp, out, string(raw)
}

// MCP clients treat a non-JSON-RPC body as a transport failure, so the 429
// from the rate limiter must still be a JSON-RPC error carrying the request id.
func TestMCPRateLimitIsJSONRPCError(t *testing.T) {
	limit := middleware.DefaultRateLimitConfig()
	limit.RequestsPerMinute = 1
	router := gin.New()
	Mount(router, Options{
		Enabled:   true,
		Handlers:  handlers.NewHandlers(nil, nil, nil, "test"),
		RateLimit: middleware.RateLimitMiddleware(limit),
	})
	server := httptest.NewServer(router)
	defer server.Close()

	call := `{"jsonrpc":"2.0","id":7,"method":"tools/list","params":{}}`
	first, _, firstRaw := postMCP(t, server.URL, call, nil)
	if first.StatusCode == http.StatusTooManyRequests {
		t.Fatalf("first call limited: %s", firstRaw)
	}
	resp, out, raw := postMCP(t, server.URL, call, nil)
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("status = %d %s, want 429", resp.StatusCode, raw)
	}
	if resp.Header.Get("Retry-After") == "" {
		t.Fatal("Retry-After header lost")
	}
	if out.JSONRPC != "2.0" || string(out.ID) != "7" || out.Error.Code != rpcServerError || out.Error.Data.HTTPStatus != 429 {
		t.Fatalf("body = %s, want a JSON-RPC error for id 7", raw)
	}
	if !strings.Contains(out.Error.Message, "rate limit exceeded") {
		t.Fatalf("message = %q", out.Error.Message)
	}
}

func TestMCPAccessDeniedIsJSONRPCError(t *testing.T) {
	router := gin.New()
	Mount(router, Options{
		Enabled:  true,
		Handlers: handlers.NewHandlers(nil, nil, nil, "test"),
		Access: config.Access{
			Enabled:         true,
			Mode:            config.AccessModeHeader,
			Grants:          config.AccessGrantsRequired,
			TrustedPrefixes: []netip.Prefix{netip.MustParsePrefix("127.0.0.1/32"), netip.MustParsePrefix("::1/128")},
			GroupsHeader:    "X-Tsflow-Groups",
			GroupGrants:     map[string]config.Grant{"group:eng": {Tailnets: []string{"alpha"}}},
		},
	})
	server := httptest.NewServer(router)
	defer server.Close()

	resp, out, raw := postMCP(t, server.URL, `{"jsonrpc":"2.0","id":"abc","method":"tools/list"}`,
		map[string]string{"Tailscale-User-Login": "ada@example.com"})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d %s, want 403", resp.StatusCode, raw)
	}
	if string(out.ID) != `"abc"` || out.Error.Message == "" || out.Error.Data.HTTPStatus != 403 {
		t.Fatalf("body = %s, want a JSON-RPC error for id abc", raw)
	}

	// A body without a usable id still gets a valid error with a null id.
	resp, out, raw = postMCP(t, server.URL, `not json`, nil)
	if resp.StatusCode != http.StatusForbidden || string(out.ID) != "null" || out.JSONRPC != "2.0" {
		t.Fatalf("garbage body = %d %s", resp.StatusCode, raw)
	}
}
