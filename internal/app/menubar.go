package app

import (
	"slices"
	"strconv"
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/brohd11/bubblestack/components"
	"github.com/brohd11/bubblestack/core"
)

// headerMenus are the header's menus, left to right. key opens each one from anywhere on
// the home screen (alt + its first letter, underlined on the label) — except that a menu
// that yieldsToEditor leaves its chord to a focused editor: alt+f is the byte pair many
// terminals send for Option+→, the editor's word-forward (see
// TestHomeLeavesWordMotionsToTheEditor). alt + a menu's position (headerMenuDigit) opens it
// too and never yields, so File stays a chord away from the editor as alt+1.
var headerMenus = []struct {
	id, label      string
	key            key.Binding
	yieldsToEditor bool
}{
	{"file", "File", key.NewBinding(key.WithKeys("alt+f"), key.WithHelp("alt+f", "File menu")), true},
	{"edit", "Edit", key.NewBinding(key.WithKeys("alt+e"), key.WithHelp("alt+e", "Edit menu")), false},
	{"view", "View", key.NewBinding(key.WithKeys("alt+v"), key.WithHelp("alt+v", "View menu")), false},
	{"options", "Options", key.NewBinding(key.WithKeys("alt+o"), key.WithHelp("alt+o", "Options menu")), false},
}

// headerMenuRows are the indexes into headerMenus with rows right now: minimal mode drops
// what a single file cannot use (headerMenuItems), and a menu left empty is not drawn.
func (s *homeScreen) headerMenuRows(sh *core.Shared) []int {
	var rows []int
	for i, m := range headerMenus {
		if len(s.headerMenuItems(sh, m.id)) > 0 {
			rows = append(rows, i)
		}
	}
	return rows
}

// headerMenuSpans are the drawn labels' columns on the header row, none while minimal
// mode's bar is hidden. They depend only on the labels, so a menu opens from the keyboard
// before anything has been drawn.
func (s *homeScreen) headerMenuSpans(sh *core.Shared) []statusSpan {
	if !s.menuBar {
		return nil
	}
	return menuSpans(s.headerMenuRows(sh))
}

// menuSpans lays out the labels of menus (indexes into headerMenus): " Edit  View ".
func menuSpans(menus []int) []statusSpan {
	spans := make([]statusSpan, 0, len(menus))
	x := 0
	for _, i := range menus {
		m := headerMenus[i]
		w := len([]rune(m.label))
		spans = append(spans, statusSpan{m.id, x + 1, x + 1 + w})
		x += w + 2
	}
	return spans
}

// headerMenuLabel is the label of the menu id.
func headerMenuLabel(id string) string {
	for _, m := range headerMenus {
		if m.id == id {
			return m.label
		}
	}
	return id
}

// menusWidth is the cells spans take, padding included; 0 without any.
func menusWidth(spans []statusSpan) int {
	if len(spans) == 0 {
		return 0
	}
	return spans[len(spans)-1].x1 + 1
}

// headerMenuDigit is the positional chord of the drawn menu at pos: alt+1 for the first.
func headerMenuDigit(pos int) key.Binding {
	k := "alt+" + strconv.Itoa(pos+1)
	return key.NewBinding(key.WithKeys(k), key.WithHelp(k, "menu "+strconv.Itoa(pos+1)))
}

// headerMenuKey opens the menu whose chord is k, if one is. In minimal mode a chord also
// reveals the hidden bar, for the rest of the run, counting positions as it will draw them.
func (s *homeScreen) headerMenuKey(sh *core.Shared, k string) (core.Action, bool) {
	if s.header == nil {
		return core.Action{}, false
	}
	menus := s.headerMenuRows(sh)
	for pos, i := range menus {
		m := headerMenus[i]
		digit := core.MatchKey(k, headerMenuDigit(pos))
		if !digit && !core.MatchKey(k, m.key) {
			continue
		}
		if !digit && m.yieldsToEditor && s.editorHasKeys() {
			return core.Action{}, false
		}
		var reveal tea.Cmd
		if !s.menuBar {
			s.menuBar = true
			reveal = s.rebuildModular(sh, noFocus) // the side column's frames now meet the rule
		}
		act := s.openHeaderMenu(sh, menuSpans(menus)[pos])
		act.Cmd = tea.Batch(reveal, act.Cmd)
		return act, true
	}
	return core.Action{}, false
}

