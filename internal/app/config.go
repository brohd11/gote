package app

import (
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// Config is the parsed ~/.gote/config.yml. A missing file yields the defaults, so a
// fresh install needs no setup.
type Config struct {
	Extension string `yaml:"extension,omitempty"`  // doc extension the lists filter on (default "md")
	ScanDepth int    `yaml:"scan_depth,omitempty"` // default recursive scan depth (default 2)
}

// DefaultConfig is what a missing ~/.gote/config.yml means.
func DefaultConfig() Config {
	return Config{Extension: "md", ScanDepth: 2}
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
		cfg.ScanDepth = 2
	}
	return cfg, nil
}
