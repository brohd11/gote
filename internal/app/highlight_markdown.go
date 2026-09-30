package app

import (
	"sort"
	"strings"
	"unicode/utf8"

	"charm.land/lipgloss/v2"
	"github.com/brohd11/bubblestack/components/editor"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/text"
)

// The markdown palette, resolved from Config.SyntaxColors like the source palette, so
// shared slots match. Bold, italic and underline are structure and not configurable.
var (
	mdHeadingStyle  lipgloss.Style
	mdEmphasisStyle lipgloss.Style
	mdStrongStyle   lipgloss.Style
	mdCodeStyle     lipgloss.Style
	mdQuoteStyle    lipgloss.Style
	mdLinkStyle     lipgloss.Style
	mdListStyle     lipgloss.Style
)

// mdPalette is the palette the styles above came from; live preview derives its own
// styles from it per parse (live_markdown.go).
var mdPalette syntaxPalette

// mdStyle IDs index mdStyles (0 is unstyled), so grouping compares IDs rather than
// lipgloss.Style values.
const (
	mdStyleNone = iota
	mdStyleHeading
	mdStyleEmphasis
	mdStyleStrong
	mdStyleCode
	mdStyleQuote
	mdStyleLink
	mdStyleList

	// Live preview only, resolved through liveStyles rather than mdStyles.
	mdStyleH1
	mdStyleH2
	mdStyleH3
	mdStyleCodeSpan
	mdStyleLiveStrong
	mdStyleLiveEm
)

// Pointers, freshly allocated on every rebuild: see chromaStyles for why a span holds a
// style by reference and why the table is replaced rather than written through.
var mdStyles []*lipgloss.Style

// applyMarkdownPalette rebuilds the styles from p, and mdStyles with them.
func applyMarkdownPalette(p syntaxPalette) {
	mdHeadingStyle = lipgloss.NewStyle().Bold(true).Foreground(p.mdHeading)
	mdEmphasisStyle = lipgloss.NewStyle().Italic(true).Foreground(p.mdEmphasis)
	mdStrongStyle = lipgloss.NewStyle().Bold(true).Foreground(p.mdStrong)
	mdCodeStyle = lipgloss.NewStyle().Foreground(p.mdCode)
	mdQuoteStyle = lipgloss.NewStyle().Foreground(p.mdQuote)
	mdLinkStyle = lipgloss.NewStyle().Foreground(p.mdLink).Underline(true)
	mdListStyle = lipgloss.NewStyle().Foreground(p.mdList).Bold(true)
	mdPalette = p

	mdStyles = []*lipgloss.Style{
		mdStyleNone:     nil, // the unstyled run
		mdStyleHeading:  styleRef(mdHeadingStyle),
		mdStyleEmphasis: styleRef(mdEmphasisStyle),
		mdStyleStrong:   styleRef(mdStrongStyle),
		mdStyleCode:     styleRef(mdCodeStyle),
		mdStyleQuote:    styleRef(mdQuoteStyle),
		mdStyleLink:     styleRef(mdLinkStyle),
		mdStyleList:     styleRef(mdListStyle),
	}
}

// mdInterval is a styled rune-column range [lo, hi) on one line. prio resolves overlaps:
// inline (1) paints over block (0), so `code` in a heading is code-colored.
type mdInterval struct {
	lo, hi, id, prio int
}

// markdownHighlighter walks the goldmark AST once into per-line intervals; HighlightLine
// groups a row's intervals into spans covering the whole line.
type markdownHighlighter struct {
	src       []byte          // the parsed document
	lines     []string        // src split on '\n' (no newline runes)
	lineStart []int           // byte offset of each line's first byte
	intervals [][]mdInterval  // per line, in discovery order
	spans     [][]editor.Span // the baked answer; nil per unstyled line
	restart   []int           // nearest block opener usable for a preview parse

	// Live preview (live_markdown.go): per-line markup ops, live-only style intervals,
	// and the baked display with its rune → source-column map. nil rows render as source.
	ops           [][]liveOp
	liveIntervals [][]mdInterval
	live          [][]editor.Span
	liveCols      [][]int
	liveStyles    map[int]*lipgloss.Style    // live-only styles for this parse (newLiveStyles)
	pairStyles    map[[2]int]*lipgloss.Style // resolved (block, inline) pairs
	fills         []liveFill                 // rows drawn as a rule to the render width
	glyphs        [][]editor.Glyph           // bullets kept while a row shows its source
}

var _ editor.HighlightRestartProvider = (*markdownHighlighter)(nil)

// newMarkdownHighlighter highlights CommonMark: headings, emphasis, code (spans and
// blocks), blockquotes, links, and list markers (not the item text).
func newMarkdownHighlighter() editor.Highlighter {
	return &markdownHighlighter{}
}

