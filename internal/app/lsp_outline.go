package app

import (
	"context"
	"path/filepath"

	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"
)

// The persistent outline is refreshed by editing rather than by an explicit user
// request, so it has its own request slot. A background documentSymbol fetch must never
// evict a definition, hover, format, completion, or semantic-token request.
type lspOutlineRequest struct {
	id      uint64
	path    string
	editSeq int
}

type lspOutlineResult struct {
	id      uint64
	path    string
	editSeq int
	symbols []lspSymbol
	err     error
}

// RequestOutline queues the newest outline fetch. Zero means that the document is not
// tracked yet or its initialized server did not advertise documentSymbol support.
func (m *lspManager) RequestOutline(path string, editSeq int) uint64 {
	if m == nil || path == "" {
		return 0
	}
	path = filepath.Clean(path)
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return 0
	}
	doc, ok := m.desired[path]
	if !ok || !m.supportsOutlineLocked(doc) {
		m.mu.Unlock()
		return 0
	}
	m.nextOutline++
	id := m.nextOutline
	m.outline = &lspOutlineRequest{id: id, path: path, editSeq: editSeq}
	m.mu.Unlock()
	m.signal()
	return id
}

func (m *lspManager) SupportsOutline(path string) bool {
	if m == nil || path == "" {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	doc, ok := m.desired[filepath.Clean(path)]
	return ok && m.supportsOutlineLocked(doc)
}

func (m *lspManager) supportsOutlineLocked(doc lspDocument) bool {
	caps := m.capabilities[doc.key()]
	return caps != nil && lspProvided(caps.DocumentSymbolProvider)
}

func (m *lspManager) takeOutline() *lspOutlineRequest {
	m.mu.Lock()
	defer m.mu.Unlock()
	request := m.outline
	m.outline = nil
	return request
}

// startOutline flushes the matching buffer before asking for symbols, so the server
// describes the same editor generation the result names.
func (m *lspManager) startOutline(sessions map[string]*lspSession,
	pending map[string]lspDocument, request lspOutlineRequest) context.CancelFunc {
	m.mu.Lock()
	doc, ok := m.desired[request.path]
	m.mu.Unlock()
	if !ok || doc.editSeq != request.editSeq {
		m.emitOutline(request, lspOutlineResult{})
		return nil
	}
	session := sessions[doc.key()]
	if session == nil || session.server == nil {
		m.emitOutline(request, lspOutlineResult{})
		return nil
	}
	if session.sent[doc.path] != doc.version {
		if err := session.server.DidChange(context.Background(), &protocol.DidChangeTextDocumentParams{
			TextDocument: protocol.VersionedTextDocumentIdentifier{
				TextDocumentIdentifier: protocol.TextDocumentIdentifier{URI: uri.File(doc.path)},
				Version:                doc.version,
			},
			ContentChanges: []protocol.TextDocumentContentChangeEvent{
				&protocol.TextDocumentContentChangeWholeDocument{Text: doc.text},
			},
		}); err != nil {
			m.failSession(session, err)
			clearPendingSession(pending, session.key)
			m.emitOutline(request, lspOutlineResult{err: err})
			return nil
		}
		session.sent[doc.path] = doc.version
		delete(pending, doc.path)
	}

	ctx, cancel := context.WithTimeout(context.Background(), lspRequestLimit)
	server, path := session.server, doc.path
	m.workers.Add(1)
	go func() {
		defer m.workers.Done()
		answer, err := server.DocumentSymbol(ctx, &protocol.DocumentSymbolParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: uri.File(path)},
		})
		m.emitOutline(request, lspOutlineResult{symbols: projectSymbols(answer), err: err})
	}()
	return cancel
}

func (m *lspManager) emitOutline(request lspOutlineRequest, result lspOutlineResult) {
	m.mu.Lock()
	current := !m.closed && request.id == m.nextOutline
	m.mu.Unlock()
	if !current {
		return
	}
	result.id, result.path, result.editSeq = request.id, request.path, request.editSeq
	m.emit(lspEvent{outline: &result})
}
