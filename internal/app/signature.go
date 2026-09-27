package app

import (
	"strings"

	"github.com/brohd11/bubblestack/components"
	"github.com/brohd11/bubblestack/components/editor"
	"github.com/brohd11/bubblestack/core"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// Signature help: the parameter hint shown inside an argument list, triggered by the
// server's trigger characters ("(" and "," in practice) through the completion lane. It
// draws above the caret and the completion list below, so both can show.

// signatureUI is the hint's state: a passive FloatingPopup (nil Handle — it claims no
// input) plus the buffer generation it was computed for.
type signatureUI struct {
	popup *components.FloatingPopup
	body  string
	width int
	path  string
	// anchor is the '(' that opened the call this hint describes. It is what makes
	// dismissal a question about the caret rather than about the last keystroke.
	anchor editor.Position
}

func (s *homeScreen) closeSignature() { s.signature = signatureUI{} }

// dismissSignatureIfLeft closes the hint once the caret leaves its call. It reads only the
// caret, so it sits on the update's common exit and covers every way out (consumed
// messages, clicks, jumps, undo).
func (s *homeScreen) dismissSignatureIfLeft() {
	if s.signature.popup == nil {
		return
	}
	if s.signature.path != s.currentPath {
		s.closeSignature()
		return
	}
	start, ok := signatureCallStart(s.editor, s.editor.CursorPosition())
	if !ok || start != s.signature.anchor {
		s.closeSignature()
	}
}

// signatureScanLines bounds the backward walk so a keystroke never scans the whole
// buffer.
const signatureScanLines = 50

// signatureCallStart finds the unmatched '(' around the caret, walking back over balanced
// brackets (lexically; strings and comments count). An unmatched '[' or '{' means not in
// an argument list.
func signatureCallStart(ed *editor.Screen, position editor.Position) (
	editor.Position, bool,
) {
	depth := 0
	for line := position.Line; line >= 0 && position.Line-line < signatureScanLines; line-- {
		text, ok := ed.LineText(line)
		if !ok {
			return editor.Position{}, false
		}
		runes := []rune(text)
		column := len(runes)
		if line == position.Line {
			column = min(max(position.Column, 0), len(runes))
		}
		for column > 0 {
			column--
			switch runes[column] {
			case ')', ']', '}':
				depth++
			case '(':
				if depth == 0 {
					return editor.Position{Line: line, Column: column}, true
				}
				depth--
			case '[', '{':
				if depth == 0 {
					return editor.Position{}, false
				}
				depth--
			}
		}
	}
	return editor.Position{}, false
}

func (s *homeScreen) applySignature(result *lspRequestResult) core.Action {
	if result.signature == nil || strings.TrimSpace(result.signature.Label) == "" {
		s.closeSignature()
		return core.Action{}
	}
	// The answer arrived asynchronously, so where the caret is NOW decides whether there
	// is still a call to describe.
	anchor, ok := signatureCallStart(s.editor, s.editor.CursorPosition())
	if !ok {
		s.closeSignature()
		return core.Action{}
	}
	width := s.panelWidth(hoverMaxWidth)
	body := renderSignature(*result.signature, width)
	s.signature = signatureUI{
		popup: &components.FloatingPopup{
			Content: func() string { return components.PopupPanel(s.signature.body, s.signature.width) },
		},
		body:   body,
		width:  panelFit(body, width),
		path:   result.path,
		anchor: anchor,
	}
	return core.Action{}
}

// renderSignature emphasizes the active parameter using the server's label offsets, so
// repeated type names are handled.
func renderSignature(signature lspSignature, width int) string {
	label := []rune(signature.Label)
	body := signature.Label
	if signature.Active >= 0 && signature.Active < len(signature.Parameters) {
		active := signature.Parameters[signature.Active]
		if active.Start >= 0 && active.End <= len(label) && active.Start < active.End {
			bold := lipgloss.NewStyle().Bold(true).Underline(true)
			body = string(label[:active.Start]) +
				bold.Render(string(label[active.Start:active.End])) +
				string(label[active.End:])
		}
	}
	lines := []string{ansi.Wrap(body, width, "")}
	if doc := firstLine(signature.Doc); doc != "" {
		lines = append(lines, core.MutedStyle().Render(ansi.Truncate(doc, width, "…")))
	}
	return strings.Join(lines, "\n")
}

func firstLine(text string) string {
	return strings.TrimSpace(strings.SplitN(strings.TrimSpace(text), "\n", 2)[0])
}

// updateSignatureAfterParent opens the hint on a trigger character and closes it on esc or
// a buffer change; leaving the call is dismissSignatureIfLeft's job.
func (s *homeScreen) updateSignatureAfterParent(sh *core.Shared, msg tea.Msg, before completionBefore) tea.Cmd {
	if before.editor != s.editor || before.path != s.currentPath || !s.lspFeatureReady(sh) {
		s.closeSignature()
		return nil
	}
	key, isKey := msg.(tea.KeyPressMsg)
	if !isKey {
		return nil
	}
	if s.signature.popup != nil && key.String() == "esc" {
		s.closeSignature()
		return nil
	}
	if key.Text == "" {
		return nil
	}
	if Of(sh).lsp.SignatureTrigger(s.currentPath, key.Text) {
		s.requestAt(sh, lspReqSignature)
		return nil
	}
	// Leaving the call is handled by dismissSignatureIfLeft, based on the caret.
	return nil
}

// viewSignature places the hint ABOVE the caret, dropping below it at the top edge — the
// mirror of the completion list's placement, which is what lets both be up at once.
func (s *homeScreen) viewSignature(sh *core.Shared, body string) string {
	if s.signature.popup == nil || s.signature.path != s.currentPath {
		return body
	}
	x, absoluteY, visible := s.editor.CursorAnchor()
	if !visible {
		return body
	}
	y := absoluteY - sh.BodyY()
	s.signature.popup.Placement = s.caretPopup(x, y, true)
	return s.signature.popup.ViewOver(body, s.w, s.h)
}
