package app

import (
	"context"
	"io"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/brohd11/bubblestack/components/editor"

	"go.lsp.dev/jsonrpc2"
	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"
)

type recordedLSPCall struct {
	method, text string
	path         string
	version      int32
	position     protocol.Position
	trigger      protocol.CompletionTriggerKind
	triggerChar  string
}

type recordingLSPServer struct {
	protocol.UnimplementedServer
	calls             chan recordedLSPCall
	client            protocol.Client
	mu                sync.Mutex
	completionEnabled bool
	completionResult  protocol.CompletionResult
	initializeParams  *protocol.InitializeParams
	// The language-feature canned answers. Each is nil until a test arms it, and the
	// matching capability is advertised only when armed — which is what lets a test
	// assert that an unadvertised feature is never even requested.
	definitionResult protocol.DefinitionResult
	hoverResult      *protocol.Hover
	referencesResult []protocol.Location
	symbolsResult    protocol.DocumentSymbolResult
	formattingResult []protocol.TextEdit
	codeActionResult []protocol.CommandOrCodeAction
	signatureResult  *protocol.SignatureHelp
	// semanticResult is armed with the legend the server will advertise and the token
	// data it will answer with; the capability appears only when the legend is set.
	semanticLegend []string
	semanticData   []uint32
	// workspaceDiagnostics arms the pull lane: diagnosticProvider is advertised with
	// workspaceDiagnostics only when it is set, which is what lets a test assert that an
	// unadvertised pull is never requested. workspaceReports are handed out one per
	// request, the last repeating once they run out.
	workspaceDiagnostics bool
	workspaceReports     []*protocol.WorkspaceDiagnosticReport
	workspacePulls       []*protocol.WorkspaceDiagnosticParams
}

func newRecordingLSPServer() *recordingLSPServer {
	return &recordingLSPServer{calls: make(chan recordedLSPCall, 32)}
}

func (s *recordingLSPServer) Initialize(_ context.Context, params *protocol.InitializeParams) (*protocol.InitializeResult, error) {
	s.mu.Lock()
	s.initializeParams = params
	enabled := s.completionEnabled
	s.mu.Unlock()
	s.calls <- recordedLSPCall{method: "initialize"}
	result := &protocol.InitializeResult{}
	if enabled {
		result.Capabilities.CompletionProvider = &protocol.CompletionOptions{TriggerCharacters: []string{"."}}
	}
	s.mu.Lock()
	if s.definitionResult != nil {
		result.Capabilities.DefinitionProvider = protocol.Boolean(true)
	}
	if s.hoverResult != nil {
		result.Capabilities.HoverProvider = protocol.Boolean(true)
	}
	if s.referencesResult != nil {
		result.Capabilities.ReferencesProvider = protocol.Boolean(true)
	}
	if s.symbolsResult != nil {
		result.Capabilities.DocumentSymbolProvider = protocol.Boolean(true)
	}
	if s.formattingResult != nil || s.codeActionResult != nil {
		result.Capabilities.DocumentFormattingProvider = protocol.Boolean(true)
		result.Capabilities.CodeActionProvider = protocol.Boolean(true)
	}
	if s.semanticLegend != nil {
		result.Capabilities.SemanticTokensProvider = &protocol.SemanticTokensOptions{
			Legend: protocol.SemanticTokensLegend{TokenTypes: s.semanticLegend},
			Full:   protocol.Boolean(true),
		}
	}
	if s.signatureResult != nil {
		result.Capabilities.SignatureHelpProvider = &protocol.SignatureHelpOptions{TriggerCharacters: []string{"(", ","}}
	}
	if s.workspaceDiagnostics {
		identifier := "recording"
		result.Capabilities.DiagnosticProvider = &protocol.DiagnosticOptions{
			Identifier: &identifier, InterFileDependencies: true, WorkspaceDiagnostics: true,
		}
	}
	s.mu.Unlock()
	return result, nil
}

func (s *recordingLSPServer) Definition(_ context.Context, params *protocol.DefinitionParams) (protocol.DefinitionResult, error) {
	s.calls <- recordedLSPCall{method: "definition", position: params.Position}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.definitionResult, nil
}

func (s *recordingLSPServer) Hover(_ context.Context, params *protocol.HoverParams) (*protocol.Hover, error) {
	s.calls <- recordedLSPCall{method: "hover", position: params.Position}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.hoverResult, nil
}

func (s *recordingLSPServer) SemanticTokensFull(_ context.Context, _ *protocol.SemanticTokensParams) (*protocol.SemanticTokens, error) {
	s.calls <- recordedLSPCall{method: "semanticTokens"}
	s.mu.Lock()
	defer s.mu.Unlock()
	return &protocol.SemanticTokens{Data: s.semanticData}, nil
}

func (s *recordingLSPServer) References(_ context.Context, params *protocol.ReferenceParams) ([]protocol.Location, error) {
	s.calls <- recordedLSPCall{method: "references", position: params.Position}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.referencesResult, nil
}

func (s *recordingLSPServer) DocumentSymbol(_ context.Context, _ *protocol.DocumentSymbolParams) (protocol.DocumentSymbolResult, error) {
	s.calls <- recordedLSPCall{method: "symbols"}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.symbolsResult, nil
}

func (s *recordingLSPServer) Formatting(_ context.Context, _ *protocol.DocumentFormattingParams) ([]protocol.TextEdit, error) {
	s.calls <- recordedLSPCall{method: "formatting"}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.formattingResult, nil
}

