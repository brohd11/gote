package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/brohd11/bubblestack/components/editor"
	"github.com/brohd11/bubblestack/core"

	"go.lsp.dev/protocol"
)

func completionHome(t *testing.T) (*homeScreen, *core.Shared) {
	t.Helper()
	return completionHomeFor(t, "main.py")
}

func completionHomeFor(t *testing.T, name string) (*homeScreen, *core.Shared) {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
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
	position := completionIdentifierStart(s.editor, s.editor.CursorPosition())
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
	s.Update(sh, keyMsg("down")) // a manual choice for the old query
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

func TestCompletionDoesNotInstallPopupWithoutFuzzyMatches(t *testing.T) {
	s, sh := completionHome(t)
	s.Update(sh, keyMsg("x = nope"))
	position := completionIdentifierStart(s.editor, s.editor.CursorPosition())
	lspPosition, ok := editorPositionToLSP(s.editor, position)
	if !ok {
		t.Fatal("could not convert cursor")
	}
	s.completion.requestID = 7
	s.completion.path = s.currentPath
	s.applyCompletionResult(&lspCompletionResult{
		id: 7, path: s.currentPath, editSeq: s.editor.EditSeq(), position: lspPosition,
		items: []lspCompletionItem{{Label: "print", FilterText: "print", InsertText: "print"}},
	})
	if s.completion.popup != nil || s.completion.list != nil {
		t.Fatalf("unmatched result left an invisible completion installed: popup=%v list=%v",
			s.completion.popup != nil, s.completion.list != nil)
	}

	s.Update(sh, keyMsg("enter"))
	if got := s.editor.Text(); got != "x = nope\n" {
		t.Fatalf("Enter after unmatched completion = %q, want newline", got)
	}
}

func TestCompletionClosesWhenTypingRemovesLastMatch(t *testing.T) {
	s, sh := completionHome(t)
	s.Update(sh, keyMsg("x = pr"))
	showCompletion(t, s,
		lspCompletionItem{Label: "print", FilterText: "print", InsertText: "print"},
	)

	_, act := s.Update(sh, keyMsg("z"))
	if s.completion.popup != nil || s.completion.list != nil {
		t.Fatalf("zero-match query left an invisible completion installed: popup=%v list=%v",
			s.completion.popup != nil, s.completion.list != nil)
	}
	if act.Cmd == nil {
		t.Fatal("zero-match query did not retain the completion debounce")
	}

	s.Update(sh, keyMsg("enter"))
	if got := s.editor.Text(); got != "x = prz\n" {
		t.Fatalf("Enter after locally emptied completion = %q, want newline", got)
	}
}

// The identifier start is the LSP request position, so the server is always asked with an
// empty prefix and the whole typed word stays as the local fuzzy query.
func TestCompletionRequestsAtIdentifierStart(t *testing.T) {
	ed := editor.New(editor.Opts{})
	tests := []struct {
		name       string
		text       string
		caret      editor.Position
		wantStart  editor.Position
		wantQuery  string
		wantLSPCol uint32
	}{
		{"member", "ins.bg", editor.Position{Column: 6}, editor.Position{Column: 4}, "bg", 4},
		{"ordinary", "bg", editor.Position{Column: 2}, editor.Position{}, "bg", 0},
		{"empty member", "ins.", editor.Position{Column: 4}, editor.Position{Column: 4}, "", 4},
		// The member's first rune is one editor column but two UTF-16 code units; the
		// request position sits before it, so the conversion must not drift either way.
		{"utf16", "obj.𐐀x", editor.Position{Column: 6}, editor.Position{Column: 4}, "𐐀x", 4},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ed.SetText(tc.text)
			start := completionIdentifierStart(ed, tc.caret)
			query, ok := completionQuery(ed, start, tc.caret)
			lspStart, lspOK := editorPositionToLSP(ed, start)
			if !ok || !lspOK || start != tc.wantStart || query != tc.wantQuery || lspStart.Character != tc.wantLSPCol {
				t.Fatalf("start=%+v query=%q lsp=%+v; want start=%+v query=%q col=%d",
					start, query, lspStart, tc.wantStart, tc.wantQuery, tc.wantLSPCol)
			}
		})
	}
}

