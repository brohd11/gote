package app

import (
	"github.com/brohd11/bubblestack/components/editor"
	"github.com/brohd11/bubblestack/core"
)

// The semantic overlay: Chroma paints the syntax snapshot and the editor's host layer
// corrects ranges the language server knows better. It lives on the editor, so untouched
// rows follow edits and edited rows show Chroma until a new answer arrives.

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

// applySemanticTokens installs a successful answer on the buffer it describes, active or
// not; an empty answer clears the old overlay.
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
