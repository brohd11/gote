package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/brohd11/bubblestack/components"
	"github.com/brohd11/bubblestack/core"
	editor "github.com/brohd11/go-text-edit"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

func TestFindMatchShortcutsThroughRouter(t *testing.T) {
	for _, tc := range []struct {
		name string
		mode Mode
	}{{"project", ModeScan}, {"single file", ModeFile}} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			path := writeSearchFile(t, root, "notes.txt", "foo foo foo")
			model, s, sh := newHomeRouterCfg(t, testConfig(), Options{Mode: tc.mode, Dir: root, File: path})
			t.Cleanup(Of(sh).close)
			s.gitGutter = false
			if tc.mode == ModeScan {
				s.openDoc(sh, path)
			}
			s.editor.SetText("foo foo foo")
			s.modular.FocusSlot(s.editorSlot())
			id := s.currentID
			drive := func(chord string) {
				t.Helper()
				var cmd tea.Cmd
				model, cmd = model.Update(keyMsg(chord))
				model = pumpModel(model, cmd)
			}
			assertMatch := func(column int, fieldFocused bool) {
				t.Helper()
				if got := s.editor.CursorPosition(); got != (editor.Position{Column: column}) {
					t.Fatalf("caret=%+v, want column %d", got, column)
				}
				_, isField := model.(core.Router).Top().(*components.LineEditScreen)
				if isField != fieldFocused || !s.editorPanel.Focused() || s.currentID != id {
					t.Fatal("match navigation changed focus or the active document")
				}
			}
			drive("ctrl+f")
			drive("foo")
			drive("alt+=")
			assertMatch(4, true)
			drive("shift+f3")
			assertMatch(0, true)
			drive("f3")
			assertMatch(4, true)
			drive("alt+-")
			assertMatch(0, true)
			drive("esc")
			assertMatch(0, false)
			drive("alt+=")
			assertMatch(4, false)
			drive("bar")
			if s.editor.Text() != "foo bar foo" {
				t.Fatalf("typing did not replace the selected match: %q", s.editor.Text())
			}
			drive("f3")
			assertMatch(8, false)
			drive("alt+-")
			assertMatch(0, false)
			drive("shift+f3")
			assertMatch(8, false)
			drive("ctrl+z")
			if s.editor.Text() != "foo foo foo" {
				t.Fatal("navigation interfered with undoing the replacement")
			}
			for _, hint := range []string{"alt+-", "alt+=", "shift+f3", "f3"} {
				if !strings.Contains(s.helpText(), hint) {
					t.Fatalf("shortcut help missing %s", hint)
				}
			}
		})
	}
}

func writeSearchFile(t *testing.T, root, rel, body string) string {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestSearchFilesSmartCaseUnicodeAndPruning(t *testing.T) {
	root := t.TempDir()
	path := writeSearchFile(t, root, "a.txt", "Needle and needle\n😀 needle here\n")
	writeSearchFile(t, root, ".hidden/match.txt", "needle\n")
	writeSearchFile(t, root, "node_modules/match.txt", "needle\n")
	writeSearchFile(t, root, "sub/b.txt", "NEEDLE\n")

	got, truncated, err := searchFiles(context.Background(), root, "needle", nil, searchResultLimit)
	if err != nil || truncated || len(got) != 3 {
		t.Fatalf("lowercase smart search = %d,%v,%v, want three visible-tree matches", len(got), truncated, err)
	}
	if got[0].path != path || got[1].path != path || got[1].column != 2 || got[1].start != 3 || got[1].end != 9 {
		t.Fatalf("Unicode result coordinates are wrong: %#v", got)
	}

	got, _, err = searchFiles(context.Background(), root, "Needle", nil, searchResultLimit)
	if err != nil || len(got) != 1 || got[0].line != 0 {
		t.Fatalf("uppercase query should be case-sensitive: %#v, %v", got, err)
	}
}

func TestSearchFilesUsesSnapshotsAndCapsResults(t *testing.T) {
	root := t.TempDir()
	path := writeSearchFile(t, root, "open.txt", "disk has no match\n")
	got, truncated, err := searchFiles(context.Background(), root, "needle", map[string]string{
		path: "unsaved needle\nneedle again\nthird needle\n",
	}, 2)
	if err != nil || !truncated || len(got) != 2 {
		t.Fatalf("snapshot/cap = %#v,%v,%v", got, truncated, err)
	}
	for _, entry := range got {
		if entry.path != path {
			t.Fatalf("snapshot result path = %q, want %q", entry.path, path)
		}
	}
}

func TestSearchFilesCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, err := searchFiles(ctx, t.TempDir(), "needle", nil, searchResultLimit)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled search error = %v", err)
	}
}

func TestResolveSearchRoot(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "sub")
	outside := t.TempDir()
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := DefaultConfig()
	cfg.AutoLSP = false
	c := New("test", cfg, Options{Mode: ModeScan, Dir: root})
	defer c.close()

	for raw, want := range map[string]string{"": root, "sub": sub, outside: outside} {
		got, err := resolveSearchRoot(c, raw)
		if err != nil || got != want {
			t.Errorf("resolveSearchRoot(%q) = %q,%v, want %q", raw, got, err, want)
		}
	}
	file := writeSearchFile(t, root, "not-a-dir", "x")
	if _, err := resolveSearchRoot(c, file); err == nil {
		t.Fatal("a file path should be rejected")
	}
}

