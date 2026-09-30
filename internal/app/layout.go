package app

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"
	"github.com/brohd11/bubblestack/components"
	"github.com/brohd11/bubblestack/core"
)

// editorLeft is the column the editor pane starts at (the side column's width when shown),
// the left bound for caret-anchored panels.
func (s *homeScreen) editorLeft() int {
	if len(s.groups()) > 1 && s.editorPanel != nil {
		return s.editorPanel.x
	}
	if s.sideColumnVisible() {
		return s.sidebarPaneWidth()
	}
	return 0
}

func (s *homeScreen) sideColumnPanels() []components.Panel {
	if s.minimal {
		if s.outlineVisible {
			return []components.Panel{s.outlinePanel}
		}
		return nil
	}
	if !s.sidebar {
		return nil
	}
	panels := []components.Panel{s.docsPane()}
	if s.outlineVisible {
		panels = append(panels, s.outlinePanel)
	}
	return panels
}

func (s *homeScreen) sideColumnVisible() bool { return len(s.sideColumnPanels()) > 0 }

func (s *homeScreen) sidebarSplitKey() string {
	var names []string
	for _, panel := range s.sideColumnPanels() {
		switch panel {
		case s.docsPanel, s.filePanel, s.groupedPanel:
			names = append(names, "docs")
		case s.outlinePanel:
			names = append(names, "outline")
		}
	}
	return strings.Join(names, "/")
}

func (s *homeScreen) sidebarPaneWidth() int {
	if s.sidebarW > 0 {
		return s.sidebarW
	}
	return sidebarWidth
}

// editorSlot is the editor pane's flat slot index in the current layout.
func (s *homeScreen) editorSlot() int {
	return s.panelSlot(s.editorPanel)
}

// firstSlot is the first pane below the header: where focus lands when no panel claims it.
func (s *homeScreen) firstSlot() int {
	if s.header != nil {
		return 1
	}
	return 0
}

// setSidebar rebuilds the layout with or without the sidebar. Minimal mode refuses here,
// the one path the sidebar can return through, so no caller needs a guard.
func (s *homeScreen) setSidebar(visible bool) {
	if s.minimal {
		return
	}
	s.sidebar = visible
	s.rebuildModular(s.sh, noFocus)
}

// setFileView swaps the Docs view, keeping all panels and their cursors.
// Minimal mode refuses, as in setSidebar.
func (s *homeScreen) setFileView(view fileView) {
	if s.minimal || view == s.fileView {
		return
	}
	s.fileView = view
	s.rebuildModular(s.sh, noFocus)
}

// docsPane is whichever panel fills the docs slot.
type docsPane interface {
	components.Panel
	RowY(int) (int, bool)
	List() *list.Model
}

func (s *homeScreen) docsPane() docsPane {
	switch s.fileView {
	case fileViewFolder:
		return s.filePanel
	case fileViewGrouped:
		return s.groupedPanel
	}
	return s.docsPanel
}

// noFocus tells rebuildModular to preserve the focused panel if it survives,
// otherwise use the first focusable panel in the new layout.
const noFocus = -1

// rebuildModular swaps in a layout for the current flags: blur the outgoing focused panel
// first (or it keeps a focus ring), size the new screen, then restore panel focus.
// Panels are retained; rebuilding must not re-initialize their editors.
func (s *homeScreen) rebuildModular(sh *core.Shared, focus int) tea.Cmd {
	prior := s.focusedPane()
	s.rebuildLayout(sh)
	if focus == noFocus {
		focus = s.panelSlot(prior)
		if focus == noFocus {
			focus = s.firstSlot()
		}
	}
	cmd := s.modular.FocusSlot(focus)
	if p, ok := s.focusedPane().(*groupPanel); ok {
		s.activateGroup(p.group)
	}
	return cmd
}

// rebuildLayout replaces geometry without selecting a different active document.
// The caller restores focus by panel identity after the new slots exist.
func (s *homeScreen) rebuildLayout(sh *core.Shared) {
	s.modular.SetFocused(false)
	s.modular = s.buildModular()
	if s.w > 0 {
		s.modular.SetSize(sh, s.w, s.h)
	}
}

