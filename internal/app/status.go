package app

import (
	"charm.land/lipgloss/v2"
	"github.com/brohd11/bubblestack/core"
)

// The status line, drawn by gote rather than the router: the router's status row takes a
// row from the body, so panes would jump on each message. With ChromeMask.Status set, the
// screen paints it into space the frame already reserves:
//   - into the screen's own one-row status row, drawn as its help bar (statusRow), or
//   - over the body's last row when that row is masked away (statusOver).
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

// statusRow is the one-row status line the home screen draws in the help bar's place.
// It is always one row, blank without a message, so a message never resizes the body.
func statusRow(sh *core.Shared) string {
	if line := statusLine(sh); line != "" {
		return line
	}
	return " " // vheight("") is 0: the row must exist even when empty
}

// statusOver paints the status over the body's last row (minimal mode, no status row),
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
