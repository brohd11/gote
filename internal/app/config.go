package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/brohd11/goutil/configdir"
	"github.com/brohd11/goutil/strutil"
)

// Config is the parsed ~/.gote/config.yml. A missing file yields the defaults, so a
// fresh install needs no setup.
// Nothing is omitempty: the written file is the only place the schema is visible, so
// every key appears even when its value is empty. An unset extensions list renders as
// "extensions: []" and an empty vault map as "vaults: {}", which reads as "this key
// exists and takes a list" rather than not appearing at all.
type Config struct {
	Extensions []string `yaml:"extensions"` // restrict the lists to these; empty (the default) means any text file
	ScanDepth  int      `yaml:"scan_depth"` // default recursive scan depth (default 5)
	AutoLSP    bool     `yaml:"auto-lsp"`   // lazily start/connect configured servers for supported files
	// FolderView opens the sidebar on the folder explorer instead of the flat scan list —
	// a preset for alt+t, not a mode: the scan still runs and the flat list is still
	// seeded behind it, so the toggle shows it with nothing left to load.
	FolderView bool `yaml:"folder_view"`
	// IndentGuides makes the editor visualize complete leading indent levels. It is
	// off by default so existing configs retain the uncluttered rendering.
	IndentGuides bool `yaml:"indent_guides"`
	// GitGutter decides whether the editor draws change markers against HEAD:
	// gutterOn, gutterOff, or gutterAuto (the default) to let the launch decide —
	// see gutterDefault. Auto exists because the two launches want opposite answers
	// and neither is wrong: `gote <file>` is the chrome-less editor, and a git column
	// is the kind of thing that launch exists to leave out.
	GitGutter string `yaml:"git_gutter"`
	// LanguageServers owns transport configuration, while language.go owns which files
	// use which server. A missing entry receives its built-in transport; Disabled is the
	// explicit way to suppress one without copying the rest of its defaults.
	LanguageServers map[string]LanguageServerConfig `yaml:"language_servers"`
	Default         string                          `yaml:"default"` // what a bare launch opens: a directory path, or a named vault
	Vaults          map[string]VaultConfig          `yaml:"vaults"`
}

// LanguageServerConfig selects exactly one transport. Address is a TCP endpoint for a
// server managed elsewhere; Command is an executable followed by its arguments for a
// stdio server whose process lifetime belongs to gote. InitializationOptions is the
// server-specific JSON-shaped object sent during the standard LSP handshake.
type LanguageServerConfig struct {
	Disabled              bool           `yaml:"disabled"`
	Address               string         `yaml:"address"`
	Command               []string       `yaml:"command"`
	InitializationOptions map[string]any `yaml:"initialization_options,omitempty"`
}

// The values Config.GitGutter takes. Anything else reads as gutterAuto rather than
// failing the load — a typo in one key should not cost the user their whole config.
const (
	gutterAuto = "auto"
	gutterOn   = "on"
	gutterOff  = "off"
)

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

// DefaultConfig is what a missing ~/.gote/config.yml means, and — since EnsureConfig
// writes exactly this — what a fresh one says. Extensions is left nil on purpose:
// unconfigured, gote lists every text file it finds, and narrowing that is the opt-in.
// (Nil rather than an empty slice because normalizeExts collapses an empty list back to
// nil, and a loaded config must still equal this one.) ScanDepth is the depth `gote here`
// (and a bare directory argument) scans to when none is given on the command line — deep
// enough that a project's docs turn up without asking for it.
//
// Default names the home store it already resolves to, so seeding it changes nothing
// about how gote launches (resolveDefault maps that path back to ModeHome). It is there
// to show the user the key exists and what shape its value takes.
func DefaultConfig() Config {
	return Config{
		ScanDepth:       5,
		AutoLSP:         true,
		GitGutter:       gutterAuto,
		LanguageServers: defaultLanguageServers(),
		Default:         defaultDocsRef,
		Vaults:          map[string]VaultConfig{},
	}
}

func defaultLanguageServers() map[string]LanguageServerConfig {
	return map[string]LanguageServerConfig{
		"gdscript": {Address: "127.0.0.1:6005", Command: []string{}},
		"python": {
			Command: []string{"pylsp"},
			InitializationOptions: map[string]any{"pylsp": map[string]any{
				"plugins": map[string]any{"jedi_completion": map[string]any{"include_params": true}},
			}},
		},
	}
}

