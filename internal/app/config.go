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
	// ClickDefinition and ClickContext name the modifier each gesture rides on, because
	// terminals disagree about which ones they hand over: Terminal.app claims ctrl+click
	// for its own contextual menu, iTerm2 turns it into a right click before the app sees
	// it, and shift is reserved almost everywhere for the terminal's own text selection.
	// A chord that works on one machine can be dead or redundant on the next, so both are
	// settings rather than constants. "none" turns a gesture off; see clickModifier.
	ClickDefinition string `yaml:"click_definition"` // alt (default), ctrl, shift, none
	ClickContext    string `yaml:"click_context"`    // ctrl (default), alt, shift, none
	// FormatOnSave asks the language server to format (and organize imports) on every
	// ctrl+s. The reformat lands just AFTER the write rather than blocking it — see
	// homeScreen.formatOnSave — so the buffer is left dirty and the next save settles
	// it. Off by default: a save should not rewrite a buffer until asked.
	FormatOnSave bool `yaml:"format_on_save"`
	// SyntaxColors is the editor's syntax palette. See palette.go for the defaults and
	// what governs them; highlight_chroma.go and highlight_markdown.go are the two files
	// that draw with them.
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

// SyntaxColors is one color per highlighted slot, as an ANSI-256 index ("133") or a hex
// literal ("#af5faf"). It is in the config file rather than only in the code because
// syntax colors are taste, and the defaults are absolute colors that no terminal scheme
// adjusts for the user — so the file has to be where they can be adjusted by hand.
//
// The first ten slots color source tokens, the Md ones markdown structure; a slot may
// repeat a color, as Inserted and String do. An empty or unparseable value falls back to
// its default rather than failing the load (normalizeSyntaxColors), which is also why
// every key is written out: an unwritten key is one nobody knows they can set.
//
// BasicColors overrides all of them with the eight-color ANSI palette gote used before
// this key existed. Those colors are the terminal's own, so they follow whatever scheme
// the user runs — the one thing a 256-color palette cannot do. The other keys are left
// alone while it is set, so turning it off returns the palette the file names.
type SyntaxColors struct {
	BasicColors bool `yaml:"basic_colors"`

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

// LanguageServerConfig selects exactly one transport. Address is a TCP endpoint for a
// server managed elsewhere; Command is an executable followed by its arguments for a
// stdio server whose process lifetime belongs to gote. InitializationOptions is the
// server-specific JSON-shaped object sent during the standard LSP handshake.
type LanguageServerConfig struct {
	Disabled              bool           `yaml:"disabled"`
	Address               string         `yaml:"address"`
	Command               []string       `yaml:"command"`
	InitializationOptions map[string]any `yaml:"initialization_options,omitempty"`
	// SemanticTokens overrides how this server's semantic token types land on the syntax
	// palette, as token-type name to syntax_colors slot name. It is omitempty and normally
	// absent: the type names are the LSP specification's, so gote's built-in map already
	// serves every server that speaks them (see semantic_map.go). This is for the
	// non-standard names a server invents on top — rust-analyzer's builtinType and
	// lifetime, say. An empty slot value turns a type off rather than painting it.
	SemanticTokens map[string]string `yaml:"semantic_tokens,omitempty"`
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
			// completeFunctionCalls off makes an accepted completion insert the name and
			// nothing else. The parens are worth giving up because the parameter hint
			// fires on a TYPED trigger character: when gopls supplies "()" itself, no "("
			// keypress ever happens and the hint never appears for the call you just
			// completed. Typing it yourself pairs the bracket and raises the hint, which
			// is the whole point of having one.
			//
			// usePlaceholders is moot while calls are not completed at all, and is spelled
			// out anyway so turning calls back on does not also bring back a completion
			// that types "Sprintf(format string, a ...any)" into the buffer as literal
			// text — abandoning that tab cycle leaves the signature in the code.
			// semanticTokens is gopls's own switch and not the LSP capability gote
			// declares at initialize; gopls defaults it to false and returns no
			// SemanticTokensProvider at all without it, so both are required. Note this
			// only reaches a config that has no initialization_options of its own —
			// LoadConfig fills the fallback wholesale rather than merging keys — so a
			// user with custom go options here must add it by hand.
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

// SyncConfig materializes the config file and then rewrites it from itself, so a file
// written before a key existed gains that key. EnsureConfig alone cannot do this: it
// writes only when the file is MISSING, which leaves anyone who configured gote before a
// release with no way to discover — or edit — what that release added.
//
// A malformed file is left exactly as it is. LoadConfig answers a parse error with the
// defaults, and writing those back would destroy the config the user is on their way to
// go fix; the path is still returned so `gote config` opens the broken file rather than
// refusing. The cost of the rewrite is that hand-written YAML is reformatted, which is
// why only `gote config` calls this and a launch does not.
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
	normalizeSyntaxColors(&cfg.SyntaxColors)
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
