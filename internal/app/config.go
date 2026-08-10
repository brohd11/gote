package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// Config is the parsed ~/.gote/config.yml. A missing file yields the defaults, so a
// fresh install needs no setup.
type Config struct {
	Extension string                 `yaml:"extension,omitempty"`  // doc extension the lists filter on (default "md")
	ScanDepth int                    `yaml:"scan_depth,omitempty"` // default recursive scan depth (default 5)
	Default   string                 `yaml:"default,omitempty"`    // optional named vault used by a bare launch
	Vaults    map[string]VaultConfig `yaml:"vaults,omitempty"`
}

// VaultConfig is one named document root. Open is reserved now so the persisted
// shape can grow session restoration later without another schema change.
type VaultConfig struct {
	Path string   `yaml:"path"`
	Open []string `yaml:"open"`
}

// DefaultConfig is what a missing ~/.gote/config.yml means. ScanDepth is the depth
// `gote here` (and a bare directory argument) scans to when none is given on the
// command line — deep enough that a project's docs turn up without asking for it.
func DefaultConfig() Config {
	return Config{Extension: "md", ScanDepth: 5, Vaults: map[string]VaultConfig{}}
}

// Dir is ~/.gote, the home for stored docs and config.yml.
func Dir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".gote"), nil
}

// LoadConfig reads ~/.gote/config.yml. A missing file is not an error — it returns
// the defaults; an unreadable or malformed file falls back to them per-key.
func LoadConfig() (Config, error) {
	cfg := DefaultConfig()
	dir, err := Dir()
	if err != nil {
		return cfg, err
	}
	data, err := os.ReadFile(filepath.Join(dir, "config.yml"))
	if os.IsNotExist(err) {
		return cfg, nil
	}
	if err != nil {
		return cfg, err
	}
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return DefaultConfig(), err
	}
	if cfg.Extension == "" {
		cfg.Extension = "md"
	}
	if cfg.ScanDepth <= 0 {
		cfg.ScanDepth = 5
	}
	if cfg.Vaults == nil {
		cfg.Vaults = map[string]VaultConfig{}
	}
	return cfg, nil
}

// SaveConfig writes the complete gote config atomically. The temp file lives beside
// config.yml so rename stays on one filesystem and a failed write cannot truncate a
// working config.
func SaveConfig(cfg Config) error {
	dir, err := Dir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, "config-*.yml")
	if err != nil {
		return err
	}
	tmp := f.Name()
	ok := false
	defer func() {
		_ = f.Close()
		if !ok {
			_ = os.Remove(tmp)
		}
	}()
	if err := f.Chmod(0o644); err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp, filepath.Join(dir, "config.yml")); err != nil {
		return err
	}
	ok = true
	return nil
}

// normalizeVaultPath accepts the shell-friendly forms users type into the New Vault
// form and returns the stable absolute path stored in YAML. Only the current user's
// home shorthand is expanded; ~other-user is rejected rather than guessed.
func normalizeVaultPath(raw string) (string, error) {
	p := strings.TrimSpace(raw)
	if p == "" {
		return "", fmt.Errorf("path is required")
	}
	if p == "~" || strings.HasPrefix(p, "~"+string(filepath.Separator)) {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		p = filepath.Join(home, strings.TrimPrefix(p, "~"+string(filepath.Separator)))
	} else if strings.HasPrefix(p, "~") {
		return "", fmt.Errorf("only ~/ paths are supported")
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", err
	}
	abs = filepath.Clean(abs)
	info, err := os.Stat(abs)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("%q is not a directory", abs)
	}
	return abs, nil
}