// defaultDocsRef is the home store written the ~ way rather than as this machine's
// absolute path: a config is something people copy between machines.
const defaultDocsRef = "~/.gote/docs"

// Dir is ~/.gote, gote's config home. The ~/.<app> convention itself is
// goutil/configdir's; this pins gote's own name.
func Dir() (string, error) {
	return configdir.Dir("gote")
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
	// Load unmarshals over the defaults, so keys the file omits keep them. A missing file
	// is not an error and leaves them all in place; a malformed one may have half-written
	// cfg before failing, so the defaults are rebuilt rather than returned half-parsed.
	if err := configdir.Load(path, &cfg); err != nil {
		return DefaultConfig(), err
	}
	normalizeExtensions(&cfg)
	if cfg.ScanDepth <= 0 {
		cfg.ScanDepth = 5
	}
	if cfg.Vaults == nil {
		cfg.Vaults = map[string]VaultConfig{}
	}
	if cfg.LanguageServers == nil {
		cfg.LanguageServers = map[string]LanguageServerConfig{}
	}
	for id, fallback := range defaultLanguageServers() {
		server, ok := cfg.LanguageServers[id]
		if !ok || (!server.Disabled && server.Address == "" && len(server.Command) == 0) {
			cfg.LanguageServers[id] = fallback
			continue
		}
		if server.InitializationOptions == nil {
			server.InitializationOptions = fallback.InitializationOptions
			cfg.LanguageServers[id] = server
		}
	}
	// An unset or misspelled value is auto, the default: the key is a preference, and
	// getting it wrong should change what the gutter does, not whether gote starts.
	if cfg.GitGutter != gutterOn && cfg.GitGutter != gutterOff {
		cfg.GitGutter = gutterAuto
	}
	return cfg, nil
}

// normalizeExtensions puts the extensions key into the canonical shape NewDocFilter
// defines, so a Config compares and round-trips as whatever it meant rather than as
// whatever it was typed as.
func normalizeExtensions(cfg *Config) {
	cfg.Extensions = normalizeExts(cfg.Extensions)
}

// SaveConfig writes the complete gote config atomically — a failed write cannot truncate
// a working config. The atomic-write mechanics are goutil/configdir's (ported from this
// very function when the four apps' copies were collapsed into one).
func SaveConfig(cfg Config) error {
	dir, err := Dir()
	if err != nil {
		return err
	}
	return configdir.SaveAtomic(dir, "config.yml", cfg)
}

// resolveVaultPath accepts the shell-friendly forms users type into the New Vault form
// and returns the stable absolute path stored in YAML. Tilde handling is the shared
// strutil.ExpandHome's — only the current user's home shorthand is expanded and
// ~other-user is rejected rather than guessed — and the required/absolute checks around
// it are this form's own. Nothing here touches the filesystem: New Vault resolves a path
// it is about to create, so existence is a separate question from spelling.
func resolveVaultPath(raw string) (string, error) {
	p := strings.TrimSpace(raw)
	if p == "" {
		return "", fmt.Errorf("path is required")
	}
	p, err := strutil.ExpandHome(p)
	if err != nil {
		return "", err
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", err
	}
	return filepath.Clean(abs), nil
}

// normalizeDirPath is resolveVaultPath plus the must-already-be-a-directory check that
// every reader of a configured root wants: a vault (or a default) whose directory has
// gone missing is a problem to report, not one to paper over.
func normalizeDirPath(raw string) (string, error) {
	abs, err := resolveVaultPath(raw)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("%q is not a directory", abs)
	}
	return abs, nil
}

// ensureVaultDir adopts an existing folder and creates a missing one, parents included,
// so New Vault does not send the user out to a shell to mkdir first. A path naming a
// file is still refused, and refused in this package's words rather than as MkdirAll's
// ENOTDIR.
func ensureVaultDir(abs string) error {
	info, err := os.Stat(abs)
	switch {
	case err == nil && info.IsDir():
		return nil
	case err == nil:
		return fmt.Errorf("%q is not a directory", abs)
	case !os.IsNotExist(err):
		return err
	}
	return os.MkdirAll(abs, 0o755)
}
