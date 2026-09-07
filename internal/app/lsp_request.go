package app

import (
	"context"
	"fmt"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"
	"unicode/utf16"

	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"
)

// The on-demand request lane. Completion has a lane of its own (lsp.go) because it is
// typing-driven and carries its own trigger and rebasing rules; everything else a
// language server answers is a request the user asked for, one at a time, where a newer
// one replaces whatever was queued. That is one slot, one generation counter, and one
// worker — the same shape startCompletion has, without bending completion into a
// generic mechanism it would only complicate.

const (
	lspRequestLimit = 3 * time.Second // gopls cold on a large module beats completion's 2s
	lspFormatLimit  = 5 * time.Second // a whole-file format legitimately takes longer
)

type lspRequestKind int

const (
	lspReqDefinition lspRequestKind = iota
	lspReqHover
	lspReqReferences
	lspReqFormat // organize imports followed by formatting, as one answer
	lspReqSignature
)

func (k lspRequestKind) String() string {
	switch k {
	case lspReqDefinition:
		return "definition"
	case lspReqHover:
		return "hover"
	case lspReqReferences:
		return "references"
	case lspReqFormat:
		return "format"
	case lspReqSignature:
		return "signature help"
	}
	return "request"
}

type lspRequest struct {
	id       uint64
	kind     lspRequestKind
	path     string
	editSeq  int
	position protocol.Position
}

// lspLocation is a jump target: an absolute path plus the range to reveal. The URI is
// resolved here so nothing above this file has to know locations arrive as URIs.
type lspLocation struct {
	Path  string
	Range protocol.Range
}

// lspSymbol is one node of an outline. Range is the symbol's full extent, used to
// follow the editor caret; SelectionRange is the name-sized jump target.
type lspSymbol struct {
	Name, Detail   string
	Kind           protocol.SymbolKind
	Range          protocol.Range
	SelectionRange protocol.Range
	Children       []lspSymbol
}

type lspTextEdit struct {
	Range   protocol.Range
	NewText string
}

// lspParameter carries UTF-16 offsets into its signature's label, which is how the
// active parameter gets underlined without re-finding the substring.
type lspParameter struct {
	Label      string
	Start, End int // rune offsets into lspSignature.Label; Start < 0 when unknown
}

type lspSignature struct {
	Label      string
	Doc        string
	Parameters []lspParameter
	Active     int // index into Parameters, or -1
}

// lspRequestResult is the UI-sized projection, the same discipline lspDiagnostic
// follows: every protocol union is collapsed here, so no screen ever sees one.
type lspRequestResult struct {
	id         uint64
	kind       lspRequestKind
	path       string
	editSeq    int
	position   protocol.Position
	locations  []lspLocation // definition, references
	hover      string        // markdown, flattened from HoverContents
	hoverRange *protocol.Range
	edits      []lspTextEdit
	signature  *lspSignature
	err        error
}

// Request queues an on-demand request, replacing any that had not started yet, and
// returns the generation the eventual result will carry. Like RequestCompletion it does
// no IO on the UI goroutine. A zero id means the request was refused outright — the
// document is not tracked, or the server never advertised the capability — and the
// caller should say so rather than wait for a result that will not arrive.
func (m *lspManager) Request(kind lspRequestKind, path string, editSeq int, position protocol.Position) uint64 {
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
	if !ok || !m.supportsLocked(doc, kind) {
		m.mu.Unlock()
		return 0
	}
	m.nextRequest++
	id := m.nextRequest
	m.request = &lspRequest{id: id, kind: kind, path: path, editSeq: editSeq, position: position}
	m.mu.Unlock()
	m.signal()
	return id
}

