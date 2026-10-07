package services

import (
	"testing"
	"time"

	tailscale "tailscale.com/client/tailscale/v2"
)

// "start" comes from the node's clock and "logged" from the server. Traffic
// cannot start after the server received its log, so a start later than
// logged means the node's clock is ahead; those rows must not land in the
// future, where they move the newest-data marker the UI anchors to.
func TestFlowLogTimeIgnoresStartAfterLogged(t *testing.T) {
	logged := time.Date(2026, 5, 8, 13, 45, 5, 0, time.UTC)
	ahead := logged.Add(40 * time.Minute)
	poller := NewPoller(nil, nil, DefaultPollerConfig())
	traffic := tailscale.TrafficStats{Proto: 6, Src: "100.64.0.1:1234", Dst: "100.64.0.2:443", TxBytes: 10}

	typed := poller.convertTailscaleLog(tailscale.NetworkFlowLog{
		NodeID: "node-a", Logged: logged, Start: ahead, End: ahead.Add(5 * time.Second),
		VirtualTraffic: []tailscale.TrafficStats{traffic},
	})
	if len(typed) != 1 || !typed[0].LoggedAt.Equal(logged) {
		t.Fatalf("typed log time = %+v, want logged %s", typed, logged)
	}

	mapped := poller.convertMapLog(map[string]any{
		"nodeId": "node-a",
		"logged": logged.Format(time.RFC3339Nano),
		"start":  ahead.Format(time.RFC3339Nano),
		"virtualTraffic": []any{map[string]any{
			"proto": float64(6), "src": "100.64.0.1:1234", "dst": "100.64.0.2:443", "txBytes": float64(10),
		}},
	})
	if len(mapped) != 1 || !mapped[0].LoggedAt.Equal(logged) {
		t.Fatalf("map log time = %+v, want logged %s", mapped, logged)
	}
}

func TestFlowLogTimeKeepsStartOtherwise(t *testing.T) {
	logged := time.Date(2026, 5, 8, 13, 45, 5, 0, time.UTC)
	tests := []struct {
		name          string
		start, logged time.Time
		want          time.Time
	}{
		{"normal upload delay", logged.Add(-5 * time.Second), logged, logged.Add(-5 * time.Second)},
		{"buffered upload hours later", logged.Add(-3 * time.Hour), logged, logged.Add(-3 * time.Hour)},
		{"no logged", logged, time.Time{}, logged},
		{"no start", time.Time{}, logged, logged},
		{"neither", time.Time{}, time.Time{}, time.Time{}},
	}
	for _, tt := range tests {
		if got := flowLogTime(tt.start, tt.logged); !got.Equal(tt.want) {
			t.Errorf("%s: flowLogTime = %s, want %s", tt.name, got, tt.want)
		}
	}
}
