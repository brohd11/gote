package app

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/brohd11/bubblestack/components"
	"github.com/brohd11/bubblestack/core"
	"github.com/brohd11/gitstack/repo"

	"github.com/charmbracelet/lipgloss"
)

// kind names a sign for a test's expectations, since two Signs differ only by a glyph
// and a color and neither reads well in a failure message.
func kind(s components.Sign) string {
	fg := s.Style.GetForeground()
	switch {
	case s.Text == signDelTop:
		return "del^"
	case s.Text == signDelBot:
		return "del_"
	case fg == lipgloss.Color("2"):
		return "add"
	case fg == lipgloss.Color("3"):
		return "mod"
	case fg == core.MutedColor:
		return "new"
	}
	return fmt.Sprintf("?%q", s.Text)
}

// summary renders a marker map as "line:kind" pairs in line order, so a mismatch reads
// as the picture the gutter would draw.
func summary(m map[int]components.Sign) string {
	lines := make([]int, 0, len(m))
	for n := range m {
		lines = append(lines, n)
	}
	sort.Ints(lines)
	parts := make([]string, len(lines))
	for i, n := range lines {
		parts[i] = fmt.Sprintf("%d:%s", n, kind(m[n]))
	}
	return strings.Join(parts, " ")
}

// TestDiffMarkers is the core of the feature, and it is pure over two strings — no repo,
// no editor. The cases are the four shapes a gutter has to get right; the classification
// one (an edited line is "modified", not "added") is the one a unified diff does not
// draw for you.
func TestDiffMarkers(t *testing.T) {
	cases := []struct {
		name     string
		baseline string
		buf      string
		want     string
	}{
		{"unchanged", "a\nb\nc\n", "a\nb\nc\n", ""},
		{"pure insert", "a\nb\n", "a\nX\nb\n", "1:add"},
		{"insert run", "a\nb\n", "a\nX\nY\nb\n", "1:add 2:add"},
		{
			// Both a deletion and an insertion in one block: the line was edited, and
			// green would claim it is new.
			"edit in place", "a\nb\nc\n", "a\nB\nc\n", "1:mod",
		},
		{
			// A deletion has no line of its own, so exactly one boundary tick lands on
			// the line it sat in front of — not a marker per removed line.
			"delete mid-file", "a\nb\nc\n", "a\nc\n", "1:del^",
		},
		{
			// Off the end of the buffer: nothing below to point at, so the last line
			// takes a lower tick instead.
			"delete off the end", "a\nb\nc", "a\nb", "1:del_",
		},
		{
			// Two changes far enough apart to be separate hunks: the second one's line
			// numbers must account for the first one's growth.
			"two hunks", "a\nb\nc\nd\ne\nf\ng\nh\n", "a\nX\nb\nc\nd\ne\nf\nG\nh\n",
			"1:add 7:mod",
		},
		{"whole file replaced", "a\nb\n", "x\ny\n", "0:mod 1:mod"},
		{"empty baseline", "", "a\n", "0:add"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := summary(diffMarkers(c.baseline, c.buf)); got != c.want {
				t.Errorf("markers = %q, want %q", got, c.want)
			}
		})
	}
}

// TestMarkersByBaselineState: the gutter answers a different question for each thing
// HEAD had to say. A file git is not tracking is not a file with no changes, and a file
// git is deliberately ignoring should not be marked at all.
func TestMarkersByBaselineState(t *testing.T) {
	const buf = "a\nb\nc"

	if got := summary(markers("a\nb\nc", buf, repo.BaselineOK)); got != "" {
		t.Errorf("a buffer matching HEAD should have no markers, got %q", got)
	}
	if got := summary(markers("", buf, repo.BaselineAbsent)); got != "0:new 1:new 2:new" {
		t.Errorf("an untracked file is new all the way down, got %q", got)
	}
	for _, state := range []repo.Baseline{repo.BaselineIgnored, repo.BaselineNone} {
		if m := markers("", buf, state); m != nil {
			t.Errorf("%s should draw nothing, got %q", state, summary(m))
		}
	}
}

