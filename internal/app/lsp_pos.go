package app

import (
	"unicode/utf16"

	"github.com/brohd11/bubblestack/components"

	"go.lsp.dev/protocol"
)

// Position conversion between the editor's rune columns and LSP's UTF-16 code units.
// UTF-16 is what gote negotiates at initialize (PositionEncodingKindUTF16), and every
// feature that names a point in a document converts through here — which is why these
// live in a file of their own rather than beside the first caller that needed them.

func editorPositionToLSP(editor *components.EditorScreen, position components.EditorPosition) (protocol.Position, bool) {
	line, ok := editor.LineText(position.Line)
	if !ok {
		return protocol.Position{}, false
	}
	runes := []rune(line)
	if position.Column < 0 || position.Column > len(runes) {
		return protocol.Position{}, false
	}
	return protocol.Position{Line: uint32(position.Line), Character: uint32(len(utf16.Encode(runes[:position.Column])))}, true
}

func lspPositionToEditor(editor *components.EditorScreen, position protocol.Position) (components.EditorPosition, bool) {
	line, ok := editor.LineText(int(position.Line))
	if !ok {
		return components.EditorPosition{}, false
	}
	target, units := int(position.Character), 0
	for i, r := range []rune(line) {
		if units == target {
			return components.EditorPosition{Line: int(position.Line), Column: i}, true
		}
		width := 1
		if utf16.RuneLen(r) == 2 {
			width = 2
		}
		if units+width > target {
			return components.EditorPosition{}, false
		}
		units += width
	}
	if units == target {
		return components.EditorPosition{Line: int(position.Line), Column: len([]rune(line))}, true
	}
	return components.EditorPosition{}, false
}

// lspRangeToEditor converts both ends of an LSP range against a live buffer.
func lspRangeToEditor(editor *components.EditorScreen, r protocol.Range) (components.EditorRange, bool) {
	start, ok := lspPositionToEditor(editor, r.Start)
	if !ok {
		return components.EditorRange{}, false
	}
	end, ok := lspPositionToEditor(editor, r.End)
	if !ok {
		return components.EditorRange{}, false
	}
	return components.EditorRange{Start: start, End: end}, true
}

// lspPositionToEditorClamped is the conversion a JUMP needs. A jump target names a point
// in a file the editor may not have open yet, may have edited since the server last saw
// it, or (for a whole-symbol range) that legitimately points one past the last line. An
// exact conversion failing there should still land the caret somewhere sensible rather
// than refusing to navigate, so this clamps to the nearest real position instead.
func lspPositionToEditorClamped(editor *components.EditorScreen, position protocol.Position) components.EditorPosition {
	if exact, ok := lspPositionToEditor(editor, position); ok {
		return exact
	}
	line := int(position.Line)
	text, ok := editor.LineText(line)
	if !ok {
		// Past the end of the buffer: the line itself is the best guess, and
		// EditorScreen.Reveal rejects it if the buffer is still loading.
		return components.EditorPosition{Line: line}
	}
	return components.EditorPosition{Line: line, Column: len([]rune(text))}
}
