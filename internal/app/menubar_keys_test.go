package app

import (
	"path/filepath"
	"testing"

	"github.com/brohd11/bubblestack/components"
	"github.com/brohd11/bubblestack/core"
)

// topMenuLabels are the labels of the menu on top, or nil when none is.
func topMenuLabels(r core.Router) []string {
	if m, ok := r.Top().(*components.MenuScreen); ok {
		return menuLabels(m.Items())
	}
	return nil
}

// TestMenuBarKeyboardPath is the user's example: alt+v, f (File view), ↓, enter — the
// sidebar changes view without the pointer.
func TestMenuBarKeyboardPath(t *testing.T) {
	model, s, sh := newHomeRouter(t, Options{Mode: ModeScan, Dir: scanTree(t)})
	defer Of(sh).close()
	for _, k := range []string{"alt+v", "f", "down", "enter"} {
		model, _ = model.Update(keyMsg(k))
	}
	if model.(core.Router).Top() != s || s.fileView != fileViewFolder {
		t.Fatalf("alt+v f ↓ enter should pick the folder view and close the menus: view %v top %T",
			s.fileView, model.(core.Router).Top())
	}
	// Straight to a row by its letter: alt+v f g.
	for _, k := range []string{"alt+v", "f", "g"} {
		model, _ = model.Update(keyMsg(k))
	}
	if model.(core.Router).Top() != s || s.fileView != fileViewGrouped {
		t.Fatalf("alt+v f g should pick the grouped view: %v", s.fileView)
	}
}

// TestMenuBarChords: each menu's alt chord opens it; with one open, another chord or
// ←/→ switches, wrapping at the ends.
func TestMenuBarChords(t *testing.T) {
	model, s, sh := newHomeRouter(t, Options{Mode: ModeScan, Dir: scanTree(t)})
	defer Of(sh).close()
	s.modular.FocusSlot(s.panelSlot(s.docsPane())) // alt+f is the editor's while it types
	first := func() string {
		labels := topMenuLabels(model.(core.Router))
		if len(labels) == 0 {
			return ""
		}
		return labels[0]
	}
	for chord, want := range map[string]string{"alt+f": "Vaults", "alt+e": "Copy", "alt+v": "Preview", "alt+o": "LSP"} {
		model, _ = model.Update(keyMsg(chord))
		if got := first(); got != want {
			t.Fatalf("%s should open the menu starting %q, got %q", chord, want, got)
		}
		model, _ = model.Update(keyMsg("esc"))
	}

	// → switches only from a row that opens nothing (on a submenu row it opens the
	// submenu); ← always switches from a top menu.
	model, _ = model.Update(keyMsg("alt+e")) // Edit: its selected row, Paste, is plain
	for _, step := range []struct{ key, want string }{
		{"right", "Preview"}, // Edit → View
		{"alt+o", "LSP"},     // View → Options by its chord
		{"left", "Preview"},  // Options → View
		{"left", "Copy"},     // View → Edit
		{"left", "Vaults"},   // Edit → File
		{"left", "LSP"},      // File → Options, wrapping
		{"alt+f", "Vaults"},  // → File by its chord
	} {
		model, _ = model.Update(keyMsg(step.key))
		if got := first(); got != step.want {
			t.Fatalf("%s should switch to the menu starting %q, got %q", step.key, step.want, got)
		}
	}
	model, _ = model.Update(keyMsg("esc"))
	if model.(core.Router).Top() != s {
		t.Fatal("esc should close the menu bar's menu")
	}
}

// TestMenuBarLeavesAltFToTheEditor: alt+f is the terminal's Option+→, so a focused editor
// keeps it for word-forward; the other chords still open their menus from the editor.
func TestMenuBarLeavesAltFToTheEditor(t *testing.T) {
	model, s, sh := newHomeRouter(t, Options{})
	defer Of(sh).close()
	s.openDoc(sh, filepath.Join(t.TempDir(), "a.txt"))
	model, _ = model.Update(keyMsg("alpha beta"))
	model, _ = model.Update(keyMsg("alt+b"))
	back := s.editor.CursorPosition()
	model, _ = model.Update(keyMsg("alt+f"))
	if model.(core.Router).Top() != s || s.editor.CursorPosition() == back {
		t.Fatalf("alt+f in the editor should move a word, not open File (top %T)", model.(core.Router).Top())
	}
	model, _ = model.Update(keyMsg("alt+e"))
	if labels := topMenuLabels(model.(core.Router)); len(labels) == 0 || labels[0] != "Copy" {
		t.Fatalf("alt+e should open Edit from the editor, got %v", labels)
	}
}

// TestMenuAcceleratorsUnique: within each menu, no two rows share a letter — a duplicate
// would make the second row unreachable by key.
func TestMenuAcceleratorsUnique(t *testing.T) {
	_, s, sh := newHomeRouter(t, Options{})
	defer Of(sh).close()
	menus := map[string][]components.MenuItem{
		"file": s.fileMenuItems(), "edit": s.editMenuItems(), "view": s.viewMenuItems(sh),
		"options": s.optionsMenuItems(sh), "preview": s.previewMenuItems(),
		"file view": s.fileViewMenuItems(), "tab groups": s.tabGroupMenuItems(),
		"lsp": s.lspMenuItems(sh), "vaults": s.vaultMenuItems(),
	}
	for name, items := range menus {
		seen := map[rune]string{}
		for _, it := range items {
			if it.Key == 0 {
				continue
			}
			if prev, dup := seen[it.Key]; dup {
				t.Errorf("%s: %q and %q share accelerator %q", name, prev, it.Label, it.Key)
			}
			seen[it.Key] = it.Label
		}
	}
}
