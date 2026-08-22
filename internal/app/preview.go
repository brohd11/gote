package app

import (
	"math"
	"path/filepath"
	"strings"

	"github.com/brohd11/bubblestack/components"
	"github.com/brohd11/bubblestack/core"

	tea "github.com/charmbracelet/bubbletea"
)

// The markdown preview: the ctrl+p side pane and the alt+p reader that takes the editor's
// own pane, what may be previewed, and keeping the side pane in sync with the editor's
// buffer and scroll position. The homeScreen fields these read (preview, previewPrior,
// fullPreview, previewSrc/W/Map/At) are declared with the rest in screen.go.

// previewable reports whether ctrl+p has anything worth showing. The pane is a
// markdown reader — it joins paragraphs and re-flows to its own width — so pointing it
// at a .go or .json file destroys exactly the indentation that made the file readable.
// The unnamed scratch buffer counts as markdown, which is what it has always been.
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

// enforcePreview closes a preview the current document no longer earns — the doc switched
// to a non-markdown file, or a save-as renamed the open one out of markdown underneath it.
// Called wherever currentPath moves; a no-op when neither preview is up.
//
// It returns closeFullPreview's cmd (the editor's Init, re-armed as it goes back into the
// pane) rather than swallowing it: today that cmd is always nil — every editor gote hands
// the pane is loaded by then — but a caller that batches it cannot be broken by a future
// where it isn't.
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

// cyclePreview steps ctrl+p through the panes: off → the side pane → off. Every rung is
// a layout change, so nothing here touches the router's stack and there is no
// navigation state to keep in sync. On a file the renderer would mangle, ctrl+p does
// nothing at all.
//
// The reader hands the editor back first: ctrl+p is the SIDE pane's key, and the two
// previews are never on screen together (the rule alt+p keeps from its own side).
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

// previewScreen builds alt+p's reader: bubblestack's read-only DocScreen, hosted as the
// EDITOR PANE's child (toggleFullPreview) rather than pushed over the app. That is what
// leaves the sidebar drawn and toggleable beside it — the reader covers the editor and
// nothing else — and it is why the reader needs no chrome mask of its own any more: the
// home screen is still the top screen, so the mask the router asks for every frame is the
// one it was already answering with.
//
// Every caller reads the live buffer, the --preview launch included — it seeds the editor
// before swapping the reader in, so there is no launch that renders off disk (homeScreen.Init).
//
// The buffer accessor is bound HERE rather than called from inside the render closure: the
// reader is built over ONE document, so a doc swap builds a new reader (paneChild) instead
// of leaving this one aimed at the editor that has left the pane.
func (s *homeScreen) previewScreen() *components.DocScreen {
	src := s.editor.Text
	return components.NewDocScreen(components.DocOpts{
		// Document first, mode second — the editor's own title bar is the filename, and
		// the reader is a view of the same document, so the two bars line up.
		Title:  s.previewName() + " · preview",
		Render: func(width int) string { return components.RenderMarkdown(src(), width) },
		// No Help and no OnKey, which is what being a pane rather than a screen costs and
		// saves: a ScreenPanel contributes no PanelHelp, so the way out is named by the
		// host's bar instead (buildModular), and closing the reader is a change to the
		// HOME screen's state, so the home screen claims alt+p and esc before the pane is
		// ever consulted. A Crumb would go the same way — the stack never moves, so the
		// breadcrumb stays the editor's.
	})
}

// toggleFullPreview is the whole of alt+p: the reader takes the editor pane, or gives it
// back. Nothing is pushed and nothing is masked — the layout, the sidebar and the
// breadcrumb are the home screen's throughout, which is what makes the sidebar usable
// while the reader is up (pick a doc and it opens INTO the preview, see paneChild).
//
// Minimal mode needs no branch here: it has no sidebar, so the editor pane IS the
// terminal and the reader covers it exactly as the pushed screen used to.
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

// closeFullPreview puts the editor back in its pane, exactly as it stood: SetChild runs
// the child's Init, and EditorScreen.Init is idempotent (its loaded flag), so the buffer,
// cursor, scroll and undo history survive the round trip rather than being re-read.
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

