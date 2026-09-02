package app

import (
	"strings"
	"unicode/utf8"

	"github.com/brohd11/bubblestack/components"
	"github.com/brohd11/bubblestack/core"
	"github.com/brohd11/gitstack/repo"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/aymanbagabas/go-udiff"
)

// The git diff gutter: a marker beside every line the buffer has changed since HEAD.
//
// It is deliberately not `git diff`. That command describes the file on disk, and an
// editor's gutter has to describe the buffer — otherwise every keystroke leaves the
// markers a save behind, and the one moment you most want to see what you have touched
// is the moment the markers are wrong. So HEAD's copy of the file is fetched once
// (repo.HeadBlob) and diffed here against the live text, which is what makes the markers
// move as you type and what keeps git out of the keystroke path entirely.
//
// bubblestack draws the column but knows nothing about git (components.Sign) — the same
// division EditorOpts.Highlighter draws. Everything below this line is gote's.

// gutter is the diff state for the doc the editor pane is showing. One doc's worth: the
// pane shows one buffer, and a baseline is cheap enough to re-read on a switch that
// caching per open doc would only add an invalidation problem (HEAD moves under a
// commit) for no visible gain.
type gutter struct {
	path    string        // the doc base belongs to; "" ⇒ nothing loaded
	loading string        // a read in flight for this path, so it is issued once
	base    string        // HEAD's copy of it
	state   repo.Baseline // and whether HEAD had one at all
	src     string        // the buffer text the drawn signs were computed from
	drawn   bool          // src is meaningful — an empty buffer is a real state, not "unset"
}

// baselineMsg carries a finished HeadBlob read. It travels as a PropagateAll payload
// rather than as a plain async result because a plain result reaches only the screen on
// top of the stack: open the actions menu while the read is in flight and the answer
// lands nowhere, leaving a gutter that never appears. The broadcast reaches this screen
// wherever it sits, which is what ReseedMsg already relies on.
type baselineMsg struct {
	path  string // guarded against on receipt: the pane may have moved on
	base  string
	state repo.Baseline
}

// The markers, in the vocabulary git's own diffs use — green added, red removed — with
// yellow for the third case a diff has no word for: a line that is neither, having been
// edited in place.
//
// Deletions have no line to mark, only a gap between two, so they take a boundary tick
// on the line the removal sat in front of: an upper edge normally, a lower one when the
// deletion ran off the end of the buffer and there is no line below it to point at.
//
// Styles are rebuilt per call so a theme switch repaints them, as every other style in
// this app is (core.MutedColor is a live var, not a constant).

// signBar is the glyph EVERY whole-line marker draws — added, modified and new-file
// alike, which differ by color and not by shape. One constant rather than three literals
// so the column can be re-spelled in one place, and so the two candidates below can be
// traded by moving a single comment.
//
// Which one reads better is a question about the terminal and the font, not about the
// code, so both are kept here to be swapped and compared. Both are chosen for the same
// property: a run of changed lines has to join into ONE unbroken margin rule, or the
// column reads as a stack of separate marks and stops answering "how much of this did I
// touch" at a glance.
//
//   - The half block fills its cell edge to edge, so it joins by construction.
//   - The box-drawing vertical is a thinner rule that joins just as cleanly on a terminal
//     that draws it full-height.
//
// The ASCII pipe is deliberately NOT one of the candidates: it leaves a gap between rows
// (measured, not assumed), which is the one thing this column cannot have.
//
// Whatever replaces it must measure exactly one display cell: components.Sign says so,
// and the editor's whole left-gutter width is derived from that assumption.
// const signBar = "▌" // U+258C left half block
const signBar = "┃" // U+2502 box-drawing light vertical

// The deletion ticks. They mark an edge rather than a line, so they stay pinned to the
// top and bottom of the cell and have no bearing on how signBar is spelled — swapping
// that one does not ask for these to change too.
const (
	signDelTop = "▔" // U+2594 upper one-eighth block
	signDelBot = "▁" // U+2581 lower one-eighth block
)

func addedSign() components.Sign {
	return components.Sign{Text: signBar, Style: lipgloss.NewStyle().Foreground(lipgloss.Color("2"))}
}

func modifiedSign() components.Sign {
	return components.Sign{Text: signBar, Style: lipgloss.NewStyle().Foreground(lipgloss.Color("3"))}
}

func deletedSign(text string) components.Sign {
	return components.Sign{Text: text, Style: lipgloss.NewStyle().Foreground(lipgloss.Color("1"))}
}

// newFileSign is the whole-file wash an untracked file gets: every line is new, which is
// true but not worth a column of green shouting it. Muted says "git has never seen this"
// without competing with the markers that report an actual change.
func newFileSign() components.Sign {
	return components.Sign{Text: signBar, Style: lipgloss.NewStyle().Foreground(core.MutedColor)}
}