// Parse runs goldmark and bakes the spans. Multi-line constructs come from the AST, so no
// open/close state is tracked here.
func (m *markdownHighlighter) Parse(doc string) {
	m.src = []byte(doc)
	m.lines = strings.Split(doc, "\n")
	m.lineStart = make([]int, len(m.lines))
	off := 0
	for i, l := range m.lines {
		m.lineStart[i] = off
		off += len(l) + 1 // the '\n' the split dropped
	}
	m.intervals = make([][]mdInterval, len(m.lines))
	m.restart = make([]int, len(m.lines))
	for row := range m.restart {
		m.restart[row] = row
	}

	m.liveIntervals = make([][]mdInterval, len(m.lines))

	root := goldmark.New(goldmark.WithExtensions(extension.Table)).Parser().Parse(text.NewReader(m.src))
	ast.Walk(root, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch v := n.(type) {
		case *ast.Heading:
			// The heading's line segments start past the "# " marker; the block
			// style paints the WHOLE row, marker included.
			for i := 0; i < v.Lines().Len(); i++ {
				r := m.rowOf(v.Lines().At(i).Start)
				m.addBlock(r, r, mdStyleHeading)
			}
		case *ast.FencedCodeBlock:
			first, last := m.rowOf(v.Pos()), m.fencedLastRow(v)
			m.setRestart(first, last, first)
			m.addBlock(first, last, mdStyleCode)
		case *ast.CodeBlock:
			if ls := v.Lines(); ls.Len() > 0 {
				first, last := m.rowOf(ls.At(0).Start), m.rowOf(ls.At(ls.Len()-1).Stop-1)
				m.setRestart(first, last, first)
				m.addBlock(first, last, mdStyleCode)
			}
		case *ast.Blockquote:
			// A container: its own Lines() is empty, so the range runs from the
			// opening '>' to the deepest descendant's last line.
			first, last := m.rowOf(v.Pos()), m.lastRow(v)
			m.setRestart(first, last, first)
			m.addBlock(first, last, mdStyleQuote)
		case *ast.ListItem:
			// Only the MARKER is styled, not the item's text — the children keep
			// walking so their own inline constructs still land.
			m.addInline(v.Pos(), m.listMarkerEnd(v.Pos()), mdStyleList)
		case *ast.Emphasis:
			id := mdStyleEmphasis
			if v.Level == 2 {
				id = mdStyleStrong
			}
			start, stop := m.childRange(v)
			m.addInline(start, stop, id)
			return ast.WalkSkipChildren, nil
		case *ast.CodeSpan:
			start, stop := m.childRange(v)
			m.addInline(start, stop, mdStyleCode)
			return ast.WalkSkipChildren, nil
		case *ast.Link:
			start, stop := m.childRange(v)
			m.addInline(start, stop, mdStyleLink)
			return ast.WalkSkipChildren, nil
		case *ast.AutoLink:
			// AutoLink's text node is unexported, but Pos() is the '<' and the
			// visible text sits one byte in: [pos+1, pos+1+len(text)).
			m.addInline(v.Pos()+1, v.Pos()+1+len(v.Text(m.src)), mdStyleLink)
			return ast.WalkSkipChildren, nil
		}
		return ast.WalkContinue, nil
	})
	m.bake()
	m.collectLive(root)
	m.bakeLive()
}

// HighlightLine returns the baked spans for row — covering the line in full —
// or nil when the line carries no styling at all.
func (m *markdownHighlighter) HighlightLine(row int) []editor.Span {
	if row < 0 || row >= len(m.spans) {
		return nil
	}
	return m.spans[row]
}

func (m *markdownHighlighter) HighlightRestartLine(row int) int {
	if row < 0 || row >= len(m.restart) {
		return row
	}
	return m.restart[row]
}

func (m *markdownHighlighter) setRestart(rowA, rowB, restart int) {
	rowA, rowB = max(rowA, 0), min(rowB, len(m.restart)-1)
	for row := rowA; row <= rowB; row++ {
		if restart < m.restart[row] {
			m.restart[row] = restart
		}
	}
}

// rowOf is the line index containing byte offset off, via the line-start table.
func (m *markdownHighlighter) rowOf(off int) int {
	r := sort.Search(len(m.lineStart), func(i int) bool { return m.lineStart[i] > off }) - 1
	if r < 0 {
		return 0
	}
	if r >= len(m.lineStart) {
		return len(m.lineStart) - 1
	}
	return r
}

// runeCol converts a byte offset on row to a rune column, clamped to the line (a trailing
// '\n' never counts).
func (m *markdownHighlighter) runeCol(row, off int) int {
	start := m.lineStart[row]
	end := len(m.src)
	if row+1 < len(m.lineStart) {
		end = m.lineStart[row+1] - 1 // drop the '\n'
	}
	if off < start {
		off = start
	}
	if off > end {
		off = end
	}
	return utf8.RuneCount(m.src[start:off])
}

// addBlock styles every row in [rowA, rowB] in full (clamped to the buffer).
func (m *markdownHighlighter) addBlock(rowA, rowB, id int) {
	if rowB >= len(m.lines) {
		rowB = len(m.lines) - 1
	}
	for r := rowA; r <= rowB; r++ {
		m.intervals[r] = append(m.intervals[r], mdInterval{lo: 0, hi: utf8.RuneCountInString(m.lines[r]), id: id})
	}
}

