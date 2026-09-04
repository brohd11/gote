package app

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/brohd11/bubblestack/components/editor"
	"github.com/brohd11/bubblestack/core"

	tea "charm.land/bubbletea/v2"
	"go.lsp.dev/protocol"
)

// seedDoc writes a file, opens it into the ctx with its text already loaded, and hands
// back the editor. Opening this way skips EditorScreen's asynchronous file read, which
// a direct-screen test never drives.
func seedDoc(t *testing.T, s *homeScreen, sh *core.Shared, name, text string) (string, *editor.Screen) {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	ed := Of(sh).OpenDoc(path, s.editorOpts())
	ed.SetText(text)
	ed.SetSize(sh, 80, 20)
	return path, ed
}

// TestRetargetClickAppliesTheConfiguredModifiers: both gestures are message rewriting on
// the way down, so they can be asserted without a terminal — which is the point of doing
// them in gote rather than in the framework.
func TestRetargetClickAppliesTheConfiguredModifiers(t *testing.T) {
	s, sh := newHome(t)
	c := Of(sh)
	c.Config.ClickDefinition, c.Config.ClickContext = clickAlt, clickCtrl

	// The context gesture becomes a real right press, so the editor raises the same
	// menu a physical right click does.
	msg, wants := s.retargetClick(sh, tea.MouseClickMsg{X: 4, Y: 5, Button: tea.MouseLeft, Mod: tea.ModCtrl})
	click, ok := msg.(tea.MouseClickMsg)
	if !ok || click.Button != tea.MouseRight || click.X != 4 || click.Y != 5 {
		t.Fatalf("ctrl+click became %#v, want a right press at the same cell", msg)
	}
	if wants {
		t.Error("the context gesture must not also ask for a definition")
	}

	// The definition gesture passes through untouched: the editor treats a modified
	// left press as an ordinary caret click, which both moves the caret and focuses
	// the pane before gote acts on it.
	original := tea.MouseClickMsg{X: 4, Y: 5, Button: tea.MouseLeft, Mod: tea.ModAlt}
	msg, wants = s.retargetClick(sh, original)
	if msg != tea.Msg(original) {
		t.Errorf("alt+click was rewritten to %#v, want it left alone", msg)
	}
	if !wants {
		t.Error("alt+click should ask for a definition")
	}

	// An unmodified click, a non-left button and a disabled gesture all pass through.
	c.Config.ClickDefinition, c.Config.ClickContext = clickNone, ""
	for name, in := range map[string]tea.MouseClickMsg{
		"plain":     {X: 1, Y: 1, Button: tea.MouseLeft},
		"disabled":  {X: 1, Y: 1, Button: tea.MouseLeft, Mod: tea.ModAlt},
		"not left":  {X: 1, Y: 1, Button: tea.MouseRight, Mod: tea.ModCtrl},
		"unset ctx": {X: 1, Y: 1, Button: tea.MouseLeft, Mod: tea.ModCtrl},
	} {
		if msg, wants := s.retargetClick(sh, in); msg != tea.Msg(in) || wants {
			t.Errorf("%s: click was retargeted to %#v (definition %v)", name, msg, wants)
		}
	}
}

// TestJumpAcrossFilesAndBack: a jump into another document moves the pane and the caret,
// and ctrl+o returns to exactly the caret it left — the whole contract of the back-stack.
func TestJumpAcrossFilesAndBack(t *testing.T) {
	s, sh := newHome(t)
	from, fromEditor := seedDoc(t, s, sh, "from.py", "one\ntwo\nthree\n")
	to, _ := seedDoc(t, s, sh, "to.py", "alpha\nbeta\ngamma\ndelta\n")

	s.openDoc(sh, from)
	fromEditor.Reveal(editor.Position{Line: 1, Column: 2})

	s.jumpToLocation(sh, lspLocation{Path: to, Range: protocol.Range{
		Start: protocol.Position{Line: 2, Character: 1},
		End:   protocol.Position{Line: 2, Character: 4},
	}})
	if s.currentPath != to {
		t.Fatalf("the pane is showing %q, want the jump target", s.currentPath)
	}
	if got := s.editor.CursorPosition(); got.Line != 2 {
		t.Fatalf("caret after the jump = %+v, want line 2", got)
	}
	// The target range is highlighted, which is how a jump says what it landed on.
	if !s.editor.SelectRange(editor.Range{
		Start: editor.Position{Line: 2, Column: 1},
		End:   editor.Position{Line: 2, Column: 4},
	}) {
		t.Fatal("the target range should be selectable in the destination buffer")
	}

	if act := s.jumpBack(sh); act.Msg != nil {
		t.Fatalf("jumping back raised %#v", act.Msg)
	}
	if s.currentPath != from {
		t.Fatalf("ctrl+o left the pane on %q, want %q", s.currentPath, from)
	}
	if got := s.editor.CursorPosition(); got != (editor.Position{Line: 1, Column: 2}) {
		t.Fatalf("caret after going back = %+v, want where the jump started", got)
	}
	// The trail is walked out, not looped around.
	if act := s.jumpBack(sh); act.Msg == nil {
		t.Error("an empty jump stack should say so rather than silently do nothing")
	}
}

