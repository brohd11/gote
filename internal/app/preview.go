package app

import (
	"math"
	"path/filepath"
	"strings"

	"github.com/brohd11/bubblestack/components"
	"github.com/brohd11/bubblestack/core"

	tea "github.com/charmbracelet/bubbletea"
)

// The markdown preview: the side pane and the full-screen reader, what may be previewed,
// and keeping the pane in sync with the editor's buffer and scroll position. The homeScreen
// fields these read (preview, previewSrc/W/Map/At) are declared with the rest in screen.go.

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

// enforcePreview closes a pane the current document no longer earns — the doc switched
// to a non-markdown file, or a save-as renamed the open one out of markdown underneath
// it. Called wherever currentPath moves; a no-op when the pane is already off.
func (s *homeScreen) enforcePreview() {
	if s.preview != previewOff && !s.previewable() {
		s.setPreview(previewOff)
	}
}

// cyclePreview steps ctrl+p through the panes: off → the reader → off. Every rung is
// a layout change, so nothing here touches the router's stack and there is no
// navigation state to keep in sync. On a file the reader would mangle, ctrl+p does
// nothing at all.
func (s *homeScreen) cyclePreview() core.Action {
	if !s.previewable() {
		return core.Action{}
	}
	switch s.preview {
	case previewOff:
		s.setPreview(previewPane)
	case previewPane:
		s.setPreview(previewOff)
	}
	return core.Action{}
}

// previewScreen is alt+p's full-width reader, pushed over whatever the editor is doing.
// alt+p and esc both pop it, which is why the home screen tracks only the pane — an esc
// it never sees cannot desync it, and the pane it was opened over is still there
// underneath on the way back.
//
// src is the document to render, deferred rather than passed as a string because the two
// callers disagree about where it lives: alt+p hands over the editor's live buffer, while
// the --preview launch reads the file off disk (see homeScreen.Init).
func (s *homeScreen) previewScreen(src func() string) *previewDoc {
	return &previewDoc{minimal: s.minimal, DocScreen: components.NewDocScreen(components.DocOpts{
		Title:  "preview · " + s.previewName(),
		Crumb:  "preview",
		Render: func(width int) string { return components.RenderMarkdown(src(), width) },
		OnKey: func(_ *core.Shared, k string) (core.Action, bool) {
			if core.MatchKey(k, fullPreviewKey) {
				return core.Pop(), true
			}
			return core.Action{}, false
		},
	})}
}

// previewDoc is the full-screen reader: bubblestack's read-only DocScreen plus the chrome
// mask that makes it one. The router asks only the TOP screen for its mask, so without
// this the breadcrumb (and, in ModeFile, everything homeScreen.ChromeMask had just
// suppressed) would come back the moment the reader was pushed.
//
// The help bar follows the launch it was opened from: an ordinary launch keeps it (one
// dim line naming the way out is not noise, it is the exit), while a ModeFile launch
// drops it, because the editor underneath has none either — a reader that grew chrome
// its own screen doesn't have would read as a different app. There the exit (esc, or
// alt+p again) goes unlabeled, which is the price of the chrome-free pair.
//
// Status is masked in both cases, as it is on homeScreen: the reader paints the message
// itself so its body never changes height (View/HelpView, see status.go).
type previewDoc struct {
	*components.DocScreen
	minimal bool
	h       int // the body height the router last handed down, for statusOver
}

func (p *previewDoc) ChromeMask() core.ChromeMask {
	mask := core.FullscreenMask()
	mask.Help = p.minimal
	return mask
}

func (p *previewDoc) SetSize(sh *core.Shared, width, bodyHeight int) {
	p.h = bodyHeight
	p.DocScreen.SetSize(sh, width, bodyHeight)
}

func (p *previewDoc) View(sh *core.Shared) string {
	body := p.DocScreen.View(sh)
	if p.minimal {
		return statusOver(sh, body, p.h)
	}
	return body
}

func (p *previewDoc) HelpView(sh *core.Shared) string {
	return statusBar(sh, p.DocScreen.HelpView(sh))
}

// Update keeps the wrapper on the stack. DocScreen.Update answers with its own pointer,
// which the router stores back — dropping this type, and with it the mask, on the first
// keystroke the reader received.
func (p *previewDoc) Update(sh *core.Shared, msg tea.Msg) (core.Screen, core.Action) {
	_, act := p.DocScreen.Update(sh, msg)
	return p, act
}

// previewName labels the preview with the doc being edited, or the scratch buffer.
func (s *homeScreen) previewName() string {
	if s.currentPath == "" {
		return "scratch"
	}
	return docName(s.currentPath)
}

// setPreview swaps which preview pane (if any) sits beside the editor, rebuilding the
// layout around it. The rebuild auto-focuses the first slot, so focus is put back on
// the editor: ctrl+p is a view toggle, not a navigation.
func (s *homeScreen) setPreview(mode int) {
	if s.preview == mode {
		return
	}
	s.preview = mode
	// Focus lands on the editor pane, which has no on-focus work to hand back; dropping
	// the cmd keeps this off the four-deep enforcePreview/cyclePreview call chain.
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