func (s *recordingLSPServer) CodeAction(_ context.Context, params *protocol.CodeActionParams) ([]protocol.CommandOrCodeAction, error) {
	call := recordedLSPCall{method: "codeaction"}
	if len(params.Context.Only) > 0 {
		call.text = string(params.Context.Only[0])
	}
	s.calls <- call
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.codeActionResult, nil
}

func (s *recordingLSPServer) SignatureHelp(_ context.Context, params *protocol.SignatureHelpParams) (*protocol.SignatureHelp, error) {
	s.calls <- recordedLSPCall{method: "signature", position: params.Position}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.signatureResult, nil
}

func (s *recordingLSPServer) Initialized(context.Context, *protocol.InitializedParams) error {
	s.calls <- recordedLSPCall{method: "initialized"}
	return nil
}

func (s *recordingLSPServer) DidOpen(_ context.Context, params *protocol.DidOpenTextDocumentParams) error {
	s.calls <- recordedLSPCall{method: "open", path: uriPath(params.TextDocument.URI),
		text: params.TextDocument.Text, version: params.TextDocument.Version}
	return nil
}

func (s *recordingLSPServer) DidChange(_ context.Context, params *protocol.DidChangeTextDocumentParams) error {
	text := ""
	if len(params.ContentChanges) > 0 {
		if whole, ok := params.ContentChanges[0].(*protocol.TextDocumentContentChangeWholeDocument); ok {
			text = whole.Text
		}
	}
	s.calls <- recordedLSPCall{method: "change", path: uriPath(params.TextDocument.URI),
		text: text, version: params.TextDocument.Version}
	return nil
}

func (s *recordingLSPServer) DidSave(context.Context, *protocol.DidSaveTextDocumentParams) error {
	s.calls <- recordedLSPCall{method: "save"}
	return nil
}

func (s *recordingLSPServer) DidClose(_ context.Context, params *protocol.DidCloseTextDocumentParams) error {
	s.calls <- recordedLSPCall{method: "close", path: uriPath(params.TextDocument.URI)}
	return nil
}

func (s *recordingLSPServer) Completion(_ context.Context, params *protocol.CompletionParams) (protocol.CompletionResult, error) {
	call := recordedLSPCall{method: "completion", position: params.Position, trigger: params.Context.TriggerKind}
	if params.Context.TriggerCharacter != nil {
		call.triggerChar = *params.Context.TriggerCharacter
	}
	s.calls <- call
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.completionResult, nil
}

func (s *recordingLSPServer) DiagnosticWorkspace(_ context.Context, params *protocol.WorkspaceDiagnosticParams) (*protocol.WorkspaceDiagnosticReport, error) {
	s.mu.Lock()
	s.workspacePulls = append(s.workspacePulls, params)
	var report *protocol.WorkspaceDiagnosticReport
	switch len(s.workspaceReports) {
	case 0:
		report = &protocol.WorkspaceDiagnosticReport{}
	case 1:
		report = s.workspaceReports[0]
	default:
		report, s.workspaceReports = s.workspaceReports[0], s.workspaceReports[1:]
	}
	s.mu.Unlock()
	s.calls <- recordedLSPCall{method: "workspaceDiagnostic"}
	return report, nil
}

func (s *recordingLSPServer) Shutdown(context.Context) error { return nil }
func (s *recordingLSPServer) Exit(context.Context) error     { return nil }

func (s *recordingLSPServer) publish(path, message string, version int32) error {
	s.mu.Lock()
	client := s.client
	s.mu.Unlock()
	return client.PublishDiagnostics(context.Background(), &protocol.PublishDiagnosticsParams{
		URI: uri.File(path), Version: protocol.NewOptional(version),
		Diagnostics: []protocol.Diagnostic{{
			Range:    protocol.Range{Start: protocol.Position{Line: 1, Character: 2}, End: protocol.Position{Line: 1, Character: 4}},
			Severity: protocol.DiagnosticSeverityError, Message: protocol.String(message),
			Source: protocol.NewOptional("fake"), Code: protocol.String("E1"),
		}},
	})
}

func startRecordingTCPServer(t *testing.T, server *recordingLSPServer) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func(conn net.Conn) {
				_, rpc, client := protocol.NewServer(context.Background(), server, jsonrpc2.NewStream(conn))
				server.mu.Lock()
				server.client = client
				server.mu.Unlock()
				<-rpc.Done()
			}(conn)
		}
	}()
	return listener.Addr().String()
}

func waitLSPCall(t *testing.T, calls <-chan recordedLSPCall, method string) recordedLSPCall {
	t.Helper()
	deadline := time.After(3 * time.Second)
	for {
		select {
		case call := <-calls:
			if call.method == method {
				return call
			}
		case <-deadline:
			t.Fatalf("timed out waiting for LSP %s", method)
		}
	}
}

func assertNoLSPCall(t *testing.T, calls <-chan recordedLSPCall, method string, duration time.Duration) {
	t.Helper()
	timer := time.NewTimer(duration)
	defer timer.Stop()
	for {
		select {
		case call := <-calls:
			if call.method == method {
				t.Fatalf("received unexpected LSP %s", method)
			}
		case <-timer.C:
			return
		}
	}
}

