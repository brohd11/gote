package app

import (
	"image/color"

	"github.com/brohd11/bubblestack/components/editor"
	"github.com/brohd11/bubblestack/core"

	"charm.land/lipgloss/v2"
	"go.lsp.dev/protocol"
)

const (
	gitSignColumn        = "git"
	diagnosticSignColumn = "diagnostics"
)

func (s *homeScreen) configureSignColumns() {
	if s.editor == nil {
		return
	}
	s.editor.SetSignColumnOrder(gitSignColumn, diagnosticSignColumn)
	s.editor.ShowSignColumn(gitSignColumn, s.gitGutter)
	s.editor.ShowSignColumn(diagnosticSignColumn, s.diagnosticsGutter)
	s.refreshDiagnosticSigns()
}

func (s *homeScreen) setDiagnosticsGutter(on bool) {
	s.diagnosticsGutter = on
	if s.editor != nil {
		s.editor.ShowSignColumn(diagnosticSignColumn, on)
	}
}

func (s *homeScreen) refreshDiagnosticSigns() {
	if s.editor == nil || s.sh == nil || s.currentPath == "" {
		if s.editor != nil {
			s.editor.SetSignColumn(diagnosticSignColumn, nil)
		}
		return
	}
	m := Of(s.sh).lsp
	if m == nil {
		s.editor.SetSignColumn(diagnosticSignColumn, nil)
		return
	}
	s.editor.SetSignColumn(diagnosticSignColumn, diagnosticSigns(m.Diagnostics(s.currentPath)))
}

func diagnosticSigns(diagnostics []lspDiagnostic) map[int]editor.Sign {
	best := make(map[int]protocol.DiagnosticSeverity)
	for _, diagnostic := range diagnostics {
		severity := normalizedSeverity(diagnostic.Severity)
		line := int(diagnostic.Line)
		if prior, ok := best[line]; !ok || severity < prior {
			best[line] = severity
		}
	}
	if len(best) == 0 {
		return nil
	}
	signs := make(map[int]editor.Sign, len(best))
	for line, severity := range best {
		text, color := diagnosticMark(severity)
		signs[line] = editor.Sign{Text: text, Style: lipgloss.NewStyle().Foreground(color)}
	}
	return signs
}

func normalizedSeverity(severity protocol.DiagnosticSeverity) protocol.DiagnosticSeverity {
	if severity < protocol.DiagnosticSeverityError || severity > protocol.DiagnosticSeverityHint {
		return protocol.DiagnosticSeverityInformation
	}
	return severity
}

func diagnosticMark(severity protocol.DiagnosticSeverity) (string, color.Color) {
	switch normalizedSeverity(severity) {
	case protocol.DiagnosticSeverityError:
		return "E", lipgloss.Color("1")
	case protocol.DiagnosticSeverityWarning:
		return "W", lipgloss.Color("3")
	case protocol.DiagnosticSeverityHint:
		return "H", core.MutedColor
	default:
		return "I", lipgloss.Color("4")
	}
}

func severityName(severity protocol.DiagnosticSeverity) string {
	switch normalizedSeverity(severity) {
	case protocol.DiagnosticSeverityError:
		return "error"
	case protocol.DiagnosticSeverityWarning:
		return "warning"
	case protocol.DiagnosticSeverityHint:
		return "hint"
	default:
		return "info"
	}
}

func (s *homeScreen) restartLanguageServers(sh *core.Shared) core.Action {
	m := Of(sh).lsp
	if m == nil {
		return core.SetStatus("LSP is disabled by auto-lsp: false")
	}
	m.Restart()
	m.Reconcile(Of(sh))
	return core.SetStatus("restarting language servers")
}