// addInline styles [start, stop) at inline priority, split per row for multi-line
// emphasis.
func (m *markdownHighlighter) addInline(start, stop int, id int) {
	if stop <= start {
		return
	}
	rowA, rowB := m.rowOf(start), m.rowOf(stop-1)
	for r := rowA; r <= rowB; r++ {
		lo, hi := 0, utf8.RuneCountInString(m.lines[r])
		if r == rowA {
			lo = m.runeCol(r, start)
		}
		if r == rowB {
			hi = m.runeCol(r, stop)
		}
		if hi > lo {
			m.intervals[r] = append(m.intervals[r], mdInterval{lo: lo, hi: hi, id: id, prio: 1})
		}
	}
}

// listMarkerEnd is the offset just past the list marker at pos (a bullet, or digits plus
// "." or ")"); ListItem.Pos() is always on the marker. Anything else returns pos, an empty
// range.
func (m *markdownHighlighter) listMarkerEnd(pos int) int {
	if pos < 0 || pos >= len(m.src) {
		return pos
	}
	switch m.src[pos] {
	case '-', '+', '*':
		return pos + 1
	}
	i := pos
	for i < len(m.src) && m.src[i] >= '0' && m.src[i] <= '9' {
		i++
	}
	if i > pos && i < len(m.src) && (m.src[i] == '.' || m.src[i] == ')') {
		return i + 1
	}
	return pos
}

// childRange spans n's descendant Text segments: the visible text without delimiters.
func (m *markdownHighlighter) childRange(n ast.Node) (int, int) {
	start, stop := -1, -1
	ast.Walk(n, func(c ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		if t, ok := c.(*ast.Text); ok {
			if start < 0 {
				start = t.Segment.Start
			}
			stop = t.Segment.Stop
		}
		return ast.WalkContinue, nil
	})
	if start < 0 {
		return 0, 0
	}
	return start, stop
}

// fencedLastRow is a fenced block's last painted row: its closing fence, or the buffer's
// end when unclosed.
func (m *markdownHighlighter) fencedLastRow(v *ast.FencedCodeBlock) int {
	if ls := v.Lines(); ls.Len() > 0 {
		return m.rowOf(ls.At(ls.Len()-1).Stop-1) + 1
	}
	return m.rowOf(v.Pos()) + 1
}

// fenceCloserRow is the row of v's closing fence, when it has one: the row after the body
// holding a run of the opener's marker (past any quote prefix).
func (m *markdownHighlighter) fenceCloserRow(v *ast.FencedCodeBlock) (int, bool) {
	pos := v.Pos()
	if pos < 0 {
		return 0, false
	}
	open, row := m.rowOf(pos), m.fencedLastRow(v)
	if row <= open || row >= len(m.lines) {
		return 0, false
	}
	marker := "`"
	if i := strings.IndexAny(m.lines[open], "`~"); i >= 0 {
		marker = m.lines[open][i : i+1]
	}
	t := strings.TrimLeft(strings.TrimSpace(m.lines[row]), "> ")
	if !strings.HasPrefix(t, strings.Repeat(marker, 3)) || strings.Trim(t, marker) != "" {
		return 0, false
	}
	return row, true
}

// lastRow is the deepest row a container block reaches, from its descendant blocks
// (inline children are skipped; they panic on Lines()).
func (m *markdownHighlighter) lastRow(n ast.Node) int {
	last := m.rowOf(n.Pos())
	if ls := n.Lines(); ls.Len() > 0 {
		last = m.rowOf(ls.At(ls.Len()-1).Stop - 1)
	}
	if f, ok := n.(*ast.FencedCodeBlock); ok {
		// Lines() stops at the body; the closing fence is the block's too.
		if r, ok := m.fenceCloserRow(f); ok {
			last = max(last, r)
		}
	}
	for c := n.FirstChild(); c != nil; c = c.NextSibling() {
		if c.Type() != ast.TypeBlock {
			continue
		}
		if r := m.lastRow(c); r > last {
			last = r
		}
	}
	return last
}

// bake turns the intervals into per-row runs (block priority first, inline over it);
// unstyled lines stay nil.
func (m *markdownHighlighter) bake() {
	m.spans = make([][]editor.Span, len(m.lines))
	for r, ivs := range m.intervals {
		if len(ivs) == 0 {
			continue
		}
		runes := []rune(m.lines[r])
		ids := make([]int, len(runes))
		styled := false
		for prio := 0; prio <= 1; prio++ {
			for _, iv := range ivs {
				if iv.prio != prio {
					continue
				}
				for c := iv.lo; c < iv.hi && c < len(ids); c++ {
					ids[c] = iv.id
					styled = true
				}
			}
		}
		if !styled {
			// Intervals that styled nothing (an empty range) answer nil, like a
			// line with no intervals at all.
			continue
		}
		var spans []editor.Span
		for i := 0; i < len(runes); {
			j := i + 1
			for j < len(runes) && ids[j] == ids[i] {
				j++
			}
			spans = append(spans, editor.Span{Text: string(runes[i:j]), Style: mdStyles[ids[i]]})
			i = j
		}
		m.spans[r] = spans
	}
}
