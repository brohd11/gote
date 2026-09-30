package app

import (
	"fmt"
	"image/color"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/brohd11/bubblestack/components/editor"
	"github.com/brohd11/bubblestack/core"

	"charm.land/lipgloss/v2"

	"github.com/yuin/goldmark/ast"
	extast "github.com/yuin/goldmark/extension/ast"
)

// Live preview: markdownHighlighter as an editor.LineRenderer. Each line keeps its own
// row (nothing re-flows); markup is hidden or swapped for a glyph, and the highlight
// styles carry over. The editor shows the source on the caret's line, so everything here
// is display-only.

var _ editor.LineRenderer = (*markdownHighlighter)(nil)

// liveFill is a row drawn as a rule across the width the editor gives it (a thematic
// break, or a fence line with its label), built per RenderLine call.
type liveFill struct {
	set   bool
	label string // leading text before the rule ("─── go"), or "" for a bare rule
	id    int
}

// liveMinRule is the shortest rule drawn, however narrow the pane.
const liveMinRule = 3

// liveOp replaces the rune range [lo, hi) of one line with text in style id (hiding is
// an empty text). lo == hi inserts.
type liveOp struct {
	lo, hi int
	text   string
	id     int
}

// liveTable is a table's layout inputs, laid out after the walk: cell widths depend on
// the inline ops inside the cells.
type liveTable struct {
	header int        // source row of the header; the delimiter row follows
	rows   []int      // source rows, header first
	cells  [][][2]int // per row, each cell's [lo, hi) rune range; nil cells are missing
	align  []extast.Alignment
}

// RenderLine returns row's rendered spans, or nil when the row renders as its source.
// Fill rows are sized to ctx.Width; everything else was baked by Parse.
func (m *markdownHighlighter) RenderLine(row int, ctx editor.LiveContext) []editor.Span {
	if row < 0 || row >= len(m.live) {
		return nil
	}
	if f := m.fills[row]; f.set {
		text := f.label
		if n := ctx.Width - utf8.RuneCountInString(text); text == "" {
			text = strings.Repeat("─", max(ctx.Width, liveMinRule))
		} else if n > 1 {
			text += " " + strings.Repeat("─", n-1)
		}
		return []editor.Span{{Text: text, Style: m.style(f.id, mdStyleNone)}}
	}
	return m.live[row]
}

// ActiveGlyphs are the list bullets a row keeps while it shows its source.
func (m *markdownHighlighter) ActiveGlyphs(row int) []editor.Glyph {
	if row < 0 || row >= len(m.glyphs) {
		return nil
	}
	return m.glyphs[row]
}

// SourceCol maps a rune index of RenderLine's text back to a source column.
func (m *markdownHighlighter) SourceCol(row, i int) int {
	if row < 0 || row >= len(m.liveCols) {
		return 0
	}
	cols := m.liveCols[row]
	if cols == nil || i >= len(cols) {
		if row < len(m.lines) {
			return utf8.RuneCountInString(m.lines[row])
		}
		return 0
	}
	return cols[max(i, 0)]
}

// addOp records a replacement over the byte range [start, stop), which must sit on one row.
func (m *markdownHighlighter) addOp(start, stop int, text string, id int) {
	if start < 0 || stop < start || start > len(m.src) {
		return
	}
	r := m.rowOf(start)
	m.ops[r] = append(m.ops[r], liveOp{lo: m.runeCol(r, start), hi: m.runeCol(r, stop), text: text, id: id})
}