// Supports reports whether the initialized server for path advertised kind. Before
// initialization nothing is advertised and the answer is false, which is what keeps a
// key pressed during startup from firing a MethodNotFound at a server that would then
// look like a failed session.
func (m *lspManager) Supports(path string, kind lspRequestKind) bool {
	if m == nil || path == "" {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	doc, ok := m.desired[filepath.Clean(path)]
	if !ok {
		return false
	}
	return m.supportsLocked(doc, kind)
}

func (m *lspManager) supportsLocked(doc lspDocument, kind lspRequestKind) bool {
	caps := m.capabilities[doc.key()]
	if caps == nil {
		return false
	}
	switch kind {
	case lspReqDefinition:
		return lspProvided(caps.DefinitionProvider)
	case lspReqHover:
		return lspProvided(caps.HoverProvider)
	case lspReqReferences:
		return lspProvided(caps.ReferencesProvider)
	case lspReqFormat:
		// Either half is worth running on its own: gopls answers both, but a server
		// with only organizeImports still has something useful to do here.
		return lspProvided(caps.DocumentFormattingProvider) || lspProvided(caps.CodeActionProvider)
	case lspReqSignature:
		return caps.SignatureHelpProvider != nil
	}
	return false
}

// lspProvided reads one of the capability unions, every one of which is either a
// Boolean or a pointer to an options struct.
func lspProvided(provider any) bool {
	switch value := provider.(type) {
	case nil:
		return false
	case protocol.Boolean:
		return bool(value)
	}
	return true
}

// SignatureTrigger reports whether the server for path named text as a signature-help
// trigger character. It is CompletionTrigger's twin, over the other advertised set.
func (m *lspManager) SignatureTrigger(path, text string) bool {
	if m == nil || path == "" || text == "" {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	doc, ok := m.desired[filepath.Clean(path)]
	if !ok {
		return false
	}
	return m.signatureTriggers[doc.key()][text]
}

func (m *lspManager) takeRequest() *lspRequest {
	m.mu.Lock()
	defer m.mu.Unlock()
	request := m.request
	m.request = nil
	return request
}

// startRequest flushes the document to its session and starts the RPC, exactly as
// startCompletion does and for the same reason: the notification write happens first,
// on the actor goroutine, so the server observes the text whose position the request
// names. Only the wait moves to a worker.
func (m *lspManager) startRequest(sessions map[string]*lspSession,
	pending map[string]lspDocument, request lspRequest) context.CancelFunc {
	m.mu.Lock()
	doc, ok := m.desired[request.path]
	m.mu.Unlock()
	if !ok || doc.editSeq != request.editSeq {
		m.emitRequest(request, lspRequestResult{})
		return nil
	}
	session := sessions[doc.key()]
	if session == nil || session.server == nil {
		m.emitRequest(request, lspRequestResult{})
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
			m.emitRequest(request, lspRequestResult{err: err})
			return nil
		}
		session.sent[doc.path] = doc.version
		delete(pending, doc.path)
	}

	limit := lspRequestLimit
	if request.kind == lspReqFormat {
		limit = lspFormatLimit
	}
	ctx, cancel := context.WithTimeout(context.Background(), limit)
	server, path := session.server, doc.path
	m.workers.Add(1)
	go func() {
		defer m.workers.Done()
		m.emitRequest(request, dispatchLSPRequest(ctx, server, path, request))
	}()
	return cancel
}

// dispatchLSPRequest issues one request and projects its answer. It runs on a worker and
// touches no manager state, which is what keeps the union unwrapping out of the actor.
func dispatchLSPRequest(ctx context.Context, server protocol.Server, path string,
	request lspRequest) lspRequestResult {
	document := protocol.TextDocumentIdentifier{URI: uri.File(path)}
	positionParams := protocol.TextDocumentPositionParams{TextDocument: document, Position: request.position}
	var result lspRequestResult
	switch request.kind {
	case lspReqDefinition:
		answer, err := server.Definition(ctx, &protocol.DefinitionParams{TextDocumentPositionParams: positionParams})
		result.locations, result.err = projectDefinition(answer), err
	case lspReqReferences:
		answer, err := server.References(ctx, &protocol.ReferenceParams{
			TextDocumentPositionParams: positionParams,
			Context:                    protocol.ReferenceContext{IncludeDeclaration: true},
		})
		result.locations, result.err = projectLocations(answer), err
	case lspReqHover:
		answer, err := server.Hover(ctx, &protocol.HoverParams{TextDocumentPositionParams: positionParams})
		result.err = err
		if answer != nil {
			result.hover, result.hoverRange = flattenHover(answer.Contents), answer.Range
		}
	case lspReqSignature:
		answer, err := server.SignatureHelp(ctx, &protocol.SignatureHelpParams{TextDocumentPositionParams: positionParams})
		result.signature, result.err = projectSignature(answer), err
	case lspReqFormat:
		result.edits, result.err = requestFormatEdits(ctx, server, document, path)
	}
	return result
}

// requestFormatEdits asks for organize-imports and formatting in that order and returns
// their edits concatenated. The order is the one an editor's "format on save" uses:
// imports are rewritten against the source as it stands, then the whole file is laid
// out — and gopls only ever offers import fixes through the code-action path, which is
// why plain formatting is not enough for Go.
//
// Neither half failing aborts the other: a server with formatting and no code actions,
// or the reverse, still gets to do the part it can.
func requestFormatEdits(ctx context.Context, server protocol.Server,
	document protocol.TextDocumentIdentifier, path string) ([]lspTextEdit, error) {
	var edits []lspTextEdit
	var failure error
	actions, err := server.CodeAction(ctx, &protocol.CodeActionParams{
		TextDocument: document,
		Context:      protocol.CodeActionContext{Only: []protocol.CodeActionKind{protocol.CodeActionKindSourceOrganizeImports}},
	})
	if err != nil {
		failure = err
	}
	for _, action := range actions {
		code, ok := action.(*protocol.CodeAction)
		if !ok || code == nil || code.Edit == nil {
			continue
		}
		local, foreign := splitWorkspaceEdit(code.Edit, path)
		if foreign {
			// A code action reaching into other files is not something a format key
			// can apply safely; drop the whole action rather than half of it.
			continue
		}
		edits = append(edits, local...)
	}
	formatted, err := server.Formatting(ctx, &protocol.DocumentFormattingParams{
		TextDocument: document,
		Options:      protocol.FormattingOptions{TabSize: 4, InsertSpaces: false},
	})
	if err != nil && failure == nil {
		failure = err
	}
	edits = append(edits, projectTextEdits(formatted)...)
	if len(edits) > 0 {
		return edits, nil
	}
	return nil, failure
}

// splitWorkspaceEdit takes the edits a WorkspaceEdit aims at path, and reports whether
// it also touches any other file. Both the `changes` map and the newer `documentChanges`
// list are read, since servers answer with either.
func splitWorkspaceEdit(edit *protocol.WorkspaceEdit, path string) (local []lspTextEdit, foreign bool) {
	if edit == nil {
		return nil, false
	}
	for target, edits := range edit.Changes {
		if sameFilePath(uriPath(target), path) {
			local = append(local, projectTextEdits(edits)...)
			continue
		}
		foreign = true
	}
	for _, change := range edit.DocumentChanges {
		textEdit, ok := change.(*protocol.TextDocumentEdit)
		if !ok || textEdit == nil {
			foreign = true // a create/rename/delete operation is out of scope here
			continue
		}
		if !sameFilePath(uriPath(textEdit.TextDocument.URI), path) {
			foreign = true
			continue
		}
		for _, one := range textEdit.Edits {
			if plain, ok := one.(*protocol.TextEdit); ok && plain != nil {
				local = append(local, lspTextEdit{Range: plain.Range, NewText: plain.NewText})
			}
		}
	}
	return local, foreign
}

func projectTextEdits(edits []protocol.TextEdit) []lspTextEdit {
	out := make([]lspTextEdit, 0, len(edits))
	for _, edit := range edits {
		out = append(out, lspTextEdit{Range: edit.Range, NewText: edit.NewText})
	}
	return out
}

// projectDefinition collapses all three arms of the definition union. A DefinitionLink
// carries both the whole symbol and the range worth revealing; the selection range is
// the one that puts the caret on the name rather than on a doc comment above it.
func projectDefinition(result protocol.DefinitionResult) []lspLocation {
	switch value := result.(type) {
	case *protocol.Location:
		if value == nil {
			return nil
		}
		return projectLocations([]protocol.Location{*value})
	case protocol.LocationSlice:
		return projectLocations([]protocol.Location(value))
	case protocol.DefinitionLinkSlice:
		out := make([]lspLocation, 0, len(value))
		for _, link := range value {
			path := uriPath(link.TargetURI)
			if path == "" {
				continue
			}
			out = append(out, lspLocation{Path: path, Range: link.TargetSelectionRange})
		}
		return out
	}
	return nil
}

func projectLocations(locations []protocol.Location) []lspLocation {
	out := make([]lspLocation, 0, len(locations))
	for _, location := range locations {
		path := uriPath(location.URI)
		if path == "" {
			continue
		}
		out = append(out, lspLocation{Path: path, Range: location.Range})
	}
	return out
}

// uriPath is the one place a location's URI becomes a filesystem path. A non-file URI
// (a server answering with a jdt: or zipfile: scheme) yields "", which every caller
// treats as "not a jumpable location" rather than as an error.
func uriPath(u uri.URI) string {
	if !u.IsFile() {
		return ""
	}
	return filepath.Clean(u.FsPath())
}

// sameFilePath compares paths at the LSP URI boundary. File URIs canonicalize
// Windows drive letters (and UNC authorities) to lowercase, while paths received
// from the OS may retain their original casing. They still identify the same file.
func sameFilePath(left, right string) bool {
	left, right = filepath.Clean(left), filepath.Clean(right)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(left, right)
	}
	return left == right
}

