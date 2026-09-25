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
	"go.lsp.dev/protocol"
)

const searchResultLimit = 10_000

// ctrl+alt+f pairs with ctrl+f (this buffer / every file). It needs option-as-meta, like
// every alt chord.
var findFilesKey = key.NewBinding(key.WithKeys("ctrl+alt+f"), key.WithHelp("ctrl+alt+f", "find in files"))

type searchEntry struct {
	path       string
	line       uint32
	column     int
	start, end uint32 // UTF-16 columns, ready for the shared jump path
	text       string
}

type searchPanel struct {
	entryList[searchEntry]
	root string
}

func newSearchPanel(pick func(*core.Shared, searchEntry) core.Action) *searchPanel {
	p := &searchPanel{entryList: newEntryList("Search", pick)}
	p.status = "No search run."
	p.statusGap, p.mutedHeading = true, true
	p.group = func(e searchEntry) string { return e.path }
	p.heading = func(path string) string {
		if rel, err := filepath.Rel(p.root, path); err == nil {
			return rel
		}
		return path
	}
	p.row = func(e searchEntry) string {
		return fmt.Sprintf("%d:%d  %s", e.line+1, e.column+1, strings.TrimSpace(e.text))
	}
	p.reflow()
	return p
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
