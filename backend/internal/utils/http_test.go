package utils

import (
	"strings"
	"testing"
)

func TestHTTPErrorSurfacesAPIBody(t *testing.T) {
	t.Parallel()
	err404 := HTTPError(404, `{"message":"not found"}`)
	if !strings.Contains(err404.Error(), "status 404:") || !strings.Contains(err404.Error(), "not found") {
		t.Fatalf("404 with body = %q", err404)
	}
	if strings.Contains(err404.Error(), "tailnet not found") {
		t.Fatalf("404 must not invent tailnet not found: %q", err404)
	}
	err403 := HTTPError(403, `missing scope users:read`)
	if !strings.Contains(err403.Error(), "users:read") {
		t.Fatalf("403 with body = %q", err403)
	}
	if strings.Contains(err403.Error(), "logs:network:read") {
		t.Fatalf("403 must not invent logs:network:read: %q", err403)
	}
	empty404 := HTTPError(404, "  ")
	if empty404.Error() != "status 404: not found" {
		t.Fatalf("empty 404 = %q", empty404)
	}
}