func waitDiagnostics(t *testing.T, manager *lspManager, path string) []lspDiagnostic {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if diagnostics := manager.Diagnostics(path); len(diagnostics) > 0 {
			return diagnostics
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("timed out waiting for diagnostics")
	return nil
}

func waitCompletionResult(t *testing.T, manager *lspManager) *lspCompletionResult {
	t.Helper()
	deadline := time.After(3 * time.Second)
	for {
		select {
		case event := <-manager.events:
			if event.completion != nil {
				return event.completion
			}
		case <-deadline:
			t.Fatal("timed out waiting for LSP completion")
		}
	}
}

// waitLSPCallSet waits until every named method has been seen, and returns the last call
// recorded for each. Unlike waitLSPCall it does not discard what it is not looking for:
// a notification and a request written back to back can be dispatched concurrently on
// the server side, so their recorded order is not the order they were sent in.
func waitLSPCallSet(t *testing.T, calls <-chan recordedLSPCall, methods ...string) map[string]recordedLSPCall {
	t.Helper()
	seen := make(map[string]recordedLSPCall, len(methods))
	wanted := make(map[string]bool, len(methods))
	for _, method := range methods {
		wanted[method] = true
	}
	deadline := time.After(3 * time.Second)
	for len(seen) < len(wanted) {
		select {
		case call := <-calls:
			if wanted[call.method] {
				seen[call.method] = call
			}
		case <-deadline:
			t.Fatalf("timed out waiting for LSP %v; saw %v", methods, seen)
		}
	}
	return seen
}

// waitRequestResult drains manager events until the on-demand lane answers.
func waitRequestResult(t *testing.T, manager *lspManager) *lspRequestResult {
	t.Helper()
	deadline := time.After(3 * time.Second)
	for {
		select {
		case event := <-manager.events:
			if event.request != nil {
				return event.request
			}
		case <-deadline:
			t.Fatal("timed out waiting for an LSP request result")
		}
	}
}

func waitOutlineResult(t *testing.T, manager *lspManager) *lspOutlineResult {
	t.Helper()
	deadline := time.After(3 * time.Second)
	for {
		select {
		case event := <-manager.events:
			if event.outline != nil {
				return event.outline
			}
		case <-deadline:
			t.Fatal("timed out waiting for an LSP outline result")
		}
	}
}

// newRequestLaneCtx wires a live TCP server to one open Python buffer and waits until
// its capabilities have been cached, which is the state every on-demand request needs.
func newRequestLaneCtx(t *testing.T, server *recordingLSPServer, text string) (*Ctx, string, *editor.Screen) {
	t.Helper()
	root := t.TempDir()
	path := filepath.Join(root, "main.py")
	address := startRecordingTCPServer(t, server)
	cfg := DefaultConfig()
	cfg.LanguageServers["python"] = LanguageServerConfig{Address: address}
	c := New("test", cfg, Options{})
	t.Cleanup(c.close)
	c.lsp.changeDebounce = 30 * time.Millisecond
	ed := c.OpenDoc(path, editor.Opts{})
	ed.SetText(text)
	c.lsp.Reconcile(c)
	waitLSPCall(t, server.calls, "initialize")
	waitLSPCall(t, server.calls, "open")
	deadline := time.Now().Add(3 * time.Second)
	for !c.lsp.Supports(path, lspReqHover) && !c.lsp.Supports(path, lspReqDefinition) &&
		!c.lsp.SupportsOutline(path) && !c.lsp.Supports(path, lspReqFormat) &&
		!c.lsp.Supports(path, lspReqReferences) && !c.lsp.Supports(path, lspReqSignature) {
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for the server's capabilities to be cached")
		}
		time.Sleep(5 * time.Millisecond)
	}
	return c, path, ed
}

// TestLSPRequestLaneUnadvertisedCapabilityIsNeverRequested: Supports gates the lane, so
// a server that advertised nothing is never asked. Firing at it would answer
// MethodNotFound, which failSession would surface as a dead server.
func TestLSPRequestLaneUnadvertisedCapabilityIsNeverRequested(t *testing.T) {
	server := newRecordingLSPServer()
	server.hoverResult = &protocol.Hover{Contents: protocol.String("hi")}
	c, path, ed := newRequestLaneCtx(t, server, "value = 1\n")

	if c.lsp.Supports(path, lspReqDefinition) {
		t.Fatal("a server advertising no definition provider must not report support")
	}
	if id := c.lsp.Request(lspReqDefinition, path, ed.EditSeq(), protocol.Position{}); id != 0 {
		t.Fatalf("an unsupported request returned id %d, want a refusal", id)
	}
	assertNoLSPCall(t, server.calls, "definition", 100*time.Millisecond)

	if !c.lsp.Supports(path, lspReqHover) {
		t.Fatal("an advertised hover provider should report support")
	}
	if id := c.lsp.Request(lspReqHover, path, ed.EditSeq(), protocol.Position{}); id == 0 {
		t.Fatal("a supported request was refused")
	}
	waitLSPCall(t, server.calls, "hover")
}

