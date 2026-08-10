package app

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// TestLoadConfigDefaults: a missing config.yml means the defaults, no error.
func TestLoadConfigDefaults(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cfg, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(cfg, DefaultConfig()) {
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
	if want := DefaultConfig().ScanDepth; cfg.ScanDepth != want {
		t.Fatalf("scan depth = %d, want the default %d", cfg.ScanDepth, want)
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
	if !reflect.DeepEqual(cfg, DefaultConfig()) {
		t.Fatalf("cfg = %+v, want the defaults on a malformed file", cfg)
	}
}

func TestConfigVaultRoundTrip(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	vault := filepath.Join(home, "Notes")
	if err := os.Mkdir(vault, 0o755); err != nil {
		t.Fatal(err)
	}
	want := DefaultConfig()
	want.Default = "notes"
	want.Vaults["notes"] = VaultConfig{Path: vault, Open: []string{}}
	if err := SaveConfig(want); err != nil {
		t.Fatal(err)
	}

	raw, err := os.ReadFile(filepath.Join(home, ".gote", "config.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "open: []") {
		t.Fatalf("new vault schema must preserve the reserved empty open list:\n%s", raw)
	}
	got, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("round trip = %#v, want %#v", got, want)
	}
}

func TestNormalizeVaultPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	notes := filepath.Join(home, "Notes")
	if err := os.Mkdir(notes, 0o755); err != nil {
		t.Fatal(err)
	}
	if got, err := normalizeVaultPath("~/Notes"); err != nil || got != notes {
		t.Fatalf("normalize ~/Notes = %q, %v; want %q", got, err, notes)
	}
	file := filepath.Join(home, "note.md")
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := normalizeVaultPath(file); err == nil {
		t.Fatal("a vault path naming a file should fail")
	}
}
