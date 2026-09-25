package app

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/brohd11/bubblestack/components"
	"github.com/brohd11/bubblestack/components/editor"
	"github.com/brohd11/bubblestack/core"
	"github.com/brohd11/goutil/strutil"

	"charm.land/bubbles/v2/list"
	"go.lsp.dev/protocol"
)

// Caret-driven language-server features and the jump stack: ask the manager about the
// caret, then act on the result that comes back through homeScreen.Receive.

// jumpLimit caps the back-stack. Deep enough that a chain of definitions stays
// retraceable, shallow enough that it never becomes a second document history.
const jumpLimit = 32

// jumpSite is a document and caret worth returning to: gote's only location history.
type jumpSite struct {
	path string
	pos  editor.Position
}

// lspFeatureReady reports whether a caret request can be made now: a manager, a document
// in the editor pane, and the pane focused. Capabilities are the manager's to check.
func (s *homeScreen) lspFeatureReady(sh *core.Shared) bool {
	return sh != nil && Of(sh).lsp != nil && s.currentPath != "" &&
		s.fullPreview == nil && s.editorPanel.Focused()
}

// lspEnabled reports whether this launch has a language-server manager at all, which is
// what menu rows depend on.
func lspEnabled(sh *core.Shared) bool {
	return sh != nil && Of(sh).lsp != nil
}

// lspDisabledReason names the config key that left this launch without a manager (three
// can), so messages point at the right one. Only for launches with no manager.
func lspDisabledReason(c *Ctx) string {
	switch {
	case c == nil:
		return "auto-lsp: false"
	case !c.Config.AutoLSP:
		return "auto-lsp: false"
	case c.Mode == ModeFile:
		return "single_file_mode.default_allow_lsp: false"
	default:
		return "project_mode.default_allow_lsp: false"
	}
}

// requestAt issues an on-demand request at the caret, reporting a refusal (no server yet,
// or unsupported) rather than doing nothing.
func (s *homeScreen) requestAt(sh *core.Shared, kind lspRequestKind) core.Action {
	if !s.lspFeatureReady(sh) {
		return core.Action{}
	}
	c := Of(sh)
	c.lsp.Reconcile(c)
	position, ok := editorPositionToLSP(s.editor, s.editor.CursorPosition())
	if !ok {
		return core.Action{}
	}
	id := c.lsp.Request(kind, s.currentPath, s.editor.EditSeq(), position)
	if id == 0 {
		return core.SetStatus("no language server for " + kind.String() + " here")
	}
	s.closeHover()
	s.lspRequestID = id
	return core.Action{}
}

// applyRequestResult routes an on-demand answer, dropping stale ones as
// applyCompletionResult does, except jumps, which are meant to leave the document.
func (s *homeScreen) applyRequestResult(sh *core.Shared, result *lspRequestResult) core.Action {
	if result == nil || result.id == 0 || result.id != s.lspRequestID {
		return core.Action{}
	}
	s.lspRequestID = 0
	if result.err != nil {
		return core.SetStatus(result.kind.String() + ": " + trimLSPError(result.err.Error()))
	}
	if result.path != s.currentPath {
		return core.Action{}
	}
	switch result.kind {
	case lspReqDefinition:
		return s.applyDefinition(sh, result)
	case lspReqReferences:
		return s.applyReferences(sh, result)
	case lspReqHover:
		return s.applyHover(result)
	case lspReqFormat:
		return s.applyFormat(result)
	case lspReqSignature:
		return s.applySignature(result)
	}
	return core.Action{}
}

// trimLSPError keeps a server's message on one status row. Servers answer failures with
// anything from a word to a stack trace, and the status line has one line.
func trimLSPError(message string) string {
	message = strings.TrimSpace(strings.SplitN(message, "\n", 2)[0])
	if len(message) > 90 {
		return message[:87] + "…"
	}
	return message
}

func (s *homeScreen) applyDefinition(sh *core.Shared, result *lspRequestResult) core.Action {
	switch len(result.locations) {
	case 0:
		return core.SetStatus("no definition found")
	case 1:
		return s.jumpToLocation(sh, result.locations[0])
	}
	return core.Push(s.locationPicker(sh, "Definitions", "definitions", result.locations))
}

// jumpToLocation is every jump's single path (definition, reference, outline), recording
// the prior caret so ctrl+o can return.
func (s *homeScreen) jumpToLocation(sh *core.Shared, target lspLocation) core.Action {
	if target.Path == "" {
		return core.SetStatus("that definition is not in a file")
	}
	s.pushJump()
	return s.travelTo(sh, target.Path, target.Range)
}

// pushJump records the current caret as somewhere to come back to.
func (s *homeScreen) pushJump() {
	if s.currentPath == "" || s.editor == nil {
		return
	}
	s.jumps = append(s.jumps, jumpSite{path: s.currentPath, pos: s.editor.CursorPosition()})
	if len(s.jumps) > jumpLimit {
		s.jumps = s.jumps[len(s.jumps)-jumpLimit:]
	}
}

