package app

import (
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/brohd11/bubblestack/components"
	"github.com/brohd11/bubblestack/components/editor"
	"github.com/brohd11/bubblestack/core"
	"github.com/charmbracelet/x/ansi"
	"go.lsp.dev/protocol"
)

func groupHome(t *testing.T) (*homeScreen, *core.Shared) {
	t.Helper()
	s, sh := newHome(t)
	Of(sh).close()
	Of(sh).lsp = nil
	t.Cleanup(Of(sh).close)
	s.gitGutter = false
	return s, sh
}

func groupInvariant(t *testing.T, s *homeScreen, sh *core.Shared) {
	t.Helper()
	c := Of(sh)
	seen := map[string]bool{}
	if len(c.groups) < 1 || len(c.groups) > maxEditorGroups {
		t.Fatal("invalid group count")
	}
	for _, g := range c.groups {
		if len(c.groups) > 1 && len(g.tabs) == 0 {
			t.Fatal("empty split")
		}
		for _, id := range g.tabs {
			if seen[id] {
				t.Fatalf("duplicate membership: %q", id)
			}
			seen[id] = true
			if _, ok := c.buffer(id); !ok {
				t.Fatalf("missing buffer: %q", id)
			}
		}
		if len(g.tabs) > 0 && !slices.Contains(g.tabs, g.currentID) {
			t.Fatal("selected document is outside its group")
		}
		if g.currentPath != "" {
			ed, _ := c.buffer(g.currentID)
			if ed != g.editor {
				t.Fatal("selected editor differs from registry")
			}
		}
	}
	if len(seen) != len(c.OpenDocs()) {
		t.Fatal("unassigned buffer")
	}
	if c.activeGroup != s.editorGroup || c.activeID != s.currentID {
		t.Fatal("active document mismatch")
	}
}

func TestGroupMovePreservesEditorAndCollapses(t *testing.T) {
	s, sh := groupHome(t)
	a, _ := seedDoc(t, s, sh, "a.txt", "a")
	b, ed := seedDoc(t, s, sh, "b.txt", "b")
	s.openDoc(sh, b)
	s.Update(sh, keyMsg("!"))
	text, pos := ed.Text(), ed.CursorPosition()
	s.Update(sh, keyMsg("alt+t"))
	if len(Of(sh).groups) != 2 || s.editor != ed || !s.editorPanel.Focused() {
		t.Fatal("did not move editor right")
	}
	if s.editor.Text() != text || s.editor.CursorPosition() != pos || !s.editor.Dirty() {
		t.Fatal("move lost editor state")
	}
	if Of(sh).groups[0].currentID != a {
		t.Fatal("source did not select remaining tab")
	}
	s.Update(sh, keyMsg("ctrl+z"))
	if ed.Text() != "b" {
		t.Fatal("move lost undo history")
	}
	s.Update(sh, keyMsg("ctrl+t"))
	if len(Of(sh).groups) != 1 || s.editor != ed || !reflect.DeepEqual(s.tabs, []string{a, b}) {
		t.Fatal("left move did not collapse source")
	}
	groupInvariant(t, s, sh)
}

func TestGroupCreationLimitsAndLocalTabs(t *testing.T) {
	s, sh := groupHome(t)
	a, _ := seedDoc(t, s, sh, "a.txt", "a")
	s.openDoc(sh, a)
	s.moveTab(sh, 1)
	if len(s.groups()) != 1 {
		t.Fatal("split accepted only one tab")
	}
	for i := 1; i < maxEditorGroups; i++ {
		s.newUnsavedBuffer(sh)
		s.moveTab(sh, 1)
	}
	if len(s.groups()) != 4 {
		t.Fatal("did not create four groups")
	}
	s.newUnsavedBuffer(sh)
	s.moveTab(sh, 1)
	if len(s.groups()) != 4 {
		t.Fatal("exceeded group limit")
	}
	last := s.editorGroup
	s.stepDocument(sh, 1)
	if s.editorGroup != last || !slices.Contains(last.tabs, s.currentID) {
		t.Fatal("tab stepping escaped group")
	}
	s.openDoc(sh, a)
	if s.editorGroup != s.groups()[0] || len(Of(sh).OpenDocs()) != 5 {
		t.Fatal("existing document was duplicated")
	}
	s.moveTab(sh, -1)
	if s.editorGroup != s.groups()[0] {
		t.Fatal("left edge moved")
	}
	groupInvariant(t, s, sh)
}

