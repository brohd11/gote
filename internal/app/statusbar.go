package app

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/brohd11/bubblestack/core"
	"github.com/charmbracelet/x/ansi"
)

// The home screen's status bar, drawn in the help bar's row:
//
//	 ≡ ▁ │ Diag · Search │ <message>                │ Ln 4, Col 9
//
// a control spot (sidebar and dock toggles, then the dock's tabs while it is open), the
// status message filling the middle, and the active document's cursor on the right. It
// is one row whatever the message, so the body never moves.

const (
	statusSidebar = "sidebar"
	statusDock    = "dock"
)

// statusSpan is a clickable item's cells in the bar, [x0, x1), recorded as it renders.
type statusSpan struct {
	id     string
	x0, x1 int
}

// dockTabs are the dock's panels in bar order, labeled as the dock used to label them.
var dockTabs = []struct{ id, label string }{
	{bottomDiagnostics, "Diag"},
	{bottomSearch, "Search"},
}

// statusBar renders the bar exactly sh.Width() cells wide and records s.statusSpans.
func (s *homeScreen) statusBar(sh *core.Shared) string {
	s.statusSpans = s.statusSpans[:0]
	w := sh.Width()
	if w <= 0 {
		return " " // vheight("") is 0: the row must exist even when empty
	}
	var b strings.Builder
	x := 0
	put := func(text string, style lipgloss.Style, id string) {
		cw := lipgloss.Width(text)
		if id != "" {
			s.statusSpans = append(s.statusSpans, statusSpan{id, x, x + cw})
		}
		b.WriteString(style.Render(text))
		x += cw
	}
	plain := lipgloss.NewStyle()
	sep := lipgloss.NewStyle().Foreground(core.BorderColor)
	toggle := func(on bool) lipgloss.Style {
		if on {
			return plain
		}
		return core.MutedStyle()
	}

	put(" ", plain, "")
	put("≡", toggle(s.sidebar), statusSidebar)
	put(" ", plain, "")
	put("▁", toggle(s.bottomVisible), statusDock)
	if s.bottomVisible {
		put(" │ ", sep, "")
		for i, tab := range dockTabs {
			if i > 0 {
				put(" · ", core.MutedStyle(), "")
			}
			style := core.MutedStyle()
			if tab.id == s.bottom.active {
				style = plain.Bold(true)
				if s.bottom.Focused() {
					style = core.AccentStyle()
				}
			}
			put(tab.label, style, tab.id)
		}
	}
	put(" │ ", sep, "")

	// The cursor segment keeps its place: the message gets only what is left, and the
	// segment is dropped outright when even an empty message would not fit.
	right := ""
	if s.editor != nil {
		p := s.editor.CursorPosition()
		right = fmt.Sprintf("Ln %d, Col %d", p.Line+1, p.Column+1)
	}
	rightW := 0
	if right != "" {
		rightW = lipgloss.Width(" │ " + right + " ")
		if x+rightW > w {
			right, rightW = "", 0
		}
	}
	room := max(w-x-rightW, 0)
	msg := ansi.Truncate(statusLine(sh), room, "…")
	b.WriteString(msg)
	x += lipgloss.Width(msg)
	b.WriteString(strings.Repeat(" ", max(w-rightW-x, 0)))
	x = max(x, w-rightW)
	if right != "" {
		put(" │ ", sep, "")
		put(right+" ", core.MutedStyle(), "")
	}
	// A terminal narrower than the control spot clips it, and its spans with it.
	for i := range s.statusSpans {
		s.statusSpans[i].x1 = min(s.statusSpans[i].x1, w)
	}
	return ansi.Truncate(b.String(), w, "")
}

// statusBarInput handles a click on the status bar row, which the router hands to the
// screen because it is outside every chrome pane. Any click there is consumed.
func (s *homeScreen) statusBarInput(sh *core.Shared, msg tea.Msg) (core.Action, bool) {
	click, ok := msg.(tea.MouseClickMsg)
	if !ok || s.minimal || sh == nil || click.Y != sh.BodyY()+s.h {
		return core.Action{}, false
	}
	if click.Button != tea.MouseLeft || click.Mod != 0 {
		return core.Action{}, true
	}
	for _, span := range s.statusSpans {
		if click.X >= span.x0 && click.X < span.x1 {
			return s.statusAction(sh, span.id), true
		}
	}
	return core.Action{}, true
}

// statusAction runs a control-spot item: the same paths as the sidebar and dock keys, and
// a dock tab selects that panel and focuses the dock.
func (s *homeScreen) statusAction(sh *core.Shared, id string) core.Action {
	s.closeCompletion()
	switch id {
	case statusSidebar:
		s.setSidebar(!s.sidebar)
		return core.Action{}
	case statusDock:
		if !s.panelToggles {
			return core.Action{}
		}
		return s.toggleBottom(sh)
	case bottomDiagnostics, bottomSearch:
		if !s.bottomVisible {
			return core.Action{}
		}
		s.bottom.selectTab(id)
		return core.Async(s.modular.FocusSlot(s.panelSlot(s.bottom)))
	}
	return core.Action{}
}
