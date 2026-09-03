package app

import (
	"time"
	"unicode"

	"github.com/brohd11/bubblestack/components"
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
	start      components.EditorPosition
}

type completionBefore struct {
	editor   *components.EditorScreen
	path     string
	position components.EditorPosition
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

// updateCompletionAfterParent observes what an unhandled message actually did to the
// editor. Identifier typing and backspace keep an open list locally filtered; cursor
// motion and unrelated edits dismiss it. The returned command is only the automatic
// request debounce.
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
			// Presses, wheel notches and keys only — a release or a drag-motion ends a
			// gesture rather than starting one (dismissHoverOn carries the full reasoning,
			// and the bug that made it matter). No behavior changes here: this popup is
			// never summoned by a mouse gesture, so the press has always dismissed it
			// before its release could. The two lists are kept identical so the rule is
			// one rule.
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
			// An empty filtered list renders nothing but would still consume the
			// popup's Enter/Tab/navigation keys. Remove it now; the debounce below
			// remains active and may replace it with a fresh server result.
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
	// Seed after the initial query so the list selects its highest-ranked match. An LSP
	// preselect is useful when there is no local ranking signal, but must not override a
	// non-empty fuzzy query's answer.
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
	rangeToApply := components.EditorRange{Start: s.completion.start, End: current}
	if item.Edit != nil {
		text = item.Edit.NewText
		start, ok := lspPositionToEditor(s.editor, item.Edit.Range.Start)
		if !ok {
			s.closeCompletion()
			return
		}
		end := components.EditorPosition{}
		if item.Edit.Range.End == s.completion.resultPos || item.Edit.Range.End == s.completion.resultEnd {
			// Extend an edit ending at either the request position or the result's real
			// caret through the current locally matched query. The first covers servers
			// editing only up to the asked position; the second keeps typing-after-result
			// rebasing intact.
			end = current
		} else {
			var endOK bool
			end, endOK = lspPositionToEditor(s.editor, item.Edit.Range.End)
			if !endOK {
				s.closeCompletion()
				return
			}
		}
		rangeToApply = components.EditorRange{Start: start, End: end}
	}
	if !s.editor.ApplyCompletion(components.EditorCompletionEdit{
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
// request position, so the server is always asked with an empty prefix: "bg" asks at the
// start of the word and "value.bg" asks immediately after the dot. The server therefore
// answers with everything in scope — or every member of the receiver — and completionQuery
// carries the full typed word to the popup's own fuzzy match, which is the only thing that
// narrows. Seeding the request one rune in (the old behavior) capped the candidate set at
// names beginning with that rune, which fuzzy matching can never widen back out.
func completionIdentifierStart(editor *components.EditorScreen, position components.EditorPosition) components.EditorPosition {
	line, ok := editor.LineText(position.Line)
	if !ok {
		return position
	}
	runes := []rune(line)
	column := min(max(position.Column, 0), len(runes))
	for column > 0 && isCompletionIdentifierRune(runes[column-1]) {
		column--
	}
	return components.EditorPosition{Line: position.Line, Column: column}
}

func completionQuery(editor *components.EditorScreen, start, end components.EditorPosition) (string, bool) {
	if start.Line != end.Line || end.Column < start.Column {
		return "", false
	}
	line, ok := editor.LineText(start.Line)
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