// TestPendingJumpWaitsForTheBufferToLoad: opening a document reads its file
// asynchronously, so a jump into one lands before the text does. The destination is
// parked and retried until the buffer has the line.
func TestPendingJumpWaitsForTheBufferToLoad(t *testing.T) {
	s, sh := newHome(t)
	path := filepath.Join(t.TempDir(), "late.py")
	if err := os.WriteFile(path, []byte("a\nb\nc\nd\ne\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	s.travel(sh, path, editor.Position{Line: 3}, nil)
	if s.pendingJump == nil {
		t.Fatal("a jump into an unloaded buffer should be parked, not dropped")
	}
	if got := s.editor.CursorPosition().Line; got != 0 {
		t.Fatalf("the caret moved to line %d before the buffer loaded", got)
	}

	// The file read lands, as EditorScreen.Init's would.
	s.editor.SetSize(sh, 80, 20)
	s.editor.SetText("a\nb\nc\nd\ne\n")
	s.finishHomeUpdate(sh, core.Action{})
	if s.pendingJump != nil {
		t.Fatal("the parked jump should have been applied once the buffer had content")
	}
	if got := s.editor.CursorPosition().Line; got != 3 {
		t.Fatalf("caret after the load = line %d, want 3", got)
	}
}

// TestPendingJumpGivesUpOnAStaleTarget: a server answer against a file that has since
// shrunk names a line that does not exist. That must cost one wrong caret, not a retry
// on every update for the rest of the session.
func TestPendingJumpGivesUpOnAStaleTarget(t *testing.T) {
	s, sh := newHome(t)
	path := filepath.Join(t.TempDir(), "short.py")
	if err := os.WriteFile(path, []byte("only\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s.travel(sh, path, editor.Position{Line: 400}, nil)
	s.editor.SetSize(sh, 80, 20)
	s.editor.SetText("only\n")
	s.finishHomeUpdate(sh, core.Action{})
	if s.pendingJump != nil {
		t.Fatal("a jump at a line the loaded buffer does not have should be abandoned")
	}
}

// TestApplyFormatGuardsAndAppliesAsOneStep: the editSeq guard is applyCompletionResult's,
// and for the same reason — edits computed against text the user has since changed
// describe a document that no longer exists.
func TestApplyFormatGuardsAndAppliesAsOneStep(t *testing.T) {
	s, sh := newHome(t)
	path, ed := seedDoc(t, s, sh, "fmt.py", "import  os\nx=1\n")
	s.openDoc(sh, path)

	edits := []lspTextEdit{
		{Range: protocol.Range{
			Start: protocol.Position{Line: 0, Character: 0},
			End:   protocol.Position{Line: 0, Character: 10}}, NewText: "import os"},
		{Range: protocol.Range{
			Start: protocol.Position{Line: 1, Character: 0},
			End:   protocol.Position{Line: 1, Character: 3}}, NewText: "x = 1"},
	}
	stale := &lspRequestResult{kind: lspReqFormat, path: path, editSeq: ed.EditSeq() - 1, edits: edits}
	before := ed.Text()
	if act := s.applyFormat(stale); act.Msg == nil {
		t.Error("a format against a changed buffer should report that it was skipped")
	}
	if ed.Text() != before {
		t.Fatalf("a stale format edited the buffer: %q", ed.Text())
	}

	fresh := &lspRequestResult{kind: lspReqFormat, path: path, editSeq: ed.EditSeq(), edits: edits}
	s.applyFormat(fresh)
	if got := ed.Text(); got != "import os\nx = 1\n" {
		t.Fatalf("formatted buffer = %q", got)
	}
	// One undo, not one per edit: a format is a single thing the user did.
	s.editor.Update(sh, keyMsg("ctrl+z"))
	if got := ed.Text(); got != "import  os\nx=1\n" {
		t.Fatalf("a single undo left %q, want the whole format reverted", got)
	}

	if act := s.applyFormat(&lspRequestResult{kind: lspReqFormat, path: path, editSeq: ed.EditSeq()}); act.Msg == nil {
		t.Error("a format with no edits should say the document is already formatted")
	}
}
