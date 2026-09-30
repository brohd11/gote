package app

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/brohd11/bubblestack/core"
	"github.com/charmbracelet/x/ansi"
)

// headerRows is the header leaf's fixed height: the breadcrumb bar and its rule.
const headerRows = 2

// headerPanel is the breadcrumb drawn as the layout's top leaf, in place of the router's,
// so its rule can join the pane dividers below (see joinRule). Not Focusable: pane
// traversal skips it and clicks fall through.
type headerPanel struct {
	host *homeScreen
	w    int
}

func (p *headerPanel) SetSize(w, _ int) { p.w = w }

func (p *headerPanel) View(bool) string {
	crumbs := []core.Crumb{{Full: p.host.CrumbLabel(false), Short: p.host.CrumbLabel(true)}}
	return core.RenderBreadcrumb(crumbs, p.w) + "\n" + joinRule(p.w, "", "")
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
	lines[headerRows-1] = joinRule(s.w, "", lines[headerRows])
	return strings.Join(lines, "\n")
}