// jumpBack (ctrl+o) returns to the most recent recorded site. It records nothing itself:
// the stack is a trail out and back, not a ring the user can loop around in.
func (s *homeScreen) jumpBack(sh *core.Shared) core.Action {
	if len(s.jumps) == 0 {
		return core.SetStatus("no jump to go back to")
	}
	site := s.jumps[len(s.jumps)-1]
	s.jumps = s.jumps[:len(s.jumps)-1]
	if site.path == s.currentPath {
		s.editor.Reveal(site.pos)
		return core.Action{}
	}
	return s.travel(sh, site.path, site.pos, nil)
}

// travelTo moves to an LSP range, keeping it in UTF-16 until a buffer with the text is at
// hand (reveal converts it for other files once read).
func (s *homeScreen) travelTo(sh *core.Shared, path string, target protocol.Range) core.Action {
	pos := editor.Position{Line: int(target.Start.Line), Column: int(target.Start.Character)}
	if path == s.currentPath {
		pos = lspPositionToEditorClamped(s.editor, target.Start)
	}
	return s.travel(sh, path, pos, &target)
}

// travel shows path in the editor pane with the caret at pos. An unopened file loads
// asynchronously, so the target waits in pendingJump and is retried from finishHomeUpdate
// until the line exists.
func (s *homeScreen) travel(sh *core.Shared, path string, pos editor.Position,
	target *protocol.Range) core.Action {
	if path == s.currentPath {
		s.reveal(pos, target)
		return core.Action{}
	}
	c := Of(sh)
	_, wasOpen := c.Doc(path)
	act := s.openDoc(sh, path)
	s.pendingJump = &jumpSite{path: path, pos: pos}
	s.pendingRange = target
	s.applyPendingJump()
	if !wasOpen && !insideRoot(c, path) {
		// A definition in a dependency starts a second server session rooted there; say so.
		return core.Seq(act, core.SetStatus("opened "+shortPath(c, path)+" (outside this root)"))
	}
	return act
}

// reveal moves the caret and highlights the server's range, showing what the jump landed
// on.
func (s *homeScreen) reveal(pos editor.Position, target *protocol.Range) bool {
	if target != nil {
		if r, ok := lspRangeToEditor(s.editor, *target); ok && s.editor.SelectRange(r) {
			return true
		}
	}
	return s.editor.Reveal(pos)
}

// applyPendingJump retries a jump into a loading buffer after each message, giving up once
// the buffer has content but not the line.
func (s *homeScreen) applyPendingJump() {
	if s.pendingJump == nil {
		return
	}
	if s.pendingJump.path != s.currentPath || s.editor == nil {
		s.pendingJump, s.pendingRange = nil, nil
		return
	}
	if s.reveal(s.pendingJump.pos, s.pendingRange) || s.editor.Text() != "" {
		s.pendingJump, s.pendingRange = nil, nil
	}
}

// insideRoot reports whether path belongs to the document root gote is showing, which
// is what separates "jumped within the project" from "jumped into a dependency".
func insideRoot(c *Ctx, path string) bool {
	root := docsRoot(c)
	if root == "" {
		return true
	}
	_, ok := strutil.RelUnder(root, path)
	return ok
}

// shortPath names a file relative to the document root, or with ~ for home.
func shortPath(c *Ctx, path string) string {
	if root := docsRoot(c); root != "" {
		if rel, ok := strutil.RelUnder(root, path); ok {
			return rel
		}
	}
	home, _ := os.UserHomeDir()
	return strutil.ContractHome(path, home)
}

// fileLine reads one line from an unopened file, with a capped scanner so a huge file is
// never loaded whole.
func fileLine(path string, line int) string {
	file, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 4096), 64*1024)
	for i := 0; scanner.Scan(); i++ {
		if i == line {
			return scanner.Text()
		}
	}
	return ""
}

// locationPicker offers several jump targets as a pushed picker (lists can be long and
// want filtering).
func (s *homeScreen) locationPicker(sh *core.Shared, title, crumb string, locations []lspLocation) *components.PickerScreen {
	items := make([]list.Item, 0, len(locations))
	for _, location := range locations {
		target := location
		items = append(items, components.Item{
			Name:   fmt.Sprintf("%s:%d", shortPath(Of(sh), target.Path), target.Range.Start.Line+1),
			Desc:   locationPreview(Of(sh), target),
			Filter: target.Path,
			Pick: func(sh *core.Shared) core.Action {
				return core.Seq(core.Pop(), s.jumpToLocation(sh, target))
			},
		})
	}
	return components.NewPicker(items, components.PickerOpts{Title: title, Crumb: crumb})
}

// locationPreview is a location row's source line: from the open buffer (with unsaved
// edits) or read from disk once.
func locationPreview(c *Ctx, target lspLocation) string {
	line := int(target.Range.Start.Line)
	if ed, ok := c.Doc(target.Path); ok && ed != nil {
		if text, ok := ed.LineText(line); ok {
			return strings.TrimSpace(text)
		}
	}
	return strings.TrimSpace(fileLine(target.Path, line))
}
