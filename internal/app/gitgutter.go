package app

import (
	"strings"
	"time"
	"unicode/utf8"

	"github.com/brohd11/bubblestack/components/editor"
	"github.com/brohd11/bubblestack/core"
	"github.com/brohd11/gitstack/repo"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/aymanbagabas/go-udiff"
)

// The git diff gutter: a marker beside every line changed since HEAD. It diffs the live
// buffer against HEAD's copy (fetched once via repo.HeadBlob) rather than running `git
// diff` on the file, so markers track typing and git stays out of the keystroke path.
// bubblestack draws the column; the git meaning is gote's.

// gutter is the diff state for the doc in the editor pane. Baselines are cheap to re-read
// on a switch, so there is no per-doc cache to invalidate when HEAD moves.
type gutter struct {
	path        string        // the doc base belongs to; "" ⇒ nothing loaded
	loading     string        // a read in flight for this path, so it is issued once
	base        string        // HEAD's copy of it
	state       repo.Baseline // and whether HEAD had one at all
	seq         int           // editor generation the drawn signs were computed from
	drawn       bool          // seq is meaningful — generation zero is a real state
	pending     bool          // a debounce wake is already scheduled for pendingPath/pendingSeq
	pendingPath string
	pendingSeq  int
}

const gitGutterDebounce = 250 * time.Millisecond

// baselineMsg carries a finished HeadBlob read as a PropagateAll payload, so it reaches
// this screen even when something is on top of it.
type baselineMsg struct {
	path  string // guarded against on receipt: the pane may have moved on
	base  string
	state repo.Baseline
}

type gutterRefreshMsg struct {
	target *homeScreen
	path   string
	seq    int
}

// The markers: green added, red removed, yellow edited in place. A deletion marks an edge
// of the line after the gap (or the last line's lower edge at the end). Styles are built
// per call to follow the theme.

// signBar is the glyph every whole-line marker draws; the kinds differ by color only. It
// must be one cell wide and join vertically into an unbroken rule (an ASCII pipe leaves
// gaps between rows).
const signBar = "┃" // U+2502 box-drawing light vertical

// The deletion ticks mark a cell's top or bottom edge.
const (
	signDelTop = "▔" // U+2594 upper one-eighth block
	signDelBot = "▁" // U+2581 lower one-eighth block
)

func addedSign() editor.Sign {
	return editor.Sign{Text: signBar, Style: lipgloss.NewStyle().Foreground(lipgloss.Color("2"))}
}

func modifiedSign() editor.Sign {
	return editor.Sign{Text: signBar, Style: lipgloss.NewStyle().Foreground(lipgloss.Color("3"))}
}

func deletedSign(text string) editor.Sign {
	return editor.Sign{Text: text, Style: lipgloss.NewStyle().Foreground(lipgloss.Color("1"))}
}

// newFileSign is the muted wash for an untracked file: every line is new, not worth a
// column of green.
func newFileSign() editor.Sign {
	return editor.Sign{Text: signBar, Style: core.MutedStyle()}
}

// gutterDefault reports whether the column starts on for a launch mode (project_mode or
// single_file_mode). A vault promotion re-asks it for a new mode.
func gutterDefault(cfg Config, mode Mode) bool {
	return cfg.modeDefaults(mode).GitGutter
}

// setGitGutter turns the column on or off, dropping the baseline when off so HEAD is re-read
// later. Turning it on returns the baseline read, which the caller must run: it returns out
// of Update before refreshGutter would.
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

func (s *homeScreen) gitGutterDelay() time.Duration {
	if s.gutterDebounce <= 0 {
		return gitGutterDebounce
	}
	return s.gutterDebounce
}

func (s *homeScreen) gutterRefreshCmd(path string, seq int) tea.Cmd {
	return tea.Tick(s.gitGutterDelay(), func(time.Time) tea.Msg {
		return core.PropagateAll(gutterRefreshMsg{target: s, path: path, seq: seq})
	})
}