// projectSymbols preserves the hierarchy of DocumentSymbol results. The legacy
// SymbolInformation shape has no reliable parent relation, so it stays flat and is
// sorted into document order, which that response shape does not guarantee.
func projectSymbols(result protocol.DocumentSymbolResult) []lspSymbol {
	switch value := result.(type) {
	case protocol.DocumentSymbolSlice:
		var project func([]protocol.DocumentSymbol) []lspSymbol
		project = func(symbols []protocol.DocumentSymbol) []lspSymbol {
			out := make([]lspSymbol, 0, len(symbols))
			for _, symbol := range symbols {
				node := lspSymbol{Name: symbol.Name, Kind: symbol.Kind, Range: symbol.Range,
					SelectionRange: symbol.SelectionRange, Children: project(symbol.Children)}
				if symbol.Detail != nil {
					node.Detail = *symbol.Detail
				}
				out = append(out, node)
			}
			return out
		}
		return project([]protocol.DocumentSymbol(value))
	case protocol.SymbolInformationSlice:
		out := make([]lspSymbol, 0, len(value))
		for _, symbol := range value {
			row := lspSymbol{Name: symbol.Name, Kind: symbol.Kind, Range: symbol.Location.Range,
				SelectionRange: symbol.Location.Range}
			if symbol.ContainerName != nil {
				row.Detail = *symbol.ContainerName
			}
			out = append(out, row)
		}
		sort.SliceStable(out, func(i, j int) bool {
			if out[i].Range.Start.Line != out[j].Range.Start.Line {
				return out[i].Range.Start.Line < out[j].Range.Start.Line
			}
			return out[i].Range.Start.Character < out[j].Range.Start.Character
		})
		return out
	}
	return nil
}

