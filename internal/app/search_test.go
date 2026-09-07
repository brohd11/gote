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

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

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

func TestBottomDockUsesBottomBorderForTabs(t *testing.T) {
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
	if !strings.Contains(rows[len(rows)-1], "Diag") || !strings.Contains(rows[len(rows)-1], "Search") {
		t.Fatalf("compact tabs are not on the bottom border:\n%s", view)
	}
	if !strings.Contains(rows[len(rows)-1], "─") {
		t.Fatalf("tabs should replace only their part of the bottom edge:\n%s", view)
	}

	dock.UpdatePanel(nil, keyMsg("right"))
	if dock.active != bottomSearch || !search.Focused() || diagnostics.Focused() {
		t.Fatal("right should switch focus from diagnostics to search")
	}
	view = stripANSI(dock.View(true))
	if !strings.Contains(strings.Split(view, "\n")[0], "Search (1)") {
		t.Fatalf("search title should remain on the top edge:\n%s", view)
	}
	dock.UpdatePanel(nil, tea.MouseClickMsg{X: 2, Y: 7, Button: tea.MouseLeft})
	if dock.active != bottomDiagnostics {
		t.Fatal("clicking the bottom Diag tab should activate diagnostics")
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

func TestBottomHelpIsContextualAndCapped(t *testing.T) {
	s, sh := newHome(t)
	defer Of(sh).close()
	s.toggleBottom(sh)
	s.modular.FocusSlot(s.panelSlot(s.bottom))
	bar := stripANSI(s.HelpView(sh))
	for _, want := range []string{"panes", "esc", "enter", "more"} {
		if !strings.Contains(bar, want) {
			t.Fatalf("bottom help missing %q: %s", want, bar)
		}
	}
	if strings.Count(bar, " • ") > 3 {
		t.Fatalf("help bar contains more than four entries: %s", bar)
	}
}

func TestFindFilesShortcutRunsIntoFocusedBottomPanel(t *testing.T) {
	root := t.TempDir()
	writeSearchFile(t, root, "match.txt", "before needle after\n")
	model, s, sh := newHomeRouter(t, Options{Mode: ModeScan, Dir: root, Depth: 2, DepthSet: true})
	defer Of(sh).close()

	var cmd tea.Cmd
	model, cmd = model.Update(keyMsg("ctrl+shift+f"))
	if _, ok := model.(core.Router).Top().(*components.FormScreen); ok {
		t.Fatal("ctrl+shift+f should no longer open Find in Files")
	}
	model, cmd = model.Update(keyMsg("alt+shift+f"))
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