func TestGroupInputFocusAndPaste(t *testing.T) {
	s, sh := groupHome(t)
	a, left := seedDoc(t, s, sh, "left.txt", "left")
	b, right := seedDoc(t, s, sh, "right.txt", "right")
	s.openDoc(sh, b)
	s.moveTab(sh, 1)
	s.View(sh)
	p := s.groups()[0].editorPanel
	s.Update(sh, tea.MouseClickMsg{X: p.x + 3, Y: p.y + 2, Button: tea.MouseLeft})
	if s.currentID != a || Of(sh).activeID != a {
		t.Fatal("click did not activate before input")
	}
	s.Update(sh, tea.PasteMsg{Content: "PASTE"})
	if left.Text() == "left" || right.Text() != "right" {
		t.Fatal("paste reached the wrong editors")
	}
	s.modular.FocusSlot(s.panelSlot(s.groups()[1].editorPanel))
	if s.currentID != b {
		t.Fatal("keyboard focus did not change active document")
	}
	called := false
	s.editorGroup = s.groups()[0] // emulate stale host state; Focus must repair it synchronously
	s.groups()[1].editorPanel.Focus()
	for _, it := range s.editorViewItems(sh) {
		if it.Label == "Toggle wrap" {
			it.Pick(sh)
			called = true
			break
		}
	}
	if !called || s.currentID != b {
		t.Fatal("context action saw previous group")
	}
	groupInvariant(t, s, sh)
}

func TestGroupCloseMergesWithoutDiscard(t *testing.T) {
	s, sh := groupHome(t)
	a, _ := seedDoc(t, s, sh, "a.txt", "a")
	b, ed := seedDoc(t, s, sh, "b.txt", "b")
	s.openDoc(sh, b)
	s.moveTab(sh, 1)
	s.Update(sh, keyMsg("!"))
	s.closeEditorGroup(sh)
	if len(s.groups()) != 1 || s.editor != ed || !ed.Dirty() || !reflect.DeepEqual(s.tabs, []string{a, b}) {
		t.Fatal("close group discarded or reordered tabs")
	}
	groupInvariant(t, s, sh)
}

func TestGroupCloseTabAndDeleteInactive(t *testing.T) {
	s, sh := groupHome(t)
	a, _ := seedDoc(t, s, sh, "a.txt", "a")
	b, _ := seedDoc(t, s, sh, "b.txt", "b")
	s.openDoc(sh, b)
	s.moveTab(sh, 1)
	s.editorExit(sh)
	if len(s.groups()) != 1 || s.currentID != a {
		t.Fatal("closing last tab did not collapse and activate neighbor")
	}
	c, _ := seedDoc(t, s, sh, "c.txt", "c")
	s.openDoc(sh, c)
	s.moveTab(sh, 1)
	doc, _ := Of(sh).bufferInfo(a)
	s.submitDelete(sh, doc)
	s.reseed(sh)
	if len(s.groups()) != 1 || s.currentID != c {
		t.Fatal("inactive deletion did not collapse its group")
	}
	groupInvariant(t, s, sh)
}