// collectLive walks the AST for the markup each construct hides or replaces.
func (m *markdownHighlighter) collectLive(root ast.Node) {
	m.ops = make([][]liveOp, len(m.lines))
	m.liveStyles, m.pairStyles = nil, nil
	m.fills = make([]liveFill, len(m.lines))
	m.glyphs = make([][]editor.Glyph, len(m.lines))
	var tables []liveTable
	ast.Walk(root, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch v := n.(type) {
		case *ast.Heading:
			m.liveHeading(v)
		case *ast.ThematicBreak:
			if pos := v.Pos(); pos >= 0 {
				m.fills[m.rowOf(pos)] = liveFill{set: true, id: mdStyleQuote}
			}
		case *ast.FencedCodeBlock:
			m.liveFence(v)
		case *ast.Blockquote:
			if !hasAncestor[*ast.Blockquote](v) {
				m.liveQuote(m.rowOf(v.Pos()), m.lastRow(v))
			}
		case *ast.ListItem:
			m.liveListItem(v)
		case *ast.Emphasis:
			// Not Pos(): nested runs like "***" share one opener position.
			if start := m.inlineStart(v); start >= 0 {
				end := m.inlineEnd(v)
				m.addOp(start, start+v.Level, "", mdStyleNone)
				m.addOp(end-v.Level, end, "", mdStyleNone)
			}
		case *ast.CodeSpan:
			if lo, hi, ok := m.codeSpanFences(v); ok {
				// Each backtick run becomes one cell of the chip's tint, as the
				// previewer pads its code spans.
				m.addOp(lo[0], lo[1], " ", mdStyleCodeSpan)
				m.addOp(hi[0], hi[1], " ", mdStyleCodeSpan)
			}
			return ast.WalkSkipChildren, nil
		case *ast.Link:
			if pos := v.Pos(); pos >= 0 && pos < len(m.src) && m.src[pos] == '[' {
				m.addOp(pos, pos+1, "", mdStyleNone)
				close := m.childrenEnd(v, pos+1)
				m.addOp(close, m.linkTailEnd(close), "", mdStyleNone)
			}
		case *ast.AutoLink:
			if pos := v.Pos(); pos >= 0 && pos < len(m.src) && m.src[pos] == '<' {
				end := pos + 1 + len(v.Text(m.src))
				m.addOp(pos, pos+1, "", mdStyleNone)
				if end < len(m.src) && m.src[end] == '>' {
					m.addOp(end, end+1, "", mdStyleNone)
				}
			}
			return ast.WalkSkipChildren, nil
		case *ast.Image:
			return ast.WalkSkipChildren, nil // alt text and URL both stay visible
		case *extast.Table:
			if t, ok := m.tableLayout(v); ok {
				tables = append(tables, t)
			}
		}
		return ast.WalkContinue, nil
	})
	for _, t := range tables {
		m.liveTableOps(t)
	}
}

func hasAncestor[T ast.Node](n ast.Node) bool {
	for p := n.Parent(); p != nil; p = p.Parent() {
		if _, ok := p.(T); ok {
			return true
		}
	}
	return false
}

// liveHeading hides an ATX heading's "#" run and the spaces after it, plus any closing
// run. Setext headings keep their underline: it is the heading's own rule. Levels 1 and 2
// get their own styles (live only; a raw row keeps the plain heading style), and an h1's
// background is padded by a space each side so it reads as a block.
func (m *markdownHighlighter) liveHeading(v *ast.Heading) {
	if v.Lines().Len() == 0 {
		if pos := v.Pos(); pos >= 0 { // "#" alone: the whole row is markup
			r := m.rowOf(pos)
			m.ops[r] = append(m.ops[r], liveOp{0, utf8.RuneCountInString(m.lines[r]), "", mdStyleNone})
		}
		return
	}
	seg := v.Lines().At(0)
	r := m.rowOf(seg.Start)
	lineStart := m.lineStart[r]
	level := mdStyleH3
	switch v.Level {
	case 1:
		level = mdStyleH1
	case 2:
		level = mdStyleH2
	}
	for i := 0; i < v.Lines().Len(); i++ {
		lr := m.rowOf(v.Lines().At(i).Start)
		m.liveIntervals[lr] = append(m.liveIntervals[lr],
			mdInterval{lo: 0, hi: utf8.RuneCountInString(m.lines[lr]), id: level})
	}
	at := seg.Start
	for at > lineStart && m.src[at-1] == ' ' || at > lineStart && m.src[at-1] == '\t' {
		at--
	}
	hashes := at
	for hashes > lineStart && m.src[hashes-1] == '#' {
		hashes--
	}
	if hashes == at {
		return // setext
	}
	pad := ""
	if level == mdStyleH1 {
		pad = " "
	}
	m.addOp(hashes, seg.Start, pad, level)
	lineEnd := lineStart + len(m.lines[r])
	if seg.Stop < lineEnd {
		m.addOp(seg.Stop, lineEnd, pad, level)
	} else if pad != "" {
		m.addOp(lineEnd, lineEnd, pad, level)
	}
}