// TestLSPRequestLaneCarriesUTF16Position: the position a request names is the caret's
// column in UTF-16 code units, which is what the client negotiated at initialize.
func TestLSPRequestLaneCarriesUTF16Position(t *testing.T) {
	server := newRecordingLSPServer()
	server.hoverResult = &protocol.Hover{Contents: protocol.String("doc")}
	c, path, ed := newRequestLaneCtx(t, server, "𝄞x = 1\n")

	// One astral rune (two UTF-16 units) then "x": rune column 2 is UTF-16 column 3.
	position, ok := editorPositionToLSP(ed, editor.Position{Line: 0, Column: 2})
	if !ok {
		t.Fatal("the caret position did not convert")
	}
	c.lsp.Request(lspReqHover, path, ed.EditSeq(), position)
	call := waitLSPCall(t, server.calls, "hover")
	if call.position.Character != 3 {
		t.Fatalf("hover asked at character %d, want 3 UTF-16 units", call.position.Character)
	}
	if got := waitRequestResult(t, c.lsp); got.hover != "doc" {
		t.Fatalf("projected hover = %q", got.hover)
	}
}

// TestLSPOutlineLaneFlushesLatestDocument: startOutline writes the pending didChange on
// the actor goroutine before the RPC, so the server describes the latest text.
func TestLSPOutlineLaneFlushesLatestDocument(t *testing.T) {
	server := newRecordingLSPServer()
	server.symbolsResult = protocol.DocumentSymbolSlice{{Name: "f", Kind: protocol.SymbolKindFunction}}
	c, path, ed := newRequestLaneCtx(t, server, "one = 1\n")

	ed.SetText("one = 1\ntwo = 2\n")
	c.lsp.Reconcile(c)
	c.lsp.RequestOutline(path, ed.EditSeq())
	seen := waitLSPCallSet(t, server.calls, "change", "symbols")
	if got := seen["change"].text; got != "one = 1\ntwo = 2\n" {
		t.Fatalf("the flush sent %q, want the latest buffer", got)
	}
	if got := waitOutlineResult(t, c.lsp); len(got.symbols) != 1 || got.symbols[0].Name != "f" {
		t.Fatalf("projected symbols = %#v", got.symbols)
	}
}

func TestLSPOutlineLaneDoesNotReplaceUserRequest(t *testing.T) {
	server := newRecordingLSPServer()
	server.symbolsResult = protocol.DocumentSymbolSlice{{Name: "f", Kind: protocol.SymbolKindFunction}}
	server.hoverResult = &protocol.Hover{Contents: protocol.String("doc")}
	c, path, ed := newRequestLaneCtx(t, server, "value = 1\n")

	requestID := c.lsp.Request(lspReqHover, path, ed.EditSeq(), protocol.Position{})
	outlineID := c.lsp.RequestOutline(path, ed.EditSeq())
	if requestID == 0 || outlineID == 0 {
		t.Fatalf("request ids = user %d outline %d, want both accepted", requestID, outlineID)
	}
	waitLSPCallSet(t, server.calls, "hover", "symbols")

	var request *lspRequestResult
	var outline *lspOutlineResult
	deadline := time.After(3 * time.Second)
	for request == nil || outline == nil {
		select {
		case event := <-c.lsp.events:
			if event.request != nil {
				request = event.request
			}
			if event.outline != nil {
				outline = event.outline
			}
		case <-deadline:
			t.Fatal("timed out waiting for independent outline and user-request results")
		}
	}
	if request.id != requestID || request.hover != "doc" {
		t.Fatalf("user request = %#v, want hover id %d", request, requestID)
	}
	if outline.id != outlineID || len(outline.symbols) != 1 {
		t.Fatalf("outline result = %#v, want id %d with one symbol", outline, outlineID)
	}
}

// TestLSPRequestLaneDropsSupersededResults: only the newest request emits. A stale
// answer must not reopen a popup the user has already moved past.
func TestLSPRequestLaneDropsSupersededResults(t *testing.T) {
	server := newRecordingLSPServer()
	server.hoverResult = &protocol.Hover{Contents: protocol.String("doc")}
	c, path, ed := newRequestLaneCtx(t, server, "value = 1\n")

	first := c.lsp.Request(lspReqHover, path, ed.EditSeq(), protocol.Position{})
	second := c.lsp.Request(lspReqHover, path, ed.EditSeq(), protocol.Position{Character: 1})
	if first == 0 || second == 0 || first == second {
		t.Fatalf("request ids = %d, %d", first, second)
	}
	got := waitRequestResult(t, c.lsp)
	if got.id != second {
		t.Fatalf("result carried id %d, want the newest request %d", got.id, second)
	}
	assertNoRequestResult(t, c.lsp, 100*time.Millisecond)
}

func assertNoRequestResult(t *testing.T, manager *lspManager, duration time.Duration) {
	t.Helper()
	timer := time.NewTimer(duration)
	defer timer.Stop()
	for {
		select {
		case event := <-manager.events:
			if event.request != nil {
				t.Fatalf("received an unexpected extra request result (id %d)", event.request.id)
			}
		case <-timer.C:
			return
		}
	}
}

