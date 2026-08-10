package app

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

// withColor forces a real color profile for one test, so styled output carries ANSI
// instead of rendering to bare text (go test has no TTY). Same pattern as
// bubblestack's components tests. stripANSI (screen_test.go) removes the sequences
// for string comparison.
func withColor(t *testing.T) {
	t.Helper()
	prev := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(prev) })
}

// TestChromaCodeBlockColors: a known language comes back with colored spans, and the
// color costs no text — stripping ANSI reproduces the block exactly.
func TestChromaCodeBlockColors(t *testing.T) {
	withColor(t)

	code := []string{"x := 1 // hi", "println(x)"}
	rows := chromaCodeBlock("go", code, 40)
	if len(rows) != 2 {
		t.Fatalf("got %d rows, want 2", len(rows))
	}
	if !strings.Contains(rows[0], "\x1b[") {
		t.Errorf("a Go block should carry ANSI color, got %q", rows[0])
	}
	for i, row := range rows {
		if got := stripANSI(row); got != code[i] {
			t.Errorf("row %d strips to %q, want %q", i, got, code[i])
		}
	}
}

// TestChromaCodeBlockUnknownLang: a fence with no language (or one chroma does not
// know) falls back to the reader's muted look rather than erroring.
func TestChromaCodeBlockUnknownLang(t *testing.T) {
	withColor(t)

	for _, lang := range []string{"", "not-a-language"} {
		rows := chromaCodeBlock(lang, []string{"plain words"}, 40)
		if len(rows) != 1 || stripANSI(rows[0]) != "plain words" {
			t.Errorf("lang %q: got %v", lang, rows)
		}
	}
}

// TestChromaCodeBlockWraps: a line wider than the pane folds across rows, and the
// styling survives the fold — ANSI opens and closes inside each row, never straddling
// the cut (which would paint the rest of the block).
func TestChromaCodeBlockWraps(t *testing.T) {
	withColor(t)

	line := `"` + strings.Repeat("a", 50) + `" tail`
	rows := chromaCodeBlock("go", []string{line}, 20)
	if len(rows) < 3 {
		t.Fatalf("a %d-cell line at width 20 should fold to several rows, got %d", len(line), len(rows))
	}
	var plain strings.Builder
	for _, row := range rows {
		plain.WriteString(stripANSI(row))
		// A reset before the row ends means no sequence leaks past the cut.
		if strings.Contains(row, "\x1b[") && !strings.Contains(row, "\x1b[0m") && !strings.HasSuffix(row, "\x1b[m") {
			t.Errorf("row %q opens ANSI without closing it", row)
		}
	}
	if plain.String() != line {
		t.Errorf("the folded rows reconstruct %q, want %q", plain.String(), line)
	}
}

// TestChromaCodeBlockEmpty: an empty block renders as nothing, not a panic or a stray
// styled blank.
func TestChromaCodeBlockEmpty(t *testing.T) {
	if rows := chromaCodeBlock("go", nil, 40); len(rows) != 0 {
		t.Errorf("an empty block should render no rows, got %v", rows)
	}
}
