package app

import (
	"context"
	"path/filepath"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/brohd11/bubblestack/core"
	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"
)

// The semantic-token lane has its own slot and counter because it runs in the background:
// sharing the on-demand lane would let it evict a request the user just made.

// semanticToken is one painted run: a row, a UTF-16 span, and the palette slot, already
// resolved from the type so a decoded set is self-contained.
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

// semanticLegend returns the server's token type list from the capability union; false
// when absent.
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

// decodeSemanticTokens unpacks the wire format of relative five-tuples:
//
//	deltaLine, deltaStartChar, length, typeIndex, modifiers
//
// deltaStartChar is relative to the previous token only on the same row. typeIndex indexes
// the server's legend, so mapping is by name. Unmapped types are dropped, leaving chroma's
// span.
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

// RequestSemanticTokens queues a fetch for path, replacing an unstarted one. 0 means there
// is nothing to ask.
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

// startSemantic flushes the document and starts the semantic-tokens RPC on a worker.
func (m *lspManager) startSemantic(sessions map[string]*lspSession,
	pending map[string]lspDocument, request lspSemanticRequest) context.CancelFunc {
	var legend []string
	var slots map[string]string
	_, session, err := m.flushForRequest(sessions, pending, request.path, request.editSeq,
		func(doc lspDocument, _ *lspSession) bool {
			m.mu.Lock()
			legend, slots = m.semanticLegends[doc.key()], m.semanticSlotsLocked(doc)
			m.mu.Unlock()
			return len(legend) > 0
		})
	if session == nil {
		m.emitSemantic(request, lspSemanticResult{err: err})
		return nil
	}

	server, path := session.server, request.path
	return m.goRequest(lspRequestLimit, func(ctx context.Context) {
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
	})
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

// semanticDebounce matches the editor's highlight debounce, so both refresh on one
// cadence.
const semanticDebounce = 250 * time.Millisecond

type semanticTick struct {
	target     *homeScreen
	generation int
}

// scheduleSemanticTokens debounces behind a generation counter, so a typing burst sends
// one fetch once it settles (each fetch flushes the whole document).
func (s *homeScreen) scheduleSemanticTokens() tea.Cmd {
	if s.editor == nil || s.currentPath == "" {
		return nil
	}
	seq := s.editor.EditSeq()
	if s.semanticPath == s.currentPath && s.semanticSeq == seq {
		return nil // this generation has already been asked for
	}
	s.semanticGen++
	generation := s.semanticGen
	return tea.Tick(semanticDebounce, func(time.Time) tea.Msg {
		return semanticTick{target: s, generation: generation}
	})
}

// handleSemanticTick issues the fetch unless a later edit superseded it. A refusal is not
// recorded or reported, so a document opened before its server started is retried after
// the next edit.
func (s *homeScreen) handleSemanticTick(sh *core.Shared, tick semanticTick) {
	if tick.target != s || tick.generation != s.semanticGen {
		return
	}
	if s.editor == nil || s.currentPath == "" {
		return
	}
	c := Of(sh)
	if c == nil || c.lsp == nil {
		return
	}
	seq := s.editor.EditSeq()
	if c.lsp.RequestSemanticTokens(s.currentPath, seq) != 0 {
		s.semanticPath, s.semanticSeq = s.currentPath, seq
	}
}
