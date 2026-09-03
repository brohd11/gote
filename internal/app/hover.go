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
	hoverMaxWidth  = 62
	hoverMaxHeight = 14
)

// hoverUI is the tooltip's whole state. Unlike the completion popup it holds no list and
// no handler: FloatingPopup with a nil Handle is a passive notice, so the tooltip
// renders over the body and claims no input — every key and every click goes where it
// would have gone anyway, and dismisses the tooltip on the way past.
type hoverUI struct {
	popup *components.FloatingPopup
	body  string
	path  string
}

func (s *homeScreen) closeHover() {
	s.hover = hoverUI{}
}

// dismissHoverOn retires the tooltip on the next thing the user does. A passive popup
// that outlived its moment is the failure mode here, so the rule is deliberately blunt:
// any key press, any click, any paste takes it down.
func (s *homeScreen) dismissHoverOn(msg tea.Msg) {
	if s.hover.popup == nil {
		return
	}
	switch msg.(type) {
	case tea.KeyPressMsg, tea.PasteMsg, tea.MouseClickMsg, tea.MouseWheelMsg,
		tea.MouseMotionMsg, tea.MouseReleaseMsg:
		s.closeHover()
	}
}

func (s *homeScreen) applyHover(result *lspRequestResult) core.Action {
	body := hoverBody(result.hover, min(max(s.w-6, 20), hoverMaxWidth))
	if body == "" {
		s.closeHover()
		return core.SetStatus("no hover info here")
	}
	s.hover = hoverUI{
		popup: &components.FloatingPopup{Content: func() string { return s.hover.body }},
		body:  body,
		path:  result.path,
	}
	return core.Action{}
}

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

// viewHover places the tooltip under the caret, flipping above it near the bottom edge —
// the same anchor arithmetic the completion list uses, because they are answering the
// same question about the same caret.
func (s *homeScreen) viewHover(sh *core.Shared, body string) string {
	if s.hover.popup == nil || s.hover.path != s.currentPath {
		return body
	}
	x, absoluteY, visible := s.editor.CursorAnchor()
	if !visible {
		return body
	}
	y := absoluteY - sh.BodyY()
	s.hover.popup.Placement = components.PlacePopupAt(components.PopupAnchor{
		X: x, Y: y + 1, FlipX: x + 1, FlipY: y,
	})
	return s.hover.popup.ViewOver(body, s.w, s.h)
}
