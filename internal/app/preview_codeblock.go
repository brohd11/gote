package app

import (
	"strings"

	"github.com/brohd11/bubblestack/components"
	"github.com/brohd11/bubblestack/core"

	"github.com/alecthomas/chroma/v2/lexers"
)

// Syntax highlighting for fenced code blocks in the markdown preview and docs, via
// components.CodeBlockRenderer (unset apps get plain blocks). It reuses the editor's
// chromaHighlighter: the block is hard-wrapped first (only inserting newlines), then
// highlighted row by row, which never cuts through ANSI.

// init claims the language-neutral fenced-code renderer seam for the process.
func init() {
	components.CodeBlockRenderer = chromaCodeBlock
}

// chromaCodeBlock renders one fenced block (lang from the info string, maybe "") folded
// to width; the reader adds the indent.
func chromaCodeBlock(lang string, code []string, width int) []string {
	var rows []string
	for _, line := range code {
		rows = append(rows, strings.Split(core.HardWrap(line, width), "\n")...)
	}

	lexer := lexers.Get(lang)
	if lexer == nil {
		// No language (or none chroma knows): the reader's own muted look, so an
		// unhighlightable block reads exactly as it did before highlighting existed.
		muted := core.MutedStyle()
		for i, row := range rows {
			rows[i] = muted.Render(row)
		}
		return rows
	}

	h := chromaHighlighterFactory(lexer)()
	h.Parse(strings.Join(rows, "\n"))
	for i := range rows {
		spans := h.HighlightLine(i)
		if spans == nil {
			continue // an unstyled row renders as its raw text
		}
		var b strings.Builder
		for _, sp := range spans {
			// Every run goes through Render, which also expands tabs; this path has no expandLine
			// pass.
			style, _ := sp.SpanStyle()
			b.WriteString(style.Render(sp.Text))
		}
		rows[i] = b.String()
	}
	return rows
}
