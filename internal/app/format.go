package app

import (
	"fmt"
	"sort"

	"github.com/brohd11/bubblestack/components"
	"github.com/brohd11/bubblestack/core"
)

// Format document (alt+m): organize imports followed by formatting, applied to the live
// buffer as one undo step. The organize-imports half is a code action rather than part
// of formatting because that is the only way gopls offers it — plain textDocument/
// formatting will not touch a Go file's import block.

// applyFormat installs the server's edits. The editSeq guard is applyCompletionResult's:
// an answer computed against text the user has since changed describes a document that
// no longer exists, and applying it would corrupt rather than format.
func (s *homeScreen) applyFormat(result *lspRequestResult) core.Action {
	if len(result.edits) == 0 {
		return core.SetStatus("already formatted")
	}
	if result.editSeq != s.editor.EditSeq() {
		return core.SetStatus("format skipped: the buffer changed")
	}
	edits, ok := editorEditsFor(s.editor, result.edits)
	if !ok {
		return core.SetStatus("format skipped: the server's edits did not fit the buffer")
	}
	if !s.editor.ApplyEdits(edits) {
		return core.SetStatus("format skipped: overlapping edits")
	}
	return core.SetStatus(fmt.Sprintf("formatted (%d %s)", len(edits), plural(len(edits), "edit", "edits")))
}

// editorEditsFor converts a server's UTF-16 ranges against the live buffer. All or
// nothing: one range that will not convert means the server and the buffer disagree
// about the document, and a partial format is worse than none. Sorting is left to
// ApplyEdits, which has to order them to apply them safely anyway.
func editorEditsFor(editor *components.EditorScreen, edits []lspTextEdit) ([]components.EditorEdit, bool) {
	out := make([]components.EditorEdit, 0, len(edits))
	for _, edit := range edits {
		r, ok := lspRangeToEditor(editor, edit.Range)
		if !ok {
			return nil, false
		}
		out = append(out, components.EditorEdit{Range: r, Text: edit.NewText})
	}
	// Stable document order, so a set that reaches ApplyEdits is already the sequence a
	// reader would expect it to be in.
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i].Range.Start, out[j].Range.Start
		return a.Line < b.Line || a.Line == b.Line && a.Column < b.Column
	})
	return out, true
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// formatOnSave is the format_on_save hook, fired from editorSaved.
//
// It runs AFTER the write, not before it. gote's save is a synchronous buffer flush and
// a format is a round trip to another process: holding ctrl+s open across that RPC would
// make every save feel like the server's latency, and a server that is down would make
// it feel like a hang. So the bytes land first and the reformat arrives a moment later,
// leaving the buffer dirty against what was written — which the dirty marker and the git
// gutter both show, and which the next ctrl+s settles.
//
// It is silent about refusals, unlike every other caller of requestAt: a save must not
// print "no language server for format here" every time a plain text file is written.
// Supports is therefore checked here rather than left to the manager's refusal.
func (s *homeScreen) formatOnSave(sh *core.Shared) {
	c := Of(sh)
	if !c.Config.FormatOnSave || !s.lspFeatureReady(sh) || !c.lsp.Supports(s.currentPath, lspReqFormat) {
		return
	}
	s.requestAt(sh, lspReqFormat)
}