func TestCompletionSeededTextEditReplacesFullFuzzyQuery(t *testing.T) {
	s, sh := completionHome(t)
	s.Update(sh, keyMsg("ins.bg"))
	showCompletion(t, s, lspCompletionItem{
		Label: "begins_with", FilterText: "begins_with", InsertText: "begins_with",
		Edit: &lspCompletionEdit{
			Range:   protocol.Range{Start: protocol.Position{Character: 4}, End: protocol.Position{Character: 4}},
			NewText: "begins_with",
		},
	})
	s.Update(sh, keyMsg("tab"))
	if got := s.editor.Text(); got != "ins.begins_with" {
		t.Fatalf("seeded completion edit = %q, want full member query replaced", got)
	}
}

func TestCompletionPreselectOnlyOverridesEmptyQuery(t *testing.T) {
	s, sh := completionHome(t)
	s.Update(sh, keyMsg("bg"))
	showCompletion(t, s,
		lspCompletionItem{Label: "bg", FilterText: "bg", InsertText: "BEST"},
		lspCompletionItem{Label: "boring", FilterText: "boring", InsertText: "PRESELECT", Preselect: true},
	)
	s.Update(sh, keyMsg("tab"))
	if got := s.editor.Text(); got != "BEST" {
		t.Fatalf("non-empty fuzzy query accepted %q, want best match", got)
	}

	empty, emptySH := completionHome(t)
	showCompletion(t, empty,
		lspCompletionItem{Label: "first", InsertText: "FIRST"},
		lspCompletionItem{Label: "preferred", InsertText: "PREFERRED", Preselect: true},
	)
	empty.Update(emptySH, keyMsg("tab"))
	if got := empty.editor.Text(); got != "PREFERRED" {
		t.Fatalf("empty-query completion accepted %q, want LSP preselect", got)
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
	ed := editor.New(editor.Opts{})
	ed.SetText("a😀b")
	position, ok := editorPositionToLSP(ed, editor.Position{Line: 0, Column: 2})
	if !ok || position.Character != 3 {
		t.Fatalf("rune column 2 -> UTF-16 = %+v,%v", position, ok)
	}
	back, ok := lspPositionToEditor(ed, protocol.Position{Line: 0, Character: 3})
	if !ok || back != (editor.Position{Line: 0, Column: 2}) {
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
	if got := s.editor.CursorPosition(); got != (editor.Position{Column: 8}) {
		t.Fatalf("paired completion caret = %+v", got)
	}
}

func TestCompletionInstallsSnippetTabStops(t *testing.T) {
	s, sh := completionHome(t)
	s.Update(sh, keyMsg("ca"))
	showCompletion(t, s, lspCompletionItem{
		Label: "call", FilterText: "call", InsertText: "call(first, second)", Snippet: true,
		Stops: []editor.CompletionStop{
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
	if got := s.editor.CursorPosition(); got != (editor.Position{Column: 10}) {
		t.Fatalf("snippet final caret = %+v", got)
	}
}

// Completion must fire inside comments. Comment-driven meta-programming is a real use —
// the user's GDScript tag system depends on it — and Godot's own CodeEdit behaves the same
// way. A server that answers a comment position with nothing is the server making that
// call; the editor staying quiet would take the choice away from it. Nothing in the
// request path inspects syntax, and this pins that: suppressing completion in comments is
// exactly the kind of "optimization" someone would reach for later.
func TestCompletionFiresInsideComments(t *testing.T) {
	for _, name := range []string{"tags.gd", "main.py", "main.go"} {
		t.Run(name, func(t *testing.T) {
			s, sh := completionHomeFor(t, name)
			// requestCompletion reconciles before it asks, so the manager needs a real
			// config to recognize the document at all. GDScript additionally refuses a
			// workspace with no project.godot above it — which is the shape the tag system
			// actually runs in, so give it one.
			if name == "tags.gd" {
				marker := filepath.Join(filepath.Dir(s.currentPath), "project.godot")
				if err := os.WriteFile(marker, nil, 0o644); err != nil {
					t.Fatal(err)
				}
			}
			Of(sh).lsp = &lspManager{cfg: DefaultConfig()}

			comment := "# @export_tag Player"
			if name == "main.go" {
				comment = "// @export_tag Player"
			}
			s.Update(sh, keyMsg(comment))

			// Typing an identifier rune inside the comment still schedules a request.
			_, act := s.Update(sh, keyMsg("s"))
			if act.Cmd == nil {
				t.Fatal("typing inside a comment should still schedule a completion request")
			}

			// And the explicit gesture reaches the manager from inside a comment.
			s.requestCompletion(sh, "", true)
			if s.completion.requestID == 0 {
				t.Fatal("ctrl+space inside a comment issued no request")
			}
			if got := Of(sh).lsp.completion; got == nil || !got.manual {
				t.Fatalf("queued request = %#v, want a manual one", got)
			}
		})
	}
}