// editorHasKeys reports whether an editor pane holds focus (and so is typing).
func (s *homeScreen) editorHasKeys() bool {
	for _, g := range s.groups() {
		if g.editorPanel.Focused() {
			return true
		}
	}
	return false
}

// menuBarKey is an open header menu's OnKeyOutside: another menu's chord opens it, and
// ←/→ walk to the neighboring menu, wrapping. The menu has already closed its cascade.
func (s *homeScreen) menuBarKey(sh *core.Shared, current, k string) (core.Action, bool) {
	menus := s.headerMenuRows(sh)
	spans := menuSpans(menus)
	at := -1
	for i, span := range spans {
		if span.id == current {
			at = i
		}
	}
	if at < 0 {
		return core.Action{}, false
	}
	switch {
	case core.MatchKey(k, core.Keys.Left):
		return s.openHeaderMenu(sh, spans[(at+len(spans)-1)%len(spans)]), true
	case core.MatchKey(k, core.Keys.Right):
		return s.openHeaderMenu(sh, spans[(at+1)%len(spans)]), true
	}
	for pos, i := range menus {
		m := headerMenus[i]
		if (core.MatchKey(k, m.key) || core.MatchKey(k, headerMenuDigit(pos))) && pos != at {
			return s.openHeaderMenu(sh, spans[pos]), true
		}
	}
	return core.Action{}, false
}

// The menu bar is where gote's commands live (it replaced the Actions picker); the
// right-click menu keeps only what acts on the click (editorContextItems). Minimal mode has
// no header, so it has only its key chords.

// menuPick wraps run so the pick first closes levels menus (1 for a dropdown row, 2 for a
// submenu row) and then acts.
func menuPick(levels int, run func(*core.Shared) core.Action) func(*core.Shared) core.Action {
	return func(sh *core.Shared) core.Action { return core.Seq(core.Pop(levels), run(sh)) }
}

