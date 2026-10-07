package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/rajsinghtech/tsflow/backend/internal/config"
	"github.com/rajsinghtech/tsflow/backend/internal/services"
)

// The UI types addresses and tags as arrays. A JSON null for a device with
// no addresses stopped the whole traffic graph from loading.
func TestGetDevicesNeverReturnsNullLists(t *testing.T) {
	for name, upstream := range map[string]string{
		"device without addresses or tags": `{"devices":[{"id":"n1CNTRL","nodeId":"n1CNTRL","name":"bare.example.ts.net","addresses":[]}]}`,
		"empty tailnet":                    `{"devices":[]}`,
	} {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(upstream))
			}))
			defer server.Close()
			h := NewHandlers(services.NewTailscaleService(&config.Config{
				TailscaleAPIURL:  server.URL,
				TailscaleTailnet: "example.com",
				TailscaleAPIKey:  "test-key",
			}), nil, nil, "test")

			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest(http.MethodGet, "/api/devices", nil)
			h.GetDevices(c)
			if w.Code != http.StatusOK {
				t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
			}
			if body := w.Body.String(); strings.Contains(body, "null") {
				t.Fatalf("response has a null list: %s", body)
			}
		})
	}
}
