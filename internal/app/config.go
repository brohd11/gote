package app

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/brohd11/goutil/configdir"
	"github.com/brohd11/goutil/strutil"
)

// Config is ~/.gote/config.yml; a missing file yields the defaults. Nothing is
// omitempty, so every key appears in the written file (empty lists as [] and {}).
type Config struct {
	Extensions []string `yaml:"extensions"` // restrict the lists to these; empty (the default) means any text file
	ScanDepth  int      `yaml:"scan_depth"` // default recursive scan depth (default 5)
	AutoLSP    bool     `yaml:"auto-lsp"`   // lazily start/connect configured servers for supported files
	// FileView selects the starting Docs view: flat, folder, or grouped. Empty or
	// unrecognized values honor the legacy FolderView setting.
	FileView string `yaml:"file_view"`
	// FolderView is the legacy startup preference, used when FileView is unspecified.
	FolderView bool `yaml:"folder_view"`
	// IndentGuides makes the editor visualize complete leading indent levels. On by default.
	IndentGuides bool `yaml:"indent_guides"`
	// Project and SingleFile are per-launch startup toggles, separate because the two
	// launches want opposite defaults: `gote <file>` leaves out the gutters and diagnostics
	// that a project session wants. A leftover `git_gutter` key is ignored and dropped on the
	// next `gote config`.
	Project    ModeDefaults     `yaml:"project_mode"`     // ModeHome, ModeScan, ModeVault
	SingleFile SingleFileConfig `yaml:"single_file_mode"` // ModeFile
	// ClickDefinition and ClickContext name each gesture's modifier, since terminals differ
	// in which modifiers they pass through (Terminal.app and iTerm2 take ctrl+click, shift is
	// usually selection). "none" disables a gesture.
	ClickDefinition string `yaml:"click_definition"` // alt (default), ctrl, shift, none
	ClickContext    string `yaml:"click_context"`    // ctrl (default), alt, shift, none
	// DefaultDocMode is how a newly opened markdown doc starts: "off" (syntax highlighting)
	// or "live" (rendered in place). Anything else reads as off. Each doc's mode is
	// then its own until the buffer closes (docmode.go).
	DefaultDocMode string `yaml:"default_doc_mode"`
	// FormatOnSave formats (and organizes imports) after each ctrl+s. The reformat lands
	// after the write, leaving the buffer dirty until the next save. Off by default.
	FormatOnSave bool `yaml:"format_on_save"`
	// KittyKeyboard says the terminal speaks the Kitty keyboard protocol, so key names are
	// exact. Off (the default) suits legacy terminals (GNOME Terminal, xterm, Konsole,
	// Terminal.app), where ctrl+backspace arrives as ctrl+h; the editor then reads ctrl+h
	// that way. Other legacy-decoding workarounds belong behind this key too.
	KittyKeyboard bool `yaml:"kitty_keyboard"`
	// SyntaxColors is the syntax palette (defaults in palette.go).
	SyntaxColors SyntaxColors           `yaml:"syntax_colors"`
	Default      string                 `yaml:"default"` // what a bare launch opens: a directory path, or a named vault
	Vaults       map[string]VaultConfig `yaml:"vaults"`
	// Lang is ~/.gote/lang.yml (langconfig.go), loaded beside this file and never written
	// into it.
	Lang LangConfig `yaml:"-"`
}

// The values ClickDefinition and ClickContext take. Anything else reads as clickNone
// rather than failing the load, the same tolerance GitGutter's values get.
const (
	clickNone  = "none"
	clickAlt   = "alt"
	clickCtrl  = "ctrl"
	clickShift = "shift"
)

// SyntaxColors is one color per slot, as an ANSI-256 index ("133") or hex ("#af5faf"),
// configurable because colors are taste and 256-color defaults ignore the terminal
// scheme. The first ten slots are source tokens, the Md ones markdown; an unparseable
// value falls back to its default. A 16-color terminal gets the basic palette
// automatically; BasicColors forces it (the terminal's own eight colors, which follow its
// scheme) without discarding the other keys.
type SyntaxColors struct {
	BasicColors bool `yaml:"basic_colors"`
	// Brackets is the rainbow-bracket cycle. A missing key inherits the defaults because
	// LoadConfig unmarshals over DefaultConfig; an explicit [] disables the feature.
	Brackets []string `yaml:"brackets"`

	Keyword  string `yaml:"keyword"`
	Type     string `yaml:"type"`
	Func     string `yaml:"func"`
	String   string `yaml:"string"`
	Number   string `yaml:"number"`
	Comment  string `yaml:"comment"`
	Operator string `yaml:"operator"`
	Inserted string `yaml:"inserted"`
	Deleted  string `yaml:"deleted"`
	Error    string `yaml:"error"`

	MdHeading  string `yaml:"md_heading"`
	MdEmphasis string `yaml:"md_emphasis"`
	MdStrong   string `yaml:"md_strong"`
	MdCode     string `yaml:"md_code"`
	MdQuote    string `yaml:"md_quote"`
	MdLink     string `yaml:"md_link"`
	MdList     string `yaml:"md_list"`
}

// ModeDefaults are a launch's startup toggles, each still toggleable at runtime. The file
// is loaded over DefaultConfig, so a section naming one key keeps the others' defaults.
type ModeDefaults struct {
	Wrap              bool `yaml:"default_wrap"`               // soft-wrap each new document
	LineNumbers       bool `yaml:"default_line_numbers"`       // line numbers, independent of wrap
	GitGutter         bool `yaml:"default_git_gutter"`         // draw change markers against HEAD
	DiagnosticsGutter bool `yaml:"default_diagnostics_gutter"` // draw the LSP severity column
	// AllowLSP narrows AutoLSP for this launch (they are ANDed); off means no manager at all.
	AllowLSP bool `yaml:"default_allow_lsp"`
}

