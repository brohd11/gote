package app

import (
	"fmt"
	"strings"

	"github.com/brohd11/bubblestack/components"

	"github.com/charmbracelet/bubbles/key"
)

// helpScreen is the pushed "?" overlay: a scrollable doc listing gote's shortcuts,
// grouped. Summoned with ? while nothing is capturing text, or alt+? from anywhere —
// the editor included, since a modified key passes the capture gate. esc pops back.
func (s *homeScreen) helpScreen() *components.DocScreen {
	return components.NewDocScreen(components.DocOpts{
		Title: "gote · shortcuts",
		Crumb: "help",
		Render: func(int) string {
			return s.helpText()
		},
	})
}

// helpText renders the overlay's body. The editor section comes from the live
// editor's own HelpBindings, so its chords are stated once (in bubblestack).
func (s *homeScreen) helpText() string {
	var b strings.Builder
	writeSection := func(name string, binds []key.Binding) {
		b.WriteString(name + "\n\n")
		for _, kb := range binds {
			h := kb.Help()
			fmt.Fprintf(&b, "  %-10s %s\n", h.Key, h.Desc)
		}
		b.WriteString("\n")
	}
	writeSection("general", []key.Binding{
		key.NewBinding(key.WithKeys("q", "ctrl+c"), key.WithHelp("q/ctrl+c", "quit (confirms unsaved changes)")),
		sidebarKey, actionsKey, previewKey, wrapKey, lineNumsKey, helpKey,
	})
	writeSection("editor", s.editor.HelpBindings())
	b.WriteString("dirty-buffer exit prompt: y save as… & exit · n discard & exit · esc/c cancel\n")
	return b.String()
}