// TestLSPFormatAsksForOrganizeImportsThenFormatting: gopls only offers import fixes
// through the code-action path, so a format that skipped it would silently do half the
// job on every Go file.
func TestLSPFormatAsksForOrganizeImportsThenFormatting(t *testing.T) {
	server := newRecordingLSPServer()
	importEdit := protocol.TextEdit{
		Range:   protocol.Range{Start: protocol.Position{Line: 0}, End: protocol.Position{Line: 0}},
		NewText: "import os\n",
	}
	server.codeActionResult = []protocol.CommandOrCodeAction{&protocol.CodeAction{
		Title: "Organize Imports",
		Edit:  &protocol.WorkspaceEdit{Changes: map[uri.URI][]protocol.TextEdit{}},
	}}
	server.formattingResult = []protocol.TextEdit{{
		Range:   protocol.Range{Start: protocol.Position{Line: 1}, End: protocol.Position{Line: 1, Character: 3}},
		NewText: "ok",
	}}
	c, path, ed := newRequestLaneCtx(t, server, "one = 1\nbad\n")
	// The action's edit has to name this file to be applied, and the path is only
	// known once the temp dir exists.
	server.mu.Lock()
	server.codeActionResult[0].(*protocol.CodeAction).Edit.Changes[uri.File(path)] = []protocol.TextEdit{importEdit}
	server.mu.Unlock()

	c.lsp.Request(lspReqFormat, path, ed.EditSeq(), protocol.Position{})
	action := waitLSPCall(t, server.calls, "codeaction")
	if action.text != string(protocol.CodeActionKindSourceOrganizeImports) {
		t.Fatalf("code action asked for kind %q, want source.organizeImports", action.text)
	}
	waitLSPCall(t, server.calls, "formatting")
	got := waitRequestResult(t, c.lsp)
	if len(got.edits) != 2 {
		t.Fatalf("format returned %d edits, want the import fix and the formatting", len(got.edits))
	}
	if got.edits[0].NewText != "import os\n" || got.edits[1].NewText != "ok" {
		t.Fatalf("format edits = %#v, want imports first", got.edits)
	}
}

// TestLSPFormatDropsCrossFileActions: an organize-imports action that reaches into
// another file is dropped whole rather than applied in part — a format key must never
// half-edit a file the user is not looking at.
func TestLSPFormatDropsCrossFileActions(t *testing.T) {
	server := newRecordingLSPServer()
	server.formattingResult = []protocol.TextEdit{}
	server.codeActionResult = []protocol.CommandOrCodeAction{&protocol.CodeAction{
		Title: "Organize Imports",
		Edit: &protocol.WorkspaceEdit{Changes: map[uri.URI][]protocol.TextEdit{
			uri.File(filepath.Join(t.TempDir(), "elsewhere.py")): {{NewText: "nope"}},
		}},
	}}
	c, path, ed := newRequestLaneCtx(t, server, "one = 1\n")

	c.lsp.Request(lspReqFormat, path, ed.EditSeq(), protocol.Position{})
	waitLSPCall(t, server.calls, "codeaction")
	waitLSPCall(t, server.calls, "formatting")
	if got := waitRequestResult(t, c.lsp); len(got.edits) != 0 {
		t.Fatalf("a cross-file action contributed %d edits, want none", len(got.edits))
	}
}

func TestLSPTCPDocumentLifecycle(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "main.py")
	server := newRecordingLSPServer()
	address := startRecordingTCPServer(t, server)
	cfg := DefaultConfig()
	cfg.LanguageServers["python"] = LanguageServerConfig{Address: address}
	c := New("test", cfg, Options{})
	defer c.close()
	c.lsp.changeDebounce = 30 * time.Millisecond
	ed := c.OpenDoc(path, editor.Opts{})
	ed.SetText("print('one')\n")

	if !c.lsp.Reconcile(c) {
		t.Fatal("an open Python document should activate LSP")
	}
	waitLSPCall(t, server.calls, "initialize")
	opened := waitLSPCall(t, server.calls, "open")
	if opened.version != 1 || opened.text != "print('one')\n" {
		t.Fatalf("didOpen = version %d text %q", opened.version, opened.text)
	}

	ed.SetText("print('two')\n")
	c.lsp.Reconcile(c)
	changed := waitLSPCall(t, server.calls, "change")
	if changed.version != 2 || changed.text != "print('two')\n" {
		t.Fatalf("didChange = version %d text %q", changed.version, changed.text)
	}

	c.lsp.DidSave(path)
	waitLSPCall(t, server.calls, "save")
	if err := server.publish(path, "current", 2); err != nil {
		t.Fatal(err)
	}
	if got := waitDiagnostics(t, c.lsp, path)[0]; got.Message != "current" || got.Source != "fake" || got.Code != "E1" {
		t.Fatalf("projected diagnostic = %#v", got)
	}
	if err := server.publish(path, "stale", 1); err != nil {
		t.Fatal(err)
	}
	time.Sleep(30 * time.Millisecond)
	if got := c.lsp.Diagnostics(path)[0].Message; got != "current" {
		t.Fatalf("stale diagnostics replaced the current set with %q", got)
	}

	newPath := filepath.Join(root, "renamed.py")
	c.RekeyDoc(path, newPath, ed)
	c.lsp.Reconcile(c)
	// One converge pass sends didClose then didOpen, and the wire keeps that order — but
	// the test server is served through jsonrpc2's AsyncHandler, which dispatches in wire
	// order and then runs the handlers concurrently. Which of the two reaches the recorder
	// first is a race, and waiting for them one at a time throws the other away: a loaded
	// CI machine loses it. Wait for the pair instead.
	reopened := waitLSPCallSet(t, server.calls, "close", "open")["open"]
	if reopened.version != 1 || reopened.text != "print('two')\n" {
		t.Fatalf("save-as didOpen = version %d text %q", reopened.version, reopened.text)
	}
	if got := c.lsp.Diagnostics(path); len(got) != 0 {
		t.Fatalf("rekeyed document retained old-path diagnostics: %#v", got)
	}
	// A publish for a URI that is not an open buffer is now KEPT, and marks this root as
	// one whose server speaks for more than the open tabs. That is the whole mechanism
	// behind a server like gdscript-lsp, which diagnoses its entire project unasked and
	// never has most of those files opened. Discarding these was the old behaviour.
	if err := server.publish(path, "closed", 2); err != nil {
		t.Fatal(err)
	}
	if got := waitDiagnostics(t, c.lsp, path); got[0].Message != "closed" {
		t.Fatalf("a volunteered report for an unopened URI was dropped: %#v", got)
	}

	c.CloseDoc(newPath)
	c.lsp.Reconcile(c)
	waitLSPCall(t, server.calls, "close")
}

