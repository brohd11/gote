package app

import (
	"github.com/brohd11/bubblestack"
	"github.com/brohd11/bubblestack/components"
	"github.com/brohd11/bubblestack/core"
)

// Run loads the config, builds the context (seeding the doc list for the selected
// mode), and launches the gote TUI: a single editor tab (so bubblestack draws no tab
// strip) with no header or output pane — just the home screen, which in ModeFile masks
// away the rest of the chrome too. A transient status row appears only for feedback
// such as clipboard success/failure. The shared ~/.bubblestack theme, if
// any, is applied by bubblestack.Run.
func Run(version string, opts Options) error {
	cfg, err := LoadConfig()
	if err != nil {
		return err
	}
	return bubblestack.Run(bubblestack.Config{
		App:    New(version, cfg, opts),
		Status: components.NewStatusLine(),
		// Theme left unset — bubblestack.Run applies the shared ~/.bubblestack theme.
		Tabs: []bubblestack.TabEntry{
			{Title: "Editor", New: func(sh *core.Shared) core.Screen { return NewHomeScreen(sh) }},
		},
	})
}
