package app

import (
	"math"
	"os"
	"path/filepath"
	"strings"

	"github.com/brohd11/bubblestack/components"
	"github.com/brohd11/bubblestack/components/editor"
	"github.com/brohd11/bubblestack/core"
	"github.com/brohd11/bubblestack/sysopen"

	tea "charm.land/bubbletea/v2"
)

// The markdown preview: each doc's mode (docmode.go) — the reader in the editor's pane
// and the live render, cycled by ctrl+p — and the side preview (alt+p), a chrome column that follows the
// active doc whatever its mode.

// previewable reports whether the current doc can be rendered (previewablePath).
func (s *homeScreen) previewable() bool { return previewablePath(s.currentPath) }

// enforcePreview drops the modes a doc no longer qualifies for, wherever currentPath
// moves (a rename out of markdown). It returns paneChild's cmd for the caller to batch.
func (s *homeScreen) enforcePreview() tea.Cmd {
	if s.previewable() || s.editor == nil {
		return nil
	}
	if s.sh != nil {
		Of(s.sh).setReader(s.editor, false)
	}
	s.editor.SetLiveRender(false)
	if s.fullPreview == nil {
		return nil
	}
	return s.paneChild()
}

// toggleSidePreview shows or hides the side preview column, keeping focus where it was.
func (s *homeScreen) toggleSidePreview(sh *core.Shared) core.Action {
	return core.Async(s.togglePanes(sh, func() {
		s.sidePreview = !s.sidePreview
		s.resetPreviewCache()
	}))
}

// previewScreen builds the reader: a DocScreen hosted as the editor pane's child
// (not pushed), so the sidebar stays usable beside it. It renders the live buffer, bound
// here: a doc switch builds a new reader (paneChild).
func (s *homeScreen) previewScreen() *components.DocScreen {
	s.readerEditor = s.editor
	src := s.editor.Text
	return components.NewDocScreen(components.DocOpts{
		// Document first, mode second — the editor's own title bar is the filename, and
		// the reader is a view of the same document, so the two bars line up.
		Title:  s.readerTitle(),
		Render: func(width int) string { return components.RenderMarkdown(src(), width) },
		// Bound with the buffer for the same reason: relative links resolve against this
		// document's directory.
		Links: s.previewLinks(),
		// No Help or OnKey: the host bar names the way out, and the home screen claims ctrl+p and
		// esc before the pane.
	})
}

// closeFullPreview takes the current doc out of Reader, back to its editor exactly as it
// was: the editor's Init is idempotent, so the buffer and history survive.
func (s *homeScreen) closeFullPreview() core.Action {
	if s.fullPreview == nil {
		return core.Action{}
	}
	return s.setReader(false)
}

// setReader marks the current doc in or out of Reader and swaps the pane's child.
func (s *homeScreen) setReader(on bool) core.Action {
	Of(s.sh).setReader(s.editor, on)
	cmd := s.paneChild()
	s.relayout()
	return core.Async(cmd)
}

// paneChild points the editor pane at the current document: its reader when the doc is in
// Reader (rebuilt on a doc switch, since a reader is bound to one editor), else its editor.
func (s *homeScreen) paneChild() tea.Cmd {
	s.editor.SetTitleVisible(!s.tabsVisible())
	if !s.readerOn() {
		s.fullPreview, s.readerEditor = nil, nil
		return s.editorPanel.SetChild(s.editor)
	}
	if s.fullPreview != nil && s.readerEditor == s.editor {
		return nil
	}
	s.fullPreview = s.previewScreen()
	return s.editorPanel.SetChild(s.fullPreview)
}

// seedForPreview loads a buffer from disk that will open in the reader and never read its
// file: the editor's async load would go to the reader and be lost, leaving an empty buffer
// the first save would truncate the file with. Unsaved edits are never overwritten.
func (s *homeScreen) seedForPreview(ed *editor.Screen, path string, unread bool) {
	if unread && previewablePath(path) && Of(s.sh).inReader(ed) {
		_ = ed.LoadFile()
	}
}

// previewLinks is what a link click does in either preview. Text files open as buffers
// (openDoc); URLs go to the browser and other files are revealed in the file manager. A
// link to a missing file does nothing, rather than creating it on first save.
func (s *homeScreen) previewLinks() components.LinkHooks {
	owner := s.editorGroup
	return components.LinkHooks{
		Base: s.previewDir(),
		URL:  func(_ *core.Shared, l components.Link) core.Action { return sysopen.URL(l.Target) },
		File: func(_ *core.Shared, l components.Link) core.Action { return sysopen.Path(l.Path, true) },
		Text: func(sh *core.Shared, l components.Link) core.Action {
			if !l.Exists {
				return core.Action{}
			}
			s.activateGroup(owner)
			// A followed link is reading, so the target shows in the reader — even if it
			// was already open in another mode.
			return s.openDocAs(sh, l.Path, true)
		},
	}
}

