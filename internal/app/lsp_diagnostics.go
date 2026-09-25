package app

import (
	"fmt"

	"go.lsp.dev/protocol"
)

// lspDiagnostic is a UI-sized projection of protocol.Diagnostic with plain strings, so
// snapshots are cheap and the renderer sees no protocol unions.
type lspDiagnostic struct {
	Line, Character       uint32
	EndLine, EndCharacter uint32
	Severity              protocol.DiagnosticSeverity
	Message, Source, Code string
}

// wakeDiagnostics nudges the actor to start (or keep) the settle timer. It coalesces:
// a full channel already means the actor has been told.
func (m *lspManager) wakeDiagnostics() {
	m.start()
	select {
	case m.diagWake <- struct{}{}:
	default:
	}
}

func projectDiagnostic(item protocol.Diagnostic) lspDiagnostic {
	diagnostic := lspDiagnostic{
		Line: item.Range.Start.Line, Character: item.Range.Start.Character,
		EndLine: item.Range.End.Line, EndCharacter: item.Range.End.Character,
		Severity: item.Severity,
	}
	switch message := item.Message.(type) {
	case protocol.String:
		diagnostic.Message = string(message)
	case *protocol.MarkupContent:
		diagnostic.Message = message.Value
	}
	if source, ok := item.Source.Get(); ok {
		diagnostic.Source = source
	}
	switch code := item.Code.(type) {
	case protocol.String:
		diagnostic.Code = string(code)
	case protocol.Integer:
		diagnostic.Code = fmt.Sprint(int32(code))
	}
	return diagnostic
}

func (m *lspManager) Diagnostics(path string) []lspDiagnostic {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]lspDiagnostic(nil), m.diagnostics[diagnosticsKey(path)]...)
}

// AllDiagnostics is every file the servers have reported on, for the panel that lists a
// whole project. Diagnostics(path) answers one file and is what the gutter asks.
func (m *lspManager) AllDiagnostics() map[string][]lspDiagnostic {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	all := make(map[string][]lspDiagnostic, len(m.diagnostics))
	for path, diagnostics := range m.diagnostics {
		if len(diagnostics) == 0 {
			continue
		}
		all[path] = append([]lspDiagnostic(nil), diagnostics...)
	}
	return all
}

// DiagnosticsRevision changes only when the stored diagnostic set changes.
func (m *lspManager) DiagnosticsRevision() uint64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.diagnosticsRevision
}
