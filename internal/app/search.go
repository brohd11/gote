package app

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode"
	"unicode/utf16"

	"github.com/brohd11/bubblestack/components"
	"github.com/brohd11/bubblestack/components/editor"
	"github.com/brohd11/bubblestack/core"
	"github.com/brohd11/goutil/textfile"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"go.lsp.dev/protocol"
)

const searchResultLimit = 10_000

var findFilesKey = key.NewBinding(key.WithKeys("alt+shift+f"), key.WithHelp("alt+shift+f", "find in files"))

type searchEntry struct {
	path       string
	line       uint32
	column     int
	start, end uint32 // UTF-16 columns, ready for the shared jump path
	text       string
}

type searchPanel struct {
	*components.ScrollContainer
	entries        []searchEntry
	lines          []string
	owners, starts []int
	headings       []bool
	selected       int
	width, height  int
	root           string
	status         string
	onSelect       func(*core.Shared, searchEntry) core.Action
}

func newSearchPanel(pick func(*core.Shared, searchEntry) core.Action) *searchPanel {
	p := &searchPanel{ScrollContainer: components.NewScrollContainer("Search"), selected: -1, onSelect: pick, status: "No search run."}
	p.SetKeyHints(false)
	p.reflow()
	return p
}

func (p *searchPanel) SetSize(width, height int) {
	changed := width != p.width
	p.width, p.height = width, height
	p.ScrollContainer.SetSize(width, height)
	if changed {
		p.reflow()
	}
}

func (p *searchPanel) setSearching(query, root string) {
	p.entries, p.selected = nil, -1
	p.root = root
	p.status = fmt.Sprintf("Searching %s for %q…", root, query)
	p.SetTitle("Search · searching…")
	p.ScrollTo(0)
	p.reflow()
}

func (p *searchPanel) setResult(query, root string, entries []searchEntry, truncated bool, err error) {
	p.root, p.entries = root, entries
	p.selected = min(max(p.selected, 0), len(entries)-1)
	switch {
	case err != nil:
		p.status = "Search failed: " + err.Error()
		p.SetTitle("Search · error")
	case len(entries) == 0:
		p.status = fmt.Sprintf("No matches for %q.", query)
		p.SetTitle("Search (0)")
	default:
		p.status = ""
		p.SetTitle(fmt.Sprintf("Search (%d)", len(entries)))
		if truncated {
			p.status = fmt.Sprintf("Showing the first %d matches.", searchResultLimit)
		}
	}
	p.reflow()
}

func (p *searchPanel) reflow() {
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
		if len(p.entries) > 0 {
			appendText("", -1, false)
		}
	}
	last := ""
	for i, entry := range p.entries {
		if entry.path != last {
			if i > 0 {
				appendText("", -1, false)
			}
			name := entry.path
			if rel, err := filepath.Rel(p.root, entry.path); err == nil {
				name = rel
			}
			appendText(name, -1, true)
			last = entry.path
		}
		p.starts = append(p.starts, len(p.lines))
		appendText(fmt.Sprintf("%d:%d  %s", entry.line+1, entry.column+1, strings.TrimSpace(entry.text)), i, false)
	}
	p.paint()
	p.ScrollTo(offset)
}

func (p *searchPanel) paint() {
	lines := append([]string(nil), p.lines...)
	selected := lipgloss.NewStyle().Reverse(true)
	muted := lipgloss.NewStyle().Foreground(core.MutedColor)
	for i, owner := range p.owners {
		if p.headings[i] {
			lines[i] = muted.Render(lines[i])
		}
		if owner >= 0 && owner == p.selected {
			lines[i] = selected.Render(lines[i])
		}
	}
	p.SetLines(lines)
}

