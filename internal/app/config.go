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
	Extensions []string               `yaml:"extensions,omitempty"` // restrict the lists to these; empty (the default) means any text file
	ScanDepth  int                    `yaml:"scan_depth,omitempty"` // default recursive scan depth (default 5)
	Default    string                 `yaml:"default,omitempty"`    // optional named vault used by a bare launch
	Vaults     map[string]VaultConfig `yaml:"vaults,omitempty"`
}

// Filter is the discovery filter the config asks for: the configured extensions, or
// the zero DocFilter (any text file) when none are set. The --ext flag overrides it,
// which Ctx.New applies.
func (c Config) Filter() DocFilter { return NewDocFilter(c.Extensions) }

// VaultConfig is one named document root. Open is reserved now so the persisted
// shape can grow session restoration later without another schema change.
type VaultConfig struct {
	Path string   `yaml:"path"`
	Open []string `yaml:"open"`
}

// DefaultConfig is what a missing ~/.gote/config.yml means. Extensions is empty on
// purpose — unconfigured, gote lists every text file it finds, and narrowing that is
// the opt-in. ScanDepth is the depth `gote here` (and a bare directory argument) scans
// to when none is given on the command line — deep enough that a project's docs turn
// up without asking for it.
func DefaultConfig() Config {
	return Config{ScanDepth: 5, Vaults: map[string]VaultConfig{}}
}

// Dir is ~/.gote, gote's config home.
func Dir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".gote"), nil
}

// DocsDir is ~/.gote/docs, the home-mode document store. It sits one level below the
// config home on purpose: discovery takes any text file, so a store flat in ~/.gote
// would list gote's own config.yml as a document.
func DocsDir() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "docs"), nil
}

// ConfigPath is ~/.gote/config.yml — what LoadConfig reads, SaveConfig writes, and
// `gote config` opens.
func ConfigPath() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "config.yml"), nil
}

// EnsureConfig returns the config path, materializing a defaults file first when none
// exists. `gote config` on a fresh install should open the real schema to edit, not an
// empty buffer that gives no hint what belongs in it.
func EnsureConfig() (string, error) {
	path, err := ConfigPath()
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(path); os.IsNotExist(err) {
		if err := SaveConfig(DefaultConfig()); err != nil {
			return "", err
		}
	}
	return path, nil
}

// LoadConfig reads ~/.gote/config.yml. A missing file is not an error — it returns
// the defaults; an unreadable or malformed file falls back to them per-key.
func LoadConfig() (Config, error) {
	cfg := DefaultConfig()
	path, err := ConfigPath()
	if err != nil {
		return cfg, err
	}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return cfg, nil
	}
	if err != nil {
		return cfg, err
	}
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return DefaultConfig(), err
	}
	normalizeExtensions(&cfg)
	if cfg.ScanDepth <= 0 {
		cfg.ScanDepth = 5
	}
	if cfg.Vaults == nil {
		cfg.Vaults = map[string]VaultConfig{}
	}
	return cfg, nil
}

// normalizeExtensions puts the extensions key into the canonical shape NewDocFilter
// defines, so a Config compares and round-trips as whatever it meant rather than as
// whatever it was typed as.
func normalizeExtensions(cfg *Config) {
	cfg.Extensions = normalizeExts(cfg.Extensions)
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
