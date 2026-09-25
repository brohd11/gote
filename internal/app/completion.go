package app

import (
	"time"
	"unicode"

	"github.com/brohd11/bubblestack/components"
	"github.com/brohd11/bubblestack/components/editor"
	"github.com/brohd11/bubblestack/core"

	tea "charm.land/bubbletea/v2"
	"go.lsp.dev/protocol"
)

const completionDebounce = 150 * time.Millisecond

type completionTick struct {
	target     *homeScreen
	generation uint64
}

type completionUI struct {
	popup *components.FloatingPopup
	list  *components.PopupList[lspCompletionItem]

	generation uint64
	requestID  uint64
	path       string
	resultPos  protocol.Position // the LSP request position: the identifier start
	resultEnd  protocol.Position // real caret when that result was installed
	start      editor.Position
}

type completionBefore struct {
	editor   *editor.Screen
	path     string
	position editor.Position
	editSeq  int
	focused  bool
}

func (s *homeScreen) completionSnapshot() completionBefore {
	return completionBefore{
		editor: s.editor, path: s.currentPath, position: s.editor.CursorPosition(),
		editSeq: s.editor.EditSeq(), focused: s.editorPanel.Focused() && s.fullPreview == nil,
	}
}

func (s *homeScreen) completionAvailable(sh *core.Shared) bool {
	return sh != nil && Of(sh).lsp != nil && s.currentPath != "" && s.fullPreview == nil &&
		s.editorPanel.Focused()
}

func (s *homeScreen) closeCompletion() {
	s.completion.generation++
	s.completion.popup = nil
	s.completion.list = nil
	s.completion.requestID = 0
	s.completion.path = ""
}

func (s *homeScreen) scheduleCompletion() tea.Cmd {
	s.completion.generation++
	s.completion.requestID = 0 // an older response may not replace the locally filtered list
	generation := s.completion.generation
	return tea.Tick(completionDebounce, func(time.Time) tea.Msg {
		return completionTick{target: s, generation: generation}
	})
}

func (s *homeScreen) handleCompletionTick(sh *core.Shared, tick completionTick) (core.Action, bool) {
	if tick.target != s || tick.generation != s.completion.generation || !s.completionAvailable(sh) {
		return core.Action{}, true
	}
	return s.requestCompletion(sh, "", false), true
}

func (s *homeScreen) requestCompletion(sh *core.Shared, trigger string, manual bool) core.Action {
	if !s.completionAvailable(sh) {
		s.closeCompletion()
		return core.Action{}
	}
	c := Of(sh)
	c.lsp.Reconcile(c)
	position := completionIdentifierStart(s.editor, s.editor.CursorPosition())
	protocolPosition, ok := editorPositionToLSP(s.editor, position)
	if !ok {
		s.closeCompletion()
		return core.Action{}
	}
	id := c.lsp.RequestCompletion(s.currentPath, s.editor.EditSeq(), protocolPosition, trigger, manual)
	if id == 0 {
		s.closeCompletion()
		return core.Action{}
	}
	s.completion.requestID = id
	s.completion.path = s.currentPath
	return core.Action{}
}

// updateCompletionAfterParent reacts to what an unhandled message did to the editor:
// identifier typing and backspace re-filter an open list, other motion and edits close
// it. It returns only the request debounce.
func (s *homeScreen) updateCompletionAfterParent(sh *core.Shared, msg tea.Msg, before completionBefore) tea.Cmd {
	if before.editor != s.editor || before.path != s.currentPath || !before.focused || !s.completionAvailable(sh) {
		s.closeCompletion()
		return nil
	}
	afterPosition := s.editor.CursorPosition()
	afterSeq := s.editor.EditSeq()
	if afterSeq == before.editSeq {
		if afterPosition != before.position {
			s.closeCompletion()
			return nil
		}
		if s.completion.popup != nil {
			// Presses, wheel notches and keys only: a release or drag-motion ends a gesture rather
			// than starting one (the same list as dismissHoverOn).
			switch msg.(type) {
			case tea.KeyPressMsg, tea.PasteMsg, tea.MouseClickMsg, tea.MouseWheelMsg:
				s.closeCompletion()
			}
		}
		return nil
	}

	km, keyPress := msg.(tea.KeyPressMsg)
	text := ""
	if keyPress {
		text = km.Text
	}
	if text != "" && Of(sh).lsp.CompletionTrigger(s.currentPath, text) {
		s.closeCompletion()
		s.requestCompletion(sh, text, false)
		return nil
	}

	identifierTyped := false
	if runes := []rune(text); len(runes) == 1 && isCompletionIdentifierRune(runes[0]) {
		identifierTyped = true
	}
	backspaced := keyPress && (km.String() == "backspace" || km.String() == "ctrl+h")
	if !identifierTyped && !(backspaced && s.completion.popup != nil) {
		s.closeCompletion()
		return nil
	}
	if s.completion.popup != nil {
		query, ok := completionQuery(s.editor, s.completion.start, afterPosition)
		if !ok {
			s.closeCompletion()
			return nil
		}
		s.completion.list.SetQuery(query)
		if s.completion.list.Len() == 0 {
			// An empty list would still swallow Enter, Tab and navigation, so close it; the debounce
			// may bring a fresh result.
			s.closeCompletion()
		}
	}
	return s.scheduleCompletion()
}