func TestGroupSessionRestore(t *testing.T) {
	s, sh := groupHome(t)
	a, _ := seedDoc(t, s, sh, "a.txt", "a\nb\nc")
	b, ed := seedDoc(t, s, sh, "b.txt", "1\n2\n3")
	s.openDoc(sh, b)
	s.moveTab(sh, 1)
	ed.Reveal(editor.Position{Line: 2})
	s.groups()[0].weight, s.groups()[1].weight = 0.3, 0.7
	if err := Of(sh).saveSession(); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadSessions()
	if err != nil {
		t.Fatal(err)
	}
	session := loaded.Sessions[homeSessionKey]
	if len(session.Groups) != 2 || session.ActiveGroup != 1 || session.Groups[0].Files[0] != a {
		t.Fatalf("bad session: %+v", session)
	}
	c := New("test", Of(sh).Config, Options{})
	c.close()
	c.lsp = nil
	rsh := core.NewShared(c)
	r := NewHomeScreen(rsh).(*homeScreen)
	r.Init(rsh)
	r.SetSize(rsh, 120, 30)
	if len(c.groups) != 2 || r.currentID != b || c.groups[1].weight != 0.7 {
		t.Fatal("groups not restored")
	}
	for _, g := range c.groups {
		if err := g.editor.LoadFile(); err != nil {
			t.Fatal(err)
		}
	}
	r.finishHomeUpdate(rsh, core.Action{})
	if r.editor.CursorPosition().Line != 2 {
		t.Fatal("position not restored")
	}
	groupInvariant(t, r, rsh)
	if err := os.Remove(a); err != nil {
		t.Fatal(err)
	}
	c2 := New("test", Of(sh).Config, Options{})
	c2.close()
	c2.lsp = nil
	r2 := NewHomeScreen(core.NewShared(c2)).(*homeScreen)
	if len(c2.groups) != 1 || r2.currentID != b {
		t.Fatal("vanished group's file was restored")
	}
}

func TestGroupPreviewAndStaleRequests(t *testing.T) {
	s, sh := groupHome(t)
	a, _ := seedDoc(t, s, sh, "a.md", "# Left")
	b, _ := seedDoc(t, s, sh, "b.md", "# Right")
	s.openDoc(sh, b)
	s.moveTab(sh, 1)
	s.setPreview(previewPane)
	s.toggleFullPreview()
	reader := s.fullPreview
	s.lspRequestID = 42
	s.openDoc(sh, a)
	if s.previewTarget() == nil || s.fullPreview != nil || s.lspRequestID != 0 {
		t.Fatal("focus did not restore shared preview / invalidate request")
	}
	s.openDoc(sh, b)
	if s.fullPreview != reader || s.previewTarget() != nil {
		t.Fatal("reader state not retained")
	}
	s.applyRequestResult(sh, &lspRequestResult{id: 42, path: b, kind: lspReqFormat, editSeq: s.editor.EditSeq(),
		edits: []lspTextEdit{{Range: protocol.Range{}, NewText: "WRONG"}}})
	if s.lspRequestID != 0 || s.editor.Text() != "# Right" {
		t.Fatal("stale format response changed the document after switching back")
	}
}

func TestGroupDelayedSaveRetainsOwner(t *testing.T) {
	model, s, sh, dir := openDiskFixture(t)
	a := s.currentPath
	b := filepath.Join(dir, "second.txt")
	writeDiskDoc(t, b, "second")
	model = pumpModel(model, s.openDoc(sh, b).Cmd)
	s.moveTab(sh, 1)
	ed := s.editor
	model, _ = model.Update(keyMsg("!"))
	model, _ = model.Update(keyMsg("ctrl+s"))
	model, cmd := model.Update(keyMsg("enter"))
	// Complete the actual save only after focus leaves its document.
	s.openDoc(sh, a)
	model = pumpModel(model, cmd)
	if s.currentID != a || ed.Dirty() {
		t.Fatal("delayed save changed focus or left its owner dirty")
	}
	data, err := os.ReadFile(b)
	if err != nil || string(data) != "!second" {
		t.Fatalf("saved wrong text: %q, %v", data, err)
	}
	if s.groups()[1].editor != ed {
		t.Fatal("save replaced the inactive editor")
	}
	groupInvariant(t, s, sh)
	_ = model
}

