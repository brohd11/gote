package app

import (
	"sort"
	"strings"
	"unicode/utf8"

	"charm.land/lipgloss/v2"
	"github.com/brohd11/bubblestack/components"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/text"
)

// The default markdown palette: package-level vars like editorCursorStyle, not
// theme-driven — syntax colors join the theme palette the day one exists.
var (
	mdHeadingStyle  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("1"))
	mdEmphasisStyle = lipgloss.NewStyle().Italic(true).Foreground(lipgloss.Color("2"))
	mdStrongStyle   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("5"))
	mdCodeStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("3"))
	mdQuoteStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	mdLinkStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("4")).Underline(true)
	mdListStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("6")).Bold(true)
)

// mdStyle IDs index mdStyles; 0 is the unstyled run. Intervals carry the ID, so
// span grouping never has to compare lipgloss.Style values (they carry a func
// field, so == does not even compile).
const (
	mdStyleNone = iota
	mdStyleHeading
	mdStyleEmphasis
	mdStyleStrong
	mdStyleCode
	mdStyleQuote
	mdStyleLink
	mdStyleList
)

var mdStyles = []lipgloss.Style{
	{},
	mdHeadingStyle,
	mdEmphasisStyle,
	mdStrongStyle,
	mdCodeStyle,
	mdQuoteStyle,
	mdLinkStyle,
	mdListStyle,
}

// mdInterval is a styled half-open rune-column range [lo, hi) on one line of
// the per-line interval list it lives in. prio resolves overlaps: inline
// intervals (1) paint over block intervals (0) — `code` inside a heading is
// code-colored, not heading-bold.
type mdInterval struct {
	lo, hi, id, prio int
}

// markdownHighlighter is the goldmark-backed Highlighter. Parse walks the AST
// once and flattens every styled construct into per-line rune-column intervals;
// HighlightLine then just groups the intervals of its row into spans. Spans
// always cover the full line (unstyled runs included), so the editor's concat
// invariant holds by construction.
type markdownHighlighter struct {
	src       []byte              // the parsed document
	lines     []string            // src split on '\n' (no newline runes)
	lineStart []int               // byte offset of each line's first byte
	intervals [][]mdInterval      // per line, in discovery order
	spans     [][]components.Span // the baked answer; nil per unstyled line
}

// newMarkdownHighlighter returns a Highlighter for CommonMark markdown, styled
// with the md*Style defaults: headings bold, *em* italic, **strong** bold,
// `code` and code blocks (fenced and indented) in the code color, blockquotes
// gray, links and autolinks underlined blue, and list markers (the "-" or "1.",
// never the item's text) in the list color.
func newMarkdownHighlighter() components.Highlighter {
	return &markdownHighlighter{}
}

// Parse runs the document through goldmark and bakes the per-line spans. The
// AST hands over the multi-line state for free: a fenced code block is one node
// whose line segments span (heh) all its rows, so nothing here tracks
// open/close across lines.
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

	root := goldmark.New().Parser().Parse(text.NewReader(m.src))
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
			m.addBlock(m.rowOf(v.Pos()), m.fencedLastRow(v), mdStyleCode)
		case *ast.CodeBlock:
			if ls := v.Lines(); ls.Len() > 0 {
				m.addBlock(m.rowOf(ls.At(0).Start), m.rowOf(ls.At(ls.Len()-1).Stop-1), mdStyleCode)
			}
		case *ast.Blockquote:
			// A container: its own Lines() is empty, so the range runs from the
			// opening '>' to the deepest descendant's last line.
			m.addBlock(m.rowOf(v.Pos()), m.lastRow(v), mdStyleQuote)
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
}

// HighlightLine returns the baked spans for row — covering the line in full —
// or nil when the line carries no styling at all.
func (m *markdownHighlighter) HighlightLine(row int) []components.Span {
	if row < 0 || row >= len(m.spans) {
		return nil
	}
	return m.spans[row]
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

// runeCol converts a byte offset on row into a rune column — the editor buffer
// is []rune, so runes are the currency, not bytes. The offset is clamped to the
// line's content (a trailing '\n' inside a goldmark segment never counts).
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

// addInline styles the byte range [start, stop) at inline priority. A range
// spanning rows (multi-line emphasis) is split per row, the newline itself
// never landing on a cell.
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

// listMarkerEnd is the byte offset just past the list marker starting at pos —
// one bullet character ("-", "+", "*"), or a run of digits and the delimiter
// closing it ("12."). ListItem.Pos() lands on the marker itself in every shape
// goldmark produces (nested, indented, inside a blockquote), so scanning forward
// from it is the whole extent calculation. Anything else answers pos, which
// addInline discards as an empty range.
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

// childRange is the byte range from the first to the last descendant Text
// segment of n — the visible text of an emphasis/code-span/link, delimiters
// excluded.
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

// fencedLastRow is the last row a fenced code block paints: the closing fence
// line when the block is closed (goldmark's line segments cover the content
// only, and a closed fence immediately follows the last content line), else the
// buffer's end — an unclosed fence styles to EOF.
func (m *markdownHighlighter) fencedLastRow(v *ast.FencedCodeBlock) int {
	if ls := v.Lines(); ls.Len() > 0 {
		return m.rowOf(ls.At(ls.Len()-1).Stop-1) + 1
	}
	return m.rowOf(v.Pos()) + 1
}

// lastRow is the deepest row a container block reaches — the max last-line row
// over its descendant blocks (its own Lines() is empty). Inline children are
// skipped: they never reach past the block holding them, and ast.BaseInline
// panics on Lines() rather than answering empty.
func (m *markdownHighlighter) lastRow(n ast.Node) int {
	last := m.rowOf(n.Pos())
	if ls := n.Lines(); ls.Len() > 0 {
		last = m.rowOf(ls.At(ls.Len()-1).Stop - 1)
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

// bake flattens the intervals into the spans HighlightLine serves: per row a
// per-rune style-ID array (block priority first, inline painted over it),
// grouped into runs — adjacent runs always differ, and unstyled lines stay nil.
func (m *markdownHighlighter) bake() {
	m.spans = make([][]components.Span, len(m.lines))
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
		var spans []components.Span
		for i := 0; i < len(runes); {
			j := i + 1
			for j < len(runes) && ids[j] == ids[i] {
				j++
			}
			spans = append(spans, components.Span{Text: string(runes[i:j]), Style: mdStyles[ids[i]]})
			i = j
		}
		m.spans[r] = spans
	}
}
