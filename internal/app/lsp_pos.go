package app

import (
	"unicode/utf16"

	"github.com/brohd11/bubblestack/components/editor"

	"go.lsp.dev/protocol"
)

// Conversion between the editor's rune columns and LSP UTF-16 code units (the encoding
// negotiated at initialize).

func editorPositionToLSP(ed *editor.Screen, position editor.Position) (protocol.Position, bool) {
	line, ok := ed.LineText(position.Line)
	if !ok {
		return protocol.Position{}, false
	}
	runes := []rune(line)
	if position.Column < 0 || position.Column > len(runes) {
		return protocol.Position{}, false
	}
	return protocol.Position{Line: uint32(position.Line), Character: uint32(len(utf16.Encode(runes[:position.Column])))}, true
}

func lspPositionToEditor(ed *editor.Screen, position protocol.Position) (editor.Position, bool) {
	line, ok := ed.LineText(int(position.Line))
	if !ok {
		return editor.Position{}, false
	}
	target, units := int(position.Character), 0
	for i, r := range []rune(line) {
		if units == target {
			return editor.Position{Line: int(position.Line), Column: i}, true
		}
		width := 1
		if utf16.RuneLen(r) == 2 {
			width = 2
		}
		if units+width > target {
			return editor.Position{}, false
		}
		units += width
	}
	if units == target {
		return editor.Position{Line: int(position.Line), Column: len([]rune(line))}, true
	}
	return editor.Position{}, false
}

// lspRangeToEditor converts both ends of an LSP range against a live buffer.
func lspRangeToEditor(ed *editor.Screen, r protocol.Range) (editor.Range, bool) {
	start, ok := lspPositionToEditor(ed, r.Start)
	if !ok {
		return editor.Range{}, false
	}
	end, ok := lspPositionToEditor(ed, r.End)
	if !ok {
		return editor.Range{}, false
	}
	return editor.Range{Start: start, End: end}, true
}

// lspPositionToEditorClamped converts for a jump, clamping to the nearest real position:
// the target may be in an unopened or edited file, or one past the last line.
func lspPositionToEditorClamped(ed *editor.Screen, position protocol.Position) editor.Position {
	if exact, ok := lspPositionToEditor(ed, position); ok {
		return exact
	}
	line := int(position.Line)
	text, ok := ed.LineText(line)
	if !ok {
		// Past the end of the buffer: the line itself is the best guess, and
		// EditorScreen.Reveal rejects it if the buffer is still loading.
		return editor.Position{Line: line}
	}
	return editor.Position{Line: line, Column: len([]rune(text))}
}
