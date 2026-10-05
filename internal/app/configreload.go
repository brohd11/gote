package app

import (
	"path/filepath"
	"reflect"
	"strings"

	"github.com/brohd11/bubblestack/core"
)

// EnsureConfigFiles is what opening the config to edit it does first, from `gote config`
// or File ▸ Config: top up config.yml with any key added since it was written (SyncConfig
// rather than EnsureConfig), and materialize lang.yml beside it, as a launch would.
func EnsureConfigFiles() error {
	if _, err := SyncConfig(); err != nil {
		return err
	}
	return EnsureLangConfig()
}

// isConfigFile reports whether path is config.yml or lang.yml, whose save reloads the
// config.
func isConfigFile(path string) bool {
	if path == "" {
		return false
	}
	path = filepath.Clean(path)
	for _, resolve := range []func() (string, error){ConfigPath, LangPath} {
		if p, err := resolve(); err == nil && filepath.Clean(p) == path {
			return true
		}
	}
	return false
}

// ReloadConfig re-reads config.yml and lang.yml into the running session. A file that
// fails to load changes nothing: LoadConfig's fallback is the defaults, which would wipe
// the session's settings over a typo. restart names what this session cannot pick up.
//
// Startup states the user toggles at runtime (file_view, indent_guides, the gutters, the
// menu bar) are left as they are; docs opened from now on follow the rest.
func (c *Ctx) ReloadConfig() (restart []string, err error) {
	cfg, err := LoadConfig()
	if err != nil {
		return nil, err
	}
	old := c.Config
	// A running manager holds its own copy of the config (lspManager.cfg).
	if c.lsp != nil && (old.AutoLSP != cfg.AutoLSP || !reflect.DeepEqual(old.Lang.Servers, cfg.Lang.Servers)) {
		restart = append(restart, "language servers")
	}
	// A Highlighter bakes the palette into its spans at Parse time (see newWithColorProfile).
	if !reflect.DeepEqual(old.SyntaxColors, cfg.SyntaxColors) && len(c.OpenDocs()) > 0 {
		restart = append(restart, "colors in open docs")
	}
	c.Config = cfg
	if !c.depthSet {
		c.Depth = cfg.ScanDepth
	}
	if !c.extsSet {
		c.Filter = cfg.Filter()
		c.NewExt = defaultExt(c.Filter.Exts)
	}
	applySyntaxPalette(syntaxColorsForProfile(cfg.SyntaxColors, c.profile))
	if c.lsp == nil && cfg.anyLSP() && cfg.modeDefaults(c.Mode).AllowLSP {
		c.lsp = newLSPManager(cfg, c.Version)
	}
	// The doc list is left to the caller's ReseedMsg, which re-seeds with the new filter.
	return restart, nil
}

// reloadConfigAction is ReloadConfig with its outcome in the status bar.
func reloadConfigAction(sh *core.Shared) core.Action {
	restart, err := Of(sh).ReloadConfig()
	switch {
	case err != nil:
		// LoadConfig joins one error per file, newline-separated.
		return core.SetStatus("config not reloaded: " + strings.ReplaceAll(err.Error(), "\n", "; "))
	case len(restart) > 0:
		return core.SetStatus("config reloaded; restart gote to apply " + strings.Join(restart, " and "))
	}
	return core.SetStatus("config reloaded")
}