// TestBottomDockSwitchesPanels: the dock shows only its active panel (its tabs live in
// the status bar), and left/right switch panels and focus.
func TestBottomDockSwitchesPanels(t *testing.T) {
	diagnostics := newDiagnosticsPanel(nil)
	diagnostics.entries = []diagnosticEntry{{diagnostic: lspDiagnostic{Message: "problem"}}}
	diagnostics.reflow()
	search := newSearchPanel(nil)
	search.setResult("needle", "/tmp", []searchEntry{{path: "/tmp/a.txt", text: "needle"}}, false, nil)
	dock := newBottomDock(diagnostics, search)
	dock.SetSize(42, 8)
	dock.Focus()

	view := stripANSI(dock.View(true))
	rows := strings.Split(view, "\n")
	if lipgloss.Height(view) != 8 || !strings.Contains(rows[0], "Diagnostics (1)") {
		t.Fatalf("diagnostics title or dock height changed:\n%s", view)
	}
	if strings.Contains(rows[len(rows)-1], "Diag") {
		t.Fatalf("the dock's tabs moved to the status bar, not its bottom border:\n%s", view)
	}

	dock.UpdatePanel(nil, keyMsg("right"))
	if dock.active != bottomSearch || !search.Focused() || diagnostics.Focused() {
		t.Fatal("right should switch focus from diagnostics to search")
	}
	view = stripANSI(dock.View(true))
	if !strings.Contains(strings.Split(view, "\n")[0], "Search (1)") {
		t.Fatalf("search title should remain on the top edge:\n%s", view)
	}
	dock.UpdatePanel(nil, keyMsg("left"))
	if dock.active != bottomDiagnostics || !diagnostics.Focused() {
		t.Fatal("left should switch back to diagnostics")
	}
}

func TestSearchPanelDimsFileHeadings(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "nested", "match.txt")
	search := newSearchPanel(nil)
	search.SetSize(60, 8)
	search.setResult("needle", root, []searchEntry{{path: path, text: "needle"}}, false, nil)

	view := search.View(false)
	muted := lipgloss.NewStyle().Foreground(core.MutedColor)
	if want := muted.Render(filepath.Join("nested", "match.txt")); !strings.Contains(view, want) {
		t.Fatalf("search path heading is not muted:\n%s", view)
	}
	if unwanted := muted.Render("1:1  needle"); strings.Contains(view, unwanted) {
		t.Fatalf("search result text should keep its normal color:\n%s", view)
	}
}

func TestFindFilesShortcutRunsIntoFocusedBottomPanel(t *testing.T) {
	root := t.TempDir()
	writeSearchFile(t, root, "match.txt", "before needle after\n")
	model, s, sh := newHomeRouter(t, Options{Mode: ModeScan, Dir: root, Depth: 2, DepthSet: true})
	defer Of(sh).close()

	var cmd tea.Cmd
	model, cmd = model.Update(keyMsg("alt+shift+f"))
	if _, ok := model.(core.Router).Top().(*components.FormScreen); ok {
		t.Fatal("alt+shift+f should no longer open Find in Files")
	}
	model, cmd = model.Update(keyMsg("ctrl+alt+f"))
	model = pumpModel(model, cmd)
	if _, ok := model.(core.Router).Top().(*components.FormScreen); !ok {
		t.Fatalf("shortcut top = %T, want modal form", model.(core.Router).Top())
	}
	model, cmd = model.Update(keyMsg("needle"))
	model = pumpModel(model, cmd)
	model, cmd = model.Update(keyMsg("enter"))
	model = pumpModel(model, cmd)

	if model.(core.Router).Top() != s || !s.bottomVisible || s.bottom.active != bottomSearch || !s.bottom.Focused() {
		t.Fatal("submitted search should return home with the Search tab focused")
	}
	if len(s.search.entries) != 1 || s.search.entries[0].path != filepath.Join(root, "match.txt") {
		t.Fatalf("search results = %#v", s.search.entries)
	}
}