// TestMarkersStayInsideTheBuffer: a marker computed against a buffer is indexed into
// that buffer's lines by the editor, so a line number past the end is a bug worth
// catching here rather than a blank cell nobody notices.
func TestMarkersStayInsideTheBuffer(t *testing.T) {
	const buf = "a\nb"
	for n := range markers("a\nb\nc\nd\ne", buf, repo.BaselineOK) {
		if n < 0 || n >= bufLines(buf) {
			t.Errorf("marker on line %d, outside a %d-line buffer", n, bufLines(buf))
		}
	}
}

// TestBufLinesMatchesTheEditor pins the counting convention. go-udiff drops a trailing
// empty line and EditorScreen keeps it; indexing a gutter by the wrong one puts the last
// marker on the wrong row.
func TestBufLines(t *testing.T) {
	cases := map[string]int{"": 1, "a": 1, "a\n": 2, "a\nb": 2, "a\nb\n": 3}
	for buf, want := range cases {
		if got := bufLines(buf); got != want {
			t.Errorf("bufLines(%q) = %d, want %d", buf, got, want)
		}
		if got := len(strings.Split(buf, "\n")); got != want {
			t.Errorf("%q: the editor splits into %d lines, bufLines says %d", buf, got, want)
		}
	}
}

// TestSignGlyphsAreOneCell guards the constraint a glyph swap can quietly break. The
// editor derives its whole left-gutter width from "a sign is one display cell", so a
// two-cell replacement shifts every row of the body one column right of where clicks
// land — and nothing else in the build would object.
func TestSignGlyphsAreOneCell(t *testing.T) {
	for _, glyph := range []string{signBar, signDelTop, signDelBot} {
		if got := lipgloss.Width(glyph); got != 1 {
			t.Errorf("%q measures %d display cells, want 1", glyph, got)
		}
	}
}

// TestGutterDefault: the config decides, and its default defers to the launch. A bare
// `gote <file>` is the chrome-less editor, which is exactly the launch a git column
// should stay out of.
func TestGutterDefault(t *testing.T) {
	cases := []struct {
		setting string
		mode    Mode
		want    bool
	}{
		{gutterAuto, ModeHome, true},
		{gutterAuto, ModeScan, true},
		{gutterAuto, ModeVault, true},
		{gutterAuto, ModeFile, false},
		{gutterOn, ModeFile, true},
		{gutterOff, ModeHome, false},
		{"", ModeFile, false}, // an unset value reads as auto
	}
	for _, c := range cases {
		if got := gutterDefault(Config{GitGutter: c.setting}, c.mode); got != c.want {
			t.Errorf("gutterDefault(%q, mode %d) = %v, want %v", c.setting, c.mode, got, c.want)
		}
	}
}

