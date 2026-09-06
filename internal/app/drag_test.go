package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/brohd11/bubblestack/components/editor"
	"github.com/brohd11/bubblestack/core"
)

// A visible editor receives broadcasts through both homeScreen's pane and Ctx's
// retained-buffer registry. Exercise the real router: a screen-only tick test misses
// the duplicate delivery that used to double the number of timers every 50ms.
func TestHomeDragScrollHasOneTimer(t *testing.T) {
	doc := strings.Repeat("setting: value\n", 76) + "last: value"
	path := filepath.Join(t.TempDir(), "config.yml")
	if err := os.WriteFile(path, []byte(doc), 0600); err != nil {
		t.Fatal(err)
	}
	model, s, _ := newHomeRouter(t, Options{Mode: ModeFile, File: path})
	s.gitGutter = false // keep unrelated baseline IO out of the timer count
	s.editor.SetText(doc)
	s.semanticPath, s.semanticSeq = s.currentPath, s.editor.EditSeq()
	model.View() // publish the pane's mouse rectangle
	model, _ = model.Update(tea.MouseClickMsg{X: 20, Y: 5, Button: tea.MouseLeft})
	model, cmd := model.Update(tea.MouseMotionMsg{X: 20, Y: 35, Button: tea.MouseLeft})
	for frame := range 6 {
		messages := dragTimerMessages(cmd)
		if len(messages) != 1 {
			t.Fatalf("frame %d produced %d timers, want one", frame, len(messages))
		}
		model, cmd = model.Update(messages[0])
		model.View()
	}
	// Recover with a key when the release was lost outside the terminal. Queued
	// timer messages must neither rearm nor change the buffer after deletion.
	stale := dragTimerMessages(cmd)
	model, _ = model.Update(keyMsg("backspace"))
	after := s.editor.Text()
	if after == doc {
		t.Fatal("backspace did not delete the multiline drag selection")
	}
	for _, msg := range stale {
		model, cmd = model.Update(msg)
		if cmd != nil {
			t.Fatal("a stale drag tick rearmed after backspace")
		}
	}
	if s.editor.Text() != after {
		t.Fatal("a stale drag tick changed the deleted buffer")
	}
	model, _ = model.Update(keyMsg("ctrl+z"))
	if s.editor.Text() != doc {
		t.Fatal("undo did not restore the multiline deletion in one step")
	}
}

// Execute one generation only, flattening Bubble Tea's command batches without
// feeding the resulting timers back until the caller has counted them.
func dragTimerMessages(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	msg := cmd()
	if batch, ok := msg.(tea.BatchMsg); ok {
		var out []tea.Msg
		for _, child := range batch {
			out = append(out, dragTimerMessages(child)...)
		}
		return out
	}
	if msg == nil {
		return nil
	}
	return []tea.Msg{msg}
}

func BenchmarkHomeMultilineBackspace(b *testing.B) {
	for _, wrap := range []bool{false, true} {
		name := "unwrapped"
		if wrap {
			name = "wrapped"
		}
		b.Run(name, func(b *testing.B) {
			doc := strings.Repeat("setting: [first, second, third]\n", 76) + "last: value"
			path := filepath.Join(b.TempDir(), "config.yml")
			if err := os.WriteFile(path, []byte(doc), 0600); err != nil {
				b.Fatal(err)
			}
			c := New("test", testConfig(), Options{Mode: ModeFile, File: path})
			c.close()
			c.lsp = nil // no external server work in a UI benchmark
			sh := core.NewShared(c)
			r := core.NewRouter(sh, []core.TabEntry{{Title: "Editor", New: func(sh *core.Shared) core.Screen {
				return NewHomeScreen(sh)
			}}})
			s := r.Top().(*homeScreen)
			s.gitGutter = false
			model, _ := r.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
			model = pumpModel(model, r.Init()) // load and settle the real YAML highlighter
			if s.editor.WrapMode() != wrap {
				s.editor.ToggleWrap()
			}
			model.View()
			selection := editor.Range{Start: editor.Position{Line: 20}, End: editor.Position{Line: 40}}
			b.ReportAllocs()
			for b.Loop() {
				s.editor.SelectRange(selection)
				model, _ = model.Update(keyMsg("backspace"))
				model.View()
				model, _ = model.Update(keyMsg("ctrl+z"))
			}
		})
	}
}
