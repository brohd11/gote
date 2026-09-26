package app

import (
	"fmt"
	"strings"

	"github.com/brohd11/bubblestack/components"
	"github.com/brohd11/bubblestack/core"

	"charm.land/bubbles/v2/key"
	"charm.land/lipgloss/v2"
)

// helpScreen is the "?" overlay: gote's shortcuts, grouped, in a scrollable page. ? opens
// it when nothing captures text; alt+? works anywhere, the editor included.
func (s *homeScreen) helpScreen() *components.DocScreen {
	return components.NewDocScreen(components.DocOpts{
		Title: "gote · shortcuts",
		Crumb: "help",
		Render: func(int) string {
			return s.helpText()
		},
	})
}

// clickHelp describes the two modifier-click gestures in prose: they are configurable, and
// a terminal may claim either before gote sees it.
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

// disabledKey restates a binding with the setting that disabled it. Locked keys are listed
// and marked rather than dropped, since this page is the complete reference. The keycodes
// carry over so the entry still matches.
func disabledKey(b key.Binding, why string) key.Binding {
	h := b.Help()
	return key.NewBinding(key.WithKeys(b.Keys()...),
		key.WithHelp(h.Key, h.Desc+" — off ("+why+")"))
}

// helpText renders the complete reference (the bar shows only four entries). The editor
// section comes from the editor's own HelpBindings. The key column fits
// "ctrl+alt+backspace".
func (s *homeScreen) helpText() string {
	var b strings.Builder
	writeSection := func(name string, binds []key.Binding) {
		b.WriteString(name + "\n\n")
		for _, kb := range binds {
			h := kb.Help()
			fmt.Fprintf(&b, "  %-19s %s\n", h.Key, h.Desc)
		}
		b.WriteString("\n")
	}
	// Built from the same helpers as the bar so rebinds reach this page. "/", g/G and the esc
	// toggle are not on the bar, so this is their only listing.
	writeSection("navigation", []key.Binding{
		core.PaneHint(),
		core.Hint("back", core.Keys.Back),
		core.Hint("select", core.Keys.Select),
		key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "editor ↔ last pane")),
		core.Hint("filter", s.docsPanel.List().KeyMap.Filter),
		core.Hint("top/bottom", core.Keys.Top, core.Keys.Bottom),
	})
	// From the shared keymap so rebinding Quit reaches here; ctrl+c is the router's own.
	quitKey := core.Hint("quit (confirms unsaved changes)",
		core.Keys.Quit, key.NewBinding(key.WithKeys("ctrl+c")))
	// Keys silenced by the panel lock or a missing language server are marked with the
	// responsible setting (disabledKey).
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
	// Language-server keys fire from the editor only. The outline key opens a panel, so the
	// panel lock takes precedence over the LSP gate; jump-back is never marked, since
	// find-in-files uses the same stack.
	outlineOff := lspOff
	if panelLock != "" {
		outlineOff = panelLock
	}
	writeSection("language server", []key.Binding{
		mark(completionKey, lspOff), mark(definitionKey, lspOff), jumpBackKey,
		mark(hoverKey, lspOff), mark(symbolsKey, outlineOff), mark(referencesKey, lspOff),
		mark(formatKey, lspOff),
	})
	// One line instead of marking the ~15 keys inside panels this launch cannot open.
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
	writeSection("git diff", []key.Binding{gitDiffKey})
	b.WriteString("Click a git marker to inspect its hunk against HEAD (including unsaved edits).\n" +
		"The shortcut also accepts the hunk's three context lines. Up/Down and PgUp/PgDn scroll; Esc closes.\n" +
		"Wheel over the popup scrolls it; wheel outside closes it and scrolls the panel.\n" +
		"Typing, outside clicks, pane/document changes and layout changes close it.\n\n")
	b.WriteString(clickHelp(s.sh) + "\n")
	b.WriteString("dirty-buffer exit prompt: y save as… & exit · n discard & exit · esc/c cancel\n")
	return b.String()
}
