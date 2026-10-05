package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/brohd11/goutil/configdir"
	"gopkg.in/yaml.v3"
)

// LangConfig is ~/.gote/lang.yml: per-language overrides and language-server transports,
// kept out of config.yml so that file stays short. Everything is optional — a missing
// file, language or server behaves as the built-ins do — so unlike config.yml it is never
// written out in full.
type LangConfig struct {
	// Languages is keyed by language id (languageProfile.id: go, python, markdown, …).
	// Unknown ids are ignored.
	Languages map[string]LanguageSettings `yaml:"languages"`
	// Servers configures transports, keyed by server id (languageLSP.server). A missing
	// entry, or one without a transport, gets the built-in one; Enabled switches a server
	// for every language it serves.
	Servers map[string]LanguageServerConfig `yaml:"servers"`
}

// LanguageSettings overrides one language's built-in profile; zero values keep it.
type LanguageSettings struct {
	// Indent is "tab" or "spaces": the unit block indent (and the indent guides) use.
	Indent string `yaml:"indent"`
	// IndentSize is spaces per level. On its own it implies spaces; with tab it is ignored.
	IndentSize int `yaml:"indent_size"`
}

// LanguageServerConfig selects one transport: Address (TCP, managed elsewhere) or Command
// (a stdio server gote runs). InitializationOptions is sent in the LSP handshake.
type LanguageServerConfig struct {
	// Enabled overrides auto-lsp for this server; nil (the key omitted) inherits it.
	Enabled               *bool          `yaml:"enabled,omitempty"`
	Address               string         `yaml:"address"`
	Command               []string       `yaml:"command"`
	InitializationOptions map[string]any `yaml:"initialization_options,omitempty"`
	// SemanticTokens maps this server's non-standard semantic token types to syntax_colors
	// slots (standard LSP names are already mapped, see semantic_map.go). An empty slot turns
	// a type off.
	SemanticTokens map[string]string `yaml:"semantic_tokens,omitempty"`
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

// LangPath is ~/.gote/lang.yml — what loadLangConfig reads, EnsureLangConfig writes, and
// `gote config lang` opens.
func LangPath() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "lang.yml"), nil
}

// langHeader opens a generated lang.yml. The languages example stays commented, so a
// fresh file changes nothing.
const langHeader = `# gote language settings. A missing language or server keeps its built-in
# behavior. See docs/configuration.md#languages-langyml for the language ids.
#
# languages:
#   go:
#     indent: tab        # tab | spaces
#   python:
#     indent: spaces
#     indent_size: 4     # on its own, implies spaces
#
# servers: one entry per language server. These are the built-ins; delete an entry to
# track future built-in changes, or edit it to override. Add "enabled: true" to run a
# server with auto-lsp off, or "enabled: false" to keep it off with auto-lsp on.
`

// EnsureLangConfig writes lang.yml when missing: the commented header, then the built-in
// servers, so the transports are there to edit. An existing file is never touched.
func EnsureLangConfig() error {
	path, err := LangPath()
	if err != nil {
		return err
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		return err
	}
	body, err := yaml.Marshal(LangConfig{Languages: map[string]LanguageSettings{}, Servers: defaultLanguageServers()})
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, append([]byte(langHeader), body...), 0o644)
}

// loadLangConfig reads lang.yml over the built-in servers. A malformed file yields the
// built-ins and the error.
func loadLangConfig() (LangConfig, error) {
	lang := LangConfig{}
	path, err := LangPath()
	if err == nil {
		if err = configdir.Load(path, &lang); err != nil {
			lang, err = LangConfig{}, fmt.Errorf("%s: %w", path, err)
		}
	}
	normalizeLangConfig(&lang)
	return lang, err
}

// normalizeLangConfig lowercases language ids and fills each server from its built-in: a
// missing or transport-less entry gets the default transport, and one without
// initialization_options inherits the default options.
func normalizeLangConfig(lang *LangConfig) {
	if len(lang.Languages) == 0 {
		lang.Languages = nil // `languages: {}` means the same as no key
	} else {
		languages := make(map[string]LanguageSettings, len(lang.Languages))
		for id, set := range lang.Languages {
			languages[strings.ToLower(strings.TrimSpace(id))] = set
		}
		lang.Languages = languages
	}
	if lang.Servers == nil {
		lang.Servers = map[string]LanguageServerConfig{}
	}
	for id, fallback := range defaultLanguageServers() {
		server, ok := lang.Servers[id]
		if !ok {
			lang.Servers[id] = fallback
			continue
		}
		if server.Address == "" && len(server.Command) == 0 {
			server.Address, server.Command = fallback.Address, fallback.Command
		}
		if server.InitializationOptions == nil {
			server.InitializationOptions = fallback.InitializationOptions
		}
		lang.Servers[id] = server
	}
}

// defaultIndentSpaces is the unit for "indent: spaces" on a language whose built-in is a
// tab and which names no size.
const defaultIndentSpaces = 4

// indentSpaces is a language's block-indent unit after lang.yml (0 means a tab), given
// its built-in.
func (l LangConfig) indentSpaces(id string, builtin int) int {
	set, ok := l.Languages[id]
	if !ok {
		return builtin
	}
	switch strings.ToLower(strings.TrimSpace(set.Indent)) {
	case "tab", "tabs":
		return 0
	case "spaces", "space":
		switch {
		case set.IndentSize > 0:
			return set.IndentSize
		case builtin > 0:
			return builtin
		}
		return defaultIndentSpaces
	}
	if set.IndentSize > 0 {
		return set.IndentSize
	}
	return builtin
}

// lspFor reports whether a server may run: its own enabled setting, else auto-lsp. The
// launch mode's default_allow_lsp is applied separately, at manager creation.
func (c Config) lspFor(server string) bool {
	if cfg, ok := c.Lang.Servers[server]; ok && cfg.Enabled != nil {
		return *cfg.Enabled
	}
	return c.AutoLSP
}

// anyLSP reports whether any server may run, which is when a launch needs a manager at
// all.
func (c Config) anyLSP() bool {
	if c.AutoLSP {
		return true
	}
	for _, cfg := range c.Lang.Servers {
		if cfg.Enabled != nil && *cfg.Enabled {
			return true
		}
	}
	return false
}
