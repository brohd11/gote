package app

import (
	"github.com/brohd11/bubblestack"
	"github.com/brohd11/bubblestack/core"
)

// Run loads the config, builds the context (seeding the doc list for the selected
// mode), and launches the gote TUI: a single editor tab (so bubblestack draws no tab
// strip) with no header, output pane, or status line — just the home screen, which in
// ModeFile masks away the rest of the chrome too. The shared ~/.bubblestack theme, if
// any, is applied by bubblestack.Run.
func Run(version string, opts Options) error {
	cfg, err := LoadConfig()
	if err != nil {
		return err
	}
	return bubblestack.Run(bubblestack.Config{
		App: New(version, cfg, opts),
		// Theme left unset — bubblestack.Run applies the shared ~/.bubblestack theme.
		Tabs: []bubblestack.TabEntry{
			{Title: "Editor", New: func(sh *core.Shared) core.Screen { return NewHomeScreen(sh) }},
		},
	})
}
