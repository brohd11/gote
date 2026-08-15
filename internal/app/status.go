package app

import (
	"strings"

	"github.com/brohd11/bubblestack/core"

	"github.com/charmbracelet/lipgloss"
)

// The transient status line, drawn by gote instead of by the router.
//
// The router treats the status as a sibling of the body: belowChrome appends it and
// bodyHeightFor subtracts its height, so a message appearing costs the body a row and
// clearing gives the row back — the panes' bottoms climb one line and drop back five
// seconds later, and an editor near the end of its buffer scrolls with them. Masking
// ChromeMask.Status and painting the line ourselves puts it in space the frame already
// spends, so the body's height never changes:
//
//   - a screen with a help bar lends its blank top padding row (statusBar)
//   - a screen whose help bar is masked lends its own last row (statusOver)
//
// Every screen that masks the status MUST route through one of the two, or the message
// has nowhere to land and is lost.

// statusLine is the current status message clamped to one row and the terminal width.
// The clamp is not cosmetic: core's statusStyle sets no width, so a message wider than
// the terminal wraps to two rows while lipgloss.Height still reports one — which would
// spend the very row this file exists to save (and, drawn by the router, overflows the
// frame). "" when there is no message, no status element, or no chrome at all.
func statusLine(sh *core.Shared) string {
	if sh == nil || sh.Chrome == nil || sh.Chrome.Status == nil || !sh.Chrome.Status.Shown() {
		return ""
	}
	line := sh.Chrome.Status.View()
	if line == "" {
		return ""
	}
	clamp := lipgloss.NewStyle().MaxHeight(1)
	if w := sh.Width(); w > 0 {
		clamp = clamp.MaxWidth(w)
	}
	return clamp.Render(line)
}

// statusBar draws the status into the help bar's own blank top row. The bar is two rows,
// not one: it renders through bubbles' list HelpStyle, which pads a row above the hints
// (Padding(1, 0, 0, 2)) — so the row is already being paid for, and the status can have
// it for free. help is returned untouched when there is nothing to show.
//
// A bar whose first row is NOT blank isn't one we can borrow from, so the line goes above
// it instead: that costs a row (the old behaviour) but never swallows a message.
func statusBar(sh *core.Shared, help string) string {
	line := statusLine(sh)
	if line == "" {
		return help
	}
	lines := strings.Split(help, "\n")
	if len(lines) < 2 || strings.TrimSpace(lines[0]) != "" {
		return lipgloss.JoinVertical(lipgloss.Left, line, help)
	}
	// HelpStyle indents the hints two cells; statusStyle pads one, so one more space
	// puts the message on the same column as the bar below it.
	lines[0] = " " + line
	return strings.Join(lines, "\n")
}

// statusOver paints the status over the last row of a body — the fallback for a screen
// with no help bar to lend a row (minimal mode masks it). The body keeps every row it
// had; the message covers only the cells it is wide, so a short one leaves a side pane's
// bottom row alone, and the text underneath comes back when the message clears.
//
// bodyHeight is the height the router handed to SetSize: a body that drew short is padded
// out to it first, so the line lands on the frame's bottom row rather than mid-screen.
func statusOver(sh *core.Shared, body string, bodyHeight int) string {
	line := statusLine(sh)
	if line == "" || bodyHeight < 1 {
		return body
	}
	if pad := bodyHeight - lipgloss.Height(body); pad > 0 {
		body = lipgloss.JoinVertical(lipgloss.Left, body, core.Blanks(pad))
	}
	return core.Composite(body, line, 0, bodyHeight-1)
}
