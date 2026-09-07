package app

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/brohd11/bubblestack/components"
	"github.com/brohd11/bubblestack/components/editor"
	"github.com/brohd11/bubblestack/core"

	"charm.land/bubbles/v2/list"
	"go.lsp.dev/protocol"
)

// Position-driven language-server features and the jump stack they navigate with.
// Everything here shares one shape: ask the manager for something at the caret, and act
// on the single result that comes back through homeScreen.Receive.

// jumpLimit caps the back-stack. Deep enough that a chain of definitions stays
// retraceable, shallow enough that it never becomes a second document history.
const jumpLimit = 32

// jumpSite is a place worth coming back to: a document and a caret in it. Nothing else
// in gote records one — the router's stack tracks screens, and the open set tracks
// buffers without their cursors — so this is the whole of gote's location history.
type jumpSite struct {
	path string
	pos  editor.Position
}

// lspFeatureReady reports whether a caret-driven request can be made at all: a manager
// exists, a document is open in the editor pane, and the pane has the keys. It is
// completionAvailable's counterpart, and deliberately does not consult the server's
// capabilities — that check belongs to the manager, which knows what was advertised.
func (s *homeScreen) lspFeatureReady(sh *core.Shared) bool {
	return sh != nil && Of(sh).lsp != nil && s.currentPath != "" &&
		s.fullPreview == nil && s.editorPanel.Focused()
}

// requestAt issues one on-demand request for the caret's position. A refusal is
// reported as a status rather than silently: pressing a key and getting nothing back is
// indistinguishable from a hang, and the two reasons a request is refused — no server
// yet, or this server does not do that — are both worth saying out loud.
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

// applyRequestResult routes one on-demand answer. Results are dropped when a newer
// request has replaced this one or the buffer moved on beneath it — the same three
// guards applyCompletionResult uses — except for a jump, whose whole point is to leave
// the document the request was made in.
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

// jumpToLocation is the single door every jump goes through — definition, a picked
// reference, an outline row. It records where the caret was before moving it, so
// ctrl+o always has somewhere to go back to.
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

// travelTo moves to an LSP range. The UTF-16 conversion needs the destination's text,
// which is why the target keeps its protocol range this far down rather than being
// converted at the projection: only the same-file case has a buffer in hand right now,
// and the cross-file case converts later, in reveal, once the file has been read.
func (s *homeScreen) travelTo(sh *core.Shared, path string, target protocol.Range) core.Action {
	pos := editor.Position{Line: int(target.Start.Line), Column: int(target.Start.Character)}
	if path == s.currentPath {
		pos = lspPositionToEditorClamped(s.editor, target.Start)
	}
	return s.travel(sh, path, pos, &target)
}

// travel puts the editor pane on path and the caret at pos. A document already open is
// revealed immediately; a new one has to be read off disk first, and that read is
// asynchronous — so the destination is parked in pendingJump and retried from
// finishHomeUpdate until the buffer has the line. This is the same "observe what the
// update actually did" discipline updateCompletionAfterParent follows, and it is why a
// jump into an unopened file lands on the right line rather than on line one.
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
		// A definition in a dependency is a file from another workspace: reconciling it
		// starts a second server session rooted at ITS module. That is correct, but it
		// is also a background process the user did not ask for by name.
		return core.Seq(act, core.SetStatus("opened "+shortPath(c, path)+" (outside this root)"))
	}
	return act
}

// reveal moves the caret and, when the server gave a range worth showing, highlights it.
// The highlight is how a jump says what it landed on: the caret alone leaves the user
// hunting for which identifier on the line was meant.
func (s *homeScreen) reveal(pos editor.Position, target *protocol.Range) bool {
	if target != nil {
		if r, ok := lspRangeToEditor(s.editor, *target); ok && s.editor.SelectRange(r) {
			return true
		}
	}
	return s.editor.Reveal(pos)
}

// applyPendingJump retries a jump into a buffer that was still loading. It runs from
// finishHomeUpdate, so it gets a try after every message until the file's read lands.
// It gives up once the buffer has content but not the line asked for — a stale answer
// against a file that changed on disk should cost one wrong caret, not a permanent retry.
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
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// shortPath names a file for a row or a status line: relative to the document root when
// it lives there, and otherwise with the home directory folded back to ~. A jump into a
// dependency produces paths long enough to fill a terminal on their own.
func shortPath(c *Ctx, path string) string {
	if root := docsRoot(c); root != "" {
		if rel, err := filepath.Rel(root, path); err == nil && !strings.HasPrefix(rel, "..") {
			return rel
		}
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		if rel, err := filepath.Rel(home, path); err == nil && !strings.HasPrefix(rel, "..") {
			return filepath.Join("~", rel)
		}
	}
	return path
}

// fileLine reads one line out of a file that has no open buffer. The scanner is capped
// because a location list must not be able to pull a huge generated file into memory to
// print one row of it.
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

// locationPicker is the list several jump targets are offered through — the several
// definitions of an interface method, or every reference to a symbol. It is a pushed
// PickerScreen rather than a popup because the list can be long and wants the /-filter,
// the g/G jumps and the breadcrumb that come with a screen.
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

// locationPreview is the source line a location row shows. An open buffer answers from
// memory — and answers with the user's unsaved edits, which is what they are looking at
// — while anything else is read off disk once, at the moment the list is built.
func locationPreview(c *Ctx, target lspLocation) string {
	line := int(target.Range.Start.Line)
	if ed, ok := c.Doc(target.Path); ok && ed != nil {
		if text, ok := ed.LineText(line); ok {
			return strings.TrimSpace(text)
		}
	}
	return strings.TrimSpace(fileLine(target.Path, line))
}