func TestLSPDebouncesDocumentChanges(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "main.py")
	server := newRecordingLSPServer()
	address := startRecordingTCPServer(t, server)
	cfg := DefaultConfig()
	cfg.LanguageServers["python"] = LanguageServerConfig{Address: address}
	c := New("test", cfg, Options{})
	defer c.close()
	c.lsp.changeDebounce = 80 * time.Millisecond
	ed := c.OpenDoc(path, editor.Opts{})
	ed.SetText("first\n")
	c.lsp.Reconcile(c)
	waitLSPCall(t, server.calls, "open")

	ed.SetText("second\n")
	c.lsp.Reconcile(c)
	assertNoLSPCall(t, server.calls, "change", 50*time.Millisecond)
	ed.SetText("third\n")
	c.lsp.Reconcile(c)
	// This wait crosses the first edit's deadline. The old timer must recognize that
	// version three has not received its own quiet period and start a fresh window.
	assertNoLSPCall(t, server.calls, "change", 50*time.Millisecond)
	changed := waitLSPCall(t, server.calls, "change")
	if changed.version != 3 || changed.text != "third\n" {
		t.Fatalf("debounced didChange = version %d text %q", changed.version, changed.text)
	}

	// A rename during another pending edit must close the old URI and immediately
	// open the replacement with the latest text, never emitting the stale change.
	ed.SetText("renamed\n")
	c.lsp.Reconcile(c)
	newPath := filepath.Join(root, "renamed.py")
	c.RekeyDoc(path, newPath, ed)
	c.lsp.Reconcile(c)
	// The close and the open race to the recorder; see the pair wait in the lifecycle test.
	reopened := waitLSPCallSet(t, server.calls, "close", "open")["open"]
	if reopened.version != 1 || reopened.text != "renamed\n" {
		t.Fatalf("rekeyed didOpen = version %d text %q", reopened.version, reopened.text)
	}
	assertNoLSPCall(t, server.calls, "change", 2*c.lsp.changeDebounce)
}

func TestLSPCompletionFlushesLatestDocumentAndProjectsItems(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "main.py")
	server := newRecordingLSPServer()
	server.completionEnabled = true
	server.completionResult = protocol.CompletionItemSlice{
		{
			Label: "print", Detail: protocol.NewOptional("function"), FilterText: protocol.NewOptional("print"),
			TextEdit: &protocol.TextEdit{
				Range:   protocol.Range{Start: protocol.Position{}, End: protocol.Position{Character: 3}},
				NewText: "print",
			},
		},
		{Label: "snippet", InsertTextFormat: protocol.InsertTextFormatSnippet, InsertText: protocol.NewOptional("${1:value}")},
		{Label: "unsupported", InsertTextFormat: protocol.InsertTextFormatSnippet, InsertText: protocol.NewOptional("${TM_FILENAME}")},
	}
	address := startRecordingTCPServer(t, server)
	cfg := DefaultConfig()
	cfg.LanguageServers["python"] = LanguageServerConfig{Address: address}
	c := New("test", cfg, Options{})
	defer c.close()
	c.lsp.changeDebounce = time.Second
	ed := c.OpenDoc(path, editor.Opts{})
	ed.SetText("p")
	c.lsp.Reconcile(c)
	waitLSPCall(t, server.calls, "open")

	if !c.lsp.CompletionTrigger(path, ".") {
		t.Fatal("server completion trigger was not retained")
	}
	ed.SetText("pri")
	c.lsp.Reconcile(c)
	id := c.lsp.RequestCompletion(path, ed.EditSeq(), protocol.Position{Character: 3}, ".", false)
	if id == 0 {
		t.Fatal("completion request was rejected")
	}
	var changed, completed recordedLSPCall
	deadline := time.After(3 * time.Second)
	for changed.method == "" || completed.method == "" {
		select {
		case call := <-server.calls:
			switch call.method {
			case "change":
				changed = call
			case "completion":
				completed = call
			}
		case <-deadline:
			t.Fatal("timed out waiting for change and completion calls")
		}
	}
	if changed.text != "pri" || completed.position.Character != 3 ||
		completed.trigger != protocol.CompletionTriggerKindTriggerCharacter || completed.triggerChar != "." {
		t.Fatalf("completion flush/context: change=%+v completion=%+v", changed, completed)
	}
	result := waitCompletionResult(t, c.lsp)
	if result.id != id || len(result.items) != 2 || result.items[0].Label != "print" ||
		result.items[0].Edit == nil || result.items[0].Edit.NewText != "print" {
		t.Fatalf("projected completion = %#v", result)
	}
	if item := result.items[1]; !item.Snippet || item.InsertText != "value" ||
		!reflect.DeepEqual(item.Stops, []editor.CompletionStop{{Index: 1, Start: 0, End: 5}}) {
		t.Fatalf("projected snippet = %#v", item)
	}

	server.mu.Lock()
	params := server.initializeParams
	server.mu.Unlock()
	if params == nil || params.Capabilities.TextDocument == nil ||
		params.Capabilities.TextDocument.Completion == nil || params.Capabilities.General == nil ||
		params.Capabilities.TextDocument.Completion.CompletionItem == nil ||
		params.Capabilities.TextDocument.Completion.CompletionItem.SnippetSupport == nil ||
		!*params.Capabilities.TextDocument.Completion.CompletionItem.SnippetSupport ||
		len(params.Capabilities.General.PositionEncodings) != 1 ||
		params.Capabilities.General.PositionEncodings[0] != protocol.PositionEncodingKindUTF16 {
		t.Fatalf("completion client capabilities = %#v", params)
	}
	if !strings.Contains(string(params.InitializationOptions), `"include_params":true`) {
		t.Fatalf("Python initialization options = %s", params.InitializationOptions)
	}
}

