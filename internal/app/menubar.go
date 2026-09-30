package app

import (
	tea "charm.land/bubbletea/v2"
	"github.com/brohd11/bubblestack/components"
	"github.com/brohd11/bubblestack/core"
)

// headerMenus are the header's menus, left to right.
var headerMenus = []struct{ id, label string }{
	{"file", "File"},
	{"edit", "Edit"},
	{"view", "View"},
	{"options", "Options"},
}

// headerMenuItems is the dropdown for menu id. Every menu is a placeholder for now.
func (s *homeScreen) headerMenuItems(string) []components.MenuItem {
	return []components.MenuItem{{Label: "Nothing here yet", Disabled: true}}
}

// headerInput claims presses and wheel notches over the header rows, which would
// otherwise miss every focusable pane and be broadcast to all of them. It never moves
// focus; a plain left click on a menu label opens that menu. Motion and release pass, so
// a drag begun in a pane keeps its gesture while crossing the header.
func (s *homeScreen) headerInput(sh *core.Shared, msg tea.Msg) (core.Action, bool) {
	if s.header == nil || sh == nil {
		return core.Action{}, false
	}
	var m tea.Mouse
	switch mm := msg.(type) {
	case tea.MouseClickMsg:
		m = mm.Mouse()
	case tea.MouseWheelMsg:
		m = mm.Mouse()
	default:
		return core.Action{}, false
	}
	if m.Y < sh.BodyY() || m.Y >= sh.BodyY()+headerRows {
		return core.Action{}, false
	}
	if _, click := msg.(tea.MouseClickMsg); !click || m.Button != tea.MouseLeft || m.Mod != 0 {
		return core.Action{}, true
	}
	s.closeCompletion()
	for _, span := range s.header.menuSpans {
		if m.Y == sh.BodyY() && m.X >= span.x0 && m.X < span.x1 {
			return s.openHeaderMenu(sh, span), true
		}
	}
	return core.Action{}, true
}

// openHeaderMenu drops menu span down with its top border on the rule, so the label sits
// directly over the box; the border starts one cell left of the label and flips clear of
// it near the right edge.
func (s *homeScreen) openHeaderMenu(sh *core.Shared, span statusSpan) core.Action {
	return core.Push(components.NewMenu(components.MenuOpts{
		Items:  s.headerMenuItems(span.id),
		Anchor: components.MenuAnchor{X: max(span.x0-1, 0), Y: sh.BodyY() + headerRows - 1, FlipX: span.x1 + 1},
	}))
}