func (s *homeScreen) applyCompletionResult(result *lspCompletionResult) {
	if result == nil || result.id == 0 || result.id != s.completion.requestID ||
		result.path != s.currentPath || result.path != s.completion.path ||
		result.editSeq != s.editor.EditSeq() {
		return
	}
	position := s.editor.CursorPosition()
	start := completionIdentifierStart(s.editor, position)
	protocolSeed, ok := editorPositionToLSP(s.editor, start)
	protocolEnd, endOK := editorPositionToLSP(s.editor, position)
	if !ok || !endOK || protocolSeed != result.position || result.err != nil || len(result.items) == 0 {
		s.closeCompletion()
		return
	}
	query, ok := completionQuery(s.editor, start, position)
	if !ok {
		s.closeCompletion()
		return
	}
	items := make([]components.PopupListItem[lspCompletionItem], len(result.items))
	preselect := -1
	for i, item := range result.items {
		items[i] = components.PopupListItem[lspCompletionItem]{
			Label: item.Label, Detail: item.Detail, FilterText: item.FilterText, Value: item,
		}
		if preselect < 0 && item.Preselect {
			preselect = i
		}
	}
	s.completion.start = start
	s.completion.resultPos = result.position
	s.completion.resultEnd = protocolEnd
	list := components.NewPopupList(components.PopupListOpts[lspCompletionItem]{
		MaxVisible: 8, MaxWidth: 56,
		Fuzzy: true,
		OnAccept: func(_ *core.Shared, item components.PopupListItem[lspCompletionItem]) core.Action {
			s.acceptCompletion(item.Value)
			return core.Action{}
		},
		OnCancel: func(*core.Shared) core.Action {
			s.closeCompletion()
			return core.Action{}
		},
	})
	list.SetQuery(query)
	// Seed after the query so the best fuzzy match is selected; an LSP preselect must not
	// override a non-empty query.
	list.SetItems(items)
	if query == "" && preselect >= 0 {
		list.Select(preselect)
	}
	if list.Len() == 0 {
		// The server returned candidates, but none are valid for the current local
		// fuzzy query. Do not install an invisible popup that can capture input.
		s.closeCompletion()
		return
	}
	s.completion.list = list
	s.completion.popup = &components.FloatingPopup{Content: list.View, Handle: list.Update}
}

func (s *homeScreen) acceptCompletion(item lspCompletionItem) {
	if s.completion.popup == nil || s.completion.path != s.currentPath {
		return
	}
	current := s.editor.CursorPosition()
	text := item.InsertText
	rangeToApply := editor.Range{Start: s.completion.start, End: current}
	if item.Edit != nil {
		text = item.Edit.NewText
		start, ok := lspPositionToEditor(s.editor, item.Edit.Range.Start)
		if !ok {
			s.closeCompletion()
			return
		}
		end := editor.Position{}
		if item.Edit.Range.End == s.completion.resultPos || item.Edit.Range.End == s.completion.resultEnd {
			// Extend an edit ending at the request position, or at the result's caret, through the
			// query typed since.
			end = current
		} else {
			var endOK bool
			end, endOK = lspPositionToEditor(s.editor, item.Edit.Range.End)
			if !endOK {
				s.closeCompletion()
				return
			}
		}
		rangeToApply = editor.Range{Start: start, End: end}
	}
	if !s.editor.ApplyCompletion(editor.CompletionEdit{
		Range: rangeToApply, Text: text, Stops: item.Stops, PairTrailingOpener: !item.Snippet,
	}) {
		s.closeCompletion()
		return
	}
	s.closeCompletion()
}

func (s *homeScreen) viewCompletion(sh *core.Shared, body string) string {
	popup := s.completion.popup
	if popup == nil || s.completion.list == nil || s.completion.list.Len() == 0 {
		return body
	}
	x, absoluteY, visible := s.editor.CursorAnchor()
	if !visible {
		return body
	}
	y := absoluteY - sh.BodyY()
	s.completion.list.SetMaxWidth(max(min(56, s.w-4), 1))
	popup.Placement = components.PlacePopupAt(components.PopupAnchor{
		X: x, Y: y + 1, FlipX: x + 1, FlipY: y,
	})
	return popup.ViewOver(body, s.w, s.h)
}

func isCompletionIdentifierRune(r rune) bool {
	return r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r)
}

// completionIdentifierStart is both the left edge of the local fuzzy query and the LSP
// request position, so the server is asked with an empty prefix ("bg" at the word start,
// "value.bg" right after the dot) and returns everything in scope. The popup's fuzzy
// match does the narrowing; a one-rune prefix would cap what fuzzy matching can find.
func completionIdentifierStart(ed *editor.Screen, position editor.Position) editor.Position {
	line, ok := ed.LineText(position.Line)
	if !ok {
		return position
	}
	runes := []rune(line)
	column := min(max(position.Column, 0), len(runes))
	for column > 0 && isCompletionIdentifierRune(runes[column-1]) {
		column--
	}
	return editor.Position{Line: position.Line, Column: column}
}

func completionQuery(ed *editor.Screen, start, end editor.Position) (string, bool) {
	if start.Line != end.Line || end.Column < start.Column {
		return "", false
	}
	line, ok := ed.LineText(start.Line)
	if !ok {
		return "", false
	}
	runes := []rune(line)
	if start.Column < 0 || end.Column > len(runes) {
		return "", false
	}
	for _, r := range runes[start.Column:end.Column] {
		if !isCompletionIdentifierRune(r) {
			return "", false
		}
	}
	return string(runes[start.Column:end.Column]), true
}
