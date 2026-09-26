package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/brohd11/bubblestack/components/editor"
	"github.com/brohd11/bubblestack/core"
	"github.com/brohd11/gitstack/repo"
	"github.com/charmbracelet/x/ansi"
)

func gitDiffHome(t *testing.T, base, buf string) (*homeScreen, *core.Shared) {
	t.Helper()
	s, sh := completionHomeFor(t, "diff.txt")
	s.editor.SetText(buf)
	s.applyBaseline(baselineMsg{path: s.currentPath, base: base, state: repo.BaselineOK})
	s.View(sh)
	return s, sh
}

func showGitDiff(t *testing.T, s *homeScreen, sh *core.Shared) *gitDiffUI {
	t.Helper()
	s.View(sh)
	s.Update(sh, keyMsg("alt+D"))
	if s.gitDiff == nil || s.gitDiff.popup == nil {
		t.Fatal("diff did not open")
	}
	return s.gitDiff
}

func TestGitDiffShortcutAliasesAndEditingBindings(t *testing.T) {
	for _, chord := range []string{"alt+D", "alt+shift+d", "alt+shift+D"} {
		t.Run(chord, func(t *testing.T) {
			s, sh := gitDiffHome(t, "old\n", "new\n")
			s.Update(sh, keyMsg(chord))
			if s.gitDiff == nil || s.gitDiff.popup == nil {
				t.Fatal("shortcut did not open the diff")
			}
			s.Update(sh, keyMsg(chord))
			if s.gitDiff != nil || s.editor.Text() != "new\n" {
				t.Fatal("toggle did not close cleanly")
			}
		})
	}
	s, sh := gitDiffHome(t, "old\n", "one two three\n")
	s.Update(sh, keyMsg("alt+f"))
	if s.editor.CursorPosition().Column == 0 {
		t.Fatal("word forward was intercepted")
	}
	s.Update(sh, keyMsg("alt+b"))
	if s.editor.CursorPosition().Column != 3 {
		t.Fatal("word backward was intercepted")
	}
	s.Update(sh, keyMsg("alt+d"))
	if s.gitDiff != nil || s.editor.Text() == "one two three\n" {
		t.Fatal("Alt+D no longer deletes a word")
	}
}

func TestGitDiffScrollAndDismissal(t *testing.T) {
	s, sh := gitDiffHome(t, strings.Repeat("old\n", 60), strings.Repeat("new\n", 60))
	p := showGitDiff(t, s, sh)
	before := s.editor.CursorPosition()
	beforeScroll, _, _ := s.editor.ScrollSpan()
	s.Update(sh, keyMsg("down"))
	if p.top != 1 || s.editor.CursorPosition() != before {
		t.Fatal("down did not scroll just the popup")
	}
	s.Update(sh, keyMsg("pgdown"))
	if p.top <= 1 || s.editor.CursorPosition() != before {
		t.Fatal("page down did not scroll just the popup")
	}
	x, y, _, _ := s.placeGitDiff(sh)
	at := p.top
	s.Update(sh, tea.MouseWheelMsg{X: x + 2, Y: y + 2, Button: tea.MouseWheelDown})
	if p.top != at+3 {
		t.Fatalf("wheel inside popup moved top from %d to %d", at, p.top)
	}
	if scroll, _, _ := s.editor.ScrollSpan(); scroll != beforeScroll {
		t.Fatal("popup wheel leaked to editor")
	}
	s.Update(sh, tea.MouseClickMsg{X: x + 2, Y: y + 2, Button: tea.MouseLeft})
	s.Update(sh, tea.MouseReleaseMsg{X: x + 2, Y: y + 2, Button: tea.MouseLeft})
	if s.gitDiff != p || s.editor.CursorPosition() != before {
		t.Fatal("inside click or release moved the editor or dismissed the popup")
	}
	s.Update(sh, keyMsg("esc"))
	if s.gitDiff != nil || !s.editorPanel.Focused() || s.editor.CursorPosition() != before {
		t.Fatal("escape must only dismiss the popup")
	}
	p = showGitDiff(t, s, sh)
	// The caret's row is just outside the popup, in the editor pane.
	s.Update(sh, tea.MouseWheelMsg{X: p.x, Y: p.y, Button: tea.MouseWheelDown})
	if s.gitDiff != nil {
		t.Fatal("wheel outside did not dismiss")
	}
	if scroll, _, _ := s.editor.ScrollSpan(); scroll <= beforeScroll {
		t.Fatal("outside wheel did not reach the editor")
	}
	// Bring the caret back into view before reopening after browse-only scrolling.
	s.editor.Reveal(before)
	showGitDiff(t, s, sh)
	s.Update(sh, keyMsg("X"))
	if s.gitDiff != nil || !strings.HasPrefix(s.editor.Text(), "Xnew") {
		t.Fatal("typing did not dismiss and edit")
	}
	showGitDiff(t, s, sh)
	s.Update(sh, tea.PasteMsg{Content: "paste"})
	if s.gitDiff != nil || !strings.HasPrefix(s.editor.Text(), "Xpaste") {
		t.Fatal("paste did not dismiss and edit")
	}
}

