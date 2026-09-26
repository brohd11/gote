package app

import (
	"fmt"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/brohd11/bubblestack/components"
	"github.com/brohd11/bubblestack/components/editor"
	"github.com/brohd11/bubblestack/core"
	"github.com/brohd11/gitstack/repo"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

var gitDiffKey = altShiftKey("d", "diff at cursor")

// gitDiffUI is also the identity of an opening request. Dropping it cancels any
// pending baseline read without allowing a late result to reopen the popup.
type gitDiffUI struct {
	editor *editor.Screen
	path   string
	seq    int
	caret  editor.Position
	line   int
	x, y   int // absolute anchor, distinct from the caret for gutter clicks

	popup         *components.FloatingPopup
	hunk          diffHunk
	rows          []diffLine
	width, height int
	top           int
}

type gitDiffBaselineMsg struct {
	target *homeScreen
	ui     *gitDiffUI
	base   string
	state  repo.Baseline
	err    error
}

func (s *homeScreen) closeGitDiff() { s.gitDiff = nil }

func (s *homeScreen) dismissInvalidGitDiff() {
	p := s.gitDiff
	if p == nil {
		return
	}
	if p.editor != s.editor || p.path != s.currentPath || p.seq != s.editor.EditSeq() ||
		p.caret != s.editor.CursorPosition() || !s.editorPanel.Focused() || s.fullPreview != nil ||
		s.completion.popup != nil || s.hover.popup != nil || s.signature.popup != nil {
		s.closeGitDiff()
	}
}

func (s *homeScreen) gitSignClick(sh *core.Shared, hit editor.SignClick) (core.Action, bool) {
	if hit.Column != gitSignColumn {
		return core.Action{}, false
	}
	return s.openGitDiff(sh, hit.Line, hit.X, hit.Y), true
}

func (s *homeScreen) openGitDiff(sh *core.Shared, line, x, y int) core.Action {
	s.closeGitDiff()
	s.closeCompletion()
	s.closeHover()
	s.closeSignature()
	if s.currentPath == "" {
		return core.SetStatus("No git diff available for this buffer")
	}
	if s.w < panelChrome+4 || s.h < 6 {
		return core.SetStatus("Window too small for git diff")
	}
	p := &gitDiffUI{
		editor: s.editor, path: s.currentPath, seq: s.editor.EditSeq(),
		caret: s.editor.CursorPosition(), line: line, x: x, y: y,
	}
	s.gitDiff = p
	if s.gutter.path == p.path {
		return s.installGitDiff(sh, s.gutter.base, s.gutter.state)
	}
	// A hidden gutter intentionally drops its baseline. Keyboard inspection still
	// works, without turning the column on or changing its refresh lifecycle.
	return core.Async(func() tea.Msg {
		base, state, err := readGitBaseline(p.path)
		return core.PropagateAll(gitDiffBaselineMsg{target: s, ui: p, base: base, state: state, err: err})
	})
}

func (s *homeScreen) applyGitDiffBaseline(sh *core.Shared, msg gitDiffBaselineMsg) core.Action {
	s.dismissInvalidGitDiff()
	if msg.target != s || s.gitDiff == nil || msg.ui != s.gitDiff {
		return core.Action{}
	}
	if msg.err != nil {
		s.closeGitDiff()
		return core.SetStatus("Cannot read git diff: " + msg.err.Error())
	}
	return s.installGitDiff(sh, msg.base, msg.state)
}

func (s *homeScreen) installGitDiff(sh *core.Shared, base string, state repo.Baseline) core.Action {
	p := s.gitDiff
	if state != repo.BaselineOK && state != repo.BaselineAbsent {
		s.closeGitDiff()
		return core.SetStatus("No git diff available for this file")
	}
	buf := s.editor.Text()
	if state == repo.BaselineAbsent {
		base = ""
	}
	hunks := diffHunks(base, buf)
	if state == repo.BaselineAbsent && buf == "" {
		hunks = []diffHunk{{lines: []diffLine{{' ', "(empty new file)"}}}}
	}
	for _, hunk := range hunks {
		if !hunk.contains(p.line, bufLines(buf)) {
			continue
		}
		p.hunk = hunk
		p.width = min(80, s.w-panelChrome)
		for _, line := range hunk.lines {
			for _, row := range strings.Split(ansi.Hardwrap(diffDisplayText(line.text), p.width-2, true), "\n") {
				p.rows = append(p.rows, diffLine{line.kind, row})
			}
		}
		p.height = min(18, s.h-5, len(p.rows)) // border, title, hunk header, footer
		// Keep the anchor row exposed whenever either side can hold a panel. A
		// tall hunk should scroll inside its box rather than cover its own marker.
		anchorY := p.y - sh.BodyY()
		if room := max(anchorY, s.h-anchorY-1); room >= 6 {
			p.height = min(p.height, room-5)
		}
		p.popup = &components.FloatingPopup{Content: p.view}
		s.placeGitDiff(sh)
		return core.Action{}
	}
	s.closeGitDiff()
	return core.SetStatus("No change at cursor")
}

// File content is plain text, never terminal control sequences. Expand tabs like
// the editor, and make other controls visible without interpreting them as ANSI.
func diffDisplayText(text string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return '�'
		}
		return r
	}, strings.ReplaceAll(text, "\t", "    "))
}

