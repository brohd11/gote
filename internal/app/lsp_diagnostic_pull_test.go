package app

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/brohd11/bubblestack/components/editor"

	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"
)

// pythonProject lays down a directory pylsp's root markers will claim, so lspRootForPath
// resolves the same root for every file in it rather than each file's own directory.
func pythonProject(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	writeProjectFile(t, filepath.Join(root, "setup.py"), "")
	for name, text := range files {
		writeProjectFile(t, filepath.Join(root, name), text)
	}
	return root
}

func writeProjectFile(t *testing.T, path, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

// waitLSPCallFor waits for a call to method naming path. Like waitLSPCallSet it exists
// because the recorder discards what it is not looking for.
func waitLSPCallFor(t *testing.T, calls <-chan recordedLSPCall, method, path string) recordedLSPCall {
	t.Helper()
	deadline := time.After(3 * time.Second)
	for {
		select {
		case call := <-calls:
			if call.method == method && sameFilePath(call.path, path) {
				return call
			}
		case <-deadline:
			t.Fatalf("timed out waiting for LSP %s of %s", method, path)
		}
	}
}

func assertNoLSPCallFor(t *testing.T, calls <-chan recordedLSPCall, method, path string, duration time.Duration) {
	t.Helper()
	timer := time.NewTimer(duration)
	defer timer.Stop()
	for {
		select {
		case call := <-calls:
			if call.method == method && sameFilePath(call.path, path) {
				t.Fatalf("received unexpected LSP %s of %s", method, path)
			}
		case <-timer.C:
			return
		}
	}
}

func waitLSPEvent(manager *lspManager, d time.Duration) bool {
	select {
	case <-manager.events:
		return true
	case <-time.After(d):
		return false
	}
}

func drainLSPEvents(manager *lspManager, quiet time.Duration) {
	for {
		select {
		case <-manager.events:
		case <-time.After(quiet):
			return
		}
	}
}

func fullReport(path, message, resultID string) *protocol.WorkspaceFullDocumentDiagnosticReport {
	report := &protocol.WorkspaceFullDocumentDiagnosticReport{URI: uri.File(path)}
	report.Kind = "full"
	report.ResultID = &resultID
	report.Items = []protocol.Diagnostic{{
		Range:    protocol.Range{Start: protocol.Position{Line: 1}, End: protocol.Position{Line: 1, Character: 3}},
		Severity: protocol.DiagnosticSeverityError, Message: protocol.String(message),
	}}
	return report
}

func newPullCtx(t *testing.T, server *recordingLSPServer, root string) *Ctx {
	t.Helper()
	cfg := DefaultConfig()
	cfg.LanguageServers["python"] = LanguageServerConfig{Address: startRecordingTCPServer(t, server)}
	c := New("test", cfg, Options{})
	t.Cleanup(c.close)
	main := filepath.Join(root, "main.py")
	c.OpenDoc(main, editor.Opts{}).SetText("print('main')\n")
	if !c.lsp.Reconcile(c) {
		t.Fatal("an open Python document should activate LSP")
	}
	return c
}

// The point of the lane: one request, and files nobody opened come back diagnosed.
func TestWorkspacePullReportsUnopenedFiles(t *testing.T) {
	root := pythonProject(t, map[string]string{"main.py": "print('main')\n", "other.py": "print('other')\n"})
	other := filepath.Join(root, "other.py")
	server := newRecordingLSPServer()
	server.workspaceDiagnostics = true
	server.workspaceReports = []*protocol.WorkspaceDiagnosticReport{
		{Items: []protocol.WorkspaceDocumentDiagnosticReport{fullReport(other, "pulled", "r1")}},
	}
	c := newPullCtx(t, server, root)

	waitLSPCall(t, server.calls, "workspaceDiagnostic")
	got := waitDiagnostics(t, c.lsp, other)
	if len(got) != 1 || got[0].Message != "pulled" {
		t.Fatalf("pulled diagnostics for an unopened file = %#v", got)
	}
	// Nothing was opened to get them — that is the whole difference from walking the
	// project and sending didOpen for every file.
	assertNoLSPCallFor(t, server.calls, "open", other, 100*time.Millisecond)
}

// The gopls case: no diagnosticProvider, so the lane must stay silent rather than fire a
// request the server would answer with MethodNotFound — which failSession would surface
// as a dead server on a 15s backoff.
func TestWorkspacePullSkippedWhenUnadvertised(t *testing.T) {
	root := pythonProject(t, map[string]string{"main.py": "print('main')\n"})
	server := newRecordingLSPServer()
	c := newPullCtx(t, server, root)
	waitLSPCallFor(t, server.calls, "open", filepath.Join(root, "main.py"))
	assertNoLSPCall(t, server.calls, "workspaceDiagnostic", 300*time.Millisecond)
	if got := c.lsp.AllDiagnostics(); len(got) != 0 {
		t.Fatalf("a server that advertised nothing produced %#v", got)
	}
}

// An unchanged report is the server saying "what you have still stands", and it can only
// say that because the previous result id went back out with the request.
func TestWorkspacePullEchoesResultIDsAndHonoursUnchanged(t *testing.T) {
	root := pythonProject(t, map[string]string{"main.py": "print('main')\n", "other.py": "print('other')\n"})
	other := filepath.Join(root, "other.py")
	unchanged := &protocol.WorkspaceUnchangedDocumentDiagnosticReport{URI: uri.File(other)}
	unchanged.Kind = "unchanged"
	unchanged.ResultID = "r2"
	server := newRecordingLSPServer()
	server.workspaceDiagnostics = true
	server.workspaceReports = []*protocol.WorkspaceDiagnosticReport{
		{Items: []protocol.WorkspaceDocumentDiagnosticReport{fullReport(other, "first", "r1")}},
		{Items: []protocol.WorkspaceDocumentDiagnosticReport{unchanged}},
	}
	c := newPullCtx(t, server, root)
	waitDiagnostics(t, c.lsp, other)

	// The second pull is paced by lspPullMinInterval, so give it room.
	deadline := time.Now().Add(6 * time.Second)
	for time.Now().Before(deadline) {
		server.mu.Lock()
		pulls := len(server.workspacePulls)
		server.mu.Unlock()
		if pulls >= 2 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	server.mu.Lock()
	pulls := append([]*protocol.WorkspaceDiagnosticParams(nil), server.workspacePulls...)
	server.mu.Unlock()
	if len(pulls) < 2 {
		t.Fatalf("the lane issued %d pulls, want it to keep asking", len(pulls))
	}
	var echoed string
	for _, previous := range pulls[1].PreviousResultIds {
		if sameFilePath(uriPath(previous.URI), other) {
			echoed = previous.Value
		}
	}
	if echoed != "r1" {
		t.Fatalf("second pull echoed previousResultId %q, want the stored r1", echoed)
	}
	if got := c.lsp.Diagnostics(other); len(got) != 1 || got[0].Message != "first" {
		t.Fatalf("an unchanged report discarded what it said was unchanged: %#v", got)
	}
}

// A server that pushes without being asked — gdscript-lsp diagnoses its whole project on
// `initialized` and volunteers the result, and rust-analyzer does it across a crate.
// Keeping those is the entire diagnostics story for both.
func TestVolunteeredDiagnosticsForUnopenedFilesAreKept(t *testing.T) {
	root := pythonProject(t, map[string]string{"main.py": "print('main')\n"})
	server := newRecordingLSPServer()
	c := newPullCtx(t, server, root)
	main := filepath.Join(root, "main.py")
	waitLSPCallFor(t, server.calls, "open", main)

	volunteered := filepath.Join(root, "never", "opened.py")
	if err := server.publish(volunteered, "volunteered", 1); err != nil {
		t.Fatal(err)
	}
	if got := waitDiagnostics(t, c.lsp, volunteered); got[0].Message != "volunteered" {
		t.Fatalf("volunteered diagnostics = %#v", got)
	}
	// And they outlive the tab: the server still stands behind them.
	c.CloseDoc(main)
	c.lsp.Reconcile(c)
	time.Sleep(50 * time.Millisecond)
	if got := c.lsp.Diagnostics(volunteered); len(got) != 1 {
		t.Fatalf("closing an unrelated buffer dropped a volunteered file: %#v", got)
	}
}

// The panel reflows every row it holds whenever the revision moves, and a project report
// covers dozens of files at once. Only the file in the editor gets to wake it directly.
func TestProjectDiagnosticsAreCoalescedButOpenFilesAreNot(t *testing.T) {
	root := pythonProject(t, map[string]string{"main.py": "print('main')\n"})
	server := newRecordingLSPServer()
	c := newPullCtx(t, server, root)
	main := filepath.Join(root, "main.py")
	waitLSPCallFor(t, server.calls, "open", main)
	drainLSPEvents(c.lsp, 400*time.Millisecond)

	other := filepath.Join(root, "other.py")
	if err := server.publish(other, "project file", 1); err != nil {
		t.Fatal(err)
	}
	if waitLSPEvent(c.lsp, lspDiagnosticsSettle/2) {
		t.Fatal("a project file woke the panel immediately instead of settling")
	}
	if !waitLSPEvent(c.lsp, 2*time.Second) {
		t.Fatal("the settled project diagnostics never reached the panel")
	}
	if got := c.lsp.Diagnostics(other); len(got) != 1 {
		t.Fatalf("coalescing lost the diagnostics themselves: %#v", got)
	}

	drainLSPEvents(c.lsp, 400*time.Millisecond)
	if err := server.publish(main, "open file", 1); err != nil {
		t.Fatal(err)
	}
	if !waitLSPEvent(c.lsp, lspDiagnosticsSettle/2) {
		t.Fatal("the file in the editor was made to wait for the settle timer")
	}
}

func TestWorkspaceDiagnosticOptionsReadsBothShapes(t *testing.T) {
	identifier := "x"
	registration := &protocol.DiagnosticRegistrationOptions{}
	registration.WorkspaceDiagnostics = true
	registration.Identifier = &identifier
	cases := []struct {
		name     string
		provider protocol.DiagnosticProvider
		want     bool
	}{
		{"absent", nil, false},
		{"plain off", &protocol.DiagnosticOptions{}, false},
		{"plain on", &protocol.DiagnosticOptions{WorkspaceDiagnostics: true}, true},
		{"registration on", registration, true},
	}
	for _, tc := range cases {
		if got, _ := workspaceDiagnosticOptions(tc.provider); got != tc.want {
			t.Fatalf("%s: workspaceDiagnostics = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// Everything that stores a diagnostic learns the path from a file URI; everything that
// reads one back — the gutter, the panel, these tests — spells it the way the OS does.
// On Windows those two spellings differ, because a file URI lowercases the drive letter
// and a UNC authority, and keying the map on one while looking it up with the other lost
// every diagnostic for a file gote had not opened. This runs everywhere and only has
// something to catch on Windows, which is precisely where it was not caught.
func TestDiagnosticsKeySurvivesTheURIRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "main.py")
	roundTripped := uriPath(uri.File(path))
	if !sameFilePath(roundTripped, path) {
		t.Fatalf("%q round-tripped through a file URI as %q", path, roundTripped)
	}
	if got, want := diagnosticsKey(roundTripped), diagnosticsKey(path); got != want {
		t.Fatalf("diagnostics key for %q = %q via its URI, %q from the OS", path, got, want)
	}
}
