package app

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/brohd11/bubblestack/components/editor"
	"github.com/brohd11/bubblestack/core"
	"github.com/charmbracelet/x/ansi"
	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"
)

func setTestDiagnostics(c *Ctx, path string, entries ...lspDiagnostic) {
	c.lsp.mu.Lock()
	c.lsp.diagnostics[path] = entries
	c.lsp.diagnosticsRevision++
	c.lsp.mu.Unlock()
}

func TestBottomTogglePreservesEditorAndSplit(t *testing.T) {
	s, sh := newHome(t)
	defer Of(sh).close()
	path, ed := seedDoc(t, s, sh, "doc.md", "alpha beta\nsecond line")
	s.openDoc(sh, path)
	ed.Reveal(editor.Position{Line: 0, Column: 8})
	s.Update(sh, keyMsg("!"))
	text := ed.Text()
	if !ed.Dirty() {
		t.Fatal("fixture must contain unsaved edits")
	}
	before := ed.CursorPosition()
	s.Update(sh, tea.KeyPressMsg{Code: '\\', Mod: tea.ModAlt})
	if !s.bottomVisible || !s.editorPanel.Focused() || ed.CursorPosition() != before || ed.Text() != text {
		t.Fatal(`alt+\ changed editor state or focus`)
	}
	if !strings.Contains(s.View(sh), "Diagnostics") {
		t.Fatal("bottom missing from view")
	}
	s.modular.Nudge(0, -3)
	s.modular.SetResizing(true)
	s.modular.SetResizing(false)
	split := s.bottomFraction
	if split <= 0.25 {
		t.Fatalf("split was not saved: %f", split)
	}
	height := s.diagnostics.height
	for range 5 {
		s.toggleBottom(sh)
		s.toggleBottom(sh)
		if s.diagnostics.height != height {
			t.Fatal("repeated toggles drifted the retained panel height")
		}
	}
	weights := s.modular.ResizeState().Splits["workspace"].Weights
	if weights[1]/(weights[0]+weights[1]) != split {
		t.Fatal("toggle lost resized split")
	}
	if s.editor != ed || ed.Text() != text {
		t.Fatal("toggle reinitialized editor")
	}
	s.modular.FocusSlot(s.editorSlot() + 1)
	if !s.diagnostics.Focused() {
		t.Fatal("bottom focus failed")
	}
	s.modular.SetResizing(true)
	s.Update(sh, tea.KeyPressMsg{Code: tea.KeyEscape})
	if s.modular.Resizing() || !s.diagnostics.Focused() {
		t.Fatal("Escape must finish resizing before leaving diagnostics")
	}
	s.Update(sh, tea.KeyPressMsg{Code: tea.KeyEscape})
	if !s.bottomVisible || !s.editorPanel.Focused() {
		t.Fatal("Escape should focus editor and leave bottom open")
	}
	s.modular.FocusSlot(s.editorSlot() + 1)
	s.toggleBottom(sh)
	if s.bottomVisible || !s.editorPanel.Focused() {
		t.Fatal("closing focused bottom should focus editor")
	}
}

func TestDiagnosticsRefreshCacheSelectionAndClear(t *testing.T) {
	s, sh := newHome(t)
	c := Of(sh)
	defer c.close()
	path, _ := seedDoc(t, s, sh, "main.py", "first\nsecond")
	a := lspDiagnostic{Line: 0, Message: "first warning"}
	b := lspDiagnostic{Line: 1, Message: "second warning"}
	setTestDiagnostics(c, path, a, b)
	s.toggleBottom(sh)
	p := s.diagnostics
	p.selectEntry(1)
	rows := &p.lines[0]
	for range 20 {
		s.SetSize(sh, 100, 30)
		p.refresh(c, s.currentPath)
	}
	if &p.lines[0] != rows {
		t.Fatal("unchanged message rebuilt diagnostic rows")
	}
	setTestDiagnostics(c, path, lspDiagnostic{Message: "new entry"}, a, b)
	s.Receive(sh, lspEvent{})
	if p.entries[p.selected].diagnostic != b {
		t.Fatal("refresh lost selected diagnostic identity")
	}
	c.CloseDoc(path)
	p.refresh(c, s.currentPath)
	if len(p.entries) != 0 || p.selected != -1 || !strings.Contains(p.status, "No diagnostics") {
		t.Fatal("closed-file diagnostics remain")
	}
	c.lsp.Close()
	c.lsp = nil
	p.refresh(c, s.currentPath)
	if !strings.Contains(p.status, "disabled") {
		t.Fatal("disabled language server needs an explicit state")
	}
}