// paneChild points the editor pane at the document the screen has just moved to — the
// editor itself, or a reader over it while the full preview is up. Rebuilding the reader
// is what makes a doc picked from the sidebar open INTO the preview: previewScreen binds
// one editor's Text at build time, and the old reader would otherwise keep rendering the
// document that has just left.
func (s *homeScreen) paneChild() tea.Cmd {
	if s.fullPreview == nil {
		return s.editorPanel.SetChild(s.editor)
	}
	s.fullPreview = s.previewScreen()
	return s.editorPanel.SetChild(s.fullPreview)
}

// seedForPreview loads a freshly opened buffer off disk when the reader — not the editor —
// is what the pane is about to show. EditorScreen.Init reads ASYNCHRONOUSLY and the result
// comes back as a message the router hands to the top screen, which routes it to the pane's
// child: with the reader in that seat the load reaches nothing, leaving an empty buffer
// aimed at a file that is not empty, which the first save would truncate. SetText marks the
// editor loaded, so the Init that follows dispatches no read at all.
//
// Only for a buffer that is NEW to the open set: an already-open one may hold unsaved
// edits, and seeding those away is the very loss this exists to prevent.
func (s *homeScreen) seedForPreview(ed *components.EditorScreen, path string, fresh bool) {
	if fresh && s.fullPreview != nil {
		ed.SetText(fileText(path))
	}
}

// previewName labels the preview with the doc being edited, or the scratch buffer.
func (s *homeScreen) previewName() string {
	if s.currentPath == "" {
		return "scratch"
	}
	return docName(s.currentPath)
}

// setPreview swaps which preview pane (if any) sits beside the editor, rebuilding the
// layout around it. ctrl+p is a view toggle, not a navigation, so focus goes back to the
// editor pane.
func (s *homeScreen) setPreview(mode int) {
	if s.preview == mode {
		return
	}
	s.preview = mode
	s.relayout()
}

// relayout rebuilds the layout around the current preview flags and re-seeds the side
// pane. Shared by the ctrl+p toggle and the alt+p reader, which both change what the
// editor column holds and what the help bar has to name (buildModular).
//
// Focus lands on the editor pane, which has no on-focus work to hand back; dropping the
// cmd keeps this off the four-deep enforcePreview/cyclePreview call chain.
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

// refreshPreview re-renders the live pane when the buffer changed under it, or when
// the pane changed width (the render is wrapped to it). It runs from Update — once per
// message — rather than from View, which would re-render the whole document on every
// frame, mouse motion included.
//
// The mapped renderer costs nothing over the plain one and is what makes the scroll
// sync exact, so the map is kept with the render it belongs to.
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
}

// syncPreviewScroll scrolls the live pane to follow the editor. The two views do not
// hold the same number of rows for the same text — the render joins paragraphs, gives
// every heading a blank line and grows rules around fences — so a single alignment
// cannot be right everywhere. This one is right where it matters and eases where it
// cannot be:
//
//   - Through the body of the document the editor's MIDDLE line sits at the pane's
//     middle row, on the row RenderMarkdownMapped says that line rendered to. Aligning
//     the middles (rather than the tops) keeps the correspondence readable across the
//     whole pane, and leaves a half-pane of slack at each end for the two views to
//     disagree in.
//   - Within one editor screenful of either end the target eases into the end itself:
//     offset 0 at the top, the pane's last page at the bottom. Without that the pane
//     could never reach the bottom at all — the render outruns the source, so the
//     editor bottoms out while the anchor still points a screenful short.
//
// The sync fires only when the editor's scroll offset actually moves, so a manually
// scrolled pane is left alone until the editor scrolls again.
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

	// ends is where the pane must be when the editor is AT an end; w is how much of it
	// to apply — nothing through the body, all of it at either extreme, smoothstepped
	// between so the handoff has no kink.
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

// resetPreviewCache drops the memo of what the preview pane last rendered. Clearing it
// is what makes REOPENING the pane work: the buffer has not changed since the pane was
// last up, so the skip-on-unchanged check would otherwise bring it back blank. The
// scroll anchor goes with it so the pane re-syncs to wherever the editor is scrolled.
func (s *homeScreen) resetPreviewCache() {
	s.previewSrc, s.previewW, s.previewMap = "", 0, nil
	s.previewAt = -1
}