// flattenHover reduces every arm of HoverContents to one markdown string. The
// MarkedString arms are the pre-3.15 shape and are still what several servers answer
// with, so they are wrapped back into fenced blocks rather than dropped.
func flattenHover(contents protocol.HoverContents) string {
	switch value := contents.(type) {
	case *protocol.MarkupContent:
		if value == nil {
			return ""
		}
		return strings.TrimSpace(value.Value)
	case protocol.String:
		return strings.TrimSpace(string(value))
	case *protocol.MarkedStringWithLanguage:
		if value == nil {
			return ""
		}
		return fencedMarkedString(value.Language, value.Value)
	case protocol.MarkedStringSlice:
		parts := make([]string, 0, len(value))
		for _, one := range value {
			switch marked := one.(type) {
			case protocol.String:
				parts = append(parts, strings.TrimSpace(string(marked)))
			case *protocol.MarkedStringWithLanguage:
				if marked != nil {
					parts = append(parts, fencedMarkedString(marked.Language, marked.Value))
				}
			}
		}
		return strings.TrimSpace(strings.Join(parts, "\n\n"))
	}
	return ""
}

func fencedMarkedString(language, value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if language == "" {
		return value
	}
	return fmt.Sprintf("```%s\n%s\n```", language, value)
}

// projectSignature keeps only the active signature. A popup two lines tall has no room
// for the overload list, and the server has already decided which one the caret is in.
func projectSignature(help *protocol.SignatureHelp) *lspSignature {
	if help == nil || len(help.Signatures) == 0 {
		return nil
	}
	index := 0
	if help.ActiveSignature != nil {
		index = int(*help.ActiveSignature)
	}
	if index < 0 || index >= len(help.Signatures) {
		index = 0
	}
	source := help.Signatures[index]
	signature := &lspSignature{Label: source.Label, Active: -1}
	signature.Doc = flattenSignatureDoc(source.Documentation)
	label := []rune(source.Label)
	for _, parameter := range source.Parameters {
		projected := lspParameter{Start: -1, End: -1}
		switch value := parameter.Label.(type) {
		case protocol.String:
			projected.Label = string(value)
			if at := strings.Index(source.Label, projected.Label); at >= 0 && projected.Label != "" {
				projected.Start = len([]rune(source.Label[:at]))
				projected.End = projected.Start + len([]rune(projected.Label))
			}
		case protocol.ParameterInformationLabelTuple:
			// The offsets are UTF-16 code units into the label, as Position is.
			start, end := utf16OffsetToRune(label, int(value[0])), utf16OffsetToRune(label, int(value[1]))
			if start >= 0 && end >= start {
				projected.Start, projected.End = start, end
				projected.Label = string(label[start:end])
			}
		}
		signature.Parameters = append(signature.Parameters, projected)
	}
	active := -1
	if value, ok := source.ActiveParameter.Get(); ok {
		active = int(value)
	} else if value, ok := help.ActiveParameter.Get(); ok {
		active = int(value)
	}
	if active >= 0 && active < len(signature.Parameters) {
		signature.Active = active
	}
	return signature
}

