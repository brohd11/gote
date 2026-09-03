package app

import (
	"fmt"
	"image/color"
	"sort"
	"strings"

	"github.com/brohd11/bubblestack/components"
	"github.com/brohd11/bubblestack/core"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
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

func diagnosticSigns(diagnostics []lspDiagnostic) map[int]components.Sign {
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
	signs := make(map[int]components.Sign, len(best))
	for line, severity := range best {
		text, color := diagnosticMark(severity)
		signs[line] = components.Sign{Text: text, Style: lipgloss.NewStyle().Foreground(color)}
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

func (s *homeScreen) diagnosticsScreen(sh *core.Shared) *components.DocScreen {
	c := Of(sh)
	current := s.currentPath
	return components.NewDocScreen(components.DocOpts{
		Title:  "Diagnostics",
		Crumb:  "diagnostics",
		Render: func(width int) string { return renderDiagnostics(c, current, width) },
	})
}

func renderDiagnostics(c *Ctx, current string, width int) string {
	if c == nil || c.lsp == nil {
		return "Language-server support is disabled (auto-lsp: false)."
	}
	paths := make([]string, 0, len(c.OpenDocs()))
	for _, doc := range c.OpenDocs() {
		if doc.Path != "" && doc.Path != current {
			paths = append(paths, doc.Path)
		}
	}
	sort.Strings(paths)
	if current != "" {
		if _, ok := c.Doc(current); ok {
			paths = append([]string{current}, paths...)
		}
	}

	var body strings.Builder
	groups := 0
	for _, path := range paths {
		diagnostics := c.lsp.Diagnostics(path)
		if len(diagnostics) == 0 {
			continue
		}
		sort.SliceStable(diagnostics, func(i, j int) bool {
			if diagnostics[i].Line != diagnostics[j].Line {
				return diagnostics[i].Line < diagnostics[j].Line
			}
			if diagnostics[i].Character != diagnostics[j].Character {
				return diagnostics[i].Character < diagnostics[j].Character
			}
			return normalizedSeverity(diagnostics[i].Severity) < normalizedSeverity(diagnostics[j].Severity)
		})
		if groups > 0 {
			body.WriteString("\n")
		}
		groups++
		fmt.Fprintf(&body, "%s  (%d)\n\n", path, len(diagnostics))
		for _, diagnostic := range diagnostics {
			meta := severityName(diagnostic.Severity)
			if diagnostic.Source != "" {
				meta += " · " + diagnostic.Source
			}
			if diagnostic.Code != "" {
				meta += " " + diagnostic.Code
			}
			prefix := fmt.Sprintf("  %d:%d  %-7s ", diagnostic.Line+1, diagnostic.Character+1, meta)
			messageWidth := max(width-ansi.StringWidth(prefix), 20)
			message := ansi.Wrap(strings.TrimSpace(diagnostic.Message), messageWidth, "")
			lines := strings.Split(message, "\n")
			body.WriteString(prefix)
			body.WriteString(lines[0])
			body.WriteByte('\n')
			indent := strings.Repeat(" ", ansi.StringWidth(prefix))
			for _, line := range lines[1:] {
				body.WriteString(indent + line + "\n")
			}
		}
	}
	if groups == 0 {
		return "No diagnostics for open files."
	}
	return strings.TrimRight(body.String(), "\n")
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
