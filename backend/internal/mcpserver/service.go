// Package mcpserver serves a read-only Model Context Protocol endpoint
// for the flow data TSFlow already stores.
package mcpserver

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rajsinghtech/tsflow/backend/internal/access"
	"github.com/rajsinghtech/tsflow/backend/internal/config"
	"github.com/rajsinghtech/tsflow/backend/internal/handlers"
)

const instructions = "Read-only TSFlow flow data. Tailnet allowlists match the REST API. " +
	"Identity autoscope is the default device view and can be cleared with scope=all. " +
	"Physical transport is excluded unless trafficTypes includes physical. DERP relays are labeled DERP relay. " +
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
	return server
}

// Mount registers POST, GET, and DELETE /mcp when opts.Enabled is set.
// The route uses the same access middleware as /api, so tailnet grants and
// WhoIs or trusted-proxy identity apply before a tool runs.
func Mount(router *gin.Engine, opts Options) {
	if router == nil || !opts.Enabled {
		return
	}
	svc := New(opts.Handlers, opts.Version)
	handle := func(c *gin.Context) {
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
	router.POST("/mcp", chain...)
	router.GET("/mcp", chain...)
	router.DELETE("/mcp", chain...)
	log.Printf("MCP: read-only streamable HTTP at /mcp")
}