// Menu row marks, one column before the label. Rows without a mark get blanks as wide as
// the mark, so labels line up; swap the glyphs here to try others.
const (
	menuCheckMark = "✓"      // a toggle that is on
	menuRadioMark = "✓"      // the chosen value of an enum submenu (View → Preview)
	menuMoreLabel = "⋯ More" // a submenu's way on to the full list (File → Vaults)
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
// checks reflect that moment. Minimal mode leaves out the rows that reach past its one
// file (other docs, the sidebar, groups, project-wide LSP requests).
func (s *homeScreen) headerMenuItems(sh *core.Shared, id string) []components.MenuItem {
	switch id {
	case "file":
		return tidySeparators(s.fileMenuItems())
	case "edit":
		return tidySeparators(s.editMenuItems())
	case "view":
		return tidySeparators(s.viewMenuItems(sh))
	case "options":
		return tidySeparators(s.optionsMenuItems(sh))
	}
	return nil
}

// tidySeparators drops the separators that rows left out of a menu strand: leading,
// trailing and doubled.
func tidySeparators(items []components.MenuItem) []components.MenuItem {
	var out []components.MenuItem
	for _, item := range items {
		if item.Separator && (len(out) == 0 || out[len(out)-1].Separator) {
			continue
		}
		out = append(out, item)
	}
	if len(out) > 0 && out[len(out)-1].Separator {
		out = out[:len(out)-1]
	}
	return out
}

// fileMenuItems are the current doc's saves, the session's document roots, and the app
// itself. Minimal mode keeps the saves and Quit.
func (s *homeScreen) fileMenuItems() []components.MenuItem {
	items := []components.MenuItem{
		{Label: "Save", Key: 's', Hint: "ctrl+s", Pick: menuPick(1, func(sh *core.Shared) core.Action { return s.editor.Save(sh) })},
		{Label: "Save As", Key: 'a', Hint: "ctrl+shift+s", Pick: menuPick(1, func(sh *core.Shared) core.Action { return s.editor.SaveAs(sh) })},
		{Label: "Close", Key: 'c', Hint: "alt+w", Pick: menuPick(1, func(sh *core.Shared) core.Action { return s.editor.Close(sh) })},
		{Separator: true},
	}
	if !s.minimal {
		items = append(items,
			keyed(submenu("Vaults", false, s.vaultMenuItems), 'v'),
			components.MenuItem{Label: "Refresh", Key: 'r', Pick: menuPick(1, refreshAction)},
			components.MenuItem{Separator: true},
			components.MenuItem{Label: "Update gote", Key: 'u', Pick: menuPick(1, func(sh *core.Shared) core.Action {
				return core.Push(components.NewSelfUpdateLoading(selfUpdateHooks(Of(sh).Version)))
			})},
			components.MenuItem{Separator: true})
	}
	return append(items, components.MenuItem{Label: "Quit", Key: 'q', Hint: hint(core.Keys.ForceQuit), Pick: menuPick(1, s.quit)})
}

// vaultMenuItems are File → Vaults: gote (Config.Default, what a bare launch opens), then
// up to maxRecentVaults vaults — the recently visited first, then the rest by name while
// there is room — with the active root marked, then More, the full Vaults picker: the
// place to add a vault or reach one not listed.
func (s *homeScreen) vaultMenuItems() []components.MenuItem {
	c := Of(s.sh)
	// A default that names a vault is the gote row; listing it again below would repeat it.
	defMode, defName, defDir := resolveDefault(c.Config)
	listed := map[string]bool{}
	if defMode == ModeVault {
		listed[defName] = true
	}
	var names []string
	for _, n := range recentVaults(c.Config) {
		if !listed[n] {
			names = append(names, n)
			listed[n] = true
		}
	}
	for _, v := range VaultList(c.Config) {
		if len(names) >= maxRecentVaults {
			break
		}
		if !listed[v.Name] {
			names = append(names, v.Name)
		}
	}
	onDefault := c.Mode == defMode && c.VaultName == defName && (defMode == ModeHome || c.ScanDir == defDir)
	defHint := c.Config.Default
	if defHint == "" {
		defHint = defaultDocsRef
	}
	items := []components.MenuItem{
		{Label: selected(onDefault, "gote"), Hint: defHint, Pick: menuPick(2, func(*core.Shared) core.Action {
			return core.PropagateAll(SwitchVaultMsg{Default: true})
		})},
		{Separator: true},
	}
	for _, name := range names {
		active := c.Mode == ModeVault && c.VaultName == name
		items = append(items, components.MenuItem{Label: selected(active, name), Pick: menuPick(2, func(*core.Shared) core.Action {
			return core.PropagateAll(SwitchVaultMsg{Name: name})
		})})
	}
	if len(names) > 0 {
		items = append(items, components.MenuItem{Separator: true})
	}
	// Marked like the rows above, so its label lines up with theirs.
	return append(items, components.MenuItem{Label: selected(false, menuMoreLabel), Key: 'm', Pick: menuPick(2, func(sh *core.Shared) core.Action {
		return core.Push(vaultsMenu(sh))
	})})
}

// refreshAction reseeds the doc list and checks open buffers for external changes. The
// home screen does the list reload on the broadcast.
func refreshAction(sh *core.Shared) core.Action {
	return core.Seq(core.PropagateAll(ReseedMsg{}), core.Async(Of(sh).checkDiskChanges()))
}

// menuStyle is every gote menu's look, matching the sidebar: a background-filled
// selection with un-accented text, and a border in the frame color rather than the accent.
var menuStyle = components.MenuStyle{
	Selection: core.SelectionOpts{Style: core.SelectBackground, NoAccent: true},
	Focus:     components.FocusLegend,
}

// submenu is a row that opens items beside it (enter, click, → or hover); the menu opens
// the child itself, in its own style. Esc in the child returns to the parent; the child's
// rows close both (menuPick(2, …)).
func submenu(label string, disabled bool, items func() []components.MenuItem) components.MenuItem {
	return components.MenuItem{Label: label, Disabled: disabled, Submenu: items}
}

// keyed gives item accelerator k.
func keyed(item components.MenuItem, k rune) components.MenuItem {
	item.Key = k
	return item
}

// editMenuItems are the editor's own clipboard rows, labeled with gote's chords.
func (s *homeScreen) editMenuItems() []components.MenuItem {
	clipboard := s.editor.ClipboardItems()
	for i, chord := range []string{"ctrl+c", "ctrl+x", "ctrl+v"} {
		if i < len(clipboard) {
			clipboard[i].Hint = chord
		}
	}
	edits := map[string]components.MenuItem{}
	for _, it := range s.editor.EditItems() {
		edits[it.Label] = it
	}
	row := func(label string) []components.MenuItem {
		if it, ok := edits[label]; ok {
			return []components.MenuItem{it}
		}
		return nil
	}
	sep := []components.MenuItem{{Separator: true}}
	items := slices.Concat(row("Undo"), row("Redo"), sep, clipboard, sep, row("Search"))
	// The reader shows the doc, not the buffer these rows would change.
	if s.readerOn() {
		for i := range items {
			items[i].Disabled = true
		}
	}
	// Search results open the bottom panel, so this respects the panel lock; it searches
	// other files, so minimal mode leaves it out.
	if s.panelToggles && !s.minimal {
		items = append(items,
			components.MenuItem{Label: "Find in Files", Key: 'f', Hint: hint(findFilesKey), Pick: menuPick(1, func(sh *core.Shared) core.Action {
				return core.Push(s.findFilesForm(sh))
			})})
	}
	tail := slices.Concat(sep, row("Indent"), row("Dedent"), row("Toggle comment"))
	if s.readerOn() {
		for i := range tail {
			tail[i].Disabled = true
		}
	}
	return append(items, tail...)
}

func (s *homeScreen) viewMenuItems(sh *core.Shared) []components.MenuItem {
	items := []components.MenuItem{
		keyed(submenu(checked(false, "Preview"), false, func() []components.MenuItem { return s.previewMenuItems(sh) }), 'p'),
	}
	if !s.minimal {
		items = append(items,
			keyed(submenu(checked(false, "Tab Groups"), false, s.tabGroupMenuItems), 't'),
			keyed(submenu(checked(false, "File view"), !s.sidebar, s.fileViewMenuItems), 'f'))
	}
	items = append(items, components.MenuItem{Separator: true})
	if !s.minimal {
		items = append(items, components.MenuItem{Label: checked(s.sidebar, "Sidebar"), Key: 's', Hint: hint(sidebarKey), Pick: menuPick(1, func(*core.Shared) core.Action {
			s.setSidebar(!s.sidebar)
			return core.Action{}
		})})
	}
	if s.panelToggles {
		items = append(items,
			components.MenuItem{Label: checked(s.outlineVisible, "Outline"), Key: 'o', Hint: hint(symbolsKey), Pick: menuPick(1, s.toggleOutline)},
			components.MenuItem{Label: checked(s.bottomVisible, "Bottom panel"), Key: 'b', Hint: hint(bottomKey), Pick: menuPick(1, s.toggleBottom)})
	}
	return append(items,
		components.MenuItem{Separator: true},
		components.MenuItem{Label: checked(s.editor.WrapMode(), "Wrap"), Key: 'w', Hint: hint(wrapKey), Pick: menuPick(1, func(*core.Shared) core.Action {
			s.editor.ToggleWrap()
			return core.Action{}
		})},
		components.MenuItem{Label: checked(s.editor.LineNumMode(), "Line numbers"), Key: 'l', Hint: hint(lineNumsKey), Pick: menuPick(1, func(*core.Shared) core.Action {
			s.editor.ToggleLineNums()
			return core.Action{}
		})},
		components.MenuItem{Label: checked(s.gitGutter, "Git gutter"), Key: 'g', Pick: menuPick(1, func(*core.Shared) core.Action {
			return core.Async(s.setGitGutter(!s.gitGutter))
		})},
		components.MenuItem{Separator: true},
		components.MenuItem{Label: checked(false, "Theme"), Key: 'h', Pick: menuPick(1, func(*core.Shared) core.Action {
			return core.Push(components.ThemePicker())
		})},
		components.MenuItem{Label: checked(false, "Help"), Key: 'e', Hint: "alt+?", Pick: menuPick(1, func(*core.Shared) core.Action {
			return core.Push(s.helpScreen())
		})},
	)
}

// tabGroupMenuItems are View → Tab Groups. A row that cannot act now is disabled, for the
// same reasons the shortcuts refuse (tabMoveUnavailable).
func (s *homeScreen) tabGroupMenuItems() []components.MenuItem {
	return []components.MenuItem{
		{Label: "Move tab left", Key: 'l', Hint: hint(moveTabLeftKey), Disabled: s.tabMoveUnavailable(-1) != "",
			Pick: menuPick(2, func(sh *core.Shared) core.Action { return s.moveTab(sh, -1) })},
		{Label: "Move tab right / split", Key: 'r', Hint: hint(moveTabRightKey), Disabled: s.tabMoveUnavailable(1) != "",
			Pick: menuPick(2, func(sh *core.Shared) core.Action { return s.moveTab(sh, 1) })},
		{Label: "Close editor group", Key: 'c', Disabled: len(s.groups()) < 2, Pick: menuPick(2, s.closeEditorGroup)},
	}
}

// showDiagnostics brings the dock's diagnostics panel up and focuses it.
func (s *homeScreen) showDiagnostics(sh *core.Shared) core.Action {
	s.bottom.selectTab(bottomDiagnostics)
	if s.bottomVisible {
		return core.Async(s.modular.FocusSlot(s.panelSlot(s.bottom)))
	}
	return s.toggleBottom(sh)
}

// previewMenuItems are View → Preview: the current doc's mode (markdown only), the mode
// new docs start in (written to config.yml), and the side preview, which is chrome and so
// available on any doc.
func (s *homeScreen) previewMenuItems(sh *core.Shared) []components.MenuItem {
	return []components.MenuItem{
		keyed(submenu(checked(false, "Doc"), !s.previewable(), s.docModeMenuItems), 'd'),
		keyed(submenu(checked(false, "Default"), false, func() []components.MenuItem { return s.defaultModeMenuItems(sh) }), 'e'),
		{Separator: true},
		{Label: checked(s.sidePreview, "Side by side"), Key: 's', Hint: hint(sidePreviewKey), Pick: menuPick(2, s.toggleSidePreview)},
	}
}

// docModeMenuItems are View → Preview → Doc.
func (s *homeScreen) docModeMenuItems() []components.MenuItem {
	current := s.docMode()
	row := func(mode int, label, chord string, accel rune) components.MenuItem {
		return components.MenuItem{Label: selected(current == mode, label), Hint: chord, Key: accel,
			Pick: menuPick(3, func(*core.Shared) core.Action { return s.setDocMode(mode) })}
	}
	return []components.MenuItem{
		row(docModeOff, "Off", "", 'o'),
		row(docModeLive, "Live", "", 'l'),
		row(docModeReader, "Reader", "", 'r'),
	}
}

// defaultModeMenuItems are View → Preview → Default.
func (s *homeScreen) defaultModeMenuItems(sh *core.Shared) []components.MenuItem {
	live := Of(sh).Config.liveByDefault()
	row := func(name, label string, on bool, accel rune) components.MenuItem {
		return components.MenuItem{Label: selected(on, label), Key: accel,
			Pick: menuPick(3, func(sh *core.Shared) core.Action { return s.setDefaultDocMode(sh, name) })}
	}
	return []components.MenuItem{
		row(docModeOffName, "Off", !live, 'o'),
		row(docModeLiveName, "Live", live, 'l'),
	}
}

// fileViewMenuItems are View → File view: how the docs sidebar lists documents.
func (s *homeScreen) fileViewMenuItems() []components.MenuItem {
	row := func(view fileView, label string, accel rune) components.MenuItem {
		return components.MenuItem{Label: selected(s.fileView == view, label), Key: accel,
			Pick: menuPick(2, func(*core.Shared) core.Action { s.setFileView(view); return core.Action{} })}
	}
	return []components.MenuItem{
		row(fileViewFlat, "Flat", 'f'),
		row(fileViewFolder, "Folder", 'o'),
		row(fileViewGrouped, "Grouped", 'g'),
	}
}

func (s *homeScreen) optionsMenuItems(sh *core.Shared) []components.MenuItem {
	return []components.MenuItem{
		keyed(submenu("LSP", !lspEnabled(sh), func() []components.MenuItem { return s.lspMenuItems(sh) }), 'l'),
	}
}

// lspMenuItems are Options → LSP: the language-server commands that act on the session
// rather than the symbol under the cursor (those stay on the right-click menu). Minimal
// mode keeps the gutter and the restart.
func (s *homeScreen) lspMenuItems(sh *core.Shared) []components.MenuItem {
	var items []components.MenuItem
	if s.panelToggles {
		items = append(items, components.MenuItem{Label: checked(false, "Show diagnostics"), Key: 'd', Pick: menuPick(2, s.showDiagnostics)})
	}
	items = append(items,
		components.MenuItem{Label: checked(s.diagnosticsGutter, "Diagnostics gutter"), Key: 'g', Pick: menuPick(2, func(*core.Shared) core.Action {
			s.setDiagnosticsGutter(!s.diagnosticsGutter)
			return core.Action{}
		})})
	if !s.minimal {
		items = append(items,
			components.MenuItem{Label: checked(false, "Find references"), Key: 'r', Hint: hint(referencesKey), Pick: menuPick(2, func(sh *core.Shared) core.Action {
				return s.requestAt(sh, lspReqReferences)
			})},
			components.MenuItem{Label: checked(false, "Format document"), Key: 'f', Hint: hint(formatKey), Pick: menuPick(2, func(sh *core.Shared) core.Action {
				return s.requestAt(sh, lspReqFormat)
			})})
	}
	return append(items,
		components.MenuItem{Label: checked(false, "Restart language servers"), Key: 's', Pick: menuPick(2, s.restartLanguageServers)},
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
	if span, ok := s.headerSpanAt(sh, m.X, m.Y); ok {
		return s.openHeaderMenu(sh, span), true
	}
	return core.Action{}, true
}

// headerSpanAt is the menu label under absolute cell (x, y), if any.
func (s *homeScreen) headerSpanAt(sh *core.Shared, x, y int) (statusSpan, bool) {
	if s.header == nil || y != sh.BodyY() {
		return statusSpan{}, false
	}
	for _, span := range s.header.menuSpans {
		if x >= span.x0 && x < span.x1 {
			return span, true
		}
	}
	return statusSpan{}, false
}

// menuBarSwitch is an open header menu's OnPointerOutside: pointing at (or clicking)
// another label switches to that menu, as a desktop menu bar does; the menu has already
// closed its cascade. The open menu's own label is declined, so a click there closes it.
func (s *homeScreen) menuBarSwitch(sh *core.Shared, current string, x, y int) (core.Action, bool) {
	span, ok := s.headerSpanAt(sh, x, y)
	if !ok || span.id == current {
		return core.Action{}, false
	}
	return s.openHeaderMenu(sh, span), true
}

// openHeaderMenu drops menu span down with its top border on the rule, so the label sits
// directly over the box; the border starts one cell left of the label and flips clear of
// it near the right edge.
func (s *homeScreen) openHeaderMenu(sh *core.Shared, span statusSpan) core.Action {
	return core.Push(components.NewMenu(components.MenuOpts{
		Items:  s.headerMenuItems(sh, span.id),
		Anchor: components.MenuAnchor{X: max(span.x0-1, 0), Y: sh.BodyY() + headerRows - 1, FlipX: span.x1 + 1},
		Style:  menuStyle,
		OnPointerOutside: func(sh *core.Shared, x, y int) (core.Action, bool) {
			return s.menuBarSwitch(sh, span.id, x, y)
		},
		OnKeyOutside: func(sh *core.Shared, k string) (core.Action, bool) {
			return s.menuBarKey(sh, span.id, k)
		},
	}))
}