// liveFence draws the fence lines as rules, the opener carrying its info string. The
// body renders as source (already code-styled).
func (m *markdownHighlighter) liveFence(v *ast.FencedCodeBlock) {
	pos := v.Pos()
	if pos < 0 {
		return
	}
	open := m.rowOf(pos)
	label := "" // a bare fence draws the same rule as its closer
	if v.Info != nil {
		if info := strings.TrimSpace(string(v.Info.Segment.Value(m.src))); info != "" {
			label = "─── " + info
		}
	}
	m.fills[open] = liveFill{set: true, label: label, id: mdStyleCode}
	if closeRow, ok := m.fenceCloserRow(v); ok {
		m.fills[closeRow] = liveFill{set: true, id: mdStyleCode}
	}
}

// liveQuote turns every '>' in each row's leading quote prefix into a bar.
func (m *markdownHighlighter) liveQuote(first, last int) {
	last = min(last, len(m.lines)-1)
	for r := first; r <= last; r++ {
		for c, ch := range []rune(m.lines[r]) {
			if ch == '>' {
				m.ops[r] = append(m.ops[r], liveOp{c, c + 1, "▌", mdStyleQuote})
			} else if ch != ' ' && ch != '\t' {
				break
			}
		}
	}
}

// liveBullets are the bullet glyphs by list depth.
var liveBullets = []string{"•", "◦", "▪"}

// liveListItem swaps a bullet marker for a depth glyph, and a task box for a check glyph.
func (m *markdownHighlighter) liveListItem(v *ast.ListItem) {
	pos := v.Pos()
	if pos < 0 || pos >= len(m.src) {
		return
	}
	end := m.listMarkerEnd(pos)
	if end == pos+1 { // a bullet, not an ordered marker
		depth := -1
		for p := ast.Node(v); p != nil; p = p.Parent() {
			if _, ok := p.(*ast.List); ok {
				depth++
			}
		}
		bullet := liveBullets[max(depth, 0)%len(liveBullets)]
		m.addOp(pos, end, bullet, mdStyleList)
		r := m.rowOf(pos)
		m.glyphs[r] = append(m.glyphs[r], editor.Glyph{Col: m.runeCol(r, pos), From: rune(m.src[pos]),
			Text: []rune(bullet)[0], Style: m.style(mdStyleList, mdStyleNone)})
	}
	at := end
	for at < len(m.src) && m.src[at] == ' ' {
		at++
	}
	if at+3 <= len(m.src) && (at+3 == len(m.src) || m.src[at+3] == ' ' || m.src[at+3] == '\n') {
		switch string(m.src[at : at+3]) {
		case "[ ]":
			m.addOp(at, at+3, "☐", mdStyleList)
		case "[x]", "[X]":
			m.addOp(at, at+3, "☑", mdStyleList)
		}
	}
}

// inlineStart is the byte offset of an inline node's first source byte, opening markup
// included, or -1 when unknown.
func (m *markdownHighlighter) inlineStart(n ast.Node) int {
	switch v := n.(type) {
	case *ast.Text:
		return v.Segment.Start
	case *ast.Emphasis:
		if c := v.FirstChild(); c != nil {
			if at := m.inlineStart(c); at >= v.Level {
				return at - v.Level
			}
			return -1
		}
	case *ast.CodeSpan:
		if lo, _, ok := m.codeSpanFences(v); ok {
			return lo[0]
		}
	}
	return n.Pos()
}

// inlineEnd is the byte offset just past an inline node's source, closing markup included.
func (m *markdownHighlighter) inlineEnd(n ast.Node) int {
	switch v := n.(type) {
	case *ast.Text:
		return v.Segment.Stop
	case *ast.Emphasis:
		return m.childrenEnd(v, m.inlineStart(v)+v.Level) + v.Level
	case *ast.CodeSpan:
		if _, hi, ok := m.codeSpanFences(v); ok {
			return hi[1]
		}
	case *ast.Link:
		return m.linkTailEnd(m.childrenEnd(v, v.Pos()+1))
	case *ast.AutoLink:
		end := v.Pos() + 1 + len(v.Text(m.src))
		if end < len(m.src) && m.src[end] == '>' {
			end++
		}
		return end
	case *ast.String:
		// Raw strings carry no segment; fall back to the children.
	}
	return m.childrenEnd(n, max(n.Pos(), 0))
}

