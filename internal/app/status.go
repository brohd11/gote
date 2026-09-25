package app

import (
	"strings"

	"github.com/brohd11/bubblestack/core"

	"charm.land/lipgloss/v2"
)

// The status line, drawn by gote rather than the router: the router's status row takes a
// row from the body, so panes would jump on each message. With ChromeMask.Status set, the
// screen paints it into space the frame already uses:
//   - into the help bar's blank top row (statusBar), or
//   - over the body's last row when there is no help bar (statusOver).
//
// Every screen that masks the status must use one of these, or messages are lost.

// statusLine is the current message clamped to one row of the terminal width (an
// unclamped one would wrap to a second row); "" when there is none.
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

// statusBar draws the status into the help bar's blank top row (from bubbles' HelpStyle
// padding). If that row is not blank, the line goes above the bar instead.
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

// statusOver paints the status over the body's last row (minimal mode, no help bar),
// covering only its own width. A short body is first padded to bodyHeight.
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
