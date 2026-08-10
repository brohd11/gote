package app

import (
	"strings"

	"github.com/brohd11/bubblestack/components"
	"github.com/brohd11/bubblestack/core"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/charmbracelet/lipgloss"
)

// Syntax highlighting for the fenced code blocks in gote's markdown preview (and its
// docs pages — the seam is global). components.RenderMarkdown knows nothing about
// languages: it accumulates a block's raw lines and hands them to whatever
// components.CodeBlockRenderer the consumer set, which is chromaCodeBlock here. Apps
// that set nothing get the reader's plain muted blocks, so the highlighting costs the
// framework no dependency.
//
// The renderer is the editor's chromaHighlighter reused off-label. A block is first
// hard-wrapped to the pane's width — HardWrap preserves text, only inserting '\n's —
// so the wrapped rows re-join to the exact block source, and the highlighter's
// token-to-row distribution (built for an editor's lines) works on the wrapped rows
// unchanged. Wrapping styled output would break ANSI at the cuts; wrapping first and
// styling after never does.

// init claims the seam for the process. Like the highlighter registrations in
// highlight_chroma.go, linking the package is the whole setup.
func init() {
	components.CodeBlockRenderer = chromaCodeBlock
}

// chromaCodeBlock renders one fenced block: lang is the fence's info string ("go" in
// "```go", "" when absent), code the raw content lines, width the columns to fold to.
// The returned lines are emitted verbatim by the reader (it adds the indent itself).
func chromaCodeBlock(lang string, code []string, width int) []string {
	var rows []string
	for _, line := range code {
		rows = append(rows, strings.Split(core.HardWrap(line, width), "\n")...)
	}

	lexer := lexers.Get(lang)
	if lexer == nil {
		// No language (or none chroma knows): the reader's own muted look, so an
		// unhighlightable block reads exactly as it did before highlighting existed.
		muted := lipgloss.NewStyle().Foreground(core.MutedColor)
		for i, row := range rows {
			rows[i] = muted.Render(row)
		}
		return rows
	}

	h := &chromaHighlighter{lexer: chroma.Coalesce(lexer)}
	h.Parse(strings.Join(rows, "\n"))
	for i := range rows {
		spans := h.HighlightLine(i)
		if spans == nil {
			continue // an unstyled row renders as its raw text
		}
		var b strings.Builder
		for _, sp := range spans {
			b.WriteString(sp.Style.Render(sp.Text))
		}
		rows[i] = b.String()
	}
	return rows
}
