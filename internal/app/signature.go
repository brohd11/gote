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
	path  string
}

func (s *homeScreen) closeSignature() { s.signature = signatureUI{} }

func (s *homeScreen) applySignature(result *lspRequestResult) core.Action {
	if result.signature == nil || strings.TrimSpace(result.signature.Label) == "" {
		s.closeSignature()
		return core.Action{}
	}
	s.signature = signatureUI{
		popup: &components.FloatingPopup{Content: func() string { return s.signature.body }},
		body:  renderSignature(*result.signature, min(max(s.w-6, 20), hoverMaxWidth)),
		path:  result.path,
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
// it observes what a message actually did to the editor rather than predicting it. A
// trigger character opens or refreshes the hint; leaving the buffer, moving to another
// document, or pressing esc closes it.
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
	// A closing bracket ends the call the hint belongs to. Servers do not advertise it
	// as a trigger (there is nothing to show), so the dismissal is ours to make.
	if strings.ContainsAny(key.Text, ")]}") {
		s.closeSignature()
	}
	return nil
}

// viewSignature places the hint one row ABOVE the caret, flipping below it at the top
// edge — the mirror of the completion list's placement, which is what lets both be up.
func (s *homeScreen) viewSignature(sh *core.Shared, body string) string {
	if s.signature.popup == nil || s.signature.path != s.currentPath {
		return body
	}
	x, absoluteY, visible := s.editor.CursorAnchor()
	if !visible {
		return body
	}
	y := absoluteY - sh.BodyY()
	s.signature.popup.Placement = components.PlacePopupAt(components.PopupAnchor{
		X: x, Y: y, FlipX: x + 1, FlipY: y + 1,
	})
	return s.signature.popup.ViewOver(body, s.w, s.h)
}
