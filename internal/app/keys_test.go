package app

import (
	"reflect"
	"strings"
	"testing"

	"github.com/brohd11/bubblestack/components/editor"
	"github.com/brohd11/bubblestack/core"
)

func shiftedAltForms(letter string) []string {
	return []string{"alt+" + strings.ToUpper(letter), "alt+shift+" + letter, "alt+shift+" + strings.ToUpper(letter)}
}

func TestLanguageRequestShortcutsDispatchBothTerminalForms(t *testing.T) {
	for _, tc := range []struct {
		letter, action string
	}{{"g", "definition"}, {"h", "hover"}, {"r", "references"}, {"m", "format"}} {
		for _, chord := range shiftedAltForms(tc.letter) {
			t.Run(chord, func(t *testing.T) {
				s, sh := completionHomeFor(t, "keys.txt")
				// A closed manager refuses each request with its action-specific status.
				// This tests the actual home-screen dispatch without starting a server.
				Of(sh).lsp.closed = true
				s.editor.SetText("unchanged text")
				before := s.editor.CursorPosition()
				_, act := s.Update(sh, keyMsg(chord))
				want := core.SetStatus("no language server for " + tc.action + " here")
				if !reflect.DeepEqual(act.Msg, want.Msg) {
					t.Fatalf("%s dispatched %#v, want %#v", chord, act.Msg, want.Msg)
				}
				if s.editor.Text() != "unchanged text" || s.editor.CursorPosition() != before {
					t.Fatal("shortcut changed the buffer or caret")
				}
			})
		}
	}
}

func TestOutlineShortcutTerminalForms(t *testing.T) {
	s, sh := newHome(t)
	Of(sh).close()
	Of(sh).lsp = nil
	s.modular.FocusSlot(s.editorSlot())
	s.editor.SetText("unchanged text")
	for _, chord := range shiftedAltForms("o") {
		s.Update(sh, keyMsg(chord))
		if !s.outlineVisible {
			t.Fatalf("%s did not open the outline", chord)
		}
		s.Update(sh, keyMsg(chord))
		if s.outlineVisible || s.editor.Text() != "unchanged text" {
			t.Fatalf("%s did not close the outline cleanly", chord)
		}
	}
}

func TestJumpBackShortcutAfterSearchWithoutLSP(t *testing.T) {
	s, sh := newHome(t)
	Of(sh).close()
	Of(sh).lsp = nil
	from, fromEditor := seedDoc(t, s, sh, "from.txt", "one\ntwo\nthree\n")
	to, _ := seedDoc(t, s, sh, "result.txt", "alpha\nbeta\ngamma\n")
	start := editor.Position{Line: 1, Column: 2}
	for _, chord := range shiftedAltForms("b") {
		s.openDoc(sh, from)
		fromEditor.Reveal(start)
		s.activateSearchResult(sh, searchEntry{path: to, line: 2, start: 0, end: 3})
		if s.currentPath != to {
			t.Fatal("search did not jump to the result")
		}
		s.Update(sh, keyMsg(chord))
		if s.currentPath != from || s.editor.CursorPosition() != start || len(s.jumps) != 0 {
			t.Fatalf("%s did not restore search origin: path=%s caret=%+v jumps=%d",
				chord, s.currentPath, s.editor.CursorPosition(), len(s.jumps))
		}
	}
}

func TestRetiredLanguageShortcutsAreFree(t *testing.T) {
	s, sh := completionHomeFor(t, "keys.txt")
	Of(sh).lsp.closed = true
	s.editor.SetText("unchanged text")
	s.jumps = []jumpSite{{path: s.currentPath, pos: editor.Position{Column: 5}}}
	for _, chord := range []string{"alt+g", "alt+h", "alt+o", "alt+n", "alt+m", "ctrl+o"} {
		if _, handled := s.languageServerKey(sh, chord); handled {
			t.Errorf("retired shortcut %s still claimed by language tools", chord)
		}
		s.Update(sh, keyMsg(chord))
		if s.outlineVisible || s.lspRequestID != 0 || len(s.jumps) != 1 || s.editor.CursorPosition().Column != 0 || s.editor.Text() != "unchanged text" {
			t.Fatalf("retired shortcut %s triggered an action or inserted text", chord)
		}
	}
	if keys := completionKey.Keys(); !reflect.DeepEqual(keys, []string{"ctrl+space"}) {
		t.Fatalf("completion binding changed to %v", keys)
	}
}
