package app

import (
	"fmt"
	"sort"
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/brohd11/bubblestack/components"
	"github.com/brohd11/bubblestack/core"
	"github.com/charmbracelet/x/ansi"
	"go.lsp.dev/protocol"
)

type diagnosticEntry struct {
	path       string
	diagnostic lspDiagnostic
}

// diagnosticsPanel owns selection in diagnostic units and scrolling in text rows.
// Wrapped continuation rows map back to the same entry for mouse activation.
type diagnosticsPanel struct {
	*components.ScrollContainer
	entries                []diagnosticEntry
	lines                  []string
	owners, starts         []int
	selected               int
	width, height          int
	status                 string
	onSelect               func(*core.Shared, diagnosticEntry) core.Action
	ctx                    *Ctx
	manager                *lspManager
	revision, openRevision uint64
	current                string
	loaded                 bool
}

func newDiagnosticsPanel(pick func(*core.Shared, diagnosticEntry) core.Action) *diagnosticsPanel {
	p := &diagnosticsPanel{ScrollContainer: components.NewScrollContainer("Diagnostics"), selected: -1, onSelect: pick}
	p.SetKeyHints(false)
	return p
}

func (p *diagnosticsPanel) refresh(c *Ctx, current string) {
	var revision uint64
	if c.lsp != nil {
		revision = c.lsp.DiagnosticsRevision()
	}
	if p.loaded && p.ctx == c && p.manager == c.lsp && p.revision == revision && p.openRevision == c.open.revision && p.current == current {
		return
	}
	p.loaded, p.ctx, p.manager = true, c, c.lsp
	p.revision, p.openRevision, p.current = revision, c.open.revision, current
	var old diagnosticEntry
	hadSelection := p.selected >= 0 && p.selected < len(p.entries)
	if hadSelection {
		old = p.entries[p.selected]
	}
	p.entries, p.status = collectDiagnostics(c, current)
	p.selected = min(max(p.selected, 0), len(p.entries)-1)
	if hadSelection {
		for i, entry := range p.entries {
			if entry == old {
				p.selected = i
				break
			}
		}
	}
	p.reflow()
}

func collectDiagnostics(c *Ctx, current string) ([]diagnosticEntry, string) {
	if c == nil || c.lsp == nil {
		return nil, "Language-server support is disabled (auto-lsp: false)."
	}
	paths := make([]string, 0, len(c.open.byPath))
	for path := range c.open.byPath {
		paths = append(paths, path)
	}
	sort.Slice(paths, func(i, j int) bool {
		if paths[i] == current {
			return paths[j] != current
		}
		if paths[j] == current {
			return false
		}
		return paths[i] < paths[j]
	})
	var entries []diagnosticEntry
	for _, path := range paths {
		diagnostics := c.lsp.Diagnostics(path)
		sort.SliceStable(diagnostics, func(i, j int) bool {
			a, b := diagnostics[i], diagnostics[j]
			if a.Line != b.Line {
				return a.Line < b.Line
			}
			if a.Character != b.Character {
				return a.Character < b.Character
			}
			return normalizedSeverity(a.Severity) < normalizedSeverity(b.Severity)
		})
		for _, diagnostic := range diagnostics {
			entries = append(entries, diagnosticEntry{path, diagnostic})
		}
	}
	if len(entries) == 0 {
		return nil, "No diagnostics for open files."
	}
	return entries, ""
}

func (p *diagnosticsPanel) SetSize(width, height int) {
	changed := width != p.width
	p.width, p.height = width, height
	p.ScrollContainer.SetSize(width, height)
	if changed {
		p.reflow()
	}
}

func (p *diagnosticsPanel) reflow() {
	offset := p.ScrollOffset()
	p.lines, p.owners, p.starts = nil, nil, nil
	width := max(1, min(p.TextWidth(), p.width-4))
	appendText := func(text string, owner int) {
		for _, line := range strings.Split(ansi.Hardwrap(ansi.Wrap(ansi.Strip(text), width, ""), width, true), "\n") {
			p.lines = append(p.lines, line)
			p.owners = append(p.owners, owner)
		}
	}
	if p.status != "" {
		appendText(p.status, -1)
	}
	last := ""
	for i, entry := range p.entries {
		if entry.path != last {
			if i > 0 {
				appendText("", -1)
			}
			appendText(entry.path, -1)
			last = entry.path
		}
		d := entry.diagnostic
		meta := severityName(d.Severity)
		if d.Source != "" {
			meta += " · " + d.Source
		}
		if d.Code != "" {
			meta += " " + d.Code
		}
		p.starts = append(p.starts, len(p.lines))
		appendText(fmt.Sprintf("%d:%d  %s  %s", d.Line+1, d.Character+1, meta, strings.TrimSpace(d.Message)), i)
	}
	p.SetTitle(fmt.Sprintf("Diagnostics (%d)", len(p.entries)))
	p.paint()
	p.ScrollTo(offset)
}

