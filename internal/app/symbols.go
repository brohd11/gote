package app

import (
	"strconv"
	"strings"
	"time"

	"github.com/brohd11/bubblestack/components"
	"github.com/brohd11/bubblestack/core"

	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"
	"go.lsp.dev/protocol"
)

const outlineDebounce = 500 * time.Millisecond

type outlineTick struct {
	target     *homeScreen
	generation int
}

type outlineItem struct {
	id, name, detail string
	kind             protocol.SymbolKind
	extent, target   protocol.Range
}

func (i outlineItem) Title() string       { return i.name }
func (i outlineItem) SuffixText() string  { return i.detail }
func (i outlineItem) FilterValue() string { return i.name }
func (i outlineItem) PrefixText() string  { return symbolMark(i.kind) + " " }

func (s *homeScreen) newOutlinePanel() *components.TreePanel {
	return components.NewTreePanel(nil, "Outline", components.TreePanelOpts{
		Border: true,
		OnSelect: func(sh *core.Shared, node components.TreeNode) core.Action {
			item, ok := node.Item.(outlineItem)
			if !ok || s.currentPath == "" {
				return core.Action{}
			}
			preview := s.closeFullPreview()
			jump := s.jumpToLocation(sh, lspLocation{Path: s.currentPath, Range: item.target})
			focus := core.Async(s.modular.FocusSlot(s.editorSlot()))
			return core.Seq(preview, jump, focus)
		},
	})
}

// toggleOutline changes only session state. In a normal workspace turning it on also
// restores the sidebar if ctrl+b had hidden it; ModeFile instead gets an outline-only
// side column and keeps its chrome mask.
func (s *homeScreen) toggleOutline(sh *core.Shared) core.Action {
	s.closeCompletion()
	s.closeHover()
	s.saveResize(s.modular.ResizeState())
	focus := s.focusedPane()
	s.outlineVisible = !s.outlineVisible
	s.outlineGeneration++
	if s.outlineVisible {
		if !s.minimal {
			s.sidebar = true
		}
		s.prepareOutlineDocument()
	} else {
		s.outlineRequestID = 0
	}
	s.rebuildModular(sh, noFocus)
	if s.panelSlot(focus) == noFocus {
		focus = s.editorPanel
	}
	cmd := s.modular.FocusSlot(s.panelSlot(focus))
	if s.outlineVisible {
		cmd = tea.Batch(cmd, s.scheduleOutline())
	}
	return core.Async(cmd)
}

func (s *homeScreen) prepareOutlineDocument() {
	if s.outlineDataPath == s.currentPath {
		return
	}
	s.outlineDataPath, s.outlineDataSeq = s.currentPath, -1
	s.outlineNodes = nil
	s.outlineRequestID = 0
	s.outlineScheduledPath = ""
	s.outlineScheduledSeq = -1
	s.setOutlineMessage(outlineUnavailableMessage(s.sh, s.currentPath))
}

func outlineUnavailableMessage(sh *core.Shared, path string) string {
	if path == "" {
		return "Save this file to load its outline."
	}
	if sh == nil || Of(sh).lsp == nil {
		return "Language-server support is disabled."
	}
	return "Outline unavailable for this document."
}

func (s *homeScreen) setOutlineMessage(message string) {
	s.outlinePanel.SetNodes([]components.TreeNode{{
		ID:   "outline-message:" + s.currentPath,
		Item: components.CompactItem{Name: message},
	}})
}

// scheduleOutline asks immediately for a new document and waits for a quiet edit window
// on the same one. Only one timer exists for a path/generation pair.
func (s *homeScreen) scheduleOutline() tea.Cmd {
	if !s.outlineVisible || s.currentPath == "" || s.editor == nil {
		return nil
	}
	s.prepareOutlineDocument()
	if s.sh == nil || Of(s.sh).lsp == nil {
		return nil
	}
	seq := s.editor.EditSeq()
	if s.outlineDataSeq == seq || s.outlineRequestID != 0 &&
		s.outlineRequestedPath == s.currentPath && s.outlineRequestedSeq == seq ||
		s.outlineScheduledPath == s.currentPath && s.outlineScheduledSeq == seq {
		return nil
	}
	s.outlineGeneration++
	generation := s.outlineGeneration
	s.outlineScheduledPath, s.outlineScheduledSeq = s.currentPath, seq
	delay := outlineDebounce
	if s.outlineDataSeq < 0 {
		delay = 0
		s.setOutlineMessage("Loading outline…")
	}
	return tea.Tick(delay, func(time.Time) tea.Msg {
		return outlineTick{target: s, generation: generation}
	})
}

func (s *homeScreen) handleOutlineTick(sh *core.Shared, tick outlineTick) {
	if tick.target != s || tick.generation != s.outlineGeneration || !s.outlineVisible ||
		s.editor == nil || s.currentPath != s.outlineScheduledPath ||
		s.editor.EditSeq() != s.outlineScheduledSeq {
		return
	}
	c := Of(sh)
	if c == nil || c.lsp == nil {
		return
	}
	id := c.lsp.RequestOutline(s.currentPath, s.editor.EditSeq())
	if id == 0 {
		s.setOutlineMessage(outlineUnavailableMessage(sh, s.currentPath))
		return
	}
	s.outlineRequestID = id
	s.outlineRequestedPath, s.outlineRequestedSeq = s.currentPath, s.editor.EditSeq()
}

