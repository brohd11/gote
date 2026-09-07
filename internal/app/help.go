package app

import (
	"fmt"
	"strings"

	"github.com/brohd11/bubblestack/components"
	"github.com/brohd11/bubblestack/core"

	"charm.land/bubbles/v2/key"
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

// clickHelp names the two modifier-click gestures. They are described rather than listed
// as bindings because they are configured (click_definition / click_context) and because
// a terminal may claim either one before gote sees it — so the line says what gote is
// listening for, not what will certainly happen.
func clickHelp(sh *core.Shared) string {
	if sh == nil {
		return ""
	}
	cfg := Of(sh).Config
	var parts []string
	if cfg.ClickDefinition != "" && cfg.ClickDefinition != clickNone {
		parts = append(parts, cfg.ClickDefinition+"+click go to definition")
	}
	if cfg.ClickContext != "" && cfg.ClickContext != clickNone {
		parts = append(parts, cfg.ClickContext+"+click editor menu (stands in for right-click)")
	}
	if len(parts) == 0 {
		return ""
	}
	return "mouse: " + strings.Join(parts, " · ") + "\n"
}

// helpText renders the overlay's body. This is the COMPLETE reference, not the overflow
// from a bar that lists the common keys: the bar carries only "? more", so anything not
// written here is written nowhere. The editor section comes from the live editor's own
// HelpBindings, so its chords are stated once (in bubblestack).
//
// The key column is 14 wide because "alt+backspace" is 13 — every label here spells its
// modifier out rather than using ⌥, so the widest entry sets the column.
func (s *homeScreen) helpText() string {
	var b strings.Builder
	writeSection := func(name string, binds []key.Binding) {
		b.WriteString(name + "\n\n")
		for _, kb := range binds {
			h := kb.Help()
			fmt.Fprintf(&b, "  %-14s %s\n", h.Key, h.Desc)
		}
		b.WriteString("\n")
	}
	// Navigation is built from the same helpers the bar builds itself from
	// (ModularScreen.HelpView, ListPanel.PanelHelp), so rebinding any of them reaches
	// this overlay rather than leaving it quietly stale. The first three are the entries
	// the bar still shows; they are listed anyway so this page stands alone. The last two
	// are not on the bar — "/" and the g/G jumps are commands rather than navigation — so
	// this is their only home.
	writeSection("navigation", []key.Binding{
		core.PaneHint(),
		core.Hint("back", core.Keys.Back),
		core.Hint("select", core.Keys.Select),
		core.Hint("filter", s.docsPanel.List().KeyMap.Filter),
		core.Hint("top/bottom", core.Keys.Top, core.Keys.Bottom),
	})
	// The quit row is assembled from the shared keymap rather than spelled out, so
	// rebinding core.Keys.Quit reaches this overlay too. ctrl+c is not in that keymap —
	// the router answers it directly (bubblestack/core/router_keys.go) — so it is named
	// here alongside. The description is gote's own: quitting dirty prompts first.
	quitKey := core.Hint("quit (confirms unsaved changes)",
		core.Keys.Quit, key.NewBinding(key.WithKeys("ctrl+c")))
	general := []key.Binding{quitKey, sidebarKey, bottomKey, flatKey, actionsKey, previewKey, fullPreviewKey,
		wrapKey, lineNumsKey, helpKey}
	if !s.minimal {
		general = append([]key.Binding{quitKey, newBufferKey}, general[1:]...)
		general = append(general, previousDocumentKey, nextDocumentKey)
	}
	writeSection("general", general)
	if !s.minimal {
		b.WriteString("[ / ]: previous/next document outside text entry; Actions switches Open between list and tabs.\n\n")
	}
	// These act on the selected row, so they are the docs list's keys rather than the
	// screen's — and, off the bar, this is the only place they are written down.
	// The language-server section. These fire from the editor (they all carry a
	// modifier, so they reach this screen ahead of the pane) and do nothing anywhere
	// else, which is why they are their own group rather than more "general" rows.
	writeSection("language server", []key.Binding{
		completionKey, definitionKey, jumpBackKey, hoverKey, symbolsKey, referencesKey, formatKey,
	})
	writeSection("docs list", []key.Binding{renameKey, deleteKey, densityKey,
		core.Hint("up a folder (folder view)", s.filePanel.UpKey())})
	writeSection("diagnostics panel", s.diagnostics.PanelHelp())
	writeSection("editor", s.editor.HelpBindings())
	b.WriteString(clickHelp(s.sh) + "\n")
	b.WriteString("dirty-buffer exit prompt: y save as… & exit · n discard & exit · esc/c cancel\n")
	return b.String()
}
