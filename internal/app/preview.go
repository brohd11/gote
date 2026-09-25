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

// The markdown preview: the ctrl+p side pane and the alt+p reader in the editor's pane,
// what may be previewed, and keeping the side pane in sync with the editor.

// previewable reports whether ctrl+p has anything to show: the pane re-flows markdown,
// which would mangle code. The scratch buffer counts as markdown.
func (s *homeScreen) previewable() bool {
	if s.currentPath == "" {
		return true
	}
	switch strings.ToLower(filepath.Ext(s.currentPath)) {
	case ".md", ".markdown":
		return true
	}
	return false
}

// enforcePreview closes a preview the current document no longer qualifies for, wherever
// currentPath moves. It returns closeFullPreview's cmd (currently nil) for the caller to
// batch.
func (s *homeScreen) enforcePreview() tea.Cmd {
	if s.previewable() {
		return nil
	}
	cmd := s.closeFullPreview().Cmd
	if s.preview != previewOff {
		s.setPreview(previewOff)
	}
	return cmd
}

// cyclePreview toggles ctrl+p's side pane (off, pane, off), a pure layout change; nothing
// on files the renderer would mangle. The reader is closed first: the two previews never
// show together.
func (s *homeScreen) cyclePreview() core.Action {
	if !s.previewable() {
		return core.Action{}
	}
	act := s.closeFullPreview() // restores whatever pane alt+p folded away, then:
	switch s.preview {
	case previewOff:
		s.setPreview(previewPane)
	case previewPane:
		s.setPreview(previewOff)
	}
	return act
}

// previewScreen builds alt+p's reader: a DocScreen hosted as the editor pane's child
// (not pushed), so the sidebar stays usable beside it. It renders the live buffer, bound
// here: a doc switch builds a new reader (paneChild).
func (s *homeScreen) previewScreen() *components.DocScreen {
	src := s.editor.Text
	return components.NewDocScreen(components.DocOpts{
		// Document first, mode second — the editor's own title bar is the filename, and
		// the reader is a view of the same document, so the two bars line up.
		Title:  s.readerTitle(),
		Render: func(width int) string { return components.RenderMarkdown(src(), width) },
		// Bound with the buffer for the same reason: relative links resolve against this
		// document's directory.
		Links: s.previewLinks(),
		// No Help or OnKey: the host bar names the way out, and the home screen claims alt+p and
		// esc before the pane.
	})
}

// toggleFullPreview is alt+p: the reader takes the editor pane or gives it back. Nothing is
// pushed, so the sidebar works while it is up (a picked doc opens into the preview).
func (s *homeScreen) toggleFullPreview() core.Action {
	if s.fullPreview != nil {
		return s.closeFullPreview()
	}
	if !s.previewable() {
		return core.Action{}
	}
	// One preview on screen at a time: the ctrl+p column folds away and is restored on
	// the way out, so alt+p is a look at the document rather than a rearrangement of it.
	s.previewPrior, s.preview = s.preview, previewOff
	s.fullPreview = s.previewScreen()
	cmd := s.editorPanel.SetChild(s.fullPreview)
	s.relayout()
	return core.Async(cmd)
}

// closeFullPreview restores the editor exactly as it was: its Init is idempotent, so the
// buffer and history survive.
func (s *homeScreen) closeFullPreview() core.Action {
	if s.fullPreview == nil {
		return core.Action{}
	}
	s.fullPreview = nil
	cmd := s.editorPanel.SetChild(s.editor)
	s.preview, s.previewPrior = s.previewPrior, previewOff
	s.relayout()
	return core.Async(cmd)
}

// paneChild points the editor pane at the current document, rebuilding the reader when
// the preview is up (a reader is bound to one editor).
func (s *homeScreen) paneChild() tea.Cmd {
	s.editor.SetTitleVisible(!s.tabsVisible())
	if s.fullPreview == nil {
		return s.editorPanel.SetChild(s.editor)
	}
	s.fullPreview = s.previewScreen()
	return s.editorPanel.SetChild(s.fullPreview)
}

// seedForPreview loads a freshly opened buffer from disk when the reader will occupy the
// pane: the editor's async load would go to the reader and be lost, leaving an empty
// buffer the first save would truncate the file with. Only for buffers that never read
// their file, so unsaved edits are never overwritten.
func (s *homeScreen) seedForPreview(ed *editor.Screen, path string, unread bool) {
	if unread && s.fullPreview != nil {
		ed.SetText(fileText(path))
	}
}

// previewLinks is what a link click does in either preview. Text files open as buffers
// (openDoc); URLs go to the browser and other files are revealed in the file manager. A
// link to a missing file does nothing, rather than creating it on first save.
func (s *homeScreen) previewLinks() components.LinkHooks {
	return components.LinkHooks{
		Base: s.previewDir(),
		URL:  func(_ *core.Shared, l components.Link) core.Action { return sysopen.URL(l.Target) },
		File: func(_ *core.Shared, l components.Link) core.Action { return sysopen.Path(l.Path, true) },
		Text: func(sh *core.Shared, l components.Link) core.Action {
			if !l.Exists {
				return core.Action{}
			}
			return s.openDoc(sh, l.Path)
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

// setPreview swaps the side preview and rebuilds the layout, focusing the editor.
func (s *homeScreen) setPreview(mode int) {
	if s.preview == mode {
		return
	}
	s.preview = mode
	s.relayout()
}

// relayout rebuilds the layout for the preview flags and re-seeds the side pane, focusing
// the editor (whose on-focus cmd is empty, so it is dropped).
func (s *homeScreen) relayout() {
	_ = s.rebuildModular(s.sh, s.editorSlot())
	s.resetPreviewCache()
	s.refreshPreview()
	s.syncPreviewScroll()
}

// previewTarget answers the live pane, or nil when the preview is off.
func (s *homeScreen) previewTarget() *components.ScrollContainer {
	if s.preview == previewPane {
		return s.previewPanel
	}
	return nil
}

// refreshPreview re-renders the side pane when the buffer or its width changed, once per
// message from Update rather than per frame in View. The mapped render keeps scroll sync
// exact.
func (s *homeScreen) refreshPreview() {
	panel := s.previewTarget()
	if panel == nil || s.editor == nil {
		return
	}
	src, width := s.editor.Text(), panel.TextWidth()
	if src == s.previewSrc && width == s.previewW {
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
