package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/brohd11/bubblestack/components/editor"

	"go.lsp.dev/protocol"
)

func outlineTestSymbols() []lspSymbol {
	return []lspSymbol{{
		Name: "Thing", Kind: protocol.SymbolKindClass,
		Range:          protocol.Range{Start: protocol.Position{}, End: protocol.Position{Line: 9}},
		SelectionRange: atLine(0),
		Children: []lspSymbol{{
			Name: "Method", Kind: protocol.SymbolKindMethod, Detail: "func()",
			Range:          protocol.Range{Start: protocol.Position{Line: 2}, End: protocol.Position{Line: 6}},
			SelectionRange: atLine(2),
		}},
	}}
}

func TestOutlinePanelLayoutModesAndMinimal(t *testing.T) {
	s, sh := newHome(t)
	Of(sh).close()
	Of(sh).lsp = nil
	if s.outlineVisible {
		t.Fatal("outline should start hidden")
	}

	s.setOpenDocsTabs(sh, true)
	s.Update(sh, keyMsg("alt+o"))
	if !s.outlineVisible || s.panelSlot(s.docsPane()) != 0 || s.panelSlot(s.outlinePanel) != 1 ||
		s.panelSlot(s.openPanel) != noFocus || s.panelSlot(s.editorPanel) != 3 {
		t.Fatalf("tab outline slots = docs %d outline %d open %d editor %d",
			s.panelSlot(s.docsPane()), s.panelSlot(s.outlinePanel), s.panelSlot(s.openPanel), s.panelSlot(s.editorPanel))
	}

	s.setOpenDocsTabs(sh, false)
	if s.panelSlot(s.docsPane()) != 0 || s.panelSlot(s.openPanel) != 1 ||
		s.panelSlot(s.outlinePanel) != 2 || s.panelSlot(s.editorPanel) != 3 {
		t.Fatalf("list outline slots = docs %d open %d outline %d editor %d",
			s.panelSlot(s.docsPane()), s.panelSlot(s.openPanel), s.panelSlot(s.outlinePanel), s.panelSlot(s.editorPanel))
	}
	if !strings.Contains(s.View(sh), "Outline") {
		t.Fatal("visible outline did not render")
	}

	path := filepath.Join(t.TempDir(), "single.go")
	if err := os.WriteFile(path, []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	minimal, msh := newHomeWith(t, Options{Mode: ModeFile, File: path})
	Of(msh).close()
	Of(msh).lsp = nil
	minimal.Update(msh, keyMsg("alt+o"))
	if minimal.sidebar || !minimal.sideColumnVisible() || minimal.panelSlot(minimal.outlinePanel) != 0 ||
		minimal.panelSlot(minimal.editorPanel) != 1 {
		t.Fatalf("minimal outline layout = sidebar %v outline %d editor %d",
			minimal.sidebar, minimal.panelSlot(minimal.outlinePanel), minimal.panelSlot(minimal.editorPanel))
	}
	minimal.toggleBottom(msh)
	minimal.SetSize(msh, 80, 24)
	if !minimal.bottomVisible || minimal.diagnostics.width != 80 {
		t.Fatal("minimal outline did not compose with the full-width diagnostics panel")
	}
}

func TestOutlineResultTracksCaretFoldsAndJumps(t *testing.T) {
	s, sh := newHome(t)
	Of(sh).close()
	Of(sh).lsp = nil
	path, ed := seedDoc(t, s, sh, "outline.go", "type Thing struct {\n}\nfunc Method() {\n\n}\n\n\n\n\n")
	s.openDoc(sh, path)
	s.toggleOutline(sh)
	s.outlineRequestID = 7
	s.applyOutlineResult(&lspOutlineResult{id: 7, path: path, editSeq: ed.EditSeq(), symbols: outlineTestSymbols()})
	if len(s.outlineNodes) != 1 || len(s.outlineNodes[0].Children) != 1 {
		t.Fatalf("outline nodes = %#v", s.outlineNodes)
	}

	ed.Reveal(editorPosition(3, 0))
	s.syncOutlineCaret()
	if node, _ := s.outlinePanel.Selected(); node.ID != s.outlineNodes[0].Children[0].ID {
		t.Fatalf("caret inside method selected %q", node.ID)
	}

	// Folding the class keeps the caret-follow selection on the deepest visible ancestor.
	s.modular.FocusSlot(s.panelSlot(s.outlinePanel))
	s.outlinePanel.Select(s.outlineNodes[0].ID)
	s.outlinePanel.UpdatePanel(sh, keyMsg("space"))
	s.modular.FocusSlot(s.editorSlot())
	s.syncOutlineCaret()
	if node, _ := s.outlinePanel.Selected(); node.ID != s.outlineNodes[0].ID {
		t.Fatalf("folded method selected %q, want its visible class", node.ID)
	}

	// Outline navigation owns a normal jump, including the ctrl+o return site.
	s.modular.FocusSlot(s.panelSlot(s.outlinePanel))
	s.outlinePanel.UpdatePanel(sh, keyMsg("space"))
	s.outlinePanel.Select(s.outlineNodes[0].Children[0].ID)
	ed.Reveal(editorPosition(0, 0))
	s.outlinePanel.UpdatePanel(sh, keyMsg("enter"))
	if ed.CursorPosition().Line != 2 || !s.editorPanel.Focused() || len(s.jumps) == 0 {
		t.Fatalf("outline jump = caret %+v editor focus %v jumps %d",
			ed.CursorPosition(), s.editorPanel.Focused(), len(s.jumps))
	}
}

func TestOutlinePlaceholderAndStaleResult(t *testing.T) {
	s, sh := newHome(t)
	Of(sh).close()
	Of(sh).lsp = nil
	path, ed := seedDoc(t, s, sh, "outline.go", "package main\n")
	s.openDoc(sh, path)
	s.toggleOutline(sh)
	if view := s.outlinePanel.View(false); !strings.Contains(view, "Language-server support") {
		t.Fatalf("disabled outline placeholder =\n%s", view)
	}

	s.outlineRequestID = 9
	oldSeq := ed.EditSeq()
	ed.SetText("package main\n\n")
	s.applyOutlineResult(&lspOutlineResult{id: 9, path: path, editSeq: oldSeq, symbols: outlineTestSymbols()})
	if len(s.outlineNodes) != 0 {
		t.Fatal("a result for an edited-away generation replaced the outline")
	}
}

func editorPosition(line, column int) editor.Position {
	return editor.Position{Line: line, Column: column}
}
