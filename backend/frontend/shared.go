package frontend

import (
	"errors"
	"net/url"
	"strings"
)

// ErrFrontendNotIncluded is returned when the frontend is not embedded in the build.
// Use `go build -tags exclude_frontend` to build without the frontend.
var ErrFrontendNotIncluded = errors.New("frontend not included in build")

// trailingSlashRedirect returns the same-site location for a request path
// that ends in a slash. Leading slashes collapse to one: a Location of
// "//host" is protocol-relative and would send the browser to another site.
func trailingSlashRedirect(u *url.URL) string {
	p := strings.TrimRight(u.EscapedPath(), "/")
	p = "/" + strings.TrimLeft(p, "/")
	if u.RawQuery != "" {
		p += "?" + u.RawQuery
	}
	return p
}
