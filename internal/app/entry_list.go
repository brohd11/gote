package app

import (
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/brohd11/bubblestack/components"
	"github.com/brohd11/bubblestack/core"
	"github.com/charmbracelet/x/ansi"
)

// entryList is a bottom-dock panel listing selectable entries grouped under file headings.
// Selection moves in entries and scrolling in wrapped text rows; owners maps each row back
// to its entry (-1 for status, headings and gaps) so a click on a continuation row works.
type entryList[T any] struct {
	*components.ScrollContainer
	entries        []T
	lines          []string
	owners, starts []int
	headings       []bool
	selected       int
	width, height  int
	status         string
	onSelect       func(*core.Shared, T) core.Action

	group        func(T) string // the heading an entry sits under
	heading      func(group string) string
	row          func(T) string
	title        func() string // optional; re-read on every reflow
	statusGap    bool          // a blank row between the status line and the entries
	mutedHeading bool
}

func newEntryList[T any](title string, pick func(*core.Shared, T) core.Action) entryList[T] {
	l := entryList[T]{ScrollContainer: components.NewScrollContainer(title), selected: -1, onSelect: pick}
	l.SetKeyHints(false)
	return l
}

func (p *entryList[T]) SetSize(width, height int) {
	changed := width != p.width
	p.width, p.height = width, height
	p.ScrollContainer.SetSize(width, height)
	if changed {
		p.reflow()
	}
}

func (p *entryList[T]) reflow() {
	offset := p.ScrollOffset()
	p.lines, p.owners, p.starts, p.headings = nil, nil, nil, nil
	width := max(1, min(p.TextWidth(), p.width-4))
	appendText := func(text string, owner int, heading bool) {
		for _, line := range strings.Split(ansi.Hardwrap(ansi.Wrap(ansi.Strip(text), width, ""), width, true), "\n") {
			p.lines = append(p.lines, line)
			p.owners = append(p.owners, owner)
			p.headings = append(p.headings, heading)
		}
	}
	if p.status != "" {
		appendText(p.status, -1, false)
		if p.statusGap && len(p.entries) > 0 {
			appendText("", -1, false)
		}
	}
	last := ""
	for i, entry := range p.entries {
		if g := p.group(entry); i == 0 || g != last {
			if i > 0 {
				appendText("", -1, false)
			}
			appendText(p.heading(g), -1, true)
			last = g
		}
		p.starts = append(p.starts, len(p.lines))
		appendText(p.row(entry), i, false)
	}
	if p.title != nil {
		p.SetTitle(p.title())
	}
	p.paint()
	p.ScrollTo(offset)
}

func (p *entryList[T]) paint() {
	lines := append([]string(nil), p.lines...)
	selected := lipgloss.NewStyle().Reverse(true)
	for i, owner := range p.owners {
		if p.mutedHeading && p.headings[i] {
			lines[i] = core.MutedStyle().Render(lines[i])
		}
		if owner >= 0 && owner == p.selected {
			lines[i] = selected.Render(lines[i])
		}
	}
	p.SetLines(lines)
}

func (p *entryList[T]) selectEntry(index int) {
	if len(p.entries) == 0 {
		return
	}
	p.selected = max(0, min(index, len(p.entries)-1))
	p.paint()
	row := p.starts[p.selected]
	if row < p.ScrollOffset() || row >= p.ScrollOffset()+p.VisibleRows() {
		p.ScrollTo(row)
	}
}

func (p *entryList[T]) activate(sh *core.Shared) core.Action {
	if p.selected < 0 || p.selected >= len(p.entries) || p.onSelect == nil {
		return core.Action{}
	}
	return p.onSelect(sh, p.entries[p.selected])
}

func (p *entryList[T]) UpdatePanel(sh *core.Shared, msg tea.Msg) (core.Action, bool) {
	if !p.Focused() {
		return core.Action{}, false
	}
	if click, ok := msg.(tea.MouseClickMsg); ok {
		// Inside the frame's content area (ContentRect), whatever frame the host set.
		cx, cy, cw, ch := p.ContentRect()
		if click.Button == tea.MouseLeft && click.Mod == 0 && click.X >= cx && click.X < cx+cw && click.Y >= cy && click.Y < cy+ch {
			row := click.Y - cy + p.ScrollOffset()
			if row >= 0 && row < len(p.owners) && p.owners[row] >= 0 && click.X-cx < ansi.StringWidth(p.lines[row]) {
				p.selectEntry(p.owners[row])
				return p.activate(sh), true
			}
		}
		return core.Action{}, true
	}
	if km, ok := msg.(tea.KeyPressMsg); ok {
		k := km.String()
		switch {
		case core.MatchKey(k, core.Keys.Up):
			p.selectEntry(p.selected - 1)
		case core.MatchKey(k, core.Keys.Down):
			p.selectEntry(p.selected + 1)
		case k == "home" || core.MatchKey(k, core.Keys.Top):
			p.selectEntry(0)
		case k == "end" || core.MatchKey(k, core.Keys.Bottom):
			p.selectEntry(len(p.entries) - 1)
		case core.MatchKey(k, core.Keys.Select):
			return p.activate(sh), true
		default:
			return p.ScrollContainer.UpdatePanel(sh, msg)
		}
		return core.Action{}, true
	}
	return p.ScrollContainer.UpdatePanel(sh, msg)
}

func (p *entryList[T]) PanelHelp() []key.Binding {
	return []key.Binding{core.Hint("jump", core.Keys.Select)}
}
