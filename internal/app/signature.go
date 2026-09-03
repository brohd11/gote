package app

import (
	"strings"

	"github.com/brohd11/bubblestack/components"
	"github.com/brohd11/bubblestack/core"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// Signature help: the parameter hint that appears when you open an argument list. It is
// the one on-demand feature that is not key-triggered — a hint you have to ask for is a
// hint you will not use — so it rides the completion lane's trigger machinery, firing on
// the characters the server advertised (in practice "(" and ",").
//
// It renders ABOVE the caret while the completion list renders below it, so the two can
// legitimately be up at once without fighting for the same rows.

// signatureUI is the hint's state: a passive FloatingPopup (nil Handle — it claims no
// input) plus the buffer generation it was computed for.
type signatureUI struct {
	popup *components.FloatingPopup
	body  string
	width int
	path  string
	// anchor is the '(' that opened the call this hint describes. It is what makes
	// dismissal a question about the caret rather than about the last keystroke.
	anchor components.EditorPosition
}

func (s *homeScreen) closeSignature() { s.signature = signatureUI{} }

// dismissSignatureIfLeft retires the hint once the caret is no longer inside the call it
// was opened for. It reads only the caret, never the message, which is what lets it sit on
// the update's common exit and cover the ways out that the typing hook never sees: a
// message the completion popup consumed, an action carrying a control message, a click,
// a jump, an undo. Leaving by any of them is still leaving.
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

// signatureScanLines bounds the walk back so one keystroke can never turn into a
// whole-buffer scan. An argument list longer than this is past the point where a
// parameter hint is what the reader needs.
const signatureScanLines = 50

// signatureCallStart finds the unmatched '(' the caret sits inside, walking backwards over
// balanced brackets. It is lexical, like completionIdentifierStart and for the same
// reason: a bracket inside a string or a comment counts as a bracket. Getting that wrong
// shows or hides a hint, which is the cheapest thing in the editor to be wrong about.
//
// An unmatched '[' or '{' answers no rather than continuing past it — the caret is inside
// an index or a composite literal, not an argument list, and whatever call encloses THAT
// is not the one being typed into.
func signatureCallStart(editor *components.EditorScreen, position components.EditorPosition) (
	components.EditorPosition, bool,
) {
	depth := 0
	for line := position.Line; line >= 0 && position.Line-line < signatureScanLines; line-- {
		text, ok := editor.LineText(line)
		if !ok {
			return components.EditorPosition{}, false
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
					return components.EditorPosition{Line: line, Column: column}, true
				}
				depth--
			case '[', '{':
				if depth == 0 {
					return components.EditorPosition{}, false
				}
				depth--
			}
		}
	}
	return components.EditorPosition{}, false
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

// renderSignature draws the label with the active parameter emphasized. The parameter's
// offsets came from the server (labelOffsetSupport), so the emphasis lands on the right
// run of characters even when the same type name appears twice in the signature.
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
		lines = append(lines, lipgloss.NewStyle().Foreground(core.MutedColor).Render(ansi.Truncate(doc, width, "…")))
	}
	return strings.Join(lines, "\n")
}

func firstLine(text string) string {
	return strings.TrimSpace(strings.SplitN(strings.TrimSpace(text), "\n", 2)[0])
}

// updateSignatureAfterParent is the typing hook, updateCompletionAfterParent's sibling:
// it observes what a message actually did to the editor rather than predicting it. It owns
// only the ways the hint OPENS — a trigger character — plus the two dismissals a caret
// cannot express: esc, and the buffer moving out from under it. Leaving the call itself
// belongs to dismissSignatureIfLeft.
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
	// Nothing else here closes the hint: dismissSignatureIfLeft owns that, on the caret
	// rather than on the key, so typing the closing bracket and arrowing past one are the
	// same event to it.
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
	s.signature.popup.Placement = caretPanel(x, y, s.editorLeft(), true)
	return s.signature.popup.ViewOver(body, s.w, s.h)
}