func TestDiagnosticsWrappedClicksAndEmptySpace(t *testing.T) {
	c := New("test", DefaultConfig(), Options{})
	defer c.close()
	path := "/workspace/main.py"
	c.OpenDoc(path, editor.Opts{})
	setTestDiagnostics(c, path, lspDiagnostic{Message: strings.Repeat("a long diagnostic message ", 15)})
	picked := 0
	p := newDiagnosticsPanel(func(*core.Shared, diagnosticEntry) core.Action { picked++; return core.Action{} })
	p.SetSize(30, 8)
	p.refresh(c, path)
	p.Focus()
	for _, line := range p.lines {
		if ansi.StringWidth(line) > 26 {
			t.Fatalf("line overflow: %q", line)
		}
	}
	p.ScrollTo(p.starts[0] + 3)
	p.UpdatePanel(nil, tea.MouseClickMsg{X: 3, Y: 2, Button: tea.MouseLeft})
	if picked != 1 {
		t.Fatal("click on scrolled continuation did not activate diagnostic")
	}
	p.ScrollTo(0)
	p.UpdatePanel(nil, tea.MouseClickMsg{X: 3, Y: 1, Button: tea.MouseLeft}) // heading
	p.UpdatePanel(nil, tea.MouseClickMsg{X: 0, Y: 2, Button: tea.MouseLeft}) // border
	p.UpdatePanel(nil, tea.MouseClickMsg{X: 3, Y: 7, Button: tea.MouseLeft}) // bottom border
	if picked != 1 {
		t.Fatal("non-message area activated a diagnostic")
	}
	p.ScrollTo(p.starts[0])
	offset := p.ScrollOffset()
	p.UpdatePanel(nil, tea.KeyPressMsg{Code: tea.KeyPgDown})
	if p.ScrollOffset() <= offset {
		t.Fatal("page down did not scroll the long message")
	}
}

func TestDiagnosticActivationUnicodeAndBack(t *testing.T) {
	s, sh := newHome(t)
	defer Of(sh).close()
	from, ed := seedDoc(t, s, sh, "from.md", "original\nposition")
	to, _ := seedDoc(t, s, sh, "to.md", "a😀target\nend")
	s.openDoc(sh, from)
	ed.Reveal(editor.Position{Line: 1, Column: 2})
	s.toggleBottom(sh)
	s.toggleFullPreview()
	s.activateDiagnostic(sh, diagnosticEntry{to, lspDiagnostic{Character: 3, EndCharacter: 3, Message: "unicode"}})
	if s.currentPath != to || s.fullPreview != nil || !s.editorPanel.Focused() || !s.bottomVisible {
		t.Fatal("activation did not restore and focus target editor")
	}
	if got := s.editor.CursorPosition(); got != (editor.Position{Column: 2}) {
		t.Fatalf("UTF-16 target = %+v", got)
	}
	s.jumpBack(sh)
	if s.currentPath != from || s.editor.CursorPosition() != (editor.Position{Line: 1, Column: 2}) {
		t.Fatal("return jump lost original location")
	}
}