// buildModular declares gote's pane tree. Depth-first leaf order keeps the upper pane
// indexes stable.
func (s *homeScreen) buildModular() *components.ModularScreen {
	s.buildingGroups = true
	defer func() { s.buildingGroups = false }()
	for _, g := range s.groups() {
		g.editor.SetTitleVisible(!s.tabsVisible())
		if g.fullPreview != nil {
			s.withGroup(g, func() tea.Cmd { g.fullPreview.Title = s.readerTitle(); return nil })
		}
	}
	opts := components.ModularOpts{}
	leaf := func(panel components.Panel) components.LayoutNode {
		s.panelSlots[panel] = len(s.panelSlots)
		return components.LayoutNode{Slot: &components.Slot{Panel: panel}}
	}
	s.panelSlots = make(map[components.Panel]int)
	// The header is the first leaf, so every pane's slot counts it (see firstSlot).
	s.header = nil
	var header components.LayoutNode
	if !s.minimal {
		s.header = &headerPanel{host: s}
		header = leaf(s.header)
		header.Size, header.FixedSize = headerRows, true
	}
	main := components.LayoutNode{ID: "main", Axis: components.LayoutHorizontal}
	if panels := s.sideColumnPanels(); len(panels) > 0 {
		s.frameSidebar(panels)
		children := make([]components.LayoutNode, 0, len(panels))
		for _, panel := range panels {
			children = append(children, leaf(panel))
		}
		main.Children = append(main.Children, components.LayoutNode{
			ID: "sidebar", Axis: components.LayoutVertical, Size: s.sidebarPaneWidth(),
			Children: children,
		})
	}
	// Only a group with a neighbor on its left draws a divider; a collapsed split's
	// survivor must drop its own.
	s.openTabs.separator, s.editorPanel.separator = false, false
	if len(s.groups()) > 1 {
		groups := components.LayoutNode{ID: "groups", Axis: components.LayoutHorizontal}
		for i, g := range s.groups() {
			g.openTabs.separator, g.editorPanel.separator = i > 0, i > 0
			bar := leaf(g.openTabs)
			bar.Size, bar.FixedSize = 1, true
			groups.Children = append(groups.Children, components.LayoutNode{ID: fmt.Sprintf("group-%d", i), Axis: components.LayoutVertical, Weight: g.weight,
				Children: []components.LayoutNode{bar, leaf(g.editorPanel)}})
		}
		editors := components.LayoutNode{ID: "editors", Axis: components.LayoutHorizontal, Children: []components.LayoutNode{groups}}
		if panel := s.previewTarget(); panel != nil {
			editors.Children = append(editors.Children, leaf(panel))
		}
		main.Children = append(main.Children, editors)
	} else if s.tabsVisible() {
		bar := leaf(s.openTabs)
		bar.Size, bar.FixedSize = 1, true
		editors := components.LayoutNode{ID: "editors", Axis: components.LayoutHorizontal,
			Children: []components.LayoutNode{leaf(s.editorPanel)}}
		if panel := s.previewTarget(); panel != nil {
			editors.Children = append(editors.Children, leaf(panel))
		}
		main.Children = append(main.Children, components.LayoutNode{ID: "documents", Axis: components.LayoutVertical,
			Children: []components.LayoutNode{bar, editors}})
	} else {
		main.Children = append(main.Children, leaf(s.editorPanel))
		if panel := s.previewTarget(); panel != nil {
			main.Children = append(main.Children, leaf(panel))
		}
	}
	root := main
	if s.bottomVisible {
		main.Weight = 3
		root = components.LayoutNode{ID: "workspace", Axis: components.LayoutVertical,
			Children: []components.LayoutNode{main, {
				ID: "tools", Axis: components.LayoutHorizontal,
				Children: []components.LayoutNode{leaf(s.bottom)},
			}},
		}
	}
	if s.header != nil {
		root = components.LayoutNode{ID: "frame", Axis: components.LayoutVertical,
			Children: []components.LayoutNode{header, root}}
	}
	opts.Resize = &components.ResizeOpts{State: s.resizeState(), OnChange: s.saveResize}
	return components.NewModularLayout(root, opts)
}

