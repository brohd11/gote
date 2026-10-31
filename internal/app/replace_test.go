package app

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/brohd11/bubblestack/components"
	"github.com/brohd11/bubblestack/core"
	editor "github.com/brohd11/go-text-edit"
)

func TestReplaceThroughRouter(t *testing.T) {
	for _, mode := range []Mode{ModeScan, ModeFile} {
		for _, fromFind := range []bool{false, true} {
			name := "project"
			if mode == ModeFile {
				name = "single-file"
			}
			if fromFind {
				name += "/from-find"
			} else {
				name += "/direct"
			}
			t.Run(name, func(t *testing.T) {
				root := t.TempDir()
				path := writeSearchFile(t, root, "notes.md", "foo foo foo")
				model, s, sh := newHomeRouterCfg(t, testConfig(), Options{Mode: mode, Dir: root, File: path})
				t.Cleanup(Of(sh).close)
				s.gitGutter = false
				if mode == ModeScan {
					s.openDoc(sh, path)
					s.setSidePreview(true)
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
				if fromFind {
					drive("ctrl+f")
					drive("foo")
					drive("ctrl+r")
				} else {
					drive("ctrl+r")
					drive("foo")
					drive("tab")
				}
				top := model.(core.Router).Top()
				drive("bar")
				drive("enter")
				if s.editor.Text() != "foo foo foo" || s.editor.CursorPosition() != (editor.Position{}) {
					t.Fatal("first Enter should only select the match at the caret")
				}
				drive("enter")
				if s.editor.Text() != "bar foo foo" || s.editor.CursorPosition() != (editor.Position{Column: 4}) {
					t.Fatalf("replacement/advance failed: %q %+v", s.editor.Text(), s.editor.CursorPosition())
				}
				if model.(core.Router).Top() != top || !s.editorPanel.Focused() || s.currentID != id {
					t.Fatal("replacement changed overlay, pane, or document focus")
				}
				if mode == ModeScan && s.previewSrc != "bar foo foo" {
					t.Fatalf("preview stayed stale under overlay: %q", s.previewSrc)
				}
				for _, chord := range []string{"down", "up", "alt+=", "alt+-", "f3", "shift+f3"} {
					drive(chord)
				}
				if s.editor.CursorPosition() != (editor.Position{Column: 4}) {
					t.Fatal("replace field lost match navigation")
				}
				drive("esc")
				if model.(core.Router).Top() != s || !strings.Contains(stripANSI(s.editor.View(sh)), "replace: bar") {
					t.Fatal("Escape did not retain both rows and return home")
				}
				// Locate the retained row in the rendered terminal, then verify that
				// clicking it reopens Replace rather than changing the Find query.
				clicked := false
				for y, line := range strings.Split(view(model), "\n") {
					if x := strings.Index(line, "replace: bar"); x >= 0 {
						next, cmd := model.Update(tea.MouseClickMsg{X: x + 9, Y: y, Button: tea.MouseLeft})
						model = pumpModel(next, cmd)
						clicked = true
						break
					}
				}
				if !clicked {
					t.Fatal("retained Replace row was not rendered")
				}
				drive("!")
				if !strings.Contains(stripANSI(model.(core.Router).Top().View(sh)), "replace: bar!") {
					t.Fatal("retained row click did not focus Replace")
				}
				drive("backspace")
				drive("esc")
				drive("ctrl+f")
				find, ok := model.(core.Router).Top().(*components.LineEditScreen)
				if !ok || find.Value() != "foo" {
					t.Fatal("Ctrl+F did not convert to Find with the retained query")
				}
				drive("ctrl+r")
				if !strings.Contains(stripANSI(model.(core.Router).Top().View(sh)), "replace: bar") {
					t.Fatal("Find conversion lost replacement value")
				}
				drive("ctrl+f")
				if _, ok := model.(core.Router).Top().(*components.LineEditScreen); !ok {
					t.Fatal("Ctrl+F inside Replace did not switch back to Find")
				}
				drive("esc")
				if model.(core.Router).Top() != s {
					t.Fatal("conversion stacked overlays instead of replacing them")
				}
				drive("ctrl+z")
				if s.editor.Text() != "foo foo foo" || s.editor.Dirty() {
					t.Fatal("one undo did not restore the clean buffer")
				}
				drive("ctrl+y")
				if s.editor.Text() != "bar foo foo" {
					t.Fatal("redo did not restore replacement")
				}
			})
		}
	}
}

func TestReplaceFromFindPreservesSelectedCandidate(t *testing.T) {
	path := writeSearchFile(t, t.TempDir(), "notes.txt", "foo foo")
	model, s, sh := newHomeRouterCfg(t, testConfig(), Options{Mode: ModeFile, File: path})
	t.Cleanup(Of(sh).close)
	s.gitGutter = false
	s.editor.SetText("foo foo")
	for _, chord := range []string{"ctrl+f", "foo", "down", "ctrl+r", "bar", "enter"} {
		next, cmd := model.Update(keyMsg(chord))
		model = pumpModel(next, cmd)
	}
	if s.editor.Text() != "foo bar" || s.editor.CursorPosition() != (editor.Position{}) {
		t.Fatal("Find conversion lost the selected candidate or failed to wrap")
	}
}

func TestReplacePromotesScratchWhilePanelFocused(t *testing.T) {
	model, s, sh := newHomeRouterCfg(t, testConfig(), Options{Mode: ModeScan, Dir: t.TempDir()})
	t.Cleanup(Of(sh).close)
	s.gitGutter = false
	s.editor.SetText("foo") // remains clean and untracked until the replacement
	s.modular.FocusSlot(s.editorSlot())
	if _, tracked := Of(sh).buffer(s.currentID); tracked {
		t.Fatal("fixture should begin with an untracked scratch")
	}
	for _, chord := range []string{"ctrl+r", "foo", "tab", "bar", "enter", "enter"} {
		next, cmd := model.Update(keyMsg(chord))
		model = pumpModel(next, cmd)
	}
	if _, tracked := Of(sh).buffer(s.currentID); !tracked || model.(core.Router).Top() == s || s.editor.Text() != "bar" {
		t.Fatal("replacement did not promote scratch while the overlay stayed focused")
	}
}

func TestReplaceReconcilesLSPWhilePanelFocused(t *testing.T) {
	path := writeSearchFile(t, t.TempDir(), "main.py", "foo foo")
	server := newRecordingLSPServer()
	address := startRecordingTCPServer(t, server)
	cfg := testConfig()
	cfg.Lang.Servers["python"] = LanguageServerConfig{Address: address}
	model, s, sh := newHomeRouterCfg(t, cfg, Options{Mode: ModeFile, File: path})
	c := Of(sh)
	t.Cleanup(c.close)
	s.gitGutter = false
	s.editor.SetText("foo foo")
	c.Config.AutoLSP = true
	c.lsp = newLSPManager(c.Config, "test")
	c.lsp.changeDebounce = 10 * time.Millisecond
	// Consume server calls directly; the test pump must not wait on the UI's
	// long-lived LSP subscription while checking the replacement overlay.
	s.lspWaiting = true
	c.lsp.Reconcile(c)
	waitLSPCall(t, server.calls, "initialize")
	waitLSPCall(t, server.calls, "open")
	for _, chord := range []string{"ctrl+r", "foo", "tab", "bar", "enter", "enter"} {
		next, cmd := model.Update(keyMsg(chord))
		model = pumpModel(next, cmd)
	}
	changed := waitLSPCall(t, server.calls, "change")
	if changed.text != "bar foo" || model.(core.Router).Top() == s {
		t.Fatalf("LSP did not receive replacement under the overlay: %q", changed.text)
	}
}