func TestBottomLayoutModesAndMenus(t *testing.T) {
	s, sh := newHome(t)
	defer Of(sh).close()
	path, _ := seedDoc(t, s, sh, "doc.md", "# text")
	s.openDoc(sh, path)
	for _, sidebar := range []bool{true, false} {
		for _, preview := range []int{previewOff, previewPane} {
			s.setSidebar(sidebar)
			s.setPreview(preview)
			if !s.bottomVisible {
				s.toggleBottom(sh)
			}
			s.SetSize(sh, 100, 30)
			if s.diagnostics.width != 100 {
				t.Fatalf("bottom width=%d", s.diagnostics.width)
			}
			view := s.View(sh)
			if lipgloss.Width(view) > 100 || lipgloss.Height(view) > 30 {
				t.Fatal("bottom layout overflowed")
			}
		}
	}
	s.minimal = true
	s.SetSize(sh, 60, 20)
	if s.diagnostics.width != 60 || !s.bottomVisible {
		t.Fatal("minimal mode lost bottom")
	}
	for _, item := range s.editorViewItems(sh) {
		if item.Label == "Toggle diagnostics panel" {
			act := item.Pick(sh)
			if s.bottomVisible || act.Msg == nil {
				t.Fatal("menu toggle must close panel and dismiss menu")
			}
			return
		}
	}
	t.Fatal("missing diagnostics menu")
}

func TestDiagnosticsSortSeverityAtPosition(t *testing.T) {
	c := New("test", DefaultConfig(), Options{})
	defer c.close()
	c.OpenDoc("/file.py", editor.Opts{})
	setTestDiagnostics(c, "/file.py", lspDiagnostic{Severity: protocol.DiagnosticSeverityHint}, lspDiagnostic{Severity: protocol.DiagnosticSeverityError})
	entries, _ := collectDiagnostics(c, "")
	if entries[0].diagnostic.Severity != protocol.DiagnosticSeverityError {
		t.Fatal("severity sort lost")
	}
}

func TestDiagnosticsRevisionTracksPublishedChanges(t *testing.T) {
	c := New("test", DefaultConfig(), Options{})
	defer c.close()
	path := "/workspace/main.py"
	m := c.lsp
	m.mu.Lock()
	m.desired[path] = lspDocument{path: path}
	m.mu.Unlock()
	client := &lspClient{manager: m}
	params := &protocol.PublishDiagnosticsParams{URI: uri.File(path), Diagnostics: []protocol.Diagnostic{{Message: protocol.String("warning")}}}
	if err := client.PublishDiagnostics(context.Background(), params); err != nil {
		t.Fatal(err)
	}
	revision := m.DiagnosticsRevision()
	if revision == 0 {
		t.Fatal("publish did not change revision")
	}
	if err := client.PublishDiagnostics(context.Background(), params); err != nil {
		t.Fatal(err)
	}
	if m.DiagnosticsRevision() != revision {
		t.Fatal("identical diagnostics invalidated cache")
	}
	params.Diagnostics = nil
	if err := client.PublishDiagnostics(context.Background(), params); err != nil {
		t.Fatal(err)
	}
	if m.DiagnosticsRevision() == revision || len(m.Diagnostics(path)) != 0 {
		t.Fatal("clear did not invalidate diagnostics")
	}
}

func TestBottomActionsMenuReturnsToHome(t *testing.T) {
	model, s, sh := newHomeRouter(t, Options{})
	defer Of(sh).close()
	// Theme, Vaults, Open view, Diagnostics: exercise the real picker and router Pop,
	// including the capture gate when alt+\ is subsequently pressed in editor.
	for _, k := range []string{"a", "down", "down", "down", "enter"} {
		var cmd tea.Cmd
		model, cmd = model.Update(keyMsg(k))
		model = pumpModel(model, cmd)
	}
	if model.(core.Router).Top() != s || !s.bottomVisible {
		t.Fatal("diagnostics action did not toggle panel and dismiss menu")
	}
	s.modular.FocusSlot(s.editorSlot())
	model, _ = model.Update(keyMsg(`alt+\`))
	if s.bottomVisible || !s.editorPanel.Focused() {
		t.Fatal(`router capture gate swallowed alt+\`)
	}
}
