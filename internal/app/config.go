package app

import (
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
	// FolderView starts the sidebar in the folder view instead of the flat scan list (the
	// alt+t preset); the scan still runs.
	FolderView bool `yaml:"folder_view"`
	// IndentGuides makes the editor visualize complete leading indent levels. It is
	// off by default so existing configs retain the uncluttered rendering.
	IndentGuides bool `yaml:"indent_guides"`
	// Project and SingleFile are per-launch startup toggles, separate because the two
	// launches want opposite defaults: `gote <file>` leaves out the gutters and diagnostics
	// that a project session wants. A leftover `git_gutter` key is ignored and dropped on the
	// next `gote config`.
	Project    ModeDefaults     `yaml:"project_mode"`     // ModeHome, ModeScan, ModeVault
	SingleFile SingleFileConfig `yaml:"single_file_mode"` // ModeFile
	// LanguageServers configures transports (language.go maps files to servers). A missing
	// entry gets its built-in transport; Disabled turns one off.
	LanguageServers map[string]LanguageServerConfig `yaml:"language_servers"`
	// ClickDefinition and ClickContext name each gesture's modifier, since terminals differ
	// in which modifiers they pass through (Terminal.app and iTerm2 take ctrl+click, shift is
	// usually selection). "none" disables a gesture.
	ClickDefinition string `yaml:"click_definition"` // alt (default), ctrl, shift, none
	ClickContext    string `yaml:"click_context"`    // ctrl (default), alt, shift, none
	// FormatOnSave formats (and organizes imports) after each ctrl+s. The reformat lands
	// after the write, leaving the buffer dirty until the next save. Off by default.
	FormatOnSave bool `yaml:"format_on_save"`
	// SyntaxColors is the syntax palette (defaults in palette.go).
	SyntaxColors SyntaxColors           `yaml:"syntax_colors"`
	Default      string                 `yaml:"default"` // what a bare launch opens: a directory path, or a named vault
	Vaults       map[string]VaultConfig `yaml:"vaults"`
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

// LanguageServerConfig selects one transport: Address (TCP, managed elsewhere) or Command
// (a stdio server gote runs). InitializationOptions is sent in the LSP handshake.
type LanguageServerConfig struct {
	Disabled              bool           `yaml:"disabled"`
	Address               string         `yaml:"address"`
	Command               []string       `yaml:"command"`
	InitializationOptions map[string]any `yaml:"initialization_options,omitempty"`
	// SemanticTokens maps this server's non-standard semantic token types to syntax_colors
	// slots (standard LSP names are already mapped, see semantic_map.go). An empty slot turns
	// a type off.
	SemanticTokens map[string]string `yaml:"semantic_tokens,omitempty"`
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

// SingleFileConfig is ModeDefaults plus AllowPanelToggle, which only the minimal launch
// needs.
type SingleFileConfig struct {
	ModeDefaults `yaml:",inline"`
	// AllowPanelToggle keeps the bottom panel and outline reachable. False drops their menu
	// rows and disables alt+\, alt+shift+o and ctrl+alt+f (find in files opens the panel). The ?
	// overlay still lists them, marked off with this key's name.
	AllowPanelToggle bool `yaml:"allow_panel_toggle"`
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
		ScanDepth: 5,
		AutoLSP:   true,
		Project:   ModeDefaults{GitGutter: true, DiagnosticsGutter: true, AllowLSP: true},
		SingleFile: SingleFileConfig{
			ModeDefaults:     ModeDefaults{GitGutter: true, DiagnosticsGutter: false, AllowLSP: true},
			AllowPanelToggle: false,
		},
		LanguageServers: defaultLanguageServers(),
		ClickDefinition: clickAlt,
		ClickContext:    clickCtrl,
		SyntaxColors:    defaultSyntaxColors(),
		Default:         defaultDocsRef,
		Vaults:          map[string]VaultConfig{},
	}
}

func defaultLanguageServers() map[string]LanguageServerConfig {
	return map[string]LanguageServerConfig{
		"bash":       {Command: []string{"bash-language-server", "start"}},
		"clangd":     {Command: []string{"clangd"}},
		"csharp":     {Command: []string{"csharp-ls"}},
		"gdscript":   {Address: "127.0.0.1:6005", Command: []string{}},
		"lua":        {Command: []string{"lua-language-server"}},
		"rust":       {Command: []string{"rust-analyzer"}},
		"typescript": {Command: []string{"typescript-language-server", "--stdio"}},
		"go": {
			Command: []string{"gopls"},
			// completeFunctionCalls off inserts just the name: typing the "(" yourself raises the
			// signature hint, which a server-supplied "()" never would. usePlaceholders stays off so
			// re-enabling calls does not insert literal signatures. semanticTokens is gopls's own
			// switch (it returns no provider without it). These defaults only apply when the user's
			// config sets no initialization_options for go.
			InitializationOptions: map[string]any{
				"completeFunctionCalls": false,
				"usePlaceholders":       false,
				"semanticTokens":        true,
			},
		},
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
	cfg, err := LoadConfig()
	if err != nil {
		return path, nil
	}
	return path, SaveConfig(cfg)
}

// LoadConfig reads ~/.gote/config.yml. A missing file is not an error — it returns
// the defaults; an unreadable or malformed file falls back to them per-key.
func LoadConfig() (Config, error) {
	cfg := DefaultConfig()
	path, err := ConfigPath()
	if err != nil {
		return cfg, err
	}
	// Load over the defaults so omitted keys keep them; after a parse error, rebuild the
	// defaults rather than return a half-parsed config.
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