func TestGroupContextMenuSeesClickedDocument(t *testing.T) {
	s, sh := groupHome(t)
	a, _ := seedDoc(t, s, sh, "a.txt", "left")
	b := filepath.Join(t.TempDir(), "b.txt")
	seen := ""
	opts := s.editorOpts(Of(sh))
	opts.ContextItems = func(*core.Shared) []components.MenuItem { seen = s.currentID; return nil }
	ed := Of(sh).OpenDoc(b, opts)
	ed.SetText("right")
	s.openDoc(sh, b)
	s.moveTab(sh, 1)
	s.openDoc(sh, a)
	s.View(sh)
	p := s.groups()[1].editorPanel
	s.Update(sh, tea.MouseClickMsg{X: p.x + 3, Y: p.y + 1, Button: tea.MouseRight})
	if seen != b || s.currentID != b {
		t.Fatalf("context callback saw %q, want %q", seen, b)
	}
}

func TestGroupInactiveRenameAndDiskReload(t *testing.T) {
	model, s, sh, dir := openDiskFixture(t)
	a := s.currentPath
	first := s.editor
	b := filepath.Join(dir, "second.md")
	writeDiskDoc(t, b, "# second")
	model = pumpModel(model, s.openDoc(sh, b).Cmd)
	s.moveTab(sh, 1)
	doc, _ := Of(sh).bufferInfo(a)
	s.submitRename(sh, doc, doc.Name, "renamed.md")
	s.reseed(sh)
	renamed := filepath.Join(dir, "renamed.md")
	if s.currentID != b || s.groups()[0].currentPath != renamed {
		t.Fatal("inactive rename changed focus or kept old path")
	}
	writeDiskDoc(t, renamed, "external replacement")
	model, cmd := model.Update(tea.FocusMsg{})
	model = pumpModel(model, cmd)
	if first.Text() != "external replacement" || s.currentID != b {
		t.Fatal("inactive reload was lost or stole focus")
	}
	groupInvariant(t, s, sh)
	_ = model
}

func TestGroupPopupBoundsAndRebuildFocus(t *testing.T) {
	s, sh := groupHome(t)
	a, _ := seedDoc(t, s, sh, "a.txt", "a")
	b, _ := seedDoc(t, s, sh, "b.txt", "b")
	s.openDoc(sh, b)
	s.moveTab(sh, 1)
	s.setSidebar(false)
	if s.currentID != b || !s.editorPanel.Focused() {
		t.Fatal("layout rebuild changed the active editor")
	}
	s.openDoc(sh, a)
	s.View(sh)
	p := s.editorPanel
	x, y := s.caretPopup(p.x+p.w-1, p.y+2-sh.BodyY(), false)(s.w, s.h, 20, 3)
	if x < p.x || x+20 > p.x+p.w || y < p.y-sh.BodyY() || y+3 > p.y+p.h-sh.BodyY() {
		t.Fatal("popup escaped its editor")
	}
}

func TestGroupSaveAsCollision(t *testing.T) {
	s, sh := groupHome(t)
	a, _ := seedDoc(t, s, sh, "a.txt", "a")
	b, ed := seedDoc(t, s, sh, "b.txt", "b")
	s.openDoc(sh, b)
	s.moveTab(sh, 1)
	ed.SetPath(a)
	s.editorSaved(sh, a)
	s.reseed(sh)
	if len(s.groups()) != 1 || s.editor != ed || s.currentID != a || len(Of(sh).OpenDocs()) != 1 {
		t.Fatal("save-as collision left stale group")
	}
	groupInvariant(t, s, sh)
}

