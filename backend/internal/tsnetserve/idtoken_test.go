package tsnetserve

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rajsinghtech/tsflow/backend/internal/config"
)

func TestTsnetIDToken(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "token")
	if err := os.WriteFile(file, []byte("  header.payload.sig\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := tsnetIDToken(&config.Config{TsnetIDTokenFile: file})
	if err != nil || got != "header.payload.sig" {
		t.Fatalf("file token = %q, %v", got, err)
	}
	got, err = tsnetIDToken(&config.Config{TsnetIDToken: "inline"})
	if err != nil || got != "inline" {
		t.Fatalf("inline token = %q, %v", got, err)
	}
	got, err = tsnetIDToken(&config.Config{TsnetAudience: "aud"})
	if err != nil || got != "" {
		t.Fatalf("audience path = %q, %v", got, err)
	}

	empty := filepath.Join(dir, "empty")
	if err := os.WriteFile(empty, []byte("\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := tsnetIDToken(&config.Config{TsnetIDTokenFile: empty}); err == nil || !strings.Contains(err.Error(), "is empty") {
		t.Fatalf("empty file err = %v", err)
	}
	if _, err := tsnetIDToken(&config.Config{TsnetIDTokenFile: filepath.Join(dir, "missing")}); err == nil {
		t.Fatal("missing file should fail")
	}
}
