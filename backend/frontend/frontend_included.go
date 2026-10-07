//go:build !exclude_frontend

package frontend

import (
	"bytes"
	"embed"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/rajsinghtech/tsflow/backend/internal/middleware"
)

//go:embed all:dist/*
var frontendFS embed.FS

var writeIndexFn func(w io.Writer, nonce string) error

func init() {
	const scriptTag = "<script"

	index, err := fs.ReadFile(frontendFS, "dist/index.html")
	if err != nil {
		panic(fmt.Errorf("failed to read embedded index.html: %w", err))
	}

	idx := bytes.Index(index, []byte(scriptTag))
	if idx == -1 {
		// No script tag found, serve as-is
		writeIndexFn = func(w io.Writer, nonce string) error {
			_, err := w.Write(index)
			return err
		}
		return
	}

	// Pre-calculate parts for nonce injection
	beforeScript := index[:idx]
	afterScriptTag := index[idx+len(scriptTag):]

	writeIndexFn = func(w io.Writer, nonce string) error {
		if nonce == "" {
			_, err := w.Write(index)
			return err
		}

		// Write: [before script] + <script nonce="XXX" + [rest]
		if _, err := w.Write(beforeScript); err != nil {
			return err
		}
		if _, err := w.Write([]byte(`<script nonce="` + nonce + `"`)); err != nil {
			return err
		}
		_, err := w.Write(afterScriptTag)
		return err
	}
}

// RegisterFrontend registers the embedded frontend with the gin router.
// Must be called AFTER all API routes are registered.
func RegisterFrontend(router *gin.Engine) error {
	distFS, err := fs.Sub(frontendFS, "dist")
	if err != nil {
		return fmt.Errorf("failed to access embedded dist: %w", err)
	}

	cacheMaxAge := 24 * time.Hour
	fileServer := newFileServerWithCaching(http.FS(distFS), int(cacheMaxAge.Seconds()))

	router.NoRoute(func(c *gin.Context) {
		path := strings.TrimPrefix(c.Request.URL.Path, "/")

		// Redirect trailing slashes (except root)
		if path != "" && strings.HasSuffix(path, "/") {
			c.Redirect(http.StatusMovedPermanently, trailingSlashRedirect(c.Request.URL))
			return
		}

		// Return JSON 404 for API routes
		if strings.HasPrefix(path, "api/") {
			c.JSON(http.StatusNotFound, gin.H{"error": "API endpoint not found"})
			return
		}

		// Determine what to serve
		if path == "" {
			path = "index.html"
		} else if _, err := fs.Stat(distFS, path); os.IsNotExist(err) {
			path = "index.html" // SPA fallback
		}

		// Serve index.html with CSP nonce (no caching)
		if path == "index.html" {
			nonce := middleware.GetCSPNonce(c)
			c.Header("Content-Type", "text/html; charset=utf-8")
			c.Header("Cache-Control", "no-store")
			c.Status(http.StatusOK)
			if err := writeIndexFn(c.Writer, nonce); err != nil {
				c.AbortWithStatus(http.StatusInternalServerError)
			}
			return
		}

		// Serve static assets with caching
		c.Request.URL.Path = "/" + path
		fileServer.ServeHTTP(c.Writer, c.Request)
	})

	return nil
}

// fileServerWithCaching wraps http.FileServer with caching headers
type fileServerWithCaching struct {
	root                    http.FileSystem
	lastModified            time.Time
	cacheMaxAge             int
	lastModifiedHeaderValue string
	cacheControlHeaderValue string
}

func newFileServerWithCaching(root http.FileSystem, cacheMaxAge int) *fileServerWithCaching {
	now := time.Now()
	return &fileServerWithCaching{
		root:                    root,
		lastModified:            now,
		cacheMaxAge:             cacheMaxAge,
		lastModifiedHeaderValue: now.UTC().Format(http.TimeFormat),
		cacheControlHeaderValue: fmt.Sprintf("public, max-age=%d", cacheMaxAge),
	}
}

func (f *fileServerWithCaching) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Handle If-Modified-Since for 304 responses
	if ifModifiedSince := r.Header.Get("If-Modified-Since"); ifModifiedSince != "" {
		if t, err := time.Parse(http.TimeFormat, ifModifiedSince); err == nil {
			if f.lastModified.Before(t.Add(time.Second)) {
				w.WriteHeader(http.StatusNotModified)
				return
			}
		}
	}

	// Set caching headers
	w.Header().Set("Last-Modified", f.lastModifiedHeaderValue)
	w.Header().Set("Cache-Control", f.cacheControlHeaderValue)

	http.FileServer(f.root).ServeHTTP(w, r)
}
