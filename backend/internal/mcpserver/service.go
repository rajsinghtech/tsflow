// Package mcpserver serves a read-only Model Context Protocol endpoint
// for the flow data TSFlow already stores.
package mcpserver

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rajsinghtech/tsflow/backend/internal/access"
	"github.com/rajsinghtech/tsflow/backend/internal/config"
	"github.com/rajsinghtech/tsflow/backend/internal/handlers"
)

const instructions = "Read-only TSFlow flow data. Tailnet allowlists match the REST API. " +
	"Identity autoscope is the default device view and can be cleared with scope=all. " +
	"Physical transport is excluded unless trafficTypes includes physical. DERP relays are labeled DERP relay. " +
	"Omitting trafficTypes includes exit traffic, while the TSFlow UI defaults to virtual and subnet; pass [virtual, subnet] to match the screen. " +
	"new_connections looks back 24h by default where the UI and REST use 7d. " +
	"Pass tailnet when more than one tailnet is configured. Windows default to the last hour and cannot exceed 7 days."

// Options configures the /mcp route.
type Options struct {
	Enabled  bool
	Access   config.Access
	WhoIs    access.WhoIsClient
	Handlers *handlers.Handlers
	Version  string
	// RateLimit, when set, runs before access checks on every /mcp request,
	// as it does for /api. Tool calls run the same store queries as /api.
	RateLimit gin.HandlerFunc
	// ToolObserver, when set, gets each tools/call with its duration and
	// whether it failed. A name no tool answered to is reported as unknown.
	ToolObserver func(tool string, d time.Duration, failed bool)
}

// Viewer is the identity the access middleware attached to one HTTP request.
// Restricted is false when access control is off, which shows every configured tailnet.
type Viewer struct {
	Restricted bool
	Ident      access.Identity
}

const (
	scopeMine = "mine"
	scopeAll  = "all"
)

// deviceFilter resolves the optional scope argument.
// An empty argument follows the viewer's autoscope: mine when a device
// scope is set, otherwise all. all always clears the device filter.
// Tailnet allowlists are applied separately and are not affected.
func deviceFilter(v Viewer, raw string) (*access.DeviceScope, string, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "":
		if v.Ident.DeviceScope != nil {
			return v.Ident.DeviceScope, scopeMine, nil
		}
		return nil, scopeAll, nil
	case scopeAll:
		return nil, scopeAll, nil
	case scopeMine:
		return v.Ident.DeviceScope, scopeMine, nil
	default:
		return nil, "", fmt.Errorf("scope must be mine or all")
	}
}

type viewerKey struct{}

// WithViewer stores the viewer on the request context read by the MCP handler.
func WithViewer(ctx context.Context, v Viewer) context.Context {
	return context.WithValue(ctx, viewerKey{}, v)
}

func viewerFrom(ctx context.Context) Viewer {
	v, _ := ctx.Value(viewerKey{}).(Viewer)
	return v
}

// Service is the read-only MCP server.
type Service struct {
	h       *handlers.Handlers
	version string
	schemas *mcp.SchemaCache
	http    http.Handler
	// observe is Options.ToolObserver.
	observe func(tool string, d time.Duration, failed bool)
}

// New builds the streamable HTTP handler. Each request gets the viewer
// stored on its context, so a session cannot keep another caller's grants.
func New(h *handlers.Handlers, version string) *Service {
	if version == "" {
		version = "dev"
	}
	s := &Service{
		h:       h,
		version: version,
		schemas: mcp.NewSchemaCache(),
	}
	s.http = mcp.NewStreamableHTTPHandler(func(r *http.Request) *mcp.Server {
		return s.serverFor(viewerFrom(r.Context()))
	}, &mcp.StreamableHTTPOptions{Stateless: true})
	return s
}

func (s *Service) serverFor(v Viewer) *mcp.Server {
	server := mcp.NewServer(&mcp.Implementation{
		Name:    "tsflow",
		Title:   "TSFlow",
		Version: s.version,
	}, &mcp.ServerOptions{
		Instructions: instructions,
		SchemaCache:  s.schemas,
	})
	s.addTools(server, v)
	if s.observe != nil {
		server.AddReceivingMiddleware(s.toolMetrics)
	}
	return server
}

// toolMetrics reports tools/call requests to the observer. The SDK fails a
// call to an unregistered tool with a protocol error and answers a known
// tool's failure with IsError, so the tool label only ever carries
// registered names or unknown.
func (s *Service) toolMetrics(next mcp.MethodHandler) mcp.MethodHandler {
	return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
		call, ok := req.(*mcp.CallToolRequest)
		if !ok || call.Params == nil {
			return next(ctx, method, req)
		}
		started := time.Now()
		res, err := next(ctx, method, req)
		tool := call.Params.Name
		failed := err != nil
		if err != nil {
			tool = "unknown"
		} else if r, ok := res.(*mcp.CallToolResult); ok && r != nil && r.IsError {
			failed = true
		}
		s.observe(tool, time.Since(started), failed)
		return res, err
	}
}

// Mount registers POST, GET, and DELETE /mcp when opts.Enabled is set.
// The route uses the same access middleware as /api, so tailnet grants and
// WhoIs or trusted-proxy identity apply before a tool runs.
func Mount(router *gin.Engine, opts Options) {
	if router == nil || !opts.Enabled {
		return
	}
	svc := New(opts.Handlers, opts.Version)
	svc.observe = opts.ToolObserver
	handle := func(c *gin.Context) {
		markReached(c)
		viewer := Viewer{}
		if ident, ok := access.FromGin(c); ok {
			viewer = Viewer{Restricted: true, Ident: ident}
		}
		req := c.Request.WithContext(WithViewer(c.Request.Context(), viewer))
		svc.http.ServeHTTP(c.Writer, req)
	}
	chain := []gin.HandlerFunc{handle}
	if opts.Access.Enabled {
		chain = append([]gin.HandlerFunc{access.Middleware(opts.Access, opts.WhoIs)}, chain...)
	}
	if opts.RateLimit != nil {
		chain = append([]gin.HandlerFunc{opts.RateLimit}, chain...)
	}
	chain = append([]gin.HandlerFunc{jsonRPCErrors}, chain...)
	router.POST("/mcp", chain...)
	router.GET("/mcp", chain...)
	router.DELETE("/mcp", chain...)
	log.Printf("MCP: read-only streamable HTTP at /mcp")
}
