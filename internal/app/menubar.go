package app

import (
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/brohd11/bubblestack/components"
	"github.com/brohd11/bubblestack/core"
)

// headerMenus are the header's menus, left to right.
var headerMenus = []struct{ id, label string }{
	{"file", "File"},
	{"edit", "Edit"},
	{"view", "View"},
	{"options", "Options"},
}

// The menu bar is where the view toggles, preview modes and language-server commands
// live; the right-click menu and the Actions picker keep only what the bar does not
// cover. Minimal mode has no header, so there they stay where they always were (see
// editorViewItems, editorLanguageItems, actionsMenu).

// menuPick wraps run so the pick first closes levels menus (1 for a dropdown row, 2 for a
// submenu row) and then acts.
func menuPick(levels int, run func(*core.Shared) core.Action) func(*core.Shared) core.Action {
	return func(sh *core.Shared) core.Action { return core.Seq(core.Pop(levels), run(sh)) }
}

// Menu row marks, one column before the label. Rows without a mark get blanks as wide as
// the mark, so labels line up; swap the glyphs here to try others.
const (
	menuCheckMark = "✓" // a toggle that is on
	menuRadioMark = "✓" // the chosen value of an enum submenu (View → Preview)
)

// marked prefixes label with mark when on, or with blanks as wide as it.
func marked(mark string, on bool, label string) string {
	if on {
		return mark + " " + label
	}
	return strings.Repeat(" ", lipgloss.Width(mark)) + " " + label
}

// checked is a toggle row; plain rows in a menu of toggles take checked(false, …) too.
func checked(on bool, label string) string { return marked(menuCheckMark, on, label) }

// selected is one choice of an enum submenu.
func selected(on bool, label string) string { return marked(menuRadioMark, on, label) }

// hint is a binding's key as a menu row shows it.
func hint(b key.Binding) string { return b.Help().Key }

// headerMenuItems is the dropdown for menu id, built as it opens so disabled rows and
// checks reflect that moment. parent is the dropdown itself, for anchoring submenus.
func (s *homeScreen) headerMenuItems(sh *core.Shared, id string, parent func() *components.MenuScreen) []components.MenuItem {
	switch id {
	case "edit":
		return s.editMenuItems()
	case "view":
		return s.viewMenuItems(sh, parent)
	case "options":
		return s.optionsMenuItems(sh, parent)
	}
	return []components.MenuItem{{Label: "Nothing here yet", Disabled: true}}
}

// submenu is a row that opens items beside its parent. Esc in the child returns to the
// parent; the child's rows close both (menuPick(2, …)).
func submenu(label string, disabled bool, parent func() *components.MenuScreen, items func() []components.MenuItem) components.MenuItem {
	return components.MenuItem{Label: label, Hint: "›", Disabled: disabled, Pick: func(*core.Shared) core.Action {
		return core.Push(components.NewMenu(components.MenuOpts{Items: items(), Anchor: parent().ChildAnchor()}))
	}}
}

// editMenuItems are the editor's own clipboard rows, labeled with gote's chords.
func (s *homeScreen) editMenuItems() []components.MenuItem {
	items := s.editor.ClipboardItems()
	for i, chord := range []string{"ctrl+c", "ctrl+x", "ctrl+v"} {
		if i < len(items) {
			items[i].Hint = chord
		}
	}
	return items
}

func (s *homeScreen) viewMenuItems(sh *core.Shared, parent func() *components.MenuScreen) []components.MenuItem {
	items := []components.MenuItem{
		submenu(checked(false, "Preview"), !s.previewable(), parent, s.previewMenuItems),
		{Separator: true},
		{Label: checked(s.sidebar, "Sidebar"), Hint: hint(sidebarKey), Pick: menuPick(1, func(*core.Shared) core.Action {
			s.setSidebar(!s.sidebar)
			return core.Action{}
		})},
	}
	if s.panelToggles {
		items = append(items,
			components.MenuItem{Label: checked(s.outlineVisible, "Outline"), Hint: hint(symbolsKey), Pick: menuPick(1, s.toggleOutline)},
			components.MenuItem{Label: checked(s.bottomVisible, "Bottom panel"), Hint: hint(bottomKey), Pick: menuPick(1, s.toggleBottom)})
	}
	return append(items,
		components.MenuItem{Separator: true},
		components.MenuItem{Label: checked(s.editor.WrapMode(), "Wrap"), Hint: hint(wrapKey), Pick: menuPick(1, func(*core.Shared) core.Action {
			s.editor.ToggleWrap()
			return core.Action{}
		})},
		components.MenuItem{Label: checked(s.editor.LineNumMode(), "Line numbers"), Hint: hint(lineNumsKey), Pick: menuPick(1, func(*core.Shared) core.Action {
			s.editor.ToggleLineNums()
			return core.Action{}
		})},
		components.MenuItem{Label: checked(s.gitGutter, "Git gutter"), Pick: menuPick(1, func(*core.Shared) core.Action {
			return core.Async(s.setGitGutter(!s.gitGutter))
		})},
	)
}