func flattenSignatureDoc(doc protocol.InlayHintTooltip) string {
	switch value := doc.(type) {
	case protocol.String:
		return strings.TrimSpace(string(value))
	case *protocol.MarkupContent:
		if value != nil {
			return strings.TrimSpace(value.Value)
		}
	}
	return ""
}

// utf16OffsetToRune converts a UTF-16 code-unit offset into a rune index, returning -1
// for an offset that lands inside a surrogate pair or past the end.
func utf16OffsetToRune(runes []rune, target int) int {
	units := 0
	for i, r := range runes {
		if units == target {
			return i
		}
		if units > target {
			return -1
		}
		units += utf16.RuneLen(r)
		if units < 0 {
			return -1
		}
	}
	if units == target {
		return len(runes)
	}
	return -1
}

// emitRequest publishes a result, dropping it when a newer request has already replaced
// this one — emitCompletion's rule, over this lane's own counter.
func (m *lspManager) emitRequest(request lspRequest, result lspRequestResult) {
	m.mu.Lock()
	current := !m.closed && request.id == m.nextRequest
	m.mu.Unlock()
	if !current {
		return
	}
	result.id, result.kind = request.id, request.kind
	result.path, result.editSeq, result.position = request.path, request.editSeq, request.position
	m.emit(lspEvent{request: &result})
}