// TestHomeGutterDefaults checks the two launches end up with the column the config's
// auto promises, and that the editor itself was told — the screen flag alone would draw
// nothing.
func TestHomeGutterDefaults(t *testing.T) {
	s, _ := newHome(t)
	if !s.gitGutter || !s.editor.SignsMode() {
		t.Errorf("the full editor should open with the gutter on, screen=%v editor=%v",
			s.gitGutter, s.editor.SignsMode())
	}

	file := filepath.Join(t.TempDir(), "solo.md")
	if err := os.WriteFile(file, []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	m, _ := newHomeWith(t, Options{Mode: ModeFile, File: file})
	if m.gitGutter || m.editor.SignsMode() {
		t.Error("a single-file launch should open without the gutter")
	}
}

// TestHomeGutterToggle: alt+g flips the column, and the flip has to reach the editor and
// survive a document switch — the preference belongs to the pane, not to a buffer.
func TestHomeGutterToggle(t *testing.T) {
	s, sh := newHome(t)
	s.Update(sh, altKey('g'))
	if s.gitGutter || s.editor.SignsMode() {
		t.Fatal("alt+g should turn the gutter off")
	}

	s.openDoc(sh, filepath.Join(t.TempDir(), "a.txt"))
	if s.editor.SignsMode() {
		t.Error("a doc opened while the gutter is off should not draw the column")
	}

	s.Update(sh, altKey('g'))
	if !s.gitGutter || !s.editor.SignsMode() {
		t.Error("alt+g again should bring it back, on the doc now in the pane")
	}
}

// TestHomeGutterMarksARealRepo is the end-to-end path: a checkout on disk, a doc opened
// from it, the baseline read through a real `git show`, and markers on the lines that
// actually differ from HEAD.
//
// It runs through the router rather than calling Receive directly, because the delivery
// is half the thing under test: the read broadcasts its result (core.PropagateAll) so it
// lands whatever screen happens to be on top when it finishes, and only the router
// resolves that.
func TestHomeGutterMarksARealRepo(t *testing.T) {
	dir := t.TempDir()
	gitInit(t, dir)
	file := filepath.Join(dir, "notes.md")
	if err := os.WriteFile(file, []byte("one\ntwo\nthree\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitRun(t, dir, "add", ".")
	gitRun(t, dir, "commit", "-q", "-m", "init")

	model, s, sh := newHomeRouter(t, Options{})
	s.openDoc(sh, file)
	s.editor.SetText("one\nTWO\nthree\n")

	cmd := s.refreshGutter()
	if cmd == nil {
		t.Fatal("a doc with no baseline loaded should ask for one")
	}
	// Nothing is called after this: delivering the baseline must be enough to draw. The
	// router resolves a broadcast Action against the stack and returns WITHOUT dispatching
	// to any screen's Update, so a gutter that only recomputed there would come up empty
	// on the very read meant to fill it — markers appearing a keystroke after you open a
	// file, if at all.
	model.Update(cmd())

	if got := kind(signAt(t, s, 1)); got != "mod" {
		t.Errorf("the edited line should be marked modified, got %s", got)
	}
	if _, ok := s.editor.Signs()[0]; ok {
		t.Error("an unchanged line should carry no marker")
	}

	// A second pass with the buffer untouched must not re-read git: the baseline is
	// cached against the path, and re-spawning a subprocess per message is the failure
	// this whole design exists to avoid.
	if s.refreshGutter() != nil {
		t.Error("an unchanged buffer should ask git for nothing")
	}
}

// TestHomeGutterToggleDraws: alt+g turning the column back ON must fill it, not raise an
// empty one. The screen's key handlers return straight out of Update, past the tail where
// refreshGutter otherwise runs, so the toggle has to carry its own baseline read — and
// nothing here calls refreshGutter to cover for it.
func TestHomeGutterToggleDraws(t *testing.T) {
	dir := t.TempDir()
	gitInit(t, dir)
	file := filepath.Join(dir, "notes.md")
	if err := os.WriteFile(file, []byte("one\ntwo\nthree\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitRun(t, dir, "add", ".")
	gitRun(t, dir, "commit", "-q", "-m", "init")

	model, s, sh := newHomeRouter(t, Options{})
	s.openDoc(sh, file)
	s.editor.SetText("one\nTWO\nthree\n")

	// Off, then on — the way a user reaches for it. The off leg clears the baseline, so
	// the on leg has to go back to git for it.
	if cmd := s.setGitGutter(false); cmd != nil {
		t.Error("turning the column off should ask git for nothing")
	}
	cmd := s.setGitGutter(true)
	if cmd == nil {
		t.Fatal("turning the column on should hand back the baseline read it needs")
	}
	model.Update(cmd()) // the router resolves the broadcast; nothing else is called

	if got := kind(signAt(t, s, 1)); got != "mod" {
		t.Errorf("the edited line should be marked straight after the toggle, got %s", got)
	}
}

func signAt(t *testing.T, s *homeScreen, line int) components.Sign {
	t.Helper()
	sign, ok := s.editor.Signs()[line]
	if !ok {
		t.Fatalf("no marker on line %d", line)
	}
	return sign
}

func gitInit(t *testing.T, dir string) {
	t.Helper()
	gitRun(t, dir, "init", "-q", "-b", "main")
	gitRun(t, dir, "config", "user.email", "t@t")
	gitRun(t, dir, "config", "user.name", "t")
}

func gitRun(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}
