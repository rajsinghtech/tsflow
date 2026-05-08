package services

import (
	"testing"
	"time"
)

func TestObjectTimeSupportedCompressionSuffixes(t *testing.T) {
	tests := []string{
		"network/2026/05/08/2026-05-08-13-45-00.ndjson",
		"network/2026/05/08/2026-05-08-13-45-00.ndjson.zst",
		"network/2026/05/08/2026-05-08-13-45-00.ndjson.zstd",
		"network/2026/05/08/2026-05-08-13-45-00.ndjson.gz",
		"network/2026/05/08/2026-05-08-13-45-00.ndjson.gzip",
	}
	expected := time.Date(2026, 5, 8, 13, 45, 0, 0, time.UTC)

	for _, key := range tests {
		t.Run(key, func(t *testing.T) {
			got, ok := objectTime(key)
			if !ok {
				t.Fatalf("expected %s to parse", key)
			}
			if !got.Equal(expected) {
				t.Fatalf("expected %s, got %s", expected, got)
			}
		})
	}
}

func TestObjectTimeRejectsUnknownSuffix(t *testing.T) {
	if got, ok := objectTime("network/2026/05/08/2026-05-08-13-45-00.json.br"); ok {
		t.Fatalf("expected unsupported suffix to be rejected, got %s", got)
	}
}