// refreshGutter brings the markers up to date, cheaply once per message: the diff waits
// until edits have been quiet for the debounce window.
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
		s.gutter.pending = false
		return s.loadBaseline(s.currentPath)
	}
	seq := s.editor.EditSeq()
	if s.gutter.drawn && s.gutter.seq == seq {
		return nil
	}
	if s.gutter.pending && s.gutter.pendingPath == s.currentPath && s.gutter.pendingSeq == seq {
		return nil
	}
	s.gutter.pending = true
	s.gutter.pendingPath, s.gutter.pendingSeq = s.currentPath, seq
	return s.gutterRefreshCmd(s.currentPath, seq)
}

// drawGutter recomputes the markers when the buffer has moved. It is separate from
// refreshGutter because a finished baseline read arrives as a broadcast the router resolves
// without calling Update, so the markers are drawn right there instead of a keystroke later.
func (s *homeScreen) drawGutter() {
	if s.editor == nil {
		return
	}
	src := s.editor.Text()
	s.gutter.seq, s.gutter.drawn = s.editor.EditSeq(), true
	s.gutter.pending = false
	s.editor.SetSignColumn(gitSignColumn, markers(s.gutter.base, src, s.gutter.state))
}

func (s *homeScreen) applyGutterRefresh(m gutterRefreshMsg) {
	if m.target != s {
		return
	}
	if s.gutter.pending && s.gutter.pendingPath == m.path && s.gutter.pendingSeq == m.seq {
		s.gutter.pending = false
	}
	if !s.gitGutter || s.editor == nil || m.path != s.currentPath || m.path != s.gutter.path {
		return
	}
	if m.seq != s.editor.EditSeq() {
		return
	}
	s.drawGutter()
}

// loadBaseline reads HEAD's copy of path in the cmd lane, one read in flight at a time.
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

// applyBaseline stores a finished read, dropping one for a doc the pane has left.
func (s *homeScreen) applyBaseline(m baselineMsg) {
	if s.gutter.loading == m.path {
		s.gutter.loading = ""
	}
	if m.path != s.currentPath || !s.gitGutter {
		return
	}
	s.gutter.path, s.gutter.base, s.gutter.state = m.path, m.base, m.state
	s.gutter.drawn, s.gutter.pending = false, false
	s.drawGutter()
}

// markers computes the whole marker map from HEAD's copy, the live buffer, and what HEAD
// had, as a pure function so edge cases are testable.
func markers(baseline, buf string, state repo.Baseline) map[int]editor.Sign {
	switch state {
	case repo.BaselineOK:
		return diffMarkers(baseline, buf)
	case repo.BaselineAbsent:
		// Nothing to compare against: the file is new, so every line of it is.
		out := make(map[int]editor.Sign, bufLines(buf))
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

// diffMarkers marks the lines of buf that differ from baseline. It diffs interned lines
// (one rune per distinct line), since udiff's byte diff widens edits to whole lines and
// would mark untouched neighbors. A block with both deletions and insertions is marked
// edited, not added.
func diffMarkers(baseline, buf string) map[int]editor.Sign {
	if baseline == buf {
		return nil
	}
	var in interner
	oldEnc, oldOff := in.encode(strings.Split(baseline, "\n"))
	newEnc, _ := in.encode(strings.Split(buf, "\n"))

	last := bufLines(buf) - 1
	out := make(map[int]editor.Sign)
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

// interner assigns each distinct line a rune above ASCII, shared by both sides, so the
// encoded strings are compared rune by rune.
type interner struct {
	ids  map[string]rune
	next rune
}

// encode returns the interned lines and each line's start offset (with one past the end)
// for mapping edit offsets back to lines.
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

// mark places a sign, dropping out-of-range lines so the map describes what is drawn.
func mark(out map[int]editor.Sign, line int, sign editor.Sign, last int) {
	if line < 0 || line > last {
		return
	}
	out[line] = sign
}

// bufLines counts lines as the editor does (a trailing newline adds an empty line), which
// go-udiff does not.
func bufLines(buf string) int { return strings.Count(buf, "\n") + 1 }