func (p *searchPanel) selectEntry(index int) {
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

func (p *searchPanel) activate(sh *core.Shared) core.Action {
	if p.selected < 0 || p.selected >= len(p.entries) || p.onSelect == nil {
		return core.Action{}
	}
	return p.onSelect(sh, p.entries[p.selected])
}

func (p *searchPanel) UpdatePanel(sh *core.Shared, msg tea.Msg) (core.Action, bool) {
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

func (p *searchPanel) PanelHelp() []key.Binding {
	return []key.Binding{core.Hint("jump", core.Keys.Select)}
}

type findFilesRequest struct {
	target      *homeScreen
	query, root string
}

type findFilesResult struct {
	target      *homeScreen
	generation  uint64
	query, root string
	entries     []searchEntry
	truncated   bool
	err         error
}

func (s *homeScreen) findFilesForm(sh *core.Shared) *components.FormScreen {
	root := docsRoot(Of(sh))
	if root == "" {
		root, _ = os.Getwd()
	}
	form := components.NewForm(components.FormOpts{
		Title: "Find in Files", Overlay: true, Width: 64, Focus: "query",
		Fields: []components.FormField{
			components.NewTextField("query", "Search: ", "text to find"),
			components.NewTextField("path", "Path:   ", root),
		},
		Help: []key.Binding{
			core.Hint("field", core.Keys.NextField, core.Keys.PrevField),
			core.Hint("search", core.Keys.Select),
			core.Hint("cancel", core.Keys.Back),
		},
		OnSubmit: func(sh *core.Shared, form *components.FormScreen) core.Action {
			query := form.Value("query")
			if strings.TrimSpace(query) == "" {
				return core.Async(form.Focus("query"))
			}
			root, err := resolveSearchRoot(Of(sh), form.Value("path"))
			if err != nil {
				return core.Push(errPopup("find in files", err))
			}
			s.searchQuery, s.searchPath = query, strings.TrimSpace(form.Value("path"))
			return core.Seq(core.Pop(), core.PropagateAll(findFilesRequest{target: s, query: query, root: root}))
		},
	})
	form.SetValue("query", s.searchQuery)
	form.SetValue("path", s.searchPath)
	return form
}

func resolveSearchRoot(c *Ctx, raw string) (string, error) {
	base := docsRoot(c)
	if base == "" {
		var err error
		base, err = os.Getwd()
		if err != nil {
			return "", err
		}
	}
	raw = strings.TrimSpace(raw)
	path := base
	if raw != "" {
		path = raw
		if !filepath.IsAbs(path) {
			path = filepath.Join(base, path)
		}
	}
	path, err := filepath.Abs(filepath.Clean(path))
	if err != nil {
		return "", err
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("%q is not a directory", path)
	}
	return path, nil
}

func (s *homeScreen) beginFindFiles(sh *core.Shared, request findFilesRequest) core.Action {
	if request.target != s {
		return core.Action{}
	}
	if s.searchCancel != nil {
		s.searchCancel()
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.searchCancel = cancel
	s.searchGeneration++
	generation := s.searchGeneration
	snapshots := make(map[string]string)
	Of(sh).EachDoc(func(path string, ed *editor.Screen) {
		snapshots[filepath.Clean(path)] = ed.Text()
	})
	s.search.setSearching(request.query, request.root)
	s.bottom.selectTab(bottomSearch)
	if !s.bottomVisible {
		s.saveResize(s.modular.ResizeState())
		s.bottomVisible = true
		s.rebuildModular(sh, noFocus)
	}
	focus := s.modular.FocusSlot(s.panelSlot(s.bottom))
	cmd := func() tea.Msg {
		entries, truncated, err := searchFiles(ctx, request.root, request.query, snapshots, searchResultLimit)
		return core.PropagateAll(findFilesResult{
			target: s, generation: generation, query: request.query, root: request.root,
			entries: entries, truncated: truncated, err: err,
		})
	}
	return core.Async(tea.Batch(focus, cmd))
}

func (s *homeScreen) finishFindFiles(result findFilesResult) {
	if result.target != s || result.generation != s.searchGeneration {
		return
	}
	if s.searchCancel != nil {
		s.searchCancel()
	}
	s.searchCancel = nil
	s.search.setResult(result.query, result.root, result.entries, result.truncated, result.err)
}

func (s *homeScreen) activateSearchResult(sh *core.Shared, entry searchEntry) core.Action {
	preview := s.closeFullPreview()
	jump := s.jumpToLocation(sh, lspLocation{Path: entry.path, Range: protocol.Range{
		Start: protocol.Position{Line: entry.line, Character: entry.start},
		End:   protocol.Position{Line: entry.line, Character: entry.end},
	}})
	focus := core.Async(s.modular.FocusSlot(s.editorSlot()))
	return core.Seq(preview, jump, focus)
}

func searchFiles(ctx context.Context, root, query string, snapshots map[string]string, limit int) ([]searchEntry, bool, error) {
	caseSensitive := strings.IndexFunc(query, unicode.IsUpper) >= 0
	var entries []searchEntry
	truncated := false
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil {
			if path == root {
				return err
			}
			return nil
		}
		if d.IsDir() {
			if path != root && (strings.HasPrefix(d.Name(), ".") || skipDirs[d.Name()]) {
				return fs.SkipDir
			}
			return nil
		}
		info, err := d.Info()
		if err != nil || !info.Mode().IsRegular() {
			return nil
		}
		clean := filepath.Clean(path)
		content, open := snapshots[clean]
		if !open {
			if !textfile.IsText(clean) {
				return nil
			}
			b, err := os.ReadFile(clean)
			if err != nil {
				return nil
			}
			content = string(b)
		}
		for lineNo, line := range strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n") {
			start, end, ok := literalMatch(line, query, caseSensitive)
			if !ok {
				continue
			}
			entries = append(entries, searchEntry{
				path: clean, line: uint32(lineNo), column: start, start: uint32(utf16Units([]rune(line)[:start])),
				end: uint32(utf16Units([]rune(line)[:end])), text: line,
			})
			if len(entries) > limit {
				truncated = true
				return fs.SkipAll
			}
		}
		return nil
	})
	if err != nil {
		return nil, false, err
	}
	if truncated {
		entries = entries[:limit]
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].path != entries[j].path {
			return entries[i].path < entries[j].path
		}
		return entries[i].line < entries[j].line
	})
	return entries, truncated, nil
}

func literalMatch(line, query string, caseSensitive bool) (start, end int, ok bool) {
	lineRunes, queryRunes := []rune(line), []rune(query)
	if len(queryRunes) == 0 || len(queryRunes) > len(lineRunes) {
		return 0, 0, false
	}
	for i := 0; i+len(queryRunes) <= len(lineRunes); i++ {
		candidate := string(lineRunes[i : i+len(queryRunes)])
		if candidate == query || !caseSensitive && strings.EqualFold(candidate, query) {
			return i, i + len(queryRunes), true
		}
	}
	return 0, 0, false
}

func utf16Units(runes []rune) int { return len(utf16.Encode(runes)) }