// childrenEnd is the end of n's last child, or from when n has none.
func (m *markdownHighlighter) childrenEnd(n ast.Node, from int) int {
	if c := n.LastChild(); c != nil {
		return m.inlineEnd(c)
	}
	return from
}

// linkTailEnd scans a link's "](dest)" / "][ref]" / "]" tail from its ']'.
func (m *markdownHighlighter) linkTailEnd(close int) int {
	if close >= len(m.src) || m.src[close] != ']' {
		return close
	}
	at := close + 1
	if at < len(m.src) && m.src[at] == '(' {
		depth := 0
		for i := at; i < len(m.src) && m.src[i] != '\n'; i++ {
			switch m.src[i] {
			case '(':
				depth++
			case ')':
				if depth--; depth == 0 {
					return i + 1
				}
			}
		}
		return at
	}
	if at < len(m.src) && m.src[at] == '[' {
		if i := strings.IndexByte(string(m.src[at:]), ']'); i >= 0 && !strings.Contains(string(m.src[at:at+i]), "\n") {
			return at + i + 1
		}
	}
	return at
}

// codeSpanFences finds a code span's opening and closing backtick runs (each with its
// padding space) around its text.
func (m *markdownHighlighter) codeSpanFences(v *ast.CodeSpan) (lo, hi [2]int, ok bool) {
	first, last := v.FirstChild(), v.LastChild()
	ft, ok1 := first.(*ast.Text)
	lt, ok2 := last.(*ast.Text)
	if !ok1 || !ok2 {
		return lo, hi, false
	}
	start, stop := ft.Segment.Start, lt.Segment.Stop
	a := start
	if a > 0 && m.src[a-1] == ' ' && a > 1 && m.src[a-2] == '`' {
		a--
	}
	b := a
	for b > 0 && m.src[b-1] == '`' {
		b--
	}
	n := a - b
	if n == 0 {
		return lo, hi, false
	}
	c := stop
	if c < len(m.src) && m.src[c] == ' ' && c+1 < len(m.src) && m.src[c+1] == '`' {
		c++
	}
	d := c
	for d < len(m.src) && m.src[d] == '`' && d-c < n {
		d++
	}
	if d-c != n {
		return lo, hi, false
	}
	return [2]int{b, start}, [2]int{stop, d}, true
}

// tableLayout collects a table's rows and cell ranges.
func (m *markdownHighlighter) tableLayout(v *extast.Table) (liveTable, bool) {
	t := liveTable{align: v.Alignments}
	for row := v.FirstChild(); row != nil; row = row.NextSibling() {
		r := -1
		var cells [][2]int
		for c := row.FirstChild(); c != nil; c = c.NextSibling() {
			if c.Lines().Len() == 0 {
				cells = append(cells, [2]int{-1, -1}) // a filler for a short row
				continue
			}
			seg := c.Lines().At(0)
			if r < 0 {
				r = m.rowOf(seg.Start)
			}
			cells = append(cells, [2]int{m.runeCol(r, seg.Start), m.runeCol(r, seg.Stop)})
		}
		if r < 0 {
			if row.Pos() < 0 {
				return t, false
			}
			r = m.rowOf(row.Pos())
		}
		if len(t.rows) == 0 {
			t.header = r
		}
		t.rows = append(t.rows, r)
		t.cells = append(t.cells, cells)
	}
	return t, len(t.rows) > 0
}

// cellWidth is the displayed width of [lo, hi) on row after the ops inside it.
func (m *markdownHighlighter) cellWidth(row, lo, hi int) int {
	w := hi - lo
	for _, op := range m.ops[row] {
		if op.lo >= lo && op.hi <= hi {
			w += utf8.RuneCountInString(op.text) - (op.hi - op.lo)
		}
	}
	return w
}

