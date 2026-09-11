package app

import (
	"fmt"
	"path/filepath"
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

// collectDiagnostics gathers what the panel lists. Which files that is IS the
// diagnostic_open_only setting: the open buffers, or every file the servers have reported
// on — which, with the whole project handed over, is the project. Either way the file
// showing in the editor sorts first, since it is the one being asked about.
func collectDiagnostics(c *Ctx, current string) ([]diagnosticEntry, string) {
	if c == nil || c.lsp == nil {
		return nil, "Language-server support is disabled (auto-lsp: false)."
	}
	byPath := c.lsp.AllDiagnostics()
	paths := make([]string, 0, len(byPath))
	for path := range byPath {
		paths = append(paths, path)
	}
	sort.Slice(paths, func(i, j int) bool {
		if sameFilePath(paths[i], current) {
			return !sameFilePath(paths[j], current)
		}
		if sameFilePath(paths[j], current) {
			return false
		}
		return paths[i] < paths[j]
	})
	// The map is keyed for lookup, not for display: diagnosticsKey folds the drive letter
	// a file URI hands back on Windows. An entry has to name its file the way the rest of
	// gote spells it, or activating one opens a second buffer for a file that already has
	// a tab.
	spelled := map[string]string{}
	for _, doc := range c.OpenDocs() {
		if doc.Path != "" {
			spelled[diagnosticsKey(doc.Path)] = doc.Path
		}
	}
	var entries []diagnosticEntry
	for _, path := range paths {
		diagnostics := byPath[path]
		if open, ok := spelled[path]; ok {
			path = open
		}
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
		return nil, "No diagnostics."
	}
	return entries, ""
}

// projectPath is how a diagnostic's file is headed in the list. Absolute paths are what
// the manager keys on, but a whole project's worth of them is a column of identical
// prefixes; against a live session root the same list reads as the project's own layout.
// A path under no root — a buffer opened from somewhere else entirely — stays absolute,
// which is the honest answer for a file that is not part of the project.
func projectPath(roots []string, path string) string {
	best, relative := "", ""
	for _, root := range roots {
		if len(root) <= len(best) {
			continue
		}
		// Rel, not a prefix test: on Windows it compares case-insensitively, and the two
		// spellings of a path there routinely differ in the drive letter alone. See
		// diagnosticsKey.
		rel, err := filepath.Rel(root, path)
		if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			continue
		}
		best, relative = root, rel
	}
	if best == "" {
		return path
	}
	return relative
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
	var roots []string
	if p.manager != nil {
		roots = p.manager.Roots()
	}
	last := ""
	for i, entry := range p.entries {
		if entry.path != last {
			if i > 0 {
				appendText("", -1)
			}
			appendText(projectPath(roots, entry.path), -1)
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
	return []key.Binding{core.Hint("jump", core.Keys.Select)}
}

func (s *homeScreen) toggleBottom(sh *core.Shared) core.Action {
	s.closeCompletion()
	s.closeHover()
	s.saveResize(s.modular.ResizeState())
	focus := s.focusedPane()
	s.bottomVisible = !s.bottomVisible
	s.rebuildModular(sh, noFocus)
	if s.panelSlot(focus) == noFocus {
		focus = s.editorPanel
	}
	cmd := s.modular.FocusSlot(s.panelSlot(focus))
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
