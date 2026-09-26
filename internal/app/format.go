package app

import (
	"sort"

	"github.com/brohd11/bubblestack/components/editor"
	"github.com/brohd11/bubblestack/core"
	"github.com/brohd11/goutil/strutil"
)

// Format document (alt+shift+m): organize imports then format, as one undo step. Organizing
// imports is a code action because gopls's formatting leaves the import block alone.

// applyFormat installs the server's edits, unless the buffer changed since the request
// (applying them would corrupt it).
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
	return core.SetStatus("formatted (" + strutil.Count(len(edits), "edit") + ")")
}

// editorEditsFor converts the server's UTF-16 ranges against the live buffer, all or
// nothing: a partial format is worse than none.
func editorEditsFor(ed *editor.Screen, edits []lspTextEdit) ([]editor.Edit, bool) {
	out := make([]editor.Edit, 0, len(edits))
	for _, edit := range edits {
		r, ok := lspRangeToEditor(ed, edit.Range)
		if !ok {
			return nil, false
		}
		out = append(out, editor.Edit{Range: r, Text: edit.NewText})
	}
	// Stable document order, so a set that reaches ApplyEdits is already the sequence a
	// reader would expect it to be in.
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i].Range.Start, out[j].Range.Start
		return a.Line < b.Line || a.Line == b.Line && a.Column < b.Column
	})
	return out, true
}

// formatOnSave is the format_on_save hook, run after the write so saving never waits on
// the server; the buffer is left dirty until the next save. It checks Supports itself so
// plain files do not print a refusal on every save.
func (s *homeScreen) formatOnSave(sh *core.Shared) {
	c := Of(sh)
	if !c.Config.FormatOnSave || !s.lspFeatureReady(sh) || !c.lsp.Supports(s.currentPath, lspReqFormat) {
		return
	}
	s.requestAt(sh, lspReqFormat)
}