func TestGroupRouterShortcutsAndMinimalMode(t *testing.T) {
	model, s, sh := tabTestHome(t)
	a, _ := seedDoc(t, s, sh, "a.txt", "a")
	b, _ := seedDoc(t, s, sh, "b.txt", "b")
	s.openDoc(sh, b)
	for _, chord := range []string{"ctrl+shift+t", "ctrl+shift+T"} {
		model, _ = model.Update(keyMsg(chord))
		if len(s.groups()) != 1 {
			t.Fatalf("retired shortcut %s moved a tab", chord)
		}
	}
	priorView := s.fileView
	model, _ = model.Update(keyMsg("alt+t"))
	if len(s.groups()) != 2 || s.fileView != priorView {
		t.Fatal("alt+t did not split, or changed the file view")
	}
	model, _ = model.Update(keyMsg("ctrl+t"))
	if len(s.groups()) != 1 || s.currentID != b || !slices.Contains(s.tabs, a) {
		t.Fatal("router swallowed left move")
	}

	_ = model
	path := filepath.Join(t.TempDir(), "single.txt")
	os.WriteFile(path, []byte("single"), 0644)
	m, msh := newHomeWith(t, Options{Mode: ModeFile, File: path})
	defer Of(msh).close()
	m.moveTab(msh, 1)
	if len(m.groups()) != 1 || !m.minimal {
		t.Fatal("minimal mode split")
	}
}

func TestGroupVaultRoundTrip(t *testing.T) {
	s, sh := groupHome(t)
	c := Of(sh)
	for _, name := range []string{"one", "two"} {
		if err := c.AddVault(name, t.TempDir()); err != nil {
			t.Fatal(err)
		}
	}
	s.activateVault(sh, "one")
	s.gitGutter = false
	a, _ := seedDoc(t, s, sh, "a.txt", "a")
	b, _ := seedDoc(t, s, sh, "b.txt", "b")
	s.openDoc(sh, b)
	s.moveTab(sh, 1)
	s.activateVault(sh, "two")
	if len(c.groups) != 1 || len(c.OpenDocs()) != 0 {
		t.Fatal("outgoing groups leaked into another vault")
	}
	s.activateVault(sh, "one")
	if len(c.groups) != 2 || s.currentID != b || c.groups[0].currentID != a {
		t.Fatal("vault did not restore its groups")
	}
	groupInvariant(t, s, sh)
}

func TestGroupSessionSanitizesMembershipAndOmitsUnsaved(t *testing.T) {
	s, sh := groupHome(t)
	c := Of(sh)
	a, _ := seedDoc(t, s, sh, "a.txt", "a")
	b, _ := seedDoc(t, s, sh, "b.txt", "b")
	s.openDoc(sh, b)
	s.restoreGroups(c, Session{Files: []SessionFile{{Path: a}, {Path: b}}, ActiveGroup: 1, Groups: []SessionGroup{
		{Files: []string{a, a, "missing"}, Active: "missing", Weight: -1},
		{Files: []string{a, b}, Active: b, Weight: 2},
	}})
	s.initGroupPanels(c)
	s.rebuildGroups(sh)
	for _, g := range c.groups {
		g.editorPanel.Init(sh)
	}
	groupInvariant(t, s, sh)
	if len(c.groups) != 2 || len(c.groups[0].tabs) != 1 || s.currentID != b {
		t.Fatal("session membership was not repaired")
	}
	// An unsaved-only group is deliberately absent from the persisted session.
	s.newUnsavedBuffer(sh)
	s.moveTab(sh, 1)
	if err := c.saveSession(); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadSessions()
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Sessions[homeSessionKey].Groups) != 2 {
		t.Fatal("unsaved-only group persisted")
	}
}

func TestGroupResizeAndTabBarRouting(t *testing.T) {
	s, sh := groupHome(t)
	a, _ := seedDoc(t, s, sh, "a.txt", "a")
	b, _ := seedDoc(t, s, sh, "b.txt", "b")
	s.openDoc(sh, b)
	s.moveTab(sh, 1)
	s.saveResize(components.ResizeState{Splits: map[string]components.SplitState{"groups": {Weights: []float64{0.25, 0.75}}}})
	s.rebuildGroups(sh)
	s.View(sh)
	left, right := s.groups()[0], s.groups()[1]
	if left.editorPanel.w >= right.editorPanel.w || right.editorPanel.x <= left.editorPanel.x {
		t.Fatal("group geometry did not restore weights")
	}
	s.Update(sh, tea.MouseClickMsg{X: left.openTabs.x + 1, Y: left.openTabs.y, Button: tea.MouseLeft})
	if s.currentID != a {
		t.Fatal("left tab click routed to active right group")
	}
	s.SetSize(sh, 18, 8)
	s.View(sh)
	groupInvariant(t, s, sh)
}

