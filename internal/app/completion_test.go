package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/brohd11/bubblestack/components"
	"github.com/brohd11/bubblestack/core"

	"go.lsp.dev/protocol"
)

func completionHome(t *testing.T) (*homeScreen, *core.Shared) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "main.py")
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := DefaultConfig()
	cfg.AutoLSP = false
	c := New("test", cfg, Options{Mode: ModeFile, File: path})
	// These UI tests inject protocol results directly. A zero manager is enough to
	// keep the availability gate open without starting a process or socket.
	c.lsp = &lspManager{}
	sh := core.NewShared(c)
	s := NewHomeScreen(sh).(*homeScreen)
	s.Init(sh)
	s.SetSize(sh, 80, 24)
	_ = s.View(sh) // publishes the editor pane's absolute origin
	return s, sh
}

func showCompletion(t *testing.T, s *homeScreen, items ...lspCompletionItem) {
	t.Helper()
	position := s.editor.CursorPosition()
	lspPosition, ok := editorPositionToLSP(s.editor, position)
	if !ok {
		t.Fatal("could not convert cursor")
	}
	s.completion.requestID = 7
	s.completion.path = s.currentPath
	s.applyCompletionResult(&lspCompletionResult{
		id: 7, path: s.currentPath, editSeq: s.editor.EditSeq(), position: lspPosition, items: items,
	})
	if s.completion.popup == nil {
		t.Fatal("completion popup did not open")
	}
}

func TestCompletionPopupPassesTypingAcceptsAndUndoes(t *testing.T) {
	s, sh := completionHome(t)
	s.Update(sh, keyMsg("pri"))
	showCompletion(t, s,
		lspCompletionItem{Label: "private", FilterText: "private", InsertText: "private"},
		lspCompletionItem{Label: "print", FilterText: "print", InsertText: "print", Edit: &lspCompletionEdit{
			Range: protocol.Range{Start: protocol.Position{}, End: protocol.Position{Character: 3}}, NewText: "print",
		}},
	)
	before := s.editor.CursorPosition()
	s.Update(sh, keyMsg("down"))
	if after := s.editor.CursorPosition(); after != before {
		t.Fatalf("popup down moved editor cursor from %+v to %+v", before, after)
	}
	s.Update(sh, keyMsg("n")) // passed through, and locally filters to print
	if got := s.editor.Text(); got != "prin" {
		t.Fatalf("transparent typing = %q", got)
	}
	if view := stripANSI(s.View(sh)); !strings.Contains(view, "print") || strings.Contains(view, "private") {
		t.Fatalf("locally filtered popup:\n%s", view)
	}
	s.Update(sh, keyMsg("tab"))
	if got := s.editor.Text(); got != "print" || s.completion.popup != nil {
		t.Fatalf("accepted completion = %q popup=%v", got, s.completion.popup != nil)
	}
	s.Update(sh, keyMsg("ctrl+z"))
	if got := s.editor.Text(); got != "prin" {
		t.Fatalf("one undo after completion = %q", got)
	}
}

func TestCompletionPopupFuzzyFiltersAndRanks(t *testing.T) {
	s, sh := completionHome(t)
	s.Update(sh, keyMsg("p"))
	showCompletion(t, s,
		lspCompletionItem{Label: "sprint", FilterText: "sprint", InsertText: "sprint"},
		lspCompletionItem{Label: "print", FilterText: "print", InsertText: "print"},
		lspCompletionItem{Label: "unrelated", FilterText: "unrelated", InsertText: "unrelated"},
	)
	s.Update(sh, keyMsg("n"))
	s.Update(sh, keyMsg("t"))
	view := stripANSI(s.View(sh))
	printAt, sprintAt := strings.Index(view, "print"), strings.Index(view, "sprint")
	if printAt < 0 || sprintAt < 0 || printAt >= sprintAt || strings.Contains(view, "unrelated") {
		t.Fatalf("fuzzy-ranked popup should put print before sprint and drop unrelated:\n%s", view)
	}
	s.Update(sh, keyMsg("tab"))
	if got := s.editor.Text(); got != "print" {
		t.Fatalf("accepted fuzzy completion = %q, want print", got)
	}
}

func TestCompletionEscapeOnlyClosesPopup(t *testing.T) {
	s, sh := completionHome(t)
	s.Update(sh, keyMsg("pri"))
	showCompletion(t, s, lspCompletionItem{Label: "print", FilterText: "print", InsertText: "print"})
	_, act := s.Update(sh, keyMsg("esc"))
	if s.completion.popup != nil || act.Msg != nil || !s.editorPanel.Focused() {
		t.Fatalf("escape popup=%v action=%+v focused=%v", s.completion.popup != nil, act, s.editorPanel.Focused())
	}
}

func TestCompletionUTF16Positions(t *testing.T) {
	ed := components.NewEditorScreen(components.EditorOpts{})
	ed.SetText("a😀b")
	position, ok := editorPositionToLSP(ed, components.EditorPosition{Line: 0, Column: 2})
	if !ok || position.Character != 3 {
		t.Fatalf("rune column 2 -> UTF-16 = %+v,%v", position, ok)
	}
	back, ok := lspPositionToEditor(ed, protocol.Position{Line: 0, Character: 3})
	if !ok || back != (components.EditorPosition{Line: 0, Column: 2}) {
		t.Fatalf("UTF-16 column 3 -> editor = %+v,%v", back, ok)
	}
	if _, ok := lspPositionToEditor(ed, protocol.Position{Line: 0, Character: 2}); ok {
		t.Fatal("position inside a surrogate pair was accepted")
	}
}

func TestCompletionPairsPlainTrailingOpener(t *testing.T) {
	s, sh := completionHome(t)
	s.Update(sh, keyMsg("my"))
	showCompletion(t, s, lspCompletionItem{
		Label: "my_func", FilterText: "my_func", InsertText: "my_func(",
	})
	s.Update(sh, keyMsg("tab"))
	if got := s.editor.Text(); got != "my_func()" {
		t.Fatalf("paired completion = %q", got)
	}
	if got := s.editor.CursorPosition(); got != (components.EditorPosition{Column: 8}) {
		t.Fatalf("paired completion caret = %+v", got)
	}
}

func TestCompletionInstallsSnippetTabStops(t *testing.T) {
	s, sh := completionHome(t)
	s.Update(sh, keyMsg("ca"))
	showCompletion(t, s, lspCompletionItem{
		Label: "call", FilterText: "call", InsertText: "call(first, second)", Snippet: true,
		Stops: []components.EditorCompletionStop{
			{Index: 1, Start: 5, End: 10}, {Index: 2, Start: 12, End: 18}, {Index: 0, Start: 19, End: 19},
		},
	})
	s.Update(sh, keyMsg("tab")) // accept and select first
	s.Update(sh, keyMsg("x"))
	s.Update(sh, keyMsg("tab")) // second placeholder
	s.Update(sh, keyMsg("y"))
	s.Update(sh, keyMsg("tab")) // final $0
	if got := s.editor.Text(); got != "call(x, y)" {
		t.Fatalf("snippet edits = %q", got)
	}
	if got := s.editor.CursorPosition(); got != (components.EditorPosition{Column: 10}) {
		t.Fatalf("snippet final caret = %+v", got)
	}
}