// liveTableOps pads every row's cells to the column widths and draws the delimiter row.
func (m *markdownHighlighter) liveTableOps(t liveTable) {
	cols := len(t.align)
	widths := make([]int, cols)
	cellW := make([][]int, len(t.rows))
	for i, r := range t.rows {
		cellW[i] = make([]int, cols)
		for c, cell := range t.cells[i] {
			if c >= cols || cell[0] < 0 {
				continue
			}
			cellW[i][c] = m.cellWidth(r, cell[0], cell[1])
			widths[c] = max(widths[c], cellW[i][c])
		}
	}
	for i, r := range t.rows {
		n := utf8.RuneCountInString(m.lines[r])
		prev, pending := 0, "│ "
		for c := 0; c < cols; c++ {
			var cell [2]int
			if c < len(t.cells[i]) {
				cell = t.cells[i][c]
			} else {
				cell = [2]int{-1, -1}
			}
			pad := widths[c] - cellW[i][c]
			left := 0
			switch t.align[c] {
			case extast.AlignRight:
				left = pad
			case extast.AlignCenter:
				left = pad / 2
			}
			if cell[0] < 0 { // missing: all padding, nothing to anchor to
				pending += strings.Repeat(" ", widths[c]) + " │ "
				continue
			}
			m.ops[r] = append(m.ops[r], liveOp{prev, cell[0], pending + strings.Repeat(" ", left), mdStyleQuote})
			prev = cell[1]
			pending = strings.Repeat(" ", pad-left) + " │ "
		}
		m.ops[r] = append(m.ops[r], liveOp{prev, n, strings.TrimRight(pending, " "), mdStyleQuote})
		if i == 0 && t.header+1 < len(m.lines) {
			dr := t.header + 1
			segs := make([]string, cols)
			for c, w := range widths {
				segs[c] = strings.Repeat("─", w)
			}
			m.ops[dr] = append(m.ops[dr], liveOp{0, utf8.RuneCountInString(m.lines[dr]),
				"├─" + strings.Join(segs, "─┼─") + "─┤", mdStyleQuote})
			for _, cell := range t.cells[0] {
				if cell[0] >= 0 {
					m.liveIntervals[r] = append(m.liveIntervals[r], mdInterval{lo: cell[0], hi: cell[1], id: mdStyleStrong, prio: 1})
				}
			}
		}
	}
}

// bakeLive composes each row with ops into display spans and the source-column map.
// Rows without ops render as their source (nil).
func (m *markdownHighlighter) bakeLive() {
	m.live = make([][]editor.Span, len(m.lines))
	m.liveCols = make([][]int, len(m.lines))
	for r, ops := range m.ops {
		if len(ops) == 0 {
			continue
		}
		m.fills[r] = liveFill{} // other markup on the row (a quote bar) wins over a rule
		sort.SliceStable(ops, func(i, j int) bool { return ops[i].lo < ops[j].lo })
		kept := ops[:0]
		end := 0
		for _, op := range ops {
			if op.lo < end {
				continue // overlaps an earlier op
			}
			kept = append(kept, op)
			end = max(end, op.hi)
		}
		runes := []rune(m.lines[r])
		block, inline := m.styleIDs(r, len(runes))
		var disp []rune
		var dids [][2]int
		var cols []int
		emit := func(text string, id, col int) {
			for _, ch := range text {
				disp = append(disp, ch)
				dids = append(dids, [2]int{id, mdStyleNone})
				cols = append(cols, col)
			}
		}
		k := 0
		for c := 0; ; {
			skipped := false
			for k < len(kept) && kept[k].lo == c {
				op := kept[k]
				k++
				emit(op.text, op.id, op.lo)
				if op.hi > c {
					c, skipped = op.hi, true
					break
				}
			}
			if skipped {
				continue
			}
			if c >= len(runes) {
				break
			}
			disp = append(disp, runes[c])
			dids = append(dids, [2]int{block[c], inline[c]})
			cols = append(cols, c)
			c++
		}
		// Runs group by resolved style, so a chip's padding joins its code.
		spans := []editor.Span{}
		for i := 0; i < len(disp); {
			st := m.style(dids[i][0], dids[i][1])
			j := i + 1
			for j < len(disp) && m.style(dids[j][0], dids[j][1]) == st {
				j++
			}
			spans = append(spans, editor.Span{Text: string(disp[i:j]), Style: st})
			i = j
		}
		m.live[r], m.liveCols[r] = spans, cols
	}
}