func TestFindFilesBlankSubmissionClearsSearch(t *testing.T) {
	for _, tc := range []struct {
		name, query, tab string
		visible, focused bool
	}{
		{"empty", "", bottomSearch, true, true},
		{"whitespace", " \t ", bottomSearch, true, true},
		{"editor focused", "", bottomSearch, true, false},
		{"diagnostics", "", bottomDiagnostics, true, true},
		{"hidden", "", bottomSearch, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			path := writeSearchFile(t, root, "match.txt", strings.Repeat("needle\n", 40))
			model, s, sh := newHomeRouterCfg(t, testConfig(), Options{Mode: ModeScan, Dir: root})
			t.Cleanup(Of(sh).close)
			drive := func(chord string) {
				t.Helper()
				var cmd tea.Cmd
				model, cmd = model.Update(keyMsg(chord))
				model = pumpModel(model, cmd)
			}
			drive("ctrl+alt+f")
			form := model.(core.Router).Top().(*components.FormScreen)
			form.SetValue("query", "needle")
			form.SetValue("path", root)
			drive("enter")
			if len(s.search.entries) != 40 || s.search.entries[0].path != path {
				t.Fatal("initial search did not populate results")
			}
			s.search.selectEntry(30)
			s.modular.Nudge(0, -3)
			if !tc.visible {
				s.toggleBottom(sh)
			}
			s.bottom.selectTab(tc.tab)
			if tc.focused {
				s.modular.FocusSlot(s.panelSlot(s.bottom))
			} else {
				s.modular.FocusSlot(s.editorSlot())
			}
			priorFocus := s.focusedPane()
			priorHeight := s.search.height
			drive("ctrl+alt+f")
			form = model.(core.Router).Top().(*components.FormScreen)
			form.SetValue("query", tc.query)
			form.SetValue("path", filepath.Join(root, "missing-directory"))
			drive("enter")
			if model.(core.Router).Top() != s {
				t.Fatal("blank search should dismiss the form without validating the path")
			}
			if s.searchQuery != "" || s.searchPath != root || s.searchCancel != nil {
				t.Fatal("clear should forget the query and retain the previous search path")
			}
			if len(s.search.entries) != 0 || s.search.selected != -1 || s.search.ScrollOffset() != 0 ||
				s.search.root != "" || s.search.status != "No search run." {
				t.Fatal("clear should reset search results, selection, scrolling and status")
			}
			wantVisible := tc.visible && tc.tab == bottomDiagnostics
			if s.bottomVisible != wantVisible || s.bottom.active != tc.tab {
				t.Fatal("clear changed the wrong dock tab or visibility")
			}
			wantFocus := priorFocus
			if !wantVisible && priorFocus == s.bottom {
				wantFocus = s.editorPanel
			}
			if s.focusedPane() != wantFocus {
				t.Fatal("clear did not preserve focus or return it from the closed dock to the editor")
			}
			if !s.bottomVisible {
				s.toggleBottom(sh)
				if s.search.height != priorHeight {
					t.Fatal("closing search lost the resized dock height")
				}
			}
			drive("ctrl+alt+f")
			form = model.(core.Router).Top().(*components.FormScreen)
			if form.Value("query") != "" || form.Value("path") != root {
				t.Fatal("reopened form should have an empty query and the retained path")
			}
			form.SetValue("query", "needle")
			drive("enter")
			if len(s.search.entries) != 40 || !s.bottomVisible || !s.search.Focused() {
				t.Fatal("a new search should work after clearing")
			}
		})
	}
}

func TestSearchPanelClearResetsTitle(t *testing.T) {
	search := newSearchPanel(nil)
	search.SetSize(50, 8)
	search.setResult("needle", "/tmp", []searchEntry{{path: "/tmp/match.txt", text: "needle"}}, false, nil)
	search.clear()
	view := stripANSI(search.View(false))
	if !strings.Contains(view, "Search") || strings.Contains(view, "Search (") || !strings.Contains(view, "No search run.") {
		t.Fatalf("clear did not reset the panel title and status: %s", view)
	}
}

func TestClearFindFilesCancelsRunningSearchAndIgnoresLateResults(t *testing.T) {
	s, sh := newHomeCfg(t, testConfig(), Options{Mode: ModeScan, Dir: t.TempDir()})
	t.Cleanup(Of(sh).close)
	s.beginFindFiles(sh, findFilesRequest{target: s, query: "needle", root: docsRoot(Of(sh))})
	generation := s.searchGeneration
	cancel := s.searchCancel
	canceled := false
	s.searchCancel = func() { canceled = true; cancel() }
	s.beginFindFiles(sh, findFilesRequest{target: s})
	if !canceled || s.searchCancel != nil || s.bottomVisible {
		t.Fatal("clear should cancel the running search and close Search")
	}
	s.finishFindFiles(findFilesResult{
		target: s, generation: generation, query: "needle",
		entries: []searchEntry{{path: "late.txt", text: "needle"}},
	})
	if len(s.search.entries) != 0 || s.search.status != "No search run." {
		t.Fatal("late results restored a cleared search")
	}
}

func TestFindFilesCancelKeepsSearch(t *testing.T) {
	model, s, sh := newHomeRouterCfg(t, testConfig(), Options{Mode: ModeScan, Dir: t.TempDir()})
	t.Cleanup(Of(sh).close)
	s.searchQuery = "needle"
	s.search.setResult("needle", "/tmp", []searchEntry{{path: "/tmp/match.txt", text: "needle"}}, false, nil)
	var cmd tea.Cmd
	model, cmd = model.Update(keyMsg("ctrl+alt+f"))
	model = pumpModel(model, cmd)
	model.(core.Router).Top().(*components.FormScreen).SetValue("query", "")
	model, cmd = model.Update(keyMsg("esc"))
	model = pumpModel(model, cmd)
	if model.(core.Router).Top() != s || s.searchQuery != "needle" || len(s.search.entries) != 1 {
		t.Fatal("canceling a blank form should leave the previous search intact")
	}
}