// gutterDefault decides whether the column starts on. The config has the final say; its
// "auto" (the default) defers to the launch, and the launch answers by mode.
//
// A bare `gote <file>` is the chrome-less editor — no header, no lists, no help bar,
// nano's shape — and a git column is exactly the kind of thing that launch exists to
// leave out. The full editor is where you are working through a project's files, which
// is where the question "what have I changed here" is actually being asked.
func gutterDefault(cfg Config, mode Mode) bool {
	switch cfg.GitGutter {
	case gutterOn:
		return true
	case gutterOff:
		return false
	default:
		return mode != ModeFile
	}
}

// setGitGutter turns the column on or off, clearing the cached baseline on the way down
// so switching back re-reads it — HEAD may have moved while it was off.
//
// Turning it ON returns the work that has to happen for the column to hold anything, and
// the caller must run it. Showing the column is not drawing it: the baseline read is
// async, and every caller here is a key or a menu row that returns straight out of
// Update — past the tail where refreshGutter otherwise runs. Left to that tail, a toggle
// would raise an empty column and fill it one unrelated keystroke later.
func (s *homeScreen) setGitGutter(on bool) tea.Cmd {
	s.gitGutter = on
	if s.editor != nil {
		s.editor.ShowSignColumn(gitSignColumn, on)
	}
	if !on {
		s.gutter = gutter{}
		if s.editor != nil {
			s.editor.SetSignColumn(gitSignColumn, nil)
		}
		return nil
	}
	return s.refreshGutter()
}

// refreshGutter brings the markers up to date with the buffer, and is called once per
// message from Update — the same place and for the same reason as refreshPreview, which
// re-renders a whole markdown document on the same schedule. The text comparison is what
// makes that affordable: a diff runs only when the buffer actually changed, so mouse
// motion, scrolling and every navigation key cost one string compare.
//
// Diffing synchronously rather than behind a debounce timer is deliberate. The diff is
// pure and fast (gopls runs the same algorithm on every keystroke), and a timer would
// buy nothing here while adding the stale-result problem that comes with it — an answer
// arriving after the edit that invalidated it. The only slow part is the git read, and
// that is already async and happens once per document.
func (s *homeScreen) refreshGutter() tea.Cmd {
	if !s.gitGutter || s.editor == nil {
		return nil
	}
	// The scratch buffer is not a file, so there is nothing for HEAD to have a copy of.
	if s.currentPath == "" {
		if s.gutter.path != "" || s.gutter.drawn {
			s.gutter = gutter{}
			s.editor.SetSignColumn(gitSignColumn, nil)
		}
		return nil
	}
	if s.gutter.path != s.currentPath {
		return s.loadBaseline(s.currentPath)
	}
	s.drawGutter()
	return nil
}

// drawGutter recomputes the markers, when the buffer has moved since the ones on screen
// were computed from it.
//
// It is split out of refreshGutter because a finished baseline read needs it too, and
// cannot get it from there: that result comes back as a broadcast Action, and the router
// resolves an Action against the stack and returns without dispatching to any screen's
// Update (core/router.go). So a gutter drawn only from Update would come up empty on the
// read that was supposed to fill it, and stay empty until some unrelated key or mouse
// event happened along — markers that appear a keystroke after you open a file.
//
// Doing it inline rather than bouncing a redraw back through the event loop is what
// makes that a non-event: the baseline arrives holding everything the computation needs,
// and bubbletea renders after every message anyway, so the frame that follows this one
// already has the markers in it.
func (s *homeScreen) drawGutter() {
	if s.editor == nil {
		return
	}
	src := s.editor.Text()
	if s.gutter.drawn && src == s.gutter.src {
		return
	}
	s.gutter.src, s.gutter.drawn = src, true
	s.editor.SetSignColumn(gitSignColumn, markers(s.gutter.base, src, s.gutter.state))
}

// loadBaseline reads HEAD's copy of path in the cmd lane, where every other bit of IO in
// this framework lives. The in-flight path guard is what keeps it to one subprocess:
// refreshGutter runs on every message, so without it a slow git would be re-spawned a
// few dozen times before the first one answered.
func (s *homeScreen) loadBaseline(path string) tea.Cmd {
	if s.gutter.loading == path {
		return nil
	}
	s.gutter.loading = path
	return func() tea.Msg {
		dir, ok := repo.RepoRoot(path)
		if !ok {
			return core.PropagateAll(baselineMsg{path: path, state: repo.BaselineNone})
		}
		base, state, _ := repo.HeadBlob(dir, path)
		return core.PropagateAll(baselineMsg{path: path, base: base, state: state})
	}
}

// applyBaseline takes a finished read. A result for a doc the pane has since left is
// dropped rather than stored — it would sit there claiming to be the current file's
// baseline until the next switch, marking every line of the wrong document.
func (s *homeScreen) applyBaseline(m baselineMsg) {
	if s.gutter.loading == m.path {
		s.gutter.loading = ""
	}
	if m.path != s.currentPath || !s.gitGutter {
		return
	}
	s.gutter.path, s.gutter.base, s.gutter.state = m.path, m.base, m.state
	s.gutter.src, s.gutter.drawn = "", false // the markers on screen predate this baseline
	s.drawGutter()
}