func (p *gitDiffUI) view() string {
	clip := func(text string) string { return ansi.Truncate(text, p.width, "…") }
	lines := []string{
		core.AccentStyle().Render(clip(diffDisplayText(filepath.Base(p.path)) + " · HEAD → buffer")),
		core.MutedStyle().Render(clip(p.hunk.header())),
	}
	for _, row := range p.rows[p.top : p.top+p.height] {
		style := lipgloss.NewStyle()
		switch row.kind {
		case '-':
			style = style.Foreground(lipgloss.Color("1"))
		case '+':
			style = style.Foreground(lipgloss.Color("2"))
		case '\\':
			style = core.MutedStyle()
		}
		lines = append(lines, style.Render(string(row.kind)+" "+row.text))
	}
	footer := fmt.Sprintf("↑↓/PgUp/PgDn scroll · Esc close · %d–%d/%d", p.top+1, p.top+p.height, len(p.rows))
	lines = append(lines, core.MutedStyle().Render(clip(footer)))
	return components.PopupPanel(strings.Join(lines, "\n"), p.width)
}

// Rendering and mouse routing use the same placement, including frame clamping.
func (s *homeScreen) placeGitDiff(sh *core.Shared) (x, y, w, h int) {
	p := s.gitDiff
	w, h = p.width+panelChrome, p.height+5
	x, y = caretPanel(p.x, p.y-sh.BodyY(), s.editorLeft(), false)(s.w, s.h, w, h)
	x, y = max(0, min(x, s.w-w)), max(0, min(y, s.h-h))
	p.popup.Placement = func(int, int, int, int) (int, int) { return x, y }
	return x, y + sh.BodyY(), w, h
}

func (s *homeScreen) gitDiffContains(sh *core.Shared, x, y int) bool {
	if s.gitDiff == nil || s.gitDiff.popup == nil {
		return false
	}
	px, py, w, h := s.placeGitDiff(sh)
	return x >= px && x < px+w && y >= py && y < py+h
}

// Offer input before the tabs, completion and panes. Dismissal passes new intent
// through; scrolling the popup and Esc are the only modal keys.
func (s *homeScreen) gitDiffInput(sh *core.Shared, msg tea.Msg) (core.Action, bool) {
	s.dismissInvalidGitDiff()
	if k, ok := msg.(tea.KeyPressMsg); ok && core.MatchKey(k.String(), gitDiffKey) &&
		s.editorPanel.Focused() && s.fullPreview == nil && !s.modular.Resizing() {
		if s.gitDiff != nil {
			s.closeGitDiff()
			return core.Action{}, true
		}
		x, y, visible := s.editor.CursorAnchor()
		if !visible {
			return core.SetStatus("Cursor is outside the editor viewport"), true
		}
		return s.openGitDiff(sh, s.editor.CursorPosition().Line, x, y), true
	}
	p := s.gitDiff
	if p == nil {
		return core.Action{}, false
	}
	scroll := func(delta int) (core.Action, bool) {
		p.top = max(0, min(p.top+delta, len(p.rows)-p.height))
		return core.Action{}, true
	}
	switch m := msg.(type) {
	case tea.KeyPressMsg:
		switch m.String() {
		case "esc":
			s.closeGitDiff()
			return core.Action{}, true
		case "up":
			return scroll(-1)
		case "down":
			return scroll(1)
		case "pgup":
			return scroll(-max(1, p.height-1))
		case "pgdown":
			return scroll(max(1, p.height-1))
		}
		s.closeGitDiff()
	case tea.MouseWheelMsg:
		if s.gitDiffContains(sh, m.X, m.Y) {
			switch m.Button {
			case tea.MouseWheelUp:
				return scroll(-3)
			case tea.MouseWheelDown:
				return scroll(3)
			}
			return core.Action{}, true
		}
		s.closeGitDiff()
	case tea.MouseClickMsg:
		if s.gitDiffContains(sh, m.X, m.Y) {
			return core.Action{}, true
		}
		s.closeGitDiff()
	case tea.PasteMsg, tea.BlurMsg:
		s.closeGitDiff()
	}
	return core.Action{}, false
}

func (s *homeScreen) viewGitDiff(sh *core.Shared, body string) string {
	s.dismissInvalidGitDiff()
	if s.gitDiff == nil || s.gitDiff.popup == nil {
		return body
	}
	s.placeGitDiff(sh)
	return s.gitDiff.popup.ViewOver(body, s.w, s.h)
}
