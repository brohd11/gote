package app

import (
	"github.com/brohd11/bubblestack/components/editor"
	"github.com/brohd11/bubblestack/core"
)

// The semantic overlay: Chroma paints the immutable syntax snapshot, then the editor's
// positional host layer corrects the ranges a language server understands more precisely.
// The layer belongs to the retained editor rather than to a global cache: its row map can
// follow untouched lines through edits, while edited rows fall back to Chroma until a
// fresh server answer arrives.

// semanticHighlightRanges converts the LSP's UTF-16 columns into the editor's rune-based
// public ranges. One malformed token is dropped without costing the rest of the answer.
func semanticHighlightRanges(ed *editor.Screen, tokens []semanticToken) []editor.HighlightRange {
	out := make([]editor.HighlightRange, 0, len(tokens))
	for _, token := range tokens {
		line, ok := ed.LineText(token.line)
		if !ok {
			continue
		}
		runes := []rune(line)
		start := utf16OffsetToRune(runes, token.startUTF16)
		end := utf16OffsetToRune(runes, token.startUTF16+token.lenUTF16)
		if start < 0 || end < 0 || start >= end || end > len(runes) {
			continue
		}
		style, ok := slotStylePtr(token.slot)
		if !ok {
			continue
		}
		out = append(out, editor.HighlightRange{
			Range: editor.Range{
				Start: editor.Position{Line: token.line, Column: start},
				End:   editor.Position{Line: token.line, Column: end},
			},
			Style: style,
		})
	}
	return out
}

// applySemanticTokens installs a successful answer directly on the retained buffer it
// describes, active or not. SetHighlightOverlay repeats the generation check at
// publication. A successful empty answer is still installed: it clears an older overlay.
func (s *homeScreen) applySemanticTokens(sh *core.Shared, result *lspSemanticResult) {
	if result == nil || result.err != nil || result.path == "" {
		return
	}
	var ed *editor.Screen
	if result.path == s.currentPath {
		ed = s.editor
	}
	if ed == nil {
		if c := Of(sh); c != nil {
			ed, _ = c.Doc(result.path)
		}
	}
	if ed == nil || ed.EditSeq() != result.editSeq {
		return
	}
	ed.SetHighlightOverlay(result.editSeq, semanticHighlightRanges(ed, result.tokens))
}