// markers is the whole marker computation, pure over two strings so the edge cases can
// be tested without a repo. baseline is HEAD's copy, buf the live buffer, state what
// HEAD had to offer.
func markers(baseline, buf string, state repo.Baseline) map[int]components.Sign {
	switch state {
	case repo.BaselineOK:
		return diffMarkers(baseline, buf)
	case repo.BaselineAbsent:
		// Nothing to compare against: the file is new, so every line of it is.
		out := make(map[int]components.Sign, bufLines(buf))
		for i := range bufLines(buf) {
			out[i] = newFileSign()
		}
		return out
	default:
		// Ignored, or not in a repo at all. Neither is a change, and a column of
		// markers on a file git is deliberately not tracking says the opposite.
		return nil
	}
}

// diffMarkers marks the lines of buf that differ from baseline.
//
// The diff runs over INTERNED LINES, not over the text, and that is the whole trick.
// udiff.Strings diffs bytes and then widens each edit to the lines it touches, so
// inserting one line mid-file comes back as "the line above was deleted and two lines
// were inserted" — the unchanged line above gets swept into the change block and marked.
// Correct as a patch, useless as a gutter: it marks lines you did not touch.
//
// Giving every distinct line its own rune and diffing THOSE makes a rune-level edit a
// line-level edit exactly, with no widening step to blur it. It is the same interning the
// GDScript original does for the same reason, and it still runs the library's Myers
// rather than a hand-rolled one.
//
// The classification on top is the part a diff does not draw for you: a block holding
// both deletions and insertions is a line that was EDITED, not one removed and another
// added, and marking it green would say a line is new when what you want to know is that
// you changed it.
func diffMarkers(baseline, buf string) map[int]components.Sign {
	if baseline == buf {
		return nil
	}
	var in interner
	oldEnc, oldOff := in.encode(strings.Split(baseline, "\n"))
	newEnc, _ := in.encode(strings.Split(buf, "\n"))

	last := bufLines(buf) - 1
	out := make(map[int]components.Sign)
	delta := 0 // how far the new side has drifted from the old, in lines
	for _, e := range udiff.Strings(oldEnc, newEnc) {
		from, ok1 := oldOff[e.Start]
		to, ok2 := oldOff[e.End]
		if !ok1 || !ok2 {
			continue // an edit off a line boundary can't be placed; never seen in practice
		}
		dels := to - from
		adds := utf8.RuneCountInString(e.New)
		block := from + delta // where this edit lands in the BUFFER's numbering
		delta += adds - dels

		switch {
		case adds > 0:
			sign := addedSign()
			if dels > 0 {
				sign = modifiedSign()
			}
			for n := block; n < block+adds; n++ {
				mark(out, n, sign, last)
			}
		case block <= last:
			// A pure deletion has no line of its own. Tick the top edge of the line it
			// sat in front of.
			mark(out, block, deletedSign(signDelTop), last)
		default:
			// It ran off the end of the buffer: there is no line below to point at, so
			// the last line takes a lower tick instead.
			mark(out, last, deletedSign(signDelBot), last)
		}
	}
	return out
}

// interner assigns each distinct line a rune, shared across both sides so the same text
// encodes to the same rune. Runes start above ASCII so the encoded strings are never
// mistaken for ASCII by udiff's fast path, which diffs bytes — the encoding must be
// compared rune by rune or a multi-byte line id could match halfway through.
type interner struct {
	ids  map[string]rune
	next rune
}

// encode returns the interned form of lines, and the byte offset each line starts at —
// the table that turns an edit's offsets back into line numbers. It carries one entry
// past the end, since an edit's End may be the end of the text.
func (in *interner) encode(lines []string) (string, map[int]int) {
	if in.ids == nil {
		in.ids, in.next = map[string]rune{}, 0x100
	}
	var b strings.Builder
	offsets := make(map[int]int, len(lines)+1)
	for i, ln := range lines {
		offsets[b.Len()] = i
		r, ok := in.ids[ln]
		if !ok {
			r, in.next = in.next, in.next+1
			// Surrogates are not encodable; skipping the block keeps every id a real rune.
			if in.next == 0xD800 {
				in.next = 0xE000
			}
			in.ids[ln] = r
		}
		b.WriteRune(r)
	}
	offsets[b.Len()] = len(lines)
	return b.String(), offsets
}

// mark places a sign, ignoring one that falls outside the buffer. Out-of-range keys are
// harmless to the editor, but dropping them keeps the map an honest description of what
// is drawn — which is what the tests read.
func mark(out map[int]components.Sign, line int, sign components.Sign, last int) {
	if line < 0 || line > last {
		return
	}
	out[line] = sign
}

// bufLines counts the buffer's lines the way EditorScreen does — splitting on "\n", so a
// trailing newline yields a final empty line. go-udiff counts differently (it drops that
// line), and a gutter indexed by the wrong one puts its last marker on the wrong row.
func bufLines(buf string) int { return strings.Count(buf, "\n") + 1 }