// previewDir is where relative links resolve: the document's directory, or the cwd for an
// unsaved buffer.
func (s *homeScreen) previewDir() string {
	if s.currentPath != "" {
		return filepath.Dir(s.currentPath)
	}
	wd, err := os.Getwd()
	if err != nil {
		return ""
	}
	return wd
}

// previewName labels the preview with the saved filename or unsaved_N identity.
func (s *homeScreen) previewName() string {
	if s.currentPath == "" {
		if s.currentName != "" {
			return s.currentName
		}
		return "scratch"
	}
	return docName(s.currentPath)
}

// relayout rebuilds the layout for the preview flags and re-seeds the side pane, focusing
// the editor (whose on-focus cmd is empty, so it is dropped).
func (s *homeScreen) relayout() {
	_ = s.rebuildGroups(s.sh)
	s.resetPreviewCache()
	s.refreshPreview()
	s.syncPreviewScroll()
}

// previewTarget answers the side preview, or nil when it is hidden.
func (s *homeScreen) previewTarget() *components.ScrollContainer {
	if s.sidePreview {
		return s.previewPanel
	}
	return nil
}

// noPreview is what the side preview shows for a doc it cannot render, so switching to one
// leaves the layout alone.
const noPreview = "Could not preview"

// refreshPreview re-renders the side pane when the buffer or its width changed, once per
// message from Update rather than per frame in View. The mapped render keeps scroll sync
// exact.
func (s *homeScreen) refreshPreview() {
	panel := s.previewTarget()
	if panel == nil || s.editor == nil {
		return
	}
	src, width := s.editor.Text(), panel.TextWidth()
	if !s.previewable() {
		// The NUL keeps the sentinel from matching any buffer's text.
		src = "\x00" + noPreview
	}
	if src == s.previewSrc && width == s.previewW {
		return
	}
	if !s.previewable() {
		s.previewSrc, s.previewW, s.previewMap = src, width, nil
		s.previewAt = -1
		pad := max(0, (width-len(noPreview))/2)
		panel.SetLines([]string{"", strings.Repeat(" ", pad) + core.MutedStyle().Render(noPreview)})
		panel.SetLinks(nil)
		return
	}
	out, mapped := components.RenderMarkdownMapped(src, width)
	s.previewSrc, s.previewW, s.previewMap = src, width, mapped
	s.previewAt = -1 // the rows the last sync was computed against are gone
	panel.SetLines(strings.Split(out, "\n"))
	// Beside SetLines, never apart from it: the spans index THESE rows, and the next
	// keystroke re-flows them.
	panel.SetLinks(components.ScanLinks(out))
}

// syncPreviewScroll keeps the side pane following the editor. The render and the source
// differ in row counts, so through the body the editor's middle line is aligned with the
// pane's middle row (via RenderMarkdownMapped); within a screenful of either end the
// target eases to that end, or the pane could never reach its bottom. It only runs when
// the editor's offset moves, so a manually scrolled pane is left alone.
func (s *homeScreen) syncPreviewScroll() {
	panel := s.previewTarget()
	if panel == nil || s.editor == nil {
		return
	}
	off, maxOff, editorRows := s.editor.ScrollSpan()
	if off == s.previewAt {
		return
	}
	s.previewAt = off
	if len(s.previewMap) == 0 {
		return
	}

	// The anchor: the center line's row, placed at the pane's middle. The min guards a
	// map from a render one keystroke behind the buffer.
	center := s.editor.CenterLine()
	anchored := s.previewMap[min(center, len(s.previewMap)-1)] - panel.VisibleRows()/2

	// ends is the pane offset at an editor end; w blends toward it (smoothstep near the ends).
	w, ends := 1.0, 0.0
	if maxOff > 0 {
		band := float64(max(editorRows, 1))
		w = 1 - min(float64(min(off, maxOff-off))/band, 1)
		w = w * w * (3 - 2*w)
		ends = float64(off) / float64(maxOff) * float64(panel.MaxScrollOffset())
	}
	// ScrollTo clamps, so nothing here has to.
	panel.ScrollTo(int(math.Round((1-w)*float64(anchored) + w*ends)))
}

// resetPreviewCache forgets the last render so a reopened pane renders instead of staying
// blank, and re-syncs its scroll.
func (s *homeScreen) resetPreviewCache() {
	s.previewSrc, s.previewW, s.previewMap = "", 0, nil
	s.previewAt = -1
}