// SingleFileConfig is ModeDefaults plus AllowPanelToggle and MenuBar, which only the
// minimal launch needs.
type SingleFileConfig struct {
	ModeDefaults `yaml:",inline"`
	// AllowPanelToggle keeps the bottom panel and outline reachable. False drops their menu
	// rows and disables alt+|, alt+shift+o and ctrl+alt+f (find in files opens the panel). The ?
	// overlay still lists them, marked off with this key's name.
	AllowPanelToggle bool `yaml:"allow_panel_toggle"`
	// MenuBar shows the menus beside the title from launch. False keeps the bare title bar
	// until a menu chord (alt+digit, or a menu's alt+letter) reveals it for the run.
	MenuBar bool `yaml:"menu_bar"`
}

// modeDefaults picks the section a launch reads; the one place mode maps to section.
func (c Config) modeDefaults(mode Mode) ModeDefaults {
	if mode == ModeFile {
		return c.SingleFile.ModeDefaults
	}
	return c.Project
}

// Filter is the configured extensions filter (any text file when unset). --ext overrides
// it.
func (c Config) Filter() DocFilter { return NewDocFilter(c.Extensions) }

// VaultConfig is one named document root. Session state lives in StateDir, not in the
// user-edited config.
type VaultConfig struct {
	Path string `yaml:"path"`
}

// DefaultConfig is what a missing config means and what EnsureConfig writes. Extensions
// is nil (list every text file); ScanDepth is the default depth for `gote here` and bare
// directories. Default names the home store it already resolves to, to show the key.
func DefaultConfig() Config {
	return Config{
		ScanDepth:    5,
		AutoLSP:      false,
		IndentGuides: true,
		Project:      ModeDefaults{GitGutter: true, DiagnosticsGutter: true, AllowLSP: true},
		SingleFile: SingleFileConfig{
			ModeDefaults:     ModeDefaults{GitGutter: true, DiagnosticsGutter: false, AllowLSP: true},
			AllowPanelToggle: false,
			MenuBar:          false,
		},
		Lang:            LangConfig{Servers: defaultLanguageServers()},
		ClickDefinition: clickAlt,
		ClickContext:    clickCtrl,
		DefaultDocMode:  docModeLiveName,
		SyntaxColors:    defaultSyntaxColors(),
		Default:         defaultDocsRef,
		Vaults:          map[string]VaultConfig{},
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

// DocsDir is ~/.gote/docs, the home store; one level down so ~/.gote's own config.yml is
// not listed as a document.
func DocsDir() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "docs"), nil
}

// StateDir is ~/.gote/state, what gote writes for itself between runs. A subdirectory, so
// a dotfiles repo can ignore it with one line.
func StateDir() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "state"), nil
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

// EnsureConfig returns the config path, writing the defaults first when missing.
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

// SyncConfig ensures the config and rewrites it from itself, so keys added since it was
// written appear. A malformed file is left alone (its path still returned). The rewrite
// reformats hand-written YAML, so only `gote config` calls it.
func SyncConfig() (string, error) {
	path, err := EnsureConfig()
	if err != nil {
		return "", err
	}
	cfg, err := loadMainConfig()
	if err != nil {
		return path, nil
	}
	return path, SaveConfig(cfg)
}

// LoadConfig reads ~/.gote/config.yml and ~/.gote/lang.yml. A missing file is not an
// error — it returns the defaults; an unreadable or malformed file falls back to them.
func LoadConfig() (Config, error) {
	cfg, err := loadMainConfig()
	lang, langErr := loadLangConfig()
	cfg.Lang = lang
	return cfg, errors.Join(err, langErr)
}

// loadMainConfig is LoadConfig without lang.yml, for SyncConfig: a broken lang.yml is no
// reason to skip topping up config.yml.
func loadMainConfig() (Config, error) {
	cfg := DefaultConfig()
	path, err := ConfigPath()
	if err != nil {
		return cfg, err
	}
	// Load over the defaults so omitted keys keep them; after a parse error, rebuild the
	// defaults rather than return a half-parsed config.
	if err := configdir.Load(path, &cfg); err != nil {
		return DefaultConfig(), fmt.Errorf("%s: %w", path, err) // named, as lang.yml's are
	}
	normalizeExtensions(&cfg)
	if cfg.ScanDepth <= 0 {
		cfg.ScanDepth = 5
	}
	if cfg.Vaults == nil {
		cfg.Vaults = map[string]VaultConfig{}
	}
	normalizeSyntaxColors(&cfg.SyntaxColors)
	return cfg, nil
}

// normalizeExtensions puts extensions into NewDocFilter's canonical shape, so configs
// compare and round-trip by meaning.
func normalizeExtensions(cfg *Config) {
	cfg.Extensions = normalizeExts(cfg.Extensions)
}

// SaveConfig writes the whole config atomically.
func SaveConfig(cfg Config) error {
	dir, err := Dir()
	if err != nil {
		return err
	}
	return configdir.SaveAtomic(dir, "config.yml", cfg)
}

// resolveVaultPath turns a typed path into the absolute path stored in YAML (~ expanded,
// ~user rejected). It does not touch the filesystem: New Vault may be about to create it.
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

// normalizeDirPath is resolveVaultPath plus a must-be-a-directory check.
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

// ensureVaultDir creates a missing folder (with parents) or adopts an existing one; a
// file is refused with a clear message.
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
