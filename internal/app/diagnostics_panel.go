package app

import (
	"fmt"
	"sort"
	"strings"

	"github.com/brohd11/bubblestack/core"
	"github.com/brohd11/goutil/strutil"
	"go.lsp.dev/protocol"
)

type diagnosticEntry struct {
	path       string
	diagnostic lspDiagnostic
}

// diagnosticsPanel lists the language servers' diagnostics, grouped by file.
type diagnosticsPanel struct {
	entryList[diagnosticEntry]
	ctx                    *Ctx
	manager                *lspManager
	revision, openRevision uint64
	current                string
	loaded                 bool
}

func newDiagnosticsPanel(pick func(*core.Shared, diagnosticEntry) core.Action) *diagnosticsPanel {
	p := &diagnosticsPanel{entryList: newEntryList("Diagnostics", pick)}
	p.title = func() string { return fmt.Sprintf("Diagnostics (%d)", len(p.entries)) }
	p.group = func(e diagnosticEntry) string { return e.path }
	p.heading = func(path string) string { return projectPath(p.manager.Roots(), path) }
	p.row = func(e diagnosticEntry) string {
		d := e.diagnostic
		meta := severityName(d.Severity)
		if d.Source != "" {
			meta += " · " + d.Source
		}
		if d.Code != "" {
			meta += " " + d.Code
		}
		return fmt.Sprintf("%d:%d  %s  %s", d.Line+1, d.Character+1, meta, strings.TrimSpace(d.Message))
	}
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

// collectDiagnostics gathers what the panel lists: open buffers or every reported file,
// per diagnostic_open_only, with the file in the editor first.
func collectDiagnostics(c *Ctx, current string) ([]diagnosticEntry, string) {
	if c == nil || c.lsp == nil {
		return nil, "Language-server support is disabled (" + lspDisabledReason(c) + ")."
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
	// Map lookup keys back to gote's own spelling of each path: diagnosticsKey folds Windows
	// drive letters, and an entry must open the existing buffer, not a second one.
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

// projectPath heads a diagnostic's file relative to the deepest session root containing
// it, or absolute when none does.
func projectPath(roots []string, path string) string {
	best, relative := "", ""
	for _, root := range roots {
		if len(root) <= len(best) {
			continue
		}
		// Rel, not a prefix test: Windows paths can differ in drive-letter case alone.
		rel, ok := strutil.RelUnder(root, path)
		if !ok || rel == "." {
			continue
		}
		best, relative = root, rel
	}
	if best == "" {
		return path
	}
	return relative
}

func (s *homeScreen) toggleBottom(sh *core.Shared) core.Action {
	cmd := s.togglePanes(sh, func() { s.bottomVisible = !s.bottomVisible })
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
	jump := s.jumpToLocation(sh, lspLocation{Path: entry.path, Range: protocol.Range{
		Start: protocol.Position{Line: d.Line, Character: d.Character},
		End:   protocol.Position{Line: d.EndLine, Character: d.EndCharacter},
	}})
	// After the jump: a target doc in Reader leaves it so the location shows.
	preview := s.closeFullPreview()
	focus := core.Async(s.modular.FocusSlot(s.editorSlot()))
	return core.Seq(jump, preview, focus)
}
