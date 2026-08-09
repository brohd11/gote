package app

import (
	"github.com/brohd11/bubblestack"
	"github.com/brohd11/bubblestack/core"
)

// Run launches the gote TUI: a single editor tab (so bubblestack draws no tab strip)
// with no header, output pane, or status line — just the screen. The shared
// ~/.bubblestack theme, if any, is applied by bubblestack.Run.
// version is the binary's version string, kept on the context for the self-update flow.
func Run(version string) error {
	return bubblestack.Run(bubblestack.Config{
		App: New(version),
		// Theme left unset — bubblestack.Run applies the shared ~/.bubblestack theme.
		Tabs: []bubblestack.TabEntry{
			{Title: "Editor", New: func(sh *core.Shared) core.Screen { return NewEditorScreen(sh) }},
		},
	})
}
