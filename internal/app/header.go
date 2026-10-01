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
// Not Focusable, so pane traversal skips it; the home screen takes its mouse input
// (headerInput) before the panes can.
type headerPanel struct {
	host      *homeScreen
	w         int
	menuSpans []statusSpan // each menu label's columns, as last rendered
}

func (p *headerPanel) SetSize(w, _ int) { p.w = w }

func (p *headerPanel) View(bool) string {
	p.menuSpans = headerMenuSpans()
	var b strings.Builder
	for _, m := range headerMenus {
		// The first letter is the alt+ chord that opens the menu (menuKeys).
		b.WriteString(" " + components.AccelLabel(m.label, []rune(m.label)[0], lipgloss.NewStyle()) + " ")
	}
	b.WriteString(lipgloss.NewStyle().Foreground(core.BorderColor).Render("│"))
	x := headerMenusWidth + 1
	// The breadcrumb gets what the menus leave; it truncates, the menus never do.
	crumbs := []core.Crumb{{Full: p.host.CrumbLabel(false), Short: p.host.CrumbLabel(true)}}
	b.WriteString(core.RenderBreadcrumb(crumbs, max(p.w-x, 0)))
	row := ansi.Truncate(b.String(), p.w, "")
	for i := range p.menuSpans {
		p.menuSpans[i].x1 = min(p.menuSpans[i].x1, p.w)
	}
	return row + "\n" + joinRule(p.w, "", "")
}

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
	lines[r] = joinRule(s.w, lines[r-1], lines[r+1])
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
	rule := []rune(ansi.Strip(joinRule(x1-x0, ansi.Cut(lines[r-1], x0, x1), ansi.Cut(lines[r+1], x0, x1))))
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
	if s.header == nil {
		return body
	}
	lines := strings.SplitN(body, "\n", headerRows+2)
	if len(lines) <= headerRows {
		return body
	}
	lines[headerRows-1] = joinRule(s.w, lines[headerRows-2], lines[headerRows])
	return strings.Join(lines, "\n")
}
