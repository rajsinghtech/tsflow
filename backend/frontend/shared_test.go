package frontend

import (
	"net/url"
	"testing"
)

func TestTrailingSlashRedirectStaysOnSite(t *testing.T) {
	tests := []struct {
		raw  string
		want string
	}{
		{"/policy/", "/policy"},
		{"/analytics/?tailnet=a&x=1", "/analytics?tailnet=a&x=1"},
		{"/a/b///", "/a/b"},
		// A leading // would be a protocol-relative URL to another host.
		{"//evil.example/", "/evil.example"},
		{"///evil.example//", "/evil.example"},
		{"//evil.example/path/?q=1", "/evil.example/path?q=1"},
		// Browsers treat a backslash like a slash in the authority.
		{"/\\evil.example/", "/%5Cevil.example"},
	}
	for _, tt := range tests {
		u, err := url.ParseRequestURI(tt.raw)
		if err != nil {
			t.Fatalf("parse %q: %v", tt.raw, err)
		}
		got := trailingSlashRedirect(u)
		if got != tt.want {
			t.Errorf("trailingSlashRedirect(%q) = %q, want %q", tt.raw, got, tt.want)
		}
		if len(got) > 1 && (got[1] == '/' || got[1] == '\\') {
			t.Errorf("trailingSlashRedirect(%q) = %q leaves the site", tt.raw, got)
		}
	}
}
