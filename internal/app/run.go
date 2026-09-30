package app

import (
	"os"

	"charm.land/bubbles/v2/key"

	"github.com/brohd11/bubblestack"
	"github.com/brohd11/bubblestack/components"
	"github.com/brohd11/bubblestack/core"
	"github.com/charmbracelet/colorprofile"
)

// Run builds the context from the loaded config and launches the TUI: one tab (no tab
// strip), no header or output pane, and a status row only for brief feedback. cfg comes
// from the CLI, which already needed it to resolve vault names.
func Run(version string, cfg Config, opts Options) error {
	applyKeymap()
	// Use the same capability detector and output as Bubble Tea, before any
	// highlighter caches styles. Detection includes terminfo and tmux capabilities.
	profile := colorprofile.Detect(os.Stdout, os.Environ())
	c := newWithColorProfile(version, cfg, opts, profile)
	defer c.close()
	if c.Mode == ModeVault {
		noteRecentVault(c.VaultName) // a launch into a vault is a visit too
	}
	err := bubblestack.Run(bubblestack.Config{
		App:    c,
		Status: components.NewStatusLine(),
		// Theme left unset — bubblestack.Run applies the shared ~/.bubblestack theme.
		Tabs: []bubblestack.TabEntry{
			{Title: "Editor", New: func(sh *core.Shared) core.Screen { return NewHomeScreen(sh) }},
		},
	})
	if err != nil {
		return err // a run that fell over has no buffer state worth keeping
	}
	// Save the session here because every exit passes through (QuitGate is bypassed by
	// force-quit and minimal mode's alt+w). A failed write does not fail the program.
	_ = c.saveSession()
	return nil
}

// applyKeymap moves the router's force-quit to ctrl+q, freeing ctrl+c/x/v for the editor's
// clipboard so the chords match every desktop OS.
func applyKeymap() {
	core.Keys.ForceQuit = key.NewBinding(key.WithKeys("ctrl+q"), key.WithHelp("ctrl+q", "quit"))
}