func TestLSPSaveFlushesPendingChange(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "main.py")
	server := newRecordingLSPServer()
	address := startRecordingTCPServer(t, server)
	cfg := DefaultConfig()
	cfg.LanguageServers["python"] = LanguageServerConfig{Address: address}
	c := New("test", cfg, Options{})
	defer c.close()
	c.lsp.changeDebounce = time.Second
	ed := c.OpenDoc(path, editor.Opts{})
	ed.SetText("before\n")
	c.lsp.Reconcile(c)
	waitLSPCall(t, server.calls, "open")

	ed.SetText("saved\n")
	c.lsp.Reconcile(c)
	assertNoLSPCall(t, server.calls, "change", 30*time.Millisecond)
	c.lsp.DidSave(path)

	deadline := time.After(250 * time.Millisecond)
	seen := map[string]bool{}
	for len(seen) < 2 {
		select {
		case call := <-server.calls:
			if call.method != "change" && call.method != "save" {
				continue
			}
			if seen[call.method] {
				t.Fatalf("duplicate save flush call %q", call.method)
			}
			seen[call.method] = true
			if call.method == "change" && (call.version != 2 || call.text != "saved\n") {
				t.Fatalf("save-flushed didChange = version %d text %q", call.version, call.text)
			}
		case <-deadline:
			t.Fatalf("timed out waiting for immediate LSP change/save; saw %v", seen)
		}
	}
}

func TestLSPRestartOpensLatestPendingText(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "main.py")
	server := newRecordingLSPServer()
	address := startRecordingTCPServer(t, server)
	cfg := DefaultConfig()
	cfg.LanguageServers["python"] = LanguageServerConfig{Address: address}
	c := New("test", cfg, Options{})
	defer c.close()
	c.lsp.changeDebounce = time.Second
	ed := c.OpenDoc(path, editor.Opts{})
	ed.SetText("before\n")
	c.lsp.Reconcile(c)
	waitLSPCall(t, server.calls, "open")

	ed.SetText("latest\n")
	c.lsp.Reconcile(c)
	c.lsp.Restart()
	waitLSPCall(t, server.calls, "initialize")
	reopened := waitLSPCall(t, server.calls, "open")
	if reopened.version != 2 || reopened.text != "latest\n" {
		t.Fatalf("restart didOpen = version %d text %q", reopened.version, reopened.text)
	}
	assertNoLSPCall(t, server.calls, "change", 100*time.Millisecond)
}

// TestLSPHelperProcess becomes a tiny pylsp-shaped stdio server only in the subprocess
// spawned by TestLSPStdioTransport. The ordinary test run returns immediately.
func TestLSPHelperProcess(t *testing.T) {
	if os.Getenv("GOTE_LSP_HELPER") != "1" {
		return
	}
	server := newRecordingLSPServer()
	transport := &testStdio{Reader: os.Stdin, Writer: os.Stdout}
	_, conn, client := protocol.NewServer(context.Background(), server, jsonrpc2.NewStream(transport))
	server.mu.Lock()
	server.client = client
	server.mu.Unlock()
	go func() {
		for call := range server.calls {
			if call.method == "open" {
				_ = client.PublishDiagnostics(context.Background(), &protocol.PublishDiagnosticsParams{
					URI: uri.File(os.Getenv("GOTE_LSP_PATH")), Version: protocol.NewOptional(call.version),
					Diagnostics: []protocol.Diagnostic{{
						Range: protocol.Range{}, Severity: protocol.DiagnosticSeverityWarning,
						Message: protocol.String("stdio works"),
					}},
				})
			}
		}
	}()
	<-conn.Done()
	os.Exit(0)
}

type testStdio struct {
	io.Reader
	io.Writer
}

func (*testStdio) Close() error { return nil }

func TestLSPStdioTransport(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "script.py")
	t.Setenv("GOTE_LSP_HELPER", "1")
	t.Setenv("GOTE_LSP_PATH", path)
	cfg := DefaultConfig()
	cfg.LanguageServers["python"] = LanguageServerConfig{Command: []string{
		os.Args[0], "-test.run=^TestLSPHelperProcess$",
	}}
	c := New("test", cfg, Options{})
	defer c.close()
	ed := c.OpenDoc(path, editor.Opts{})
	ed.SetText("print('stdio')\n")
	c.lsp.Reconcile(c)

	diagnostics := waitDiagnostics(t, c.lsp, path)
	if diagnostics[0].Message != "stdio works" || diagnostics[0].Severity != protocol.DiagnosticSeverityWarning {
		t.Fatalf("stdio diagnostic = %#v", diagnostics[0])
	}
}

