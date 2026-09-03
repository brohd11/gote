package app

import (
	"strings"

	"github.com/brohd11/bubblestack/components"
	"github.com/brohd11/bubblestack/core"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// The hover tooltip: alt+h at the caret, or the context menu's Hover info row — which
// is the pointer-driven version, because a right press has already moved the caret to
// the clicked cell before the menu opens (EditorScreen.pressContext).
//
// There is no mouse-pointer hover. Motion without a held button only arrives under
// tea.MouseModeAllMotion, and bubblestack deliberately runs cell motion so that no
// hover traffic crosses Update at all (core/router_render.go).

const (
	hoverMaxWidth = 62
	// Content rows, not rendered rows: PopupPanel's border adds two more.
	hoverMaxHeight = 14
	// The cells PopupPanel's border and padding take off the content width. It mirrors
	// the package-private menuChromeW the panel adds back, so gote can budget the space
	// a box will actually occupy before it wraps anything to fit.
	panelChrome = 4
)

// hoverUI is the tooltip's whole state. Unlike the completion popup it holds no list and
// no handler: FloatingPopup with a nil Handle is a passive notice, so the tooltip
// renders over the body and claims no input — every key and every click goes where it
// would have gone anyway, and dismisses the tooltip on the way past.
type hoverUI struct {
	popup *components.FloatingPopup
	body  string
	width int
	path  string
}

func (s *homeScreen) closeHover() {
	s.hover = hoverUI{}
}

// dismissHoverOn retires the tooltip on the next thing the user DOES. A press, a wheel
// notch, a key or a paste is a new intent; a release or a drag-motion is the tail of a
// gesture already under way — the rule the router (router_keys.go), ModularScreen and
// MenuScreen each state for themselves.
//
// The distinction is load-bearing here, not stylistic. The context menu's Hover row fires
// on the PRESS and pops the menu with it, so the matching RELEASE lands on this screen a
// moment later — by which time the server has usually answered and the tooltip is up.
// Treating that release as intent closed the tooltip the same click had just asked for,
// which is why the row appeared to do nothing while alt+h worked.
//
// Motion is left out for the same reason and costs nothing today: cell-motion mode reports
// motion only while a button is held, so a motion always belongs to a gesture whose press
// has already been seen here.
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

// panelWidth is the widest content a caret-anchored panel may wrap to: whatever is left
// between the editor's own left edge and the right of the frame, once the box's chrome is
// paid for, capped at max. Budgeting the chrome BEFORE wrapping is what keeps a panel
// narrow enough to sit under the caret instead of being shoved somewhere it fits.
func (s *homeScreen) panelWidth(max int) int {
	available := s.w - s.editorLeft() - panelChrome - 1
	if available < 20 {
		available = 20
	}
	return min(max, available)
}

// panelFit is the width a panel is actually drawn at: its widest rendered row, never more
// than the wrap budget. Without it every tooltip would be a full-width slab regardless of
// what it says — the completion list sizes to its longest label for the same reason.
func panelFit(body string, budget int) int {
	width := 1
	for _, line := range strings.Split(body, "\n") {
		width = max(width, ansi.StringWidth(line))
	}
	return min(width, budget)
}

// caretPanel places a panel against the caret. It SHIFTS left to stay on screen rather
// than right-aligning at the caret the way components.PlacePopupAt does: that helper
// assumes a popup narrower than the caret's column, and a wider one flips to a negative x
// which the compositor clamps to column 0 — the panel ends up pinned to the far left of
// the screen, nowhere near the symbol it describes.
//
// leftBound keeps the box off the sidebar: a tooltip is about the caret, so it belongs
// over the text rather than over the file list.
func caretPanel(anchorX, anchorY, leftBound int, preferAbove bool) components.PopupPlacement {
	return func(frameW, frameH, popupW, popupH int) (int, int) {
		// Horizontal: start at the caret, slide left only as far as the right edge
		// demands, and never left of the editor — unless honoring that would push the
		// box off the right edge, in which case fitting on screen wins.
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

// hoverBody turns a server's markdown into the few lines a tooltip can hold. It is not
// run through components.RenderMarkdown: that renderer is for whole document pages and
// leans on glamour, which is pinned at v0.9.1 for the workspace's own reasons, and its
// block spacing would spend a tooltip's entire height on margins. What a hover actually
// needs is the code line at the top and the doc comment under it, wrapped — so fences
// are stripped, blank runs collapsed, and the result clipped.
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
		// gopls separates the signature from its doc comment with a horizontal rule,
		// which in a box this small is a whole line spent on a divider the box's own
		// border already provides.
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
	s.hover.popup.Placement = caretPanel(x, y, s.editorLeft(), false)
	return s.hover.popup.ViewOver(body, s.w, s.h)
}