func TestGroupTabSeparator(t *testing.T) {
	s, sh := groupHome(t)
	seedDoc(t, s, sh, "a.txt", "a")
	b, _ := seedDoc(t, s, sh, "b.txt", strings.Repeat("b", 40))
	s.openDoc(sh, b)
	s.moveTab(sh, 1)
	s.rebuildGroups(sh)
	s.View(sh)
	left, right := s.groups()[0], s.groups()[1]
	if row := ansi.Strip(left.openTabs.View(false)); strings.HasPrefix(row, "│") || ansi.StringWidth(row) != left.openTabs.w {
		t.Fatalf("left bar %q", row)
	}
	row := ansi.Strip(right.openTabs.View(false))
	if !strings.HasPrefix(row, "│") || ansi.StringWidth(row) != right.openTabs.w {
		t.Fatalf("right bar %q", row)
	}
	// The divider runs down the right editor too, in the border color whatever the focus.
	for _, focused := range []bool{false, true} {
		if focused {
			s.activateGroup(right)
		} else {
			s.activateGroup(left)
		}
		s.View(sh)
		if got := right.openTabs.View(false); !strings.HasPrefix(got, groupSeparator()) {
			t.Fatalf("tab separator styling follows focus (focused=%v): %q", focused, got)
		}
		lines := strings.Split(right.editorPanel.View(false), "\n")
		if len(lines) != right.editorPanel.h {
			t.Fatalf("editor rows %d, want %d", len(lines), right.editorPanel.h)
		}
		for i, line := range lines {
			if !strings.HasPrefix(line, groupSeparator()) || ansi.StringWidth(line) > right.editorPanel.w+1 {
				t.Fatalf("editor row %d %q", i, ansi.Strip(line))
			}
		}
	}
	for _, line := range strings.Split(ansi.Strip(left.editorPanel.View(false)), "\n") {
		if strings.HasPrefix(line, "│") {
			t.Fatal("left editor drew a separator")
		}
	}
	if right.editorPanel.x != right.openTabs.x+1 {
		t.Fatalf("editor content x %d, want %d", right.editorPanel.x, right.openTabs.x+1)
	}
	s.activateGroup(left)
	s.Update(sh, tea.MouseClickMsg{X: right.openTabs.x + 1, Y: right.openTabs.y, Button: tea.MouseLeft})
	if s.currentID != b {
		t.Fatal("click past the separator did not reach the right group's tab")
	}
	// A click in the text lands the caret under the pointer.
	cx, cy := right.editorPanel.x+10, right.editorPanel.y
	s.Update(sh, tea.MouseClickMsg{X: cx, Y: cy, Button: tea.MouseLeft})
	s.Update(sh, tea.MouseReleaseMsg{X: cx, Y: cy, Button: tea.MouseLeft})
	s.View(sh)
	if x, _, ok := right.editor.CursorAnchor(); !ok || x != cx {
		t.Fatalf("caret at x=%d (visible %v), clicked %d", x, ok, cx)
	}
	// A drag from the separator resizes the groups.
	x, y := right.openTabs.x, right.openTabs.y
	s.Update(sh, tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
	s.Update(sh, tea.MouseMotionMsg{X: x + 5, Y: y, Button: tea.MouseLeft})
	s.Update(sh, tea.MouseReleaseMsg{X: x + 5, Y: y, Button: tea.MouseLeft})
	s.View(sh)
	if right.openTabs.x != x+5 {
		t.Fatalf("separator drag moved the edge to %d, want %d", right.openTabs.x, x+5)
	}
	s.moveTab(sh, -1)
	s.rebuildGroups(sh)
	s.View(sh)
	if len(s.groups()) != 1 || strings.HasPrefix(ansi.Strip(s.openTabs.View(false)), "│") {
		t.Fatal("collapsed bar kept its separator")
	}
}
