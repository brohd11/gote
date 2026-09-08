package app

import (
	"os"

	"github.com/brohd11/bubblestack"
	"github.com/brohd11/bubblestack/components"
	"github.com/brohd11/bubblestack/core"
	"github.com/charmbracelet/colorprofile"
)

// Run builds the context from the already-loaded config (seeding the doc list for the
// selected mode) and launches the gote TUI: a single editor tab (so bubblestack draws
// no tab strip) with no header or output pane — just the home screen, which in ModeFile
// masks away the rest of the chrome too. A transient status row appears only for
// feedback such as clipboard success/failure. The shared ~/.bubblestack theme, if
// any, is applied by bubblestack.Run.
//
// cfg is passed in rather than loaded here because the CLI needs it first: resolving a
// bare argument against the configured vault names is part of the argument grammar, and
// reading config.yml twice could have it answer the two questions differently.
func Run(version string, cfg Config, opts Options) error {
	// Use the same capability detector and output as Bubble Tea, before any
	// highlighter caches styles. Detection includes terminfo and tmux capabilities.
	profile := colorprofile.Detect(os.Stdout, os.Environ())
	c := newWithColorProfile(version, cfg, opts, profile)
	defer c.close()
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
	// Here rather than in homeScreen.QuitGate because this is the only point EVERY exit
	// passes through: the gate is bypassed by the force-quit and by minimal mode's
	// ctrl+x. The context is still standing, which is what makes the open set readable.
	// Failing to write it is not a reason to fail the program.
	_ = c.saveSession()
	return nil
}
