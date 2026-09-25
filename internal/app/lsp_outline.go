package app

import (
	"context"
	"path/filepath"

	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"
)

// The outline has its own request slot: a background refresh must never evict a request
// the user made.
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
	doc, session, err := m.flushForRequest(sessions, pending, request.path, request.editSeq, nil)
	if session == nil {
		m.emitOutline(request, lspOutlineResult{err: err})
		return nil
	}

	server, path := session.server, doc.path
	return m.goRequest(lspRequestLimit, func(ctx context.Context) {
		answer, err := server.DocumentSymbol(ctx, &protocol.DocumentSymbolParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: uri.File(path)},
		})
		m.emitOutline(request, lspOutlineResult{symbols: projectSymbols(answer), err: err})
	})
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
