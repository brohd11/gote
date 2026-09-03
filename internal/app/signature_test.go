package app

import (
	"strings"
	"testing"

	"github.com/brohd11/bubblestack/components"
	"github.com/brohd11/bubblestack/core"
)

func TestSignatureCallStartFindsEnclosingCall(t *testing.T) {
	ed := components.NewEditorScreen(components.EditorOpts{})
	tests := []struct {
		name  string
		text  string
		caret components.EditorPosition
		want  components.EditorPosition
		ok    bool
	}{
		{"after the comma", "myfunc(c,)", components.EditorPosition{Column: 9},
			components.EditorPosition{Column: 6}, true},
		{"first argument", "myfunc(c)", components.EditorPosition{Column: 8},
			components.EditorPosition{Column: 6}, true},
		{"left of the opener", "myfunc(c,)", components.EditorPosition{Column: 3},
			components.EditorPosition{}, false},
		{"right of the closer", "myfunc(c,)", components.EditorPosition{Column: 10},
			components.EditorPosition{}, false},
		{"nested takes the inner call", "outer(inner())", components.EditorPosition{Column: 12},
			components.EditorPosition{Column: 11}, true},
		{"back out to the outer call", "outer(inner())", components.EditorPosition{Column: 13},
			components.EditorPosition{Column: 5}, true},
		// A composite literal is not an argument list, and neither is whatever encloses it.
		{"composite literal", "call([]int{})", components.EditorPosition{Column: 11},
			components.EditorPosition{}, false},
		{"index expression", "call(xs[])", components.EditorPosition{Column: 8},
			components.EditorPosition{}, false},
		{"no call at all", "x := 1", components.EditorPosition{Column: 6},
			components.EditorPosition{}, false},
		{"wrapped across lines", "myfunc(\n\tc,\n\t", components.EditorPosition{Line: 2, Column: 1},
			components.EditorPosition{Column: 6}, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ed.SetText(tc.text)
			got, ok := signatureCallStart(ed, tc.caret)
			if ok != tc.ok || got != tc.want {
				t.Fatalf("signatureCallStart = %+v,%v; want %+v,%v", got, ok, tc.want, tc.ok)
			}
		})
	}
}

// The walk back is bounded, so a call opened further away than the budget is not found —
// which retires a hint rather than scanning the buffer on every keystroke.
func TestSignatureCallStartStopsAtItsBudget(t *testing.T) {
	ed := components.NewEditorScreen(components.EditorOpts{})
	ed.SetText("myfunc(" + strings.Repeat("\n", signatureScanLines))
	if _, ok := signatureCallStart(ed, components.EditorPosition{Line: signatureScanLines - 1}); !ok {
		t.Fatal("a call inside the budget should still be found")
	}
	if _, ok := signatureCallStart(ed, components.EditorPosition{Line: signatureScanLines}); ok {
		t.Fatal("a call older than the budget should not be found")
	}
}

// signatureHome is a focused editor on a real file with the caret parked inside a call,
// and a hint already up for it.
func signatureHome(t *testing.T) (*homeScreen, *core.Shared) {
	t.Helper()
	s, sh := completionHomeFor(t, "main.go")
	s.editor.SetText("myfunc(c,)")
	s.editor.Reveal(components.EditorPosition{Column: 9})
	s.applySignature(&lspRequestResult{
		kind: lspReqSignature, path: s.currentPath,
		signature: &lspSignature{Label: "myfunc(a int, b int)"},
	})
	if s.signature.popup == nil {
		t.Fatal("a signature result should have installed a hint")
	}
	return s, sh
}

// The bug this fixes: the hint opened on "(" or "," and then stayed up forever, because
// only a typed closing bracket could retire it. Leaving the call by moving the caret is
// the same event, and so is every other way out.
func TestSignatureHintLeavesWhenTheCaretLeavesTheCall(t *testing.T) {
	for _, key := range []string{"end", "home"} {
		t.Run(key, func(t *testing.T) {
			s, sh := signatureHome(t)
			s.Update(sh, keyMsg(key))
			if s.signature.popup != nil {
				t.Fatalf("%s left the call but kept the hint (text %q, caret %+v)",
					key, s.editor.Text(), s.editor.CursorPosition())
			}
		})
	}
}

// The complement, so the fix cannot be "close it always": typing arguments keeps the hint,
// and so does wrapping the call onto another line.
func TestSignatureHintSurvivesArgumentEntry(t *testing.T) {
	s, sh := signatureHome(t)
	s.Update(sh, keyMsg("d"))
	if s.signature.popup == nil {
		t.Fatal("typing an argument closed the hint")
	}
	s.Update(sh, keyMsg("enter"))
	if s.signature.popup == nil {
		t.Fatalf("wrapping the call onto a new line closed the hint: %q", s.editor.Text())
	}
}

// esc is the one dismissal a caret cannot express, so it stays in the typing hook.
func TestSignatureHintClosesOnEscape(t *testing.T) {
	s, sh := signatureHome(t)
	s.Update(sh, keyMsg("esc"))
	if s.signature.popup != nil {
		t.Fatal("escape left the hint up")
	}
}