// Preview modes as the View → Preview submenu offers them.
const (
	previewModeOff = iota
	previewModeSide
	previewModeFull
)

// previewMode is the current mode, reading the full reader first: it folds the side pane
// away while it is up.
func (s *homeScreen) previewMode() int {
	switch {
	case s.fullPreview != nil:
		return previewModeFull
	case s.preview == previewPane:
		return previewModeSide
	}
	return previewModeOff
}

// setPreviewMode moves to mode from any other, through the same paths as ctrl+p and alt+p.
func (s *homeScreen) setPreviewMode(mode int) core.Action {
	if mode == s.previewMode() || !s.previewable() {
		return core.Action{}
	}
	if mode == previewModeFull {
		return s.toggleFullPreview()
	}
	act := s.closeFullPreview() // restores the side pane the reader folded away
	if mode == previewModeSide {
		s.setPreview(previewPane)
	} else {
		s.setPreview(previewOff)
	}
	return act
}

func (s *homeScreen) previewMenuItems() []components.MenuItem {
	current := s.previewMode()
	row := func(mode int, label, chord string) components.MenuItem {
		return components.MenuItem{Label: selected(current == mode, label), Hint: chord,
			Pick: menuPick(2, func(*core.Shared) core.Action { return s.setPreviewMode(mode) })}
	}
	return []components.MenuItem{
		row(previewModeOff, "Off", ""),
		row(previewModeSide, "Side by side", hint(previewKey)),
		row(previewModeFull, "Full", hint(fullPreviewKey)),
	}
}

func (s *homeScreen) optionsMenuItems(sh *core.Shared, parent func() *components.MenuScreen) []components.MenuItem {
	return []components.MenuItem{
		submenu("LSP", !lspEnabled(sh), parent, func() []components.MenuItem { return s.lspMenuItems(sh) }),
	}
}

// lspMenuItems are Options → LSP: the language-server commands that act on the session
// rather than the symbol under the cursor (those stay on the right-click menu).
func (s *homeScreen) lspMenuItems(sh *core.Shared) []components.MenuItem {
	var items []components.MenuItem
	if s.panelToggles {
		items = append(items, components.MenuItem{Label: checked(false, "Show diagnostics"), Pick: menuPick(2, s.showDiagnostics)})
	}
	return append(items,
		components.MenuItem{Label: checked(s.diagnosticsGutter, "Diagnostics gutter"), Pick: menuPick(2, func(*core.Shared) core.Action {
			s.setDiagnosticsGutter(!s.diagnosticsGutter)
			return core.Action{}
		})},
		components.MenuItem{Label: checked(false, "Find references"), Hint: hint(referencesKey), Pick: menuPick(2, func(sh *core.Shared) core.Action {
			return s.requestAt(sh, lspReqReferences)
		})},
		components.MenuItem{Label: checked(false, "Format document"), Hint: hint(formatKey), Pick: menuPick(2, func(sh *core.Shared) core.Action {
			return s.requestAt(sh, lspReqFormat)
		})},
		components.MenuItem{Label: checked(false, "Restart language servers"), Pick: menuPick(2, s.restartLanguageServers)},
	)
}

// headerInput claims presses and wheel notches over the header rows, which would
// otherwise miss every focusable pane and be broadcast to all of them. It never moves
// focus; a plain left click on a menu label opens that menu. Motion and release pass, so
// a drag begun in a pane keeps its gesture while crossing the header.
func (s *homeScreen) headerInput(sh *core.Shared, msg tea.Msg) (core.Action, bool) {
	if s.header == nil || sh == nil {
		return core.Action{}, false
	}
	var m tea.Mouse
	switch mm := msg.(type) {
	case tea.MouseClickMsg:
		m = mm.Mouse()
	case tea.MouseWheelMsg:
		m = mm.Mouse()
	default:
		return core.Action{}, false
	}
	if m.Y < sh.BodyY() || m.Y >= sh.BodyY()+headerRows {
		return core.Action{}, false
	}
	if _, click := msg.(tea.MouseClickMsg); !click || m.Button != tea.MouseLeft || m.Mod != 0 {
		return core.Action{}, true
	}
	s.closeCompletion()
	for _, span := range s.header.menuSpans {
		if m.Y == sh.BodyY() && m.X >= span.x0 && m.X < span.x1 {
			return s.openHeaderMenu(sh, span), true
		}
	}
	return core.Action{}, true
}

// openHeaderMenu drops menu span down with its top border on the rule, so the label sits
// directly over the box; the border starts one cell left of the label and flips clear of
// it near the right edge.
func (s *homeScreen) openHeaderMenu(sh *core.Shared, span statusSpan) core.Action {
	var menu *components.MenuScreen
	parent := func() *components.MenuScreen { return menu }
	menu = components.NewMenu(components.MenuOpts{
		Items:  s.headerMenuItems(sh, span.id, parent),
		Anchor: components.MenuAnchor{X: max(span.x0-1, 0), Y: sh.BodyY() + headerRows - 1, FlipX: span.x1 + 1},
	})
	return core.Push(menu)
}
