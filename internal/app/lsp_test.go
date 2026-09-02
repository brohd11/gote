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

	"github.com/brohd11/bubblestack/components"

	"go.lsp.dev/jsonrpc2"
	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"
)

type recordedLSPCall struct {
	method, text string
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
	return result, nil
}

func (s *recordingLSPServer) Initialized(context.Context, *protocol.InitializedParams) error {
	s.calls <- recordedLSPCall{method: "initialized"}
	return nil
}

func (s *recordingLSPServer) DidOpen(_ context.Context, params *protocol.DidOpenTextDocumentParams) error {
	s.calls <- recordedLSPCall{method: "open", text: params.TextDocument.Text, version: params.TextDocument.Version}
	return nil
}

func (s *recordingLSPServer) DidChange(_ context.Context, params *protocol.DidChangeTextDocumentParams) error {
	text := ""
	if len(params.ContentChanges) > 0 {
		if whole, ok := params.ContentChanges[0].(*protocol.TextDocumentContentChangeWholeDocument); ok {
			text = whole.Text
		}
	}
	s.calls <- recordedLSPCall{method: "change", text: text, version: params.TextDocument.Version}
	return nil
}

func (s *recordingLSPServer) DidSave(context.Context, *protocol.DidSaveTextDocumentParams) error {
	s.calls <- recordedLSPCall{method: "save"}
	return nil
}

func (s *recordingLSPServer) DidClose(context.Context, *protocol.DidCloseTextDocumentParams) error {
	s.calls <- recordedLSPCall{method: "close"}
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
	ed := c.OpenDoc(path, components.EditorOpts{})
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
	waitLSPCall(t, server.calls, "close")
	reopened := waitLSPCall(t, server.calls, "open")
	if reopened.version != 1 || reopened.text != "print('two')\n" {
		t.Fatalf("save-as didOpen = version %d text %q", reopened.version, reopened.text)
	}
	if got := c.lsp.Diagnostics(path); len(got) != 0 {
		t.Fatalf("rekeyed document retained old-path diagnostics: %#v", got)
	}
	if err := server.publish(path, "closed", 2); err != nil {
		t.Fatal(err)
	}
	time.Sleep(30 * time.Millisecond)
	if got := c.lsp.Diagnostics(path); len(got) != 0 {
		t.Fatalf("closed URI accepted diagnostics: %#v", got)
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
	ed := c.OpenDoc(path, components.EditorOpts{})
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
	waitLSPCall(t, server.calls, "close")
	reopened := waitLSPCall(t, server.calls, "open")
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
	ed := c.OpenDoc(path, components.EditorOpts{})
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
		!reflect.DeepEqual(item.Stops, []components.EditorCompletionStop{{Index: 1, Start: 0, End: 5}}) {
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
	ed := c.OpenDoc(path, components.EditorOpts{})
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
	ed := c.OpenDoc(path, components.EditorOpts{})
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
	ed := c.OpenDoc(path, components.EditorOpts{})
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

func TestLSPInvalidTransportBackoffAndRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "main.py")
	cfg := DefaultConfig()
	cfg.LanguageServers["python"] = LanguageServerConfig{Address: "127.0.0.1:6005", Command: []string{"pylsp"}}
	c := New("test", cfg, Options{})
	defer c.close()
	c.OpenDoc(path, components.EditorOpts{}).SetText("pass\n")
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
