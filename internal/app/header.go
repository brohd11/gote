package app

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/brohd11/bubblestack/components"
	"github.com/brohd11/bubblestack/core"
	"github.com/charmbracelet/x/ansi"
)

// headerRows is the header leaf's fixed height: the menu and breadcrumb row, and its rule.
const headerRows = 2

// headerPanel is the layout's top leaf, drawn in place of the router's breadcrumb so its
// rule can join the pane dividers (see joinRule): ` File  Edit  View  Options │ crumb`.
// In minimal mode the doc's title bar takes the crumb's place (the editor hides its own),
// and until the bar is revealed (homeScreen.menuBar) the title bar is all there is: the
// same two rows, so revealing never moves the body.
// Not Focusable, so pane traversal skips it; the home screen takes its mouse input
// (headerInput) before the panes can.
type headerPanel struct {
	host      *homeScreen
	w         int
	menuSpans []statusSpan // each menu label's columns, as last rendered
}

func (p *headerPanel) SetSize(w, _ int) { p.w = w }

func (p *headerPanel) View(bool) string {
	s := p.host
	p.menuSpans = s.headerMenuSpans(s.sh)
	if !s.menuBar {
		return s.titleBar(false)
	}
	var b strings.Builder
	for _, span := range p.menuSpans {
		label := headerMenuLabel(span.id)
		// The first letter is the alt+ chord that opens the menu (headerMenus).
		b.WriteString(" " + components.AccelLabel(label, []rune(label)[0], lipgloss.NewStyle()) + " ")
	}
	b.WriteString(lipgloss.NewStyle().Foreground(core.BorderColor).Render("│"))
	if s.minimal {
		b.WriteString(s.titleBar(true))
	} else {
		x := menusWidth(p.menuSpans) + 1
		// The breadcrumb gets what the menus leave; it truncates, the menus never do.
		crumbs := []core.Crumb{{Full: s.CrumbLabel(false), Short: s.CrumbLabel(true)}}
		b.WriteString(core.RenderBreadcrumb(crumbs, max(p.w-x, 0)))
	}
	row := ansi.Truncate(b.String(), p.w, "")
	for i := range p.menuSpans {
		p.menuSpans[i].x1 = min(p.menuSpans[i].x1, p.w)
	}
	return row + "\n" + joinRule(p.w, "", "")
}

// titleBar is minimal mode's doc title as the editor drew it over itself, muted while the
// pane is unfocused: both rows, or only the title row (firstRow) beside the menus.
func (s *homeScreen) titleBar(firstRow bool) string {
	title := s.previewName() + s.editor.ChangeMark()
	if s.readerOn() {
		title = s.previewName() + " · preview"
	}
	bar := core.RenderTitleBarMuted(title)
	if s.editorPanel.Focused() {
		bar = core.RenderTitleBar(title)
	}
	if firstRow {
		bar, _, _ = strings.Cut(bar, "\n")
	}
	return bar
}

// headerRule reports whether the header's second row is the rule the panes join, rather
// than minimal mode's bare title bar.
func (s *homeScreen) headerRule() bool { return s.header != nil && s.menuBar }

// joinRule renders a width-cell rule in the border color that the lines around it meet:
// a cell gets an arm up where the row above (rendered) carries a line down into it, and an
// arm down where the row below carries one up. Mid-rule that is ┬ ┴ ┼; at either end it is
// the matching corner or tee, so the rule can be the top or the shared edge of a box.
func joinRule(width int, above, below string) string {
	if width <= 0 {
		return ""
	}
	up := armCells(above, width, "│┃├┤┼┬┌┐╭╮")
	down := armCells(below, width, "│┃├┤┼┴└┘╰╯")
	cells := make([]rune, width)
	for x := range cells {
		// Glyphs indexed by [up][down] for the left end, the middle and the right end.
		glyphs := [3][2][2]rune{
			{{'─', '┌'}, {'└', '├'}},
			{{'─', '┬'}, {'┴', '┼'}},
			{{'─', '┐'}, {'┘', '┤'}},
		}
		pos := 1
		switch x {
		case 0:
			pos = 0
		case width - 1:
			pos = 2
		}
		cells[x] = glyphs[pos][b2i(up[x])][b2i(down[x])]
	}
	return lipgloss.NewStyle().Foreground(core.BorderColor).Render(string(cells))
}

// armCells marks the cells of row (ANSI and all) holding one of glyphs, over width cells.
func armCells(row string, width int, glyphs string) []bool {
	arms := make([]bool, width)
	x := 0
	for _, r := range ansi.Strip(row) {
		if x >= width {
			break
		}
		arms[x] = strings.ContainsRune(glyphs, r)
		x += ansi.StringWidth(string(r))
	}
	return arms
}

