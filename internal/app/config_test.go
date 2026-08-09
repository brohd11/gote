package app

import (
	"os"
	"path/filepath"
	"testing"
)

// TestLoadConfigDefaults: a missing config.yml means the defaults, no error.
func TestLoadConfigDefaults(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cfg, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg != DefaultConfig() {
		t.Fatalf("cfg = %+v, want %+v", cfg, DefaultConfig())
	}
}

// TestLoadConfigFile: set keys load; unset keys keep their defaults.
func TestLoadConfigFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(home, ".gote")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.yml"), []byte("extension: txt\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Extension != "txt" {
		t.Fatalf("extension = %q, want txt", cfg.Extension)
	}
	if cfg.ScanDepth != 2 {
		t.Fatalf("scan depth = %d, want the default 2", cfg.ScanDepth)
	}
}

// TestLoadConfigMalformed: a malformed file reports an error and falls back to the
// defaults.
func TestLoadConfigMalformed(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(home, ".gote")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.yml"), []byte("extension: [unclosed"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig()
	if err == nil {
		t.Fatal("a malformed config should report an error")
	}
	if cfg != DefaultConfig() {
		t.Fatalf("cfg = %+v, want the defaults on a malformed file", cfg)
	}
}