func TestGitDiffLiveBufferAndUnavailableStates(t *testing.T) {
	s, sh := gitDiffHome(t, "old\n", "old\n")
	s.Update(sh, keyMsg("new ")) // gutter is still waiting on the edit debounce
	p := showGitDiff(t, s, sh)
	if !strings.Contains(hunkText(p.hunk), "+new old") {
		t.Fatalf("popup used stale gutter text: %s", hunkText(p.hunk))
	}
	for _, state := range []repo.Baseline{repo.BaselineIgnored, repo.BaselineNone} {
		s.closeGitDiff()
		s.gutter.state = state
		_, act := s.Update(sh, keyMsg("alt+D"))
		if s.gitDiff != nil || act.Msg == nil {
			t.Fatalf("unavailable baseline %v opened a popup or omitted status", state)
		}
	}
	s.gutter.state = repo.BaselineAbsent
	s.editor.SetText("")
	p = showGitDiff(t, s, sh)
	if !strings.Contains(p.popup.View(), "empty new file") {
		t.Fatal("empty new file has no explanation")
	}
	s.closeGitDiff()
	s.currentPath = ""
	_, act := s.Update(sh, keyMsg("alt+D"))
	if s.gitDiff != nil || act.Msg == nil {
		t.Fatal("scratch opened a diff or omitted status")
	}
}