func (p *diagnosticsPanel) paint() {
	lines := append([]string(nil), p.lines...)
	selected := lipgloss.NewStyle().Reverse(true)
	for i, owner := range p.owners {
		if owner >= 0 && owner == p.selected {
			lines[i] = selected.Render(lines[i])
		}
	}
	p.SetLines(lines)
}

func (p *diagnosticsPanel) selectEntry(index int) {
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

func (p *diagnosticsPanel) activate(sh *core.Shared) core.Action {
	if p.selected < 0 || p.selected >= len(p.entries) || p.onSelect == nil {
		return core.Action{}
	}
	return p.onSelect(sh, p.entries[p.selected])
}

func (p *diagnosticsPanel) UpdatePanel(sh *core.Shared, msg tea.Msg) (core.Action, bool) {
	if !p.Focused() {
		return core.Action{}, false
	}
	if click, ok := msg.(tea.MouseClickMsg); ok {
		if click.Button == tea.MouseLeft && click.Mod == 0 && click.X >= 2 && click.X < p.width-2 && click.Y >= 1 && click.Y < p.height-1 {
			row := click.Y - 1 + p.ScrollOffset()
			if row >= 0 && row < len(p.owners) && p.owners[row] >= 0 && click.X-2 < ansi.StringWidth(p.lines[row]) {
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

func (p *diagnosticsPanel) PanelHelp() []key.Binding {
	return []key.Binding{
		core.Hint("diagnostic", core.Keys.Up, core.Keys.Down),
		core.Hint("jump", core.Keys.Select),
		key.NewBinding(key.WithKeys("home", "end"), key.WithHelp("home/end", "first/last diagnostic")),
		key.NewBinding(key.WithKeys("pgup", "pgdown"), key.WithHelp("pgup/pgdown", "scroll message")),
		key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "focus editor")),
	}
}

func (s *homeScreen) toggleBottom(sh *core.Shared) core.Action {
	s.closeCompletion()
	s.closeHover()
	s.saveResize(s.modular.ResizeState())
	focus := s.editorSlot()
	// Preserve pane identity before rebuilding; the bottom is appended after all
	// upper panes, so their flat indexes are unchanged by this toggle.
	if s.sidebar {
		if f, ok := s.docsPane().(components.Focusable); ok && f.Focused() {
			focus = 0
		}
		if s.openPanel.Focused() {
			focus = 1
		}
	}
	if s.previewTarget() != nil && s.previewPanel.Focused() {
		focus = s.editorSlot() + 1
	}
	s.bottomVisible = !s.bottomVisible
	cmd := s.rebuildModular(sh, focus)
	s.previewAt = -1
	s.refreshPreview()
	s.syncPreviewScroll()
	s.refreshDiagnostics()
	return core.Async(cmd)
}

func (s *homeScreen) refreshDiagnostics() {
	if s.bottomVisible && s.sh != nil {
		s.diagnostics.refresh(Of(s.sh), s.currentPath)
	}
}

func (s *homeScreen) activateDiagnostic(sh *core.Shared, entry diagnosticEntry) core.Action {
	// An LSP range stays in protocol coordinates until the destination buffer is
	// available; the shared jump path handles Unicode and return navigation.
	d := entry.diagnostic
	preview := s.closeFullPreview()
	jump := s.jumpToLocation(sh, lspLocation{Path: entry.path, Range: protocol.Range{
		Start: protocol.Position{Line: d.Line, Character: d.Character},
		End:   protocol.Position{Line: d.EndLine, Character: d.EndCharacter},
	}})
	focus := core.Async(s.modular.FocusSlot(s.editorSlot()))
	return core.Seq(preview, jump, focus)
}
