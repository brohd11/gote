package app

import (
	"fmt"
	"strings"

	"github.com/brohd11/bubblestack/components"
	"github.com/brohd11/bubblestack/core"

	"charm.land/bubbles/v2/key"
	"charm.land/lipgloss/v2"
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

// disabledKey restates a binding with the setting that turned it off. A locked key is
// LISTED here rather than dropped: this overlay is the complete reference, and a binding
// that silently vanished would read as a gote bug rather than as a configuration the user
// chose — while one left unmarked would promise a key that does nothing.
//
// The keycodes are carried over so the entry still matches, which is what lets a caller
// pass the result anywhere the live binding would have gone.
func disabledKey(b key.Binding, why string) key.Binding {
	h := b.Help()
	return key.NewBinding(key.WithKeys(b.Keys()...),
		key.WithHelp(h.Key, h.Desc+" — off ("+why+")"))
}

// disabledKeys is disabledKey over a group, for a whole section that one setting silences.
func disabledKeys(why string, binds ...key.Binding) []key.Binding {
	out := make([]key.Binding, 0, len(binds))
	for _, b := range binds {
		out = append(out, disabledKey(b, why))
	}
	return out
}

// helpText renders the overlay's body. This is the COMPLETE reference, not the overflow
// from the deliberately four-entry contextual bar, so anything omitted there must live
// here. The editor section comes from the live editor's own
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
	// the bar still shows; they are listed anyway so this page stands alone. The last three
	// are not on the bar — "/" and the g/G jumps are commands rather than navigation, and
	// the esc toggle is gote's own (editorRelease) — so this is their only home.
	writeSection("navigation", []key.Binding{
		core.PaneHint(),
		core.Hint("back", core.Keys.Back),
		core.Hint("select", core.Keys.Select),
		key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "editor ↔ last pane")),
		core.Hint("filter", s.docsPanel.List().KeyMap.Filter),
		core.Hint("top/bottom", core.Keys.Top, core.Keys.Bottom),
	})
	// The quit row is assembled from the shared keymap rather than spelled out, so
	// rebinding core.Keys.Quit reaches this overlay too. ctrl+c is not in that keymap —
	// the router answers it directly (bubblestack/core/router_keys.go) — so it is named
	// here alongside. The description is gote's own: quitting dirty prompts first.
	quitKey := core.Hint("quit (confirms unsaved changes)",
		core.Keys.Quit, key.NewBinding(key.WithKeys("ctrl+c")))
	// The panel lock and a missing language server silence keys rather than remove them,
	// so the entries are marked with the setting responsible (disabledKey) instead of being
	// dropped from the page.
	panelLock, lspOff := "", ""
	if !s.panelToggles {
		panelLock = "single_file_mode.allow_panel_toggle"
	}
	if s.sh != nil && !lspEnabled(s.sh) {
		lspOff = lspDisabledReason(Of(s.sh))
	}
	mark := func(b key.Binding, why string) key.Binding {
		if why == "" {
			return b
		}
		return disabledKey(b, why)
	}
	// FullHint rather than Hint on the picker: it is the one general key with an alias, and
	// ctrl+alt+a is the only way to reach it from the editor, so the page must name both.
	general := []key.Binding{quitKey, sidebarKey, mark(bottomKey, panelLock),
		mark(findFilesKey, panelLock), flatKey, core.FullHint("actions", core.Keys.Actions),
		previewKey, fullPreviewKey, wrapKey, lineNumsKey, helpKey}
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
	// Two gates over one section. symbolsKey is listed here because alt+o is one of the
	// editor's modified chords, but what it opens is a panel, so the lock is its nearer
	// cause and takes precedence. jumpBackKey is marked by NEITHER: find-in-files pushes
	// onto the same stack (jumpToLocation), so ctrl+o outlives the language server.
	outlineOff := lspOff
	if panelLock != "" {
		outlineOff = panelLock
	}
	writeSection("language server", []key.Binding{
		mark(completionKey, lspOff), mark(definitionKey, lspOff), jumpBackKey,
		mark(hoverKey, lspOff), mark(symbolsKey, outlineOff), mark(referencesKey, lspOff),
		mark(formatKey, lspOff),
	})
	// One prose line rather than a mark on each of the ~15 rows in the three sections
	// below: they describe keys INSIDE panels this launch cannot summon, so the fact worth
	// stating once is that the panels are unreachable, not that each of their keys is.
	if panelLock != "" {
		b.WriteString("The outline, bottom panel and find-in-files sections below are off for this launch\n" +
			"(" + panelLock + "); their keys are kept here for reference.\n\n")
	}
	writeSection("outline panel", []key.Binding{
		core.Hint("jump", core.Keys.Select),
		core.Hint("collapse/expand", core.Keys.Left, core.Keys.Right),
		core.Hint("toggle fold", core.Keys.Toggle),
		core.Hint("filter", s.outlinePanel.List().KeyMap.Filter),
	})
	writeSection("docs list", []key.Binding{renameKey, deleteKey, densityKey, descendKey,
		core.Hint("up a folder (folder view)", s.filePanel.UpKey())})
	b.WriteString("Git colors: ")
	for i, entry := range []struct {
		name  string
		state gitFileState
	}{
		{"staged", gitStaged}, {"untracked", gitUntracked}, {"modified", gitModified},
		{"conflict/deleted", gitConflict}, {"ignored", gitIgnored},
	} {
		if i > 0 {
			b.WriteString(" · ")
		}
		b.WriteString(lipgloss.NewStyle().Foreground(gitStateColor(entry.state)).Render(entry.name))
	}
	b.WriteString(". Clean files are plain; folders reflect changes beneath them.\n\n")
	writeSection("bottom panel", []key.Binding{
		key.NewBinding(key.WithKeys("left", "right"), key.WithHelp("left/right", "switch Diag/Search tab")),
		core.Hint("item", core.Keys.Up, core.Keys.Down),
		core.Hint("jump", core.Keys.Select),
		key.NewBinding(key.WithKeys("home", "end"), key.WithHelp("home/end", "first/last item")),
		key.NewBinding(key.WithKeys("pgup", "pgdown"), key.WithHelp("pgup/pgdown", "scroll message")),
		key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "focus editor")),
	})
	writeSection("find in files", []key.Binding{
		mark(findFilesKey, panelLock),
		key.NewBinding(key.WithKeys("tab", "shift+tab"), key.WithHelp("tab/shift+tab", "move form field")),
		key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "search / jump to result")),
		key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "cancel form")),
	})
	writeSection("editor", s.editor.HelpBindings())
	b.WriteString(clickHelp(s.sh) + "\n")
	b.WriteString("dirty-buffer exit prompt: y save as… & exit · n discard & exit · esc/c cancel\n")
	return b.String()
}