// midGlyph is an end glyph of joinRule with the outward arm added: the cell where the rule
// meets another line running on past it.
func midGlyph(r rune) rune {
	switch r {
	case '├', '┤':
		return '┼'
	case '┌', '┐':
		return '┬'
	case '└', '┘':
		return '┴'
	}
	return r
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

// headerHeight is the body rows the header leaf takes above the panes: 0 without one.
func (s *homeScreen) headerHeight() int {
	if s.header == nil {
		return 0
	}
	return headerRows
}

// joinDockRule redraws the open dock's top edge against the rows on either side, so the
// columns above close onto it and its own sides meet it. A no-op without the dock.
func (s *homeScreen) joinDockRule(sh *core.Shared, body string) string {
	if !s.bottomVisible {
		return body
	}
	r := s.bottom.y - sh.BodyY()
	lines := strings.Split(body, "\n")
	if r < 1 || r+1 >= len(lines) {
		return body
	}
	lines[r] = joinRule(s.w, s.chromeOnly(lines[r-1], r-1), s.chromeOnly(lines[r+1], r+1))
	return strings.Join(lines, "\n")
}

// joinTabRule redraws the rule under the tab bar against the rows on either side, so the
// group dividers cross it and the frames beside it — the sidebar's right edge, a preview's
// left — tee into it. The span is the bars' columns, widened by one cell at either end that
// already holds a vertical edge. A no-op without tabs (minimal mode).
func (s *homeScreen) joinTabRule(sh *core.Shared, body string) string {
	if !s.tabsVisible() {
		return body
	}
	groups := s.groups()
	if len(groups) == 0 || groups[0].openTabs.h < 2 {
		return body
	}
	x0, x1 := s.w, 0
	for _, g := range groups {
		x0, x1 = min(x0, g.openTabs.x), max(x1, g.openTabs.x+g.openTabs.w)
	}
	r := groups[0].openTabs.y + 1 - sh.BodyY()
	lines := strings.Split(body, "\n")
	if r < 1 || r+1 >= len(lines) || x0 >= x1 {
		return body
	}
	// A neighbor's edge, or the rule under its legend (a TitledFrame's ├──┤ lands on this
	// row), which the tab rule then continues as one line.
	cells := []rune(ansi.Strip(lines[r]))
	at := func(x int) rune {
		if x < len(cells) {
			return cells[x]
		}
		return ' '
	}
	left, right := ' ', ' '
	if x0 > 0 && strings.ContainsRune("│┃├┤┼", at(x0-1)) {
		x0--
		left = at(x0)
	}
	if x1 < s.w && strings.ContainsRune("│┃├┤┼", at(x1)) {
		right = at(x1)
		x1++
	}
	above, below := s.chromeOnly(lines[r-1], r-1), s.chromeOnly(lines[r+1], r+1)
	rule := []rune(ansi.Strip(joinRule(x1-x0, ansi.Cut(above, x0, x1), ansi.Cut(below, x0, x1))))
	// An end cell whose own rule ran on outward is mid-line: ├ → ┼ and so on.
	if strings.ContainsRune("┤┼", left) {
		rule[0] = midGlyph(rule[0])
	}
	if strings.ContainsRune("├┼", right) {
		rule[len(rule)-1] = midGlyph(rule[len(rule)-1])
	}
	styled := lipgloss.NewStyle().Foreground(core.BorderColor).Render(string(rule))
	lines[r] = ansi.Cut(lines[r], 0, x0) + styled + ansi.Cut(lines[r], x1, s.w)
	return strings.Join(lines, "\n")
}

// joinHeaderRule redraws the header's rule row in body against the row below it. A
// no-op when the layout has no header.
func (s *homeScreen) joinHeaderRule(body string) string {
	if !s.headerRule() {
		return body
	}
	lines := strings.SplitN(body, "\n", headerRows+2)
	if len(lines) <= headerRows {
		return body
	}
	// The row above is the header's own, whose menu separator is chrome.
	lines[headerRows-1] = joinRule(s.w, lines[headerRows-2], s.chromeOnly(lines[headerRows], headerRows))
	return strings.Join(lines, "\n")
}

// chromeOnly is body row r of the composed view with every cell blanked but the panes'
// vertical frame edges (frameEdges), for joinRule to read: the git gutter, the scrollbar,
// indent guides and quote bars draw the same glyphs as a frame but must not join a rule.
func (s *homeScreen) chromeOnly(row string, r int) string {
	edges := s.frameEdges(r)
	var b strings.Builder
	x := 0
	for _, c := range ansi.Strip(row) {
		w := ansi.StringWidth(string(c))
		if x < len(edges) && edges[x] {
			b.WriteRune(c)
		} else {
			b.WriteString(strings.Repeat(" ", w))
		}
		x += w
	}
	return b.String()
}

// frameEdges marks the columns of body row r where a pane draws a vertical frame edge.
func (s *homeScreen) frameEdges(r int) []bool {
	edges := make([]bool, s.w)
	for panel, slot := range s.panelSlots {
		x, y, w, h, ok := s.modular.SlotRect(slot)
		if !ok || r < y || r >= y+h || w <= 0 {
			continue
		}
		for _, c := range paneEdges(panel, w) {
			if x+c >= 0 && x+c < s.w {
				edges[x+c] = true
			}
		}
	}
	return edges
}

// paneEdges is the pane-local columns where panel, w cells wide, draws a vertical frame
// edge. The editor has no frame, only a split's divider in its first column; every other
// pane (the side column, the preview, the dock) is framed down both sides.
func paneEdges(panel components.Panel, w int) []int {
	switch p := panel.(type) {
	case *headerPanel:
		return nil
	case *groupPanel:
		return dividerEdge(p.separator)
	case *documentTabBar:
		return dividerEdge(p.separator)
	}
	return []int{0, w - 1}
}

// dividerEdge is a group divider's column, when the pane draws one.
func dividerEdge(separator bool) []int {
	if separator {
		return []int{0}
	}
	return nil
}