// retryOutlineAfterLSP wakes an outline that was toggled while its server was still
// initializing. Capability events are already delivered through this Receive path.
func (s *homeScreen) retryOutlineAfterLSP(sh *core.Shared) tea.Cmd {
	if !s.outlineVisible || s.currentPath == "" || s.editor == nil || s.outlineRequestID != 0 {
		return nil
	}
	c := Of(sh)
	if c == nil || c.lsp == nil || !c.lsp.SupportsOutline(s.currentPath) {
		return nil
	}
	if s.outlineDataPath == s.currentPath && s.outlineDataSeq == s.editor.EditSeq() {
		return nil
	}
	s.outlineScheduledPath = ""
	s.outlineScheduledSeq = -1
	return s.scheduleOutline()
}

func (s *homeScreen) applyOutlineResult(result *lspOutlineResult) {
	if result == nil || result.id == 0 || result.id != s.outlineRequestID {
		return
	}
	s.outlineRequestID = 0
	if !s.outlineVisible || s.editor == nil || result.path != s.currentPath ||
		result.editSeq != s.editor.EditSeq() {
		return
	}
	s.outlineDataPath, s.outlineDataSeq = result.path, result.editSeq
	if result.err != nil {
		if len(s.outlineNodes) == 0 {
			s.setOutlineMessage("Outline error: " + trimLSPError(result.err.Error()))
		}
		return
	}
	s.outlineNodes = outlineTree(result.path, result.symbols)
	if len(s.outlineNodes) == 0 {
		s.setOutlineMessage("No symbols in this document.")
		return
	}
	s.outlinePanel.SetNodes(s.outlineNodes)
	s.syncOutlineCaret()
}

func outlineTree(path string, symbols []lspSymbol) []components.TreeNode {
	var build func([]lspSymbol, string) []components.TreeNode
	build = func(symbols []lspSymbol, parent string) []components.TreeNode {
		seen := make(map[string]int)
		nodes := make([]components.TreeNode, 0, len(symbols))
		for _, symbol := range symbols {
			part := strconv.Itoa(int(symbol.Kind)) + ":" + symbol.Name
			ordinal := seen[part]
			seen[part]++
			id := strings.Join([]string{path, parent, part, strconv.Itoa(ordinal)}, "\x00")
			extent := symbol.Range
			if extent == (protocol.Range{}) && symbol.SelectionRange != (protocol.Range{}) {
				extent = symbol.SelectionRange
			}
			target := symbol.SelectionRange
			if target == (protocol.Range{}) {
				target = extent
			}
			item := outlineItem{id: id, name: symbol.Name, detail: symbol.Detail, kind: symbol.Kind,
				extent: extent, target: target}
			nodes = append(nodes, components.TreeNode{ID: id, Item: item,
				Children: build(symbol.Children, id)})
		}
		return nodes
	}
	return build(symbols, "")
}

func (s *homeScreen) syncOutlineCaret() {
	if !s.outlineVisible || s.outlineDataPath != s.currentPath || s.editor == nil ||
		!s.editorPanel.Focused() || s.fullPreview != nil ||
		s.outlinePanel.List().FilterState() != list.Unfiltered {
		return
	}
	position, ok := editorPositionToLSP(s.editor, s.editor.CursorPosition())
	if !ok {
		return
	}
	if id := outlineNodeAt(s.outlineNodes, position); id != "" {
		s.outlinePanel.Select(id)
	}
}

func outlineNodeAt(nodes []components.TreeNode, position protocol.Position) string {
	best, nearest := "", ""
	var walk func([]components.TreeNode)
	walk = func(nodes []components.TreeNode) {
		for _, node := range nodes {
			item, ok := node.Item.(outlineItem)
			if !ok {
				continue
			}
			if !positionLess(position, item.extent.Start) {
				nearest = item.id
			}
			if rangeContains(item.extent, position) {
				best = item.id
				walk(node.Children)
			}
		}
	}
	walk(nodes)
	if best != "" {
		return best
	}
	return nearest
}

func rangeContains(r protocol.Range, p protocol.Position) bool {
	return !positionLess(p, r.Start) && positionLess(p, r.End)
}

func positionLess(a, b protocol.Position) bool {
	return a.Line < b.Line || a.Line == b.Line && a.Character < b.Character
}

// symbolMark is one glyph per symbol kind, in the spirit of the diagnostics gutter's
// E/W/I/H: a narrow column that separates a function from a field at a glance.
func symbolMark(kind protocol.SymbolKind) string {
	switch kind {
	case protocol.SymbolKindFile, protocol.SymbolKindModule, protocol.SymbolKindNamespace,
		protocol.SymbolKindPackage:
		return "▣"
	case protocol.SymbolKindClass, protocol.SymbolKindInterface, protocol.SymbolKindStruct:
		return "◆"
	case protocol.SymbolKindMethod, protocol.SymbolKindFunction, protocol.SymbolKindConstructor:
		return "ƒ"
	case protocol.SymbolKindField, protocol.SymbolKindProperty:
		return "·"
	case protocol.SymbolKindEnum, protocol.SymbolKindEnumMember:
		return "≡"
	case protocol.SymbolKindConstant:
		return "◇"
	case protocol.SymbolKindVariable:
		return "○"
	}
	return "•"
}