func TestGitDiffClickUsesMarkerUnderDocumentTabs(t *testing.T) {
	s, sh := gitDiffHome(t, "a\ndeleted\nc\n", "a\nc\n")
	s.setDiagnosticsGutter(true)
	s.editor.SetSignColumn(diagnosticSignColumn, map[int]editor.Sign{1: {Text: "E"}})
	s.editor.Reveal(editor.Position{Line: 0, Column: 1})
	before := s.editor.CursorPosition()
	frame := ansi.Strip(s.View(sh))
	// Locate the rendered deletion marker in cells, not bytes (the frame contains Unicode).
	x, y := -1, -1
	for row, line := range strings.Split(frame, "\n") {
		if at := strings.Index(line, signDelTop); at >= 0 {
			x, y = ansi.StringWidth(line[:at]), row+sh.BodyY()
			break
		}
	}
	if x < 0 {
		t.Fatalf("no deletion marker in frame:\n%s", frame)
	}
	s.Update(sh, tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
	if s.gitDiff == nil || !strings.Contains(hunkText(s.gitDiff.hunk), "-deleted") {
		t.Fatal("marker click did not open the deleted section")
	}
	s.Update(sh, tea.MouseReleaseMsg{X: x, Y: y, Button: tea.MouseLeft})
	if s.gitDiff == nil || s.editor.CursorPosition() != before {
		t.Fatal("gutter click/release changed caret or dismissed its popup")
	}
}

func TestGitDiffDismissesOnPaneDocumentAndLayoutChanges(t *testing.T) {
	for _, change := range []string{"pane key", "pane focus", "document", "layout", "resize", "hover", "quit gate"} {
		t.Run(change, func(t *testing.T) {
			s, sh := newHome(t)
			s.currentPath = filepath.Join(t.TempDir(), "file.txt")
			s.editor.SetText("new\n")
			s.applyBaseline(baselineMsg{path: s.currentPath, base: "old\n", state: repo.BaselineOK})
			s.modular.FocusSlot(s.editorSlot())
			showGitDiff(t, s, sh)
			switch change {
			case "pane key":
				s.Update(sh, keyMsg("shift+tab"))
				if s.editorPanel.Focused() {
					t.Fatal("pane key was swallowed")
				}
			case "pane focus":
				s.modular.FocusSlot(s.panelSlot(s.docsPane()))
			case "document":
				s.newUnsavedBuffer(sh)
			case "layout":
				s.togglePanes(sh, func() {})
			case "resize":
				s.SetSize(sh, 70, 20)
			case "hover":
				s.applyHover(&lspRequestResult{path: s.currentPath, hover: "another popup"})
			case "quit gate":
				s.QuitGate(sh)
			}
			s.View(sh)
			if s.gitDiff != nil {
				t.Fatal("stale popup survived")
			}
			if change == "pane key" || change == "pane focus" {
				s.Update(sh, keyMsg("alt+D"))
				if s.gitDiff != nil {
					t.Fatal("shortcut should only activate from the editor")
				}
			}
		})
	}
}

func TestGitDiffAsyncWithHiddenGutterAndCancellation(t *testing.T) {
	dir := t.TempDir()
	gitInit(t, dir)
	file := filepath.Join(dir, "file.txt")
	if err := os.WriteFile(file, []byte("old\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitRun(t, dir, "add", ".")
	gitRun(t, dir, "commit", "-qm", "baseline")
	model, s, sh := newHomeRouter(t, Options{})
	s.openDoc(sh, file)
	s.editor.SetText("unsaved\n")
	s.setGitGutter(false)
	model.View()
	x, y, _ := s.editor.CursorAnchor()
	act := s.openGitDiff(sh, 0, x, y)
	if act.Cmd == nil || s.gitDiff == nil || s.gitDiff.popup != nil {
		t.Fatal("missing baseline should start a pending open")
	}
	model.Update(act.Cmd())
	if s.gitDiff == nil || s.gitDiff.popup == nil || s.gitGutter || s.editor.SignColumnMode(gitSignColumn) {
		t.Fatal("async result did not open with the gutter still hidden")
	}
	if got := hunkText(s.gitDiff.hunk); !strings.Contains(got, "-old") || !strings.Contains(got, "+unsaved") {
		t.Fatalf("diff did not compare HEAD with unsaved buffer: %s", got)
	}
	for _, cancel := range []string{"escape", "typing", "document", "new request"} {
		t.Run(cancel, func(t *testing.T) {
			s.closeGitDiff()
			s.currentPath = file
			act := s.openGitDiff(sh, 0, x, y)
			old := s.gitDiff
			switch cancel {
			case "escape":
				s.Update(sh, keyMsg("esc"))
			case "typing":
				s.Update(sh, keyMsg("x"))
			case "document":
				s.currentPath = filepath.Join(dir, "elsewhere.txt")
			case "new request":
				s.openGitDiff(sh, 0, x, y)
			}
			model.Update(act.Cmd())
			if s.gitDiff == old || (s.gitDiff != nil && s.gitDiff.popup != nil) {
				t.Fatal("late baseline opened a canceled/superseded request")
			}
		})
	}
}

func TestGitDiffWrappingBoundsAndOpaquePanel(t *testing.T) {
	s, sh := gitDiffHome(t, "old\n", strings.Repeat("界\tnew ", 80)+"\n")
	for _, size := range [][2]int{{80, 24}, {35, 12}, {12, 7}, {8, 6}} {
		s.closeGitDiff()
		s.SetSize(sh, size[0], size[1])
		// Direct anchor avoids tying very small editor viewport availability to panel fitting.
		s.openGitDiff(sh, 0, size[0]-1, sh.BodyY()+size[1]-1)
		p := s.gitDiff
		if p == nil || p.popup == nil {
			t.Fatalf("no popup at %v", size)
		}
		box := p.popup.View()
		assertOpaquePanel(t, box)
		x, y, w, h := s.placeGitDiff(sh)
		if lipgloss.Width(box) != w || lipgloss.Height(box) != h {
			t.Fatalf("mouse geometry %dx%d differs from rendered %dx%d", w, h, lipgloss.Width(box), lipgloss.Height(box))
		}
		if x < 0 || y < sh.BodyY() || x+w > size[0] || y+h > sh.BodyY()+size[1] {
			t.Fatalf("popup does not fit %v: (%d,%d) %dx%d", size, x, y, w, h)
		}
		var added strings.Builder
		for _, row := range p.rows {
			if row.kind == '+' {
				added.WriteString(row.text)
			}
		}
		if added.String() != diffDisplayText(strings.TrimSuffix(s.editor.Text(), "\n")) {
			t.Fatal("wrapping lost file content")
		}
	}
	s.closeGitDiff()
	s.SetSize(sh, 7, 5)
	act := s.openGitDiff(sh, 0, 0, 0)
	if s.gitDiff != nil || act.Msg == nil {
		t.Fatal("undersized terminal should report status rather than overflow")
	}
}

func TestGitDiffShortcutContext(t *testing.T) {
	s, sh := gitDiffHome(t, "0\n1\n2\n3\n4\n5\n6\n7\n8\n", "0\n1\n2\n3\n4\n5\n6\n7\nEIGHT\n")
	_, act := s.Update(sh, keyMsg("alt+D"))
	if s.gitDiff != nil || act.Msg == nil {
		t.Fatal("unchanged line outside context should show status")
	}
	s.editor.Reveal(editor.Position{Line: 5})
	showGitDiff(t, s, sh) // the third context line before the edit
}