// styleIDs resolves row's highlight intervals (plus live-only ones) to a block and an
// inline style id per rune; the rendered style is the inline one over the block one.
func (m *markdownHighlighter) styleIDs(r, n int) (block, inline []int) {
	block, inline = make([]int, n), make([]int, n)
	ivs := append(append([]mdInterval(nil), m.intervals[r]...), m.liveIntervals[r]...)
	for _, iv := range ivs {
		ids := block
		if iv.prio == 1 {
			ids = inline
		}
		for c := iv.lo; c < iv.hi && c < n; c++ {
			ids[c] = iv.id
		}
	}
	return block, inline
}

// liveStyles are the live preview's own styles, built per parse so the adaptive ones
// (the code chip's tint, h3's dimmed color) follow the detected background.
func newLiveStyles() map[int]*lipgloss.Style {
	p := mdPalette
	chip := lipgloss.NewStyle().Foreground(p.mdCode).
		Background(core.Resolve(core.Color{Light: 254, Dark: 236})) // the previewer's code span tint
	return map[int]*lipgloss.Style{
		mdStyleH1:         styleRef(lipgloss.NewStyle().Bold(true).Background(p.mdHeading)),
		mdStyleH2:         styleRef(lipgloss.NewStyle().Bold(true).Foreground(p.mdHeading)),
		mdStyleH3:         styleRef(lipgloss.NewStyle().Bold(true).Foreground(dimColor(p.mdHeading, liveSubheadingDim))),
		mdStyleCodeSpan:   styleRef(chip),
		mdStyleLiveStrong: styleRef(lipgloss.NewStyle().Bold(true)),
		mdStyleLiveEm:     styleRef(lipgloss.NewStyle().Italic(true)),
	}
}

// liveInline maps an inline highlight id to its live counterpart: emphasis keeps only its
// weight or slant, and a code span becomes a chip.
var liveInline = map[int]int{
	mdStyleStrong:   mdStyleLiveStrong,
	mdStyleEmphasis: mdStyleLiveEm,
	mdStyleCode:     mdStyleCodeSpan,
}

// liveSubheadingDim is how far h3+ recede from the heading color, as the previewer's
// subheadings do.
const liveSubheadingDim = 0.3

// dimColor blends c toward the terminal's ground by amount.
func dimColor(c color.Color, amount float64) color.Color {
	if c == nil {
		return c
	}
	ground := 0.0
	if !core.BackgroundIsDark() {
		ground = 255
	}
	r, g, b, _ := c.RGBA()
	mix := func(v uint32) uint8 {
		x := float64(v >> 8)
		return uint8(x + (ground-x)*amount)
	}
	return lipgloss.Color(fmt.Sprintf("#%02x%02x%02x", mix(r), mix(g), mix(b)))
}

// style resolves a (block, inline) pair to one style, the inline one inheriting what it
// leaves unset from the block (so bold in an h1 keeps its background). Pairs are cached
// per parse.
func (m *markdownHighlighter) style(block, inline int) *lipgloss.Style {
	if m.liveStyles == nil {
		m.liveStyles = newLiveStyles()
		m.pairStyles = map[[2]int]*lipgloss.Style{}
	}
	one := func(id int) *lipgloss.Style {
		if st, ok := m.liveStyles[id]; ok {
			return st
		}
		if id > 0 && id < len(mdStyles) {
			return mdStyles[id]
		}
		return nil
	}
	if inline == mdStyleNone {
		return one(block)
	}
	key := [2]int{block, inline}
	if st, ok := m.pairStyles[key]; ok {
		return st
	}
	if id, ok := liveInline[inline]; ok {
		inline = id
	}
	st := one(inline)
	if b := one(block); b != nil && st != nil {
		st = styleRef(st.Inherit(*b))
	}
	m.pairStyles[key] = st
	return st
}
