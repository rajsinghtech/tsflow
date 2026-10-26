package handlers

import "testing"

// 213505 days wraps time.Duration around to about 24h, which used to pass the
// range check as if the caller had asked for a day.
func TestParseLookbackRejectsDayOverflow(t *testing.T) {
	for _, raw := range []string{"213505d", "9223372036854775807d", "91d"} {
		if d, _, err := parseLookback(raw); err == nil {
			t.Errorf("parseLookback(%q) = %s, want an error", raw, d)
		}
	}
	if d, label, err := parseLookback("90d"); err != nil || label != "90d" {
		t.Errorf("parseLookback(90d) = %s %q %v", d, label, err)
	}
}
