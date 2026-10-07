package handlers

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// A poller that has never failed must not report an error time. omitempty
// does not drop a zero time.Time, so this used to send 0001-01-01.
func TestTailnetPollerStatusOmitsLastErrorTimeWithoutError(t *testing.T) {
	healthy, err := json.Marshal(TailnetPollerStatus{Running: true, LastPollTime: time.Unix(1_700_000_000, 0).UTC()})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(healthy), "lastErrorTime") || strings.Contains(string(healthy), "lastError\"") {
		t.Fatalf("healthy status = %s, want no error fields", healthy)
	}

	failedAt := time.Date(2026, 5, 8, 13, 45, 0, 0, time.UTC)
	failed, err := json.Marshal(TailnetPollerStatus{LastError: "boom", LastErrorTime: failedAt})
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(failed, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded["lastErrorTime"] != "2026-05-08T13:45:00Z" || decoded["lastError"] != "boom" {
		t.Fatalf("failed status = %s", failed)
	}
}
