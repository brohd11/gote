package app

import (
	"context"
	"path/filepath"

	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"
)

// The semantic-token lane. Unlike hover and go-to-definition this is not something the
// user asks for — it runs behind every open document a server will answer for — so it gets
// its own slot and counter rather than sharing the on-demand lane. Sharing would let a
// background refresh evict the definition the user just pressed a key for, which is the
// exact failure the on-demand lane's "replace what has not started" rule is designed to
// cause on purpose.

// semanticToken is one painted run: a row, a half-open span in UTF-16 code units (the
// encoding gote negotiates at initialize), and the palette slot to paint it with. The
// token TYPE is already resolved to a slot here, so nothing downstream needs the legend
// or the mapping — a decoded set is self-contained and safe to cache against a path.
type semanticToken struct {
	line       int
	startUTF16 int
	lenUTF16   int
	slot       string
}

// lspSemanticRequest is one queued fetch. editSeq rides along so a result that lands after
// the buffer moved on can be discarded rather than painted onto text it does not describe.
type lspSemanticRequest struct {
	id      uint64
	path    string
	editSeq int
}

type lspSemanticResult struct {
	id      uint64
	path    string
	editSeq int
	tokens  []semanticToken
	err     error
}

// semanticLegend unwraps the server's capability union and returns its token type list.
// The union is either options or registration options; anything else — including the
// absent capability — means the server will not answer, which is not an error.
func semanticLegend(provider protocol.SemanticTokensProvider) ([]string, bool) {
	switch value := provider.(type) {
	case *protocol.SemanticTokensOptions:
		if value == nil || len(value.Legend.TokenTypes) == 0 {
			return nil, false
		}
		return value.Legend.TokenTypes, true
	case *protocol.SemanticTokensRegistrationOptions:
		if value == nil || len(value.Legend.TokenTypes) == 0 {
			return nil, false
		}
		return value.Legend.TokenTypes, true
	}
	return nil, false
}

// decodeSemanticTokens unpacks the wire format: a flat uint32 array of five-element
// tuples, each RELATIVE to the token before it —
//
//	deltaLine, deltaStartChar, length, typeIndex, modifiers
//
// deltaLine counts rows forward from the previous token; deltaStartChar is relative to the
// previous token's start when they share a row and absolute when the row advanced. Getting
// that second rule wrong is silent: every token after the first line break drifts, and the
// colors merely look subtly wrong rather than failing.
//
// typeIndex indexes the SERVER's legend, so the same number means different things to
// different servers — which is why the legend is cached per session and the mapping is
// keyed by name. A type the palette does not map is dropped here rather than carried,
// leaving chroma's span untouched, which is what makes this an overlay.
func decodeSemanticTokens(data []uint32, legend []string, slots map[string]string) []semanticToken {
	// A trailing partial tuple is a malformed answer; take the whole tuples and ignore it
	// rather than reading past the end.
	tokens := make([]semanticToken, 0, len(data)/5)
	line, start := 0, 0
	for i := 0; i+4 < len(data); i += 5 {
		deltaLine, deltaStart := int(data[i]), int(data[i+1])
		length, typeIndex := int(data[i+2]), int(data[i+3])
		line += deltaLine
		if deltaLine == 0 {
			start += deltaStart
		} else {
			start = deltaStart
		}
		if length <= 0 || typeIndex < 0 || typeIndex >= len(legend) {
			continue
		}
		slot, ok := slots[legend[typeIndex]]
		if !ok {
			continue
		}
		tokens = append(tokens, semanticToken{
			line: line, startUTF16: start, lenUTF16: length, slot: slot,
		})
	}
	return tokens
}

// RequestSemanticTokens queues a fetch for path, replacing any earlier one that had not
// started. Returns 0 when there is nothing to ask — the document is untracked, the manager
// is closed, or the server never advertised a provider — so the caller can stop rather
// than wait for a result that will not arrive.
func (m *lspManager) RequestSemanticTokens(path string, editSeq int) uint64 {
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
	if !ok || !m.supportsSemanticLocked(doc) {
		m.mu.Unlock()
		return 0
	}
	m.nextSemantic++
	id := m.nextSemantic
	m.semantic = &lspSemanticRequest{id: id, path: path, editSeq: editSeq}
	m.mu.Unlock()
	m.signal()
	return id
}

// supportsSemanticLocked reports whether the document's server answered initialize with a
// usable legend. Held under mu by every caller.
func (m *lspManager) supportsSemanticLocked(doc lspDocument) bool {
	return len(m.semanticLegends[doc.key()]) > 0
}

func (m *lspManager) takeSemantic() *lspSemanticRequest {
	m.mu.Lock()
	defer m.mu.Unlock()
	request := m.semantic
	m.semantic = nil
	return request
}

// startSemantic flushes the document to its session and starts the RPC, the same shape
// startRequest has: the DidChange write happens on the actor goroutine so the server scores
// the exact text these tokens will be painted onto, and only the wait moves to a worker.
func (m *lspManager) startSemantic(sessions map[string]*lspSession,
	pending map[string]lspDocument, request lspSemanticRequest) context.CancelFunc {
	m.mu.Lock()
	doc, ok := m.desired[request.path]
	legend := m.semanticLegends[doc.key()]
	slots := m.semanticSlotsLocked(doc)
	m.mu.Unlock()
	if !ok || doc.editSeq != request.editSeq || len(legend) == 0 {
		m.emitSemantic(request, lspSemanticResult{})
		return nil
	}
	session := sessions[doc.key()]
	if session == nil || session.server == nil {
		m.emitSemantic(request, lspSemanticResult{})
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
			m.emitSemantic(request, lspSemanticResult{err: err})
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
		answer, err := server.SemanticTokensFull(ctx, &protocol.SemanticTokensParams{
			TextDocument: protocol.TextDocumentIdentifier{URI: uri.File(path)},
		})
		if err != nil {
			m.emitSemantic(request, lspSemanticResult{err: err})
			return
		}
		if answer == nil {
			m.emitSemantic(request, lspSemanticResult{})
			return
		}
		m.emitSemantic(request, lspSemanticResult{
			tokens: decodeSemanticTokens(answer.Data, legend, slots),
		})
	}()
	return cancel
}

// semanticSlotsLocked is the mapping this document's server paints through: the built-in
// standard-name map with the server's own config overrides merged over it. Held under mu.
func (m *lspManager) semanticSlotsLocked(doc lspDocument) map[string]string {
	return semanticSlots(m.cfg.LanguageServers[doc.server].SemanticTokens)
}

// emitSemantic publishes a result, dropping it when a newer fetch has already replaced
// this one — emitRequest's rule, over this lane's own counter.
func (m *lspManager) emitSemantic(request lspSemanticRequest, result lspSemanticResult) {
	m.mu.Lock()
	current := !m.closed && request.id == m.nextSemantic
	m.mu.Unlock()
	if !current {
		return
	}
	result.id, result.path, result.editSeq = request.id, request.path, request.editSeq
	m.emit(lspEvent{semantic: &result})
}
