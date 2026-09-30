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
	return core.RenderBreadcrumb(crumbs, p.w) + "\n" + joinRule(p.w, "")
}

// joinRule renders a width-cell rule in the border color, with ┬ over every cell of below
// (the rendered row under the rule) that carries a line upward, so dividers meet it.
func joinRule(width int, below string) string {
	if width <= 0 {
		return ""
	}
	cells := make([]rune, width)
	for i := range cells {
		cells[i] = '─'
	}
	x := 0
	for _, r := range ansi.Strip(below) {
		if x >= width {
			break
		}
		if strings.ContainsRune("│┃├┤┼┴└┘╰╯", r) {
			cells[x] = '┬'
		}
		x += ansi.StringWidth(string(r))
	}
	return lipgloss.NewStyle().Foreground(core.BorderColor).Render(string(cells))
}

// headerHeight is the body rows the header leaf takes above the panes: 0 without one.
func (s *homeScreen) headerHeight() int {
	if s.header == nil {
		return 0
	}
	return headerRows
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
	lines[headerRows-1] = joinRule(s.w, lines[headerRows])
	return strings.Join(lines, "\n")
}
