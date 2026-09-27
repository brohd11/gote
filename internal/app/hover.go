package app

import (
	"strings"

	"github.com/brohd11/bubblestack/components"
	"github.com/brohd11/bubblestack/core"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// The hover tooltip: alt+shift+h at the caret, or the context menu's Hover row (a right press
// has already moved the caret to the click). There is no pointer hover: bubblestack uses
// cell motion, which sends no motion without a held button.

const (
	hoverMaxWidth = 62
	// Content rows, not rendered rows: PopupPanel's border adds two more.
	hoverMaxHeight = 14
	// The cells PopupPanel's border and padding take, so the content can be sized before
	// wrapping.
	panelChrome = 4
)

// hoverUI is the tooltip's state: a passive FloatingPopup (nil Handle) that claims no
// input, so keys and clicks go where they would anyway and dismiss it on the way.
type hoverUI struct {
	popup *components.FloatingPopup
	body  string
	width int
	path  string
}

func (s *homeScreen) closeHover() {
	s.hover = hoverUI{}
}

// dismissHoverOn closes the tooltip on the next new intent: a press, wheel notch, key or
// paste. A release or drag-motion is the tail of a gesture and is ignored: the menu's
// Hover row fires on press, and its release would otherwise close the tooltip it just
// opened.
func (s *homeScreen) dismissHoverOn(msg tea.Msg) {
	if s.hover.popup == nil {
		return
	}
	switch msg.(type) {
	case tea.KeyPressMsg, tea.PasteMsg, tea.MouseClickMsg, tea.MouseWheelMsg:
		s.closeHover()
	}
}

func (s *homeScreen) applyHover(result *lspRequestResult) core.Action {
	width := s.panelWidth(hoverMaxWidth)
	body := hoverBody(result.hover, width)
	if body == "" {
		s.closeHover()
		return core.SetStatus("no hover info here")
	}
	s.hover = hoverUI{
		popup: &components.FloatingPopup{
			Content: func() string { return components.PopupPanel(s.hover.body, s.hover.width) },
		},
		body:  body,
		width: panelFit(body, width),
		path:  result.path,
	}
	return core.Action{}
}

// panelWidth is the widest content a caret panel may wrap to: the room right of the
// editor's left edge, minus the box's chrome, capped at max.
func (s *homeScreen) panelWidth(max int) int {
	available := s.w - s.editorLeft() - panelChrome - 1
	if len(s.groups()) > 1 {
		available = min(max, s.editorPanel.w-panelChrome-1)
		if available < 1 {
			return 1
		}
		return available
	}
	if available < 20 {
		available = 20
	}
	return min(max, available)
}

// panelFit is the drawn width: the widest rendered row, capped at the budget.
func panelFit(body string, budget int) int {
	width := 1
	for _, line := range strings.Split(body, "\n") {
		width = max(width, ansi.StringWidth(line))
	}
	return min(width, budget)
}

// caretPanel places a panel at the caret, sliding left to stay on screen (PlacePopupAt
// would pin a wide popup to column 0) but never left of the editor (leftBound), so it
// stays over the text.
func caretPanel(anchorX, anchorY, leftBound int, preferAbove bool) components.PopupPlacement {
	return func(frameW, frameH, popupW, popupH int) (int, int) {
		// Start at the caret and slide left only as far as needed, never left of the editor
		// unless that would push the box off the right edge.
		x := min(anchorX, frameW-popupW)
		x = max(x, min(leftBound, max(frameW-popupW, 0)))
		x = max(x, 0)

		// Vertical: the preferred side, the other side, then wherever it fits.
		first, second := anchorY+1, anchorY-popupH
		if preferAbove {
			first, second = second, first
		}
		y := first
		if !fitsRow(y, popupH, frameH) {
			y = second
		}
		if !fitsRow(y, popupH, frameH) {
			y = 0
		}
		return x, y
	}
}

func fitsRow(y, popupH, frameH int) bool { return y >= 0 && y+popupH <= frameH }

// hoverBody condenses a server's markdown into a few tooltip lines: fences stripped,
// blank runs collapsed, wrapped and clipped. components.RenderMarkdown's page spacing
// would spend the tooltip's height on margins.
func hoverBody(markdown string, width int) string {
	markdown = strings.TrimSpace(markdown)
	if markdown == "" {
		return ""
	}
	var lines []string
	blank := false
	for _, line := range strings.Split(markdown, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") {
			continue // the fence itself; its contents are the useful part
		}
		// gopls separates signature and doc with a rule; the box border already divides.
		if trimmed == "---" || trimmed == "***" || trimmed == "___" {
			continue
		}
		if trimmed == "" {
			// One blank line separates the signature from its prose; more is padding
			// a tooltip cannot afford.
			if blank || len(lines) == 0 {
				continue
			}
			blank = true
			lines = append(lines, "")
			continue
		}
		blank = false
		lines = append(lines, strings.Split(ansi.Wrap(line, width, ""), "\n")...)
	}
	for len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	if len(lines) > hoverMaxHeight {
		lines = append(lines[:hoverMaxHeight-1], "…")
	}
	return strings.Join(lines, "\n")
}

// viewHover places the tooltip under the caret, flipping above it near the bottom edge.
func (s *homeScreen) viewHover(sh *core.Shared, body string) string {
	if s.hover.popup == nil || s.hover.path != s.currentPath {
		return body
	}
	x, absoluteY, visible := s.editor.CursorAnchor()
	if !visible {
		return body
	}
	y := absoluteY - sh.BodyY()
	s.hover.popup.Placement = s.caretPopup(x, y, false)
	return s.hover.popup.ViewOver(body, s.w, s.h)
}

// caretPopup uses the actual editor rectangle in a split, including its tab row
// and the shared bottom dock. The old full-frame placement remains for one group.
func (s *homeScreen) caretPopup(x, y int, above bool) components.PopupPlacement {
	if len(s.groups()) == 1 {
		return caretPanel(x, y, s.editorLeft(), above)
	}
	p := s.editorPanel
	top := p.y
	if s.sh != nil {
		top -= s.sh.BodyY()
	}
	return func(_, _, w, h int) (int, int) {
		px, py := caretPanel(x-p.x, y-top, 0, above)(p.w, p.h, w, h)
		return p.x + px, top + py
	}
}