// frameSidebar stacks the side column's frames into one box: the header's rule (or a box
// top without one) caps the first, each later pane tees off the one above, and only the
// last closes the bottom.
func (s *homeScreen) frameSidebar(panels []components.Panel) {
	for i, panel := range panels {
		f, ok := panel.(interface{ SetFrame(components.FrameStyle) })
		if !ok {
			continue
		}
		// Only the legend shows focus: the edges meet the header rule, which never lights up.
		frame := components.TitledFrame{Top: components.TopTee, Bottom: i == len(panels)-1, Focus: components.FocusLegend}
		if i == 0 {
			frame.Top = components.TopNone
			if s.header == nil {
				frame.Top = components.TopBox
			}
		}
		f.SetFrame(frame)
	}
}

// resizeState maps gote's pane identities onto ModularScreen's positional state; the side
// and preview columns may be absent.
func (s *homeScreen) resizeState() components.ResizeState {
	// Gote maps pane identities to split children. Hidden panes keep their own
	// preferences, rather than saving a snapshot of a different child list over them.
	state := components.ResizeState{Splits: make(map[string]components.SplitState)}
	sizes, weights := []int{}, []float64{}
	if panels := s.sideColumnPanels(); len(panels) > 0 {
		sizes, weights = append(sizes, s.sidebarPaneWidth()), append(weights, 1)
		if saved := s.sidebarSplits[s.sidebarSplitKey()]; len(saved) == len(panels) {
			state.Splits["sidebar"] = components.SplitState{Sizes: make([]int, len(panels)), Weights: append([]float64(nil), saved...)}
		}
	}
	share := s.editorFlex
	if share <= 0 || share >= 1 {
		share = 0.5
	}
	if s.tabsVisible() {
		sizes, weights = append(sizes, 0), append(weights, 1)
		editors := components.SplitState{Sizes: []int{0}, Weights: []float64{share}}
		if s.previewTarget() != nil {
			editors.Sizes = append(editors.Sizes, 0)
			editors.Weights = append(editors.Weights, 1-share)
		}
		state.Splits["editors"] = editors
	} else {
		sizes, weights = append(sizes, 0), append(weights, share)
		if s.previewTarget() != nil {
			sizes, weights = append(sizes, 0), append(weights, 1-share)
		}
	}
	if len(s.groups()) > 1 {
		split := components.SplitState{}
		for _, g := range s.groups() {
			split.Sizes = append(split.Sizes, 0)
			split.Weights = append(split.Weights, g.weight)
		}
		state.Splits["groups"] = split
	}
	state.Splits["main"] = components.SplitState{Sizes: sizes, Weights: weights}
	if s.bottomVisible && s.bottomFraction > 0 && s.bottomFraction < 1 {
		state.Splits["workspace"] = components.SplitState{Sizes: []int{0, 0}, Weights: []float64{1 - s.bottomFraction, s.bottomFraction}}
	}
	return state
}

// saveResize retains gote's pane preferences independently of which panes exist
// in this layout. The framework knows only named splits and their child weights.
func (s *homeScreen) saveResize(state components.ResizeState) {
	if split, ok := state.Splits["groups"]; ok && len(split.Weights) == len(s.groups()) {
		for i, g := range s.groups() {
			g.weight = split.Weights[i]
		}
	}
	if split, ok := state.Splits["workspace"]; s.bottomVisible && ok && len(split.Weights) == 2 {
		s.bottomFraction = split.Weights[1] / (split.Weights[0] + split.Weights[1])
	}
	editorCol := 0
	if s.sideColumnVisible() {
		if split, ok := state.Splits["main"]; ok && len(split.Sizes) > 0 && split.Sizes[0] > 0 {
			s.sidebarW = split.Sizes[0]
		}
		if split, ok := state.Splits["sidebar"]; ok && len(split.Weights) == len(s.sideColumnPanels()) {
			sum := 0.0
			for _, weight := range split.Weights {
				sum += weight
			}
			if sum > 0 {
				weights := make([]float64, len(split.Weights))
				for i, weight := range split.Weights {
					weights[i] = weight / sum
				}
				s.sidebarSplits[s.sidebarSplitKey()] = weights
			}
		}
		editorCol = 1
	}
	group := "main"
	if s.tabsVisible() {
		group, editorCol = "editors", 0
	}
	if split, ok := state.Splits[group]; ok && s.previewTarget() != nil && len(split.Weights) > editorCol+1 {
		s.editorFlex = split.Weights[editorCol] / (split.Weights[editorCol] + split.Weights[editorCol+1])
	}
}