func TestLSPRoots(t *testing.T) {
	root := t.TempDir()
	pythonDir := filepath.Join(root, "python", "pkg")
	if err := os.MkdirAll(pythonDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "python", "pyproject.toml"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if got := lspRootForPath(filepath.Join(pythonDir, "main.py"), languageForPath("main.py")); got != filepath.Join(root, "python") {
		t.Fatalf("Python root = %q", got)
	}
	if got := lspRootForPath(filepath.Join(root, "orphan.gd"), languageForPath("orphan.gd")); got != "" {
		t.Fatalf("orphan GDScript root = %q, want inactive", got)
	}
	if err := os.WriteFile(filepath.Join(root, "project.godot"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if got := lspRootForPath(filepath.Join(root, "src", "player.gd"), languageForPath("player.gd")); got != root {
		t.Fatalf("GDScript root = %q, want %q", got, root)
	}
}

// A module inside a go.work belongs to the workspace, so the outer marker has to beat the
// nearer go.mod — otherwise every module in a monorepo gets its own gopls.
func TestLSPRootPrefersWorkspace(t *testing.T) {
	goProfile := languageForPath("main.go")
	if goProfile == nil || goProfile.lsp == nil {
		t.Fatal("Go should carry LSP activation metadata")
	}
	write := func(t *testing.T, dir, name string) {
		t.Helper()
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	work := t.TempDir()
	mod := filepath.Join(work, "mod")
	pkg := filepath.Join(mod, "internal", "app")
	write(t, work, "go.work")
	write(t, mod, "go.mod")
	if err := os.MkdirAll(pkg, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := lspRootForPath(filepath.Join(pkg, "main.go"), goProfile); got != work {
		t.Fatalf("Go root = %q, want the workspace %q", got, work)
	}

	// The same tree without the workspace file falls back to the module, exactly as it
	// did before workspaceMarkers existed.
	lone := t.TempDir()
	loneMod := filepath.Join(lone, "mod")
	write(t, loneMod, "go.mod")
	if got := lspRootForPath(filepath.Join(loneMod, "main.go"), goProfile); got != loneMod {
		t.Fatalf("Go root = %q, want the module %q", got, loneMod)
	}

	// No marker at all still opens a session, from the file's own directory.
	bare := t.TempDir()
	if got := lspRootForPath(filepath.Join(bare, "main.go"), goProfile); got != bare {
		t.Fatalf("unmarked Go root = %q, want %q", got, bare)
	}
}

// C# names its project file after the project, so the marker has to be a pattern rather
// than something exact to stat for.
func TestGlobRootMarkers(t *testing.T) {
	csharp := languageForPath("Gen.cs")
	if csharp == nil || csharp.lsp == nil {
		t.Fatal("C# should carry LSP activation metadata")
	}

	root := t.TempDir()
	deep := filepath.Join(root, "glue", "GodotSharp", "Godot.SourceGenerators")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	sln := filepath.Join(root, "glue", "GodotSharp")
	if err := os.WriteFile(filepath.Join(sln, "GodotSharp.sln"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if got := lspRootForPath(filepath.Join(deep, "Gen.cs"), csharp); got != sln {
		t.Fatalf("C# root = %q, want the solution directory %q", got, sln)
	}

	// A pattern that matches nothing keeps walking rather than claiming the directory.
	bare := t.TempDir()
	nested := filepath.Join(bare, "src")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := lspRootForPath(filepath.Join(nested, "Gen.cs"), csharp); got != nested {
		t.Fatalf("unmatched C# root = %q, want the file's own directory %q", got, nested)
	}

	// Exact markers are untouched by the pattern branch.
	py := t.TempDir()
	if err := os.WriteFile(filepath.Join(py, "pyproject.toml"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	pkg := filepath.Join(py, "pkg")
	if err := os.MkdirAll(pkg, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := lspRootForPath(filepath.Join(pkg, "main.py"), languageForPath("main.py")); got != py {
		t.Fatalf("Python root = %q, want %q", got, py)
	}
}

func TestLSPInvalidTransportBackoffAndRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "main.py")
	cfg := DefaultConfig()
	cfg.LanguageServers["python"] = LanguageServerConfig{Address: "127.0.0.1:6005", Command: []string{"pylsp"}}
	c := New("test", cfg, Options{})
	defer c.close()
	c.OpenDoc(path, editor.Opts{}).SetText("pass\n")
	c.lsp.Reconcile(c)

	waitFailure := func() {
		t.Helper()
		select {
		case event := <-c.lsp.events:
			if !strings.Contains(event.status, "exactly one of address or command") {
				t.Fatalf("failure status = %q", event.status)
			}
		case <-time.After(time.Second):
			t.Fatal("timed out waiting for invalid-transport status")
		}
	}
	waitFailure()
	c.lsp.Reconcile(c)
	select {
	case event := <-c.lsp.events:
		t.Fatalf("backoff repeated the status immediately: %#v", event)
	case <-time.After(50 * time.Millisecond):
	}
	c.lsp.Restart()
	c.lsp.Reconcile(c)
	waitFailure()
}
