package mcpserver

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/rajsinghtech/tsflow/backend/internal/handlers"
)

// Stored pairs are sorted by node id, so src/dst in pair output is not who
// started the connection. Tools that list pairs have to say so.
func TestPairToolsSayPairsAreUnordered(t *testing.T) {
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
	want := map[string]bool{"top_pairs": false, "new_connections": false}
	for _, tool := range res.Tools {
		if _, ok := want[tool.Name]; !ok {
			continue
		}
		want[tool.Name] = true
		if !strings.Contains(tool.Description, "Pairs are unordered") {
			t.Errorf("%s description does not say pairs are unordered: %q", tool.Name, tool.Description)
		}
	}
	for name, seen := range want {
		if !seen {
			t.Errorf("tool %s not listed", name)
		}
	}
}
