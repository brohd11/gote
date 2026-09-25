package app

import (
	"context"
	"path/filepath"

	"github.com/brohd11/bubblestack/components/editor"
	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"
)

type lspCompletionEdit struct {
	Range   protocol.Range
	NewText string
}

type lspCompletionItem struct {
	Label, Detail, FilterText, SortText, InsertText string
	Edit                                            *lspCompletionEdit
	Stops                                           []editor.CompletionStop
	Snippet                                         bool
	Preselect                                       bool
}

type lspCompletionRequest struct {
	id               uint64
	path             string
	editSeq          int
	position         protocol.Position
	triggerCharacter string
	manual           bool
}

type lspCompletionResult struct {
	id       uint64
	path     string
	editSeq  int
	position protocol.Position
	items    []lspCompletionItem
	// incomplete is the server's isIncomplete. Unused: gopls sets it on every answer, and the
	// popup already re-requests on each identifier keystroke.
	incomplete bool
	err        error
}

// CompletionTrigger reports whether path's server declared text a trigger character (false
// before initialization).
func (m *lspManager) CompletionTrigger(path, text string) bool {
	if m == nil || path == "" || text == "" {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	doc, ok := m.desired[filepath.Clean(path)]
	if !ok {
		return false
	}
	return m.completionTriggers[doc.key()][text]
}

// RequestCompletion replaces any queued completion request with the newest snapshot and
// returns the result's generation. No IO on the UI goroutine.
func (m *lspManager) RequestCompletion(path string, editSeq int, position protocol.Position,
	triggerCharacter string, manual bool) uint64 {
	if m == nil || path == "" {
		return 0
	}
	path = filepath.Clean(path)
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return 0
	}
	if _, ok := m.desired[path]; !ok {
		m.mu.Unlock()
		return 0
	}
	m.nextComplete++
	id := m.nextComplete
	m.completion = &lspCompletionRequest{
		id: id, path: path, editSeq: editSeq, position: position,
		triggerCharacter: triggerCharacter, manual: manual,
	}
	m.mu.Unlock()
	m.signal()
	return id
}

func (m *lspManager) takeCompletion() *lspCompletionRequest {
	m.mu.Lock()
	defer m.mu.Unlock()
	request := m.completion
	m.completion = nil
	return request
}

// startCompletion flushes the document and starts the completion RPC on a worker.
func (m *lspManager) startCompletion(sessions map[string]*lspSession,
	pending map[string]lspDocument, request lspCompletionRequest) context.CancelFunc {
	doc, session, err := m.flushForRequest(sessions, pending, request.path, request.editSeq, func(_ lspDocument, s *lspSession) bool {
		return s.caps != nil && s.caps.CompletionProvider != nil
	})
	if session == nil {
		m.emitCompletion(request, nil, false, err)
		return nil
	}

	kind := protocol.CompletionTriggerKindInvoked
	var trigger *string
	if !request.manual && request.triggerCharacter != "" {
		for _, candidate := range session.caps.CompletionProvider.TriggerCharacters {
			if candidate == request.triggerCharacter {
				kind = protocol.CompletionTriggerKindTriggerCharacter
				value := request.triggerCharacter
				trigger = &value
				break
			}
		}
	}
	server := session.server
	return m.goRequest(lspCompletionLimit, func(ctx context.Context) {
		result, err := server.Completion(ctx, &protocol.CompletionParams{
			TextDocumentPositionParams: protocol.TextDocumentPositionParams{
				TextDocument: protocol.TextDocumentIdentifier{URI: uri.File(doc.path)},
				Position:     request.position,
			},
			Context: protocol.CompletionContext{TriggerKind: kind, TriggerCharacter: trigger},
		})
		items, incomplete := projectCompletions(result)
		m.emitCompletion(request, items, incomplete, err)
	})
}

func (m *lspManager) emitCompletion(request lspCompletionRequest, items []lspCompletionItem,
	incomplete bool, err error) {
	m.mu.Lock()
	current := !m.closed && request.id == m.nextComplete
	m.mu.Unlock()
	if !current {
		return
	}
	m.emit(lspEvent{completion: &lspCompletionResult{
		id: request.id, path: request.path, editSeq: request.editSeq, position: request.position,
		items: items, incomplete: incomplete, err: err,
	}})
}

func projectCompletions(result protocol.CompletionResult) ([]lspCompletionItem, bool) {
	var source []protocol.CompletionItem
	incomplete := false
	switch result := result.(type) {
	case protocol.CompletionItemSlice:
		source = []protocol.CompletionItem(result)
	case *protocol.CompletionList:
		if result != nil {
			source, incomplete = result.Items, result.IsIncomplete
		}
	}
	items := make([]lspCompletionItem, 0, len(source))
	for _, item := range source {
		projected := lspCompletionItem{Label: item.Label}
		if value, ok := item.Detail.Get(); ok {
			projected.Detail = value
		}
		if value, ok := item.FilterText.Get(); ok {
			projected.FilterText = value
		}
		if projected.FilterText == "" {
			projected.FilterText = item.Label
		}
		if value, ok := item.SortText.Get(); ok {
			projected.SortText = value
		}
		if value, ok := item.InsertText.Get(); ok {
			projected.InsertText = value
		}
		if projected.InsertText == "" {
			projected.InsertText = item.Label
		}
		if value, ok := item.Preselect.Get(); ok {
			projected.Preselect = value
		}
		switch edit := item.TextEdit.(type) {
		case *protocol.TextEdit:
			if edit != nil {
				projected.Edit = &lspCompletionEdit{Range: edit.Range, NewText: edit.NewText}
			}
		case *protocol.InsertReplaceEdit:
			if edit != nil {
				projected.Edit = &lspCompletionEdit{Range: edit.Replace, NewText: edit.NewText}
			}
		}
		if item.InsertTextFormat == protocol.InsertTextFormatSnippet {
			text := projected.InsertText
			if projected.Edit != nil {
				text = projected.Edit.NewText
			}
			expanded, stops, ok := parseLSPSnippet(text)
			if !ok {
				continue
			}
			projected.Snippet = true
			projected.Stops = stops
			if projected.Edit != nil {
				projected.Edit.NewText = expanded
			} else {
				projected.InsertText = expanded
			}
		}
		items = append(items, projected)
	}
	return items, incomplete
}
