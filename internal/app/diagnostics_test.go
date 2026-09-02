package app

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/brohd11/bubblestack/components"

	"go.lsp.dev/protocol"
)

func TestDiagnosticSignsChooseHighestSeverity(t *testing.T) {
	signs := diagnosticSigns([]lspDiagnostic{
		{Line: 2, Severity: protocol.DiagnosticSeverityHint},
		{Line: 2, Severity: protocol.DiagnosticSeverityError},
		{Line: 3, Severity: protocol.DiagnosticSeverityWarning},
		{Line: 4}, // missing severity is presented as information
	})
	if signs[2].Text != "E" || signs[3].Text != "W" || signs[4].Text != "I" {
		t.Fatalf("diagnostic signs = %#v", signs)
	}
}

func TestRenderDiagnosticsCurrentFirst(t *testing.T) {
	root := t.TempDir()
	c := New("test", DefaultConfig(), Options{})
	defer c.close()
	paths := []string{
		filepath.Join(root, "a.py"),
		filepath.Join(root, "b.py"),
		filepath.Join(root, "c.py"),
	}
	for _, path := range paths {
		c.OpenDoc(path, components.EditorOpts{})
		c.lsp.diagnostics[path] = []lspDiagnostic{{
			Line: 1, Character: 2, Severity: protocol.DiagnosticSeverityWarning,
			Message: "a useful warning", Source: "fake", Code: "W1",
		}}
	}

	body := renderDiagnostics(c, paths[1], 60)
	positions := []int{
		strings.Index(body, paths[1]),
		strings.Index(body, paths[0]),
		strings.Index(body, paths[2]),
	}
	if positions[0] < 0 || !(positions[0] < positions[1] && positions[1] < positions[2]) {
		t.Fatalf("current file should be first, then path order: %v\n%s", positions, body)
	}
	for _, want := range []string{"2:3", "warning · fake W1", "a useful warning"} {
		if !strings.Contains(body, want) {
			t.Fatalf("diagnostics page missing %q:\n%s", want, body)
		}
	}
}

func TestDiagnosticsGutterIndependentFromGit(t *testing.T) {
	s, sh := newHome(t)
	s.currentPath = filepath.Join(t.TempDir(), "main.py")
	s.editor.SetText("one\ntwo")
	Of(sh).lsp.diagnostics[s.currentPath] = []lspDiagnostic{{Line: 1, Severity: protocol.DiagnosticSeverityError}}
	s.configureSignColumns()

	if !s.editor.SignColumnMode(gitSignColumn) || !s.editor.SignColumnMode(diagnosticSignColumn) {
		t.Fatal("both columns should start independently visible")
	}
	if got := s.editor.SignsForColumn(diagnosticSignColumn)[1].Text; got != "E" {
		t.Fatalf("diagnostic marker = %q", got)
	}
	s.setDiagnosticsGutter(false)
	if s.editor.SignColumnMode(diagnosticSignColumn) || !s.editor.SignColumnMode(gitSignColumn) {
		t.Fatal("hiding diagnostics must leave the git column alone")
	}
	s.setGitGutter(false)
	if s.editor.SignColumnMode(gitSignColumn) {
		t.Fatal("git should toggle independently after diagnostics")
	}
}
