package app

import (
	"regexp"
	"strings"
	"testing"
)

// TestChromaCodeBlockColors: a known language comes back with colored spans, and the
// color costs no text — stripping ANSI reproduces the block exactly.
func TestChromaCodeBlockColors(t *testing.T) {

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

func TestChromaCodeBlockInheritsRainbowBrackets(t *testing.T) {
	t.Cleanup(func() { applySyntaxPalette(defaultSyntaxColors()) })
	applySyntaxPalette(defaultSyntaxColors())
	rows := chromaCodeBlock("go", []string{"func f() { g() }"}, 40)
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1", len(rows))
	}
	if !strings.Contains(rows[0], chBracketStyles[0].Render("(")) ||
		!strings.Contains(rows[0], chBracketStyles[1].Render("(")) {
		t.Errorf("fenced Go block did not inherit rainbow depths: %q", rows[0])
	}
}

func TestGDScriptCodeBlockUsesChroma(t *testing.T) {
	code := []string{"func ready():", "\tpass"}
	rows := chromaCodeBlock("gdscript", code, 40)
	if len(rows) != len(code) {
		t.Fatalf("got %d rows, want %d", len(rows), len(code))
	}
	for i, row := range rows {
		// lipgloss expands a styled tab to the editor's four-cell display form.
		want := strings.ReplaceAll(code[i], "\t", "    ")
		if got := stripANSI(row); got != want {
			t.Errorf("row %d strips to %q, want %q", i, got, want)
		}
	}
	if !strings.Contains(rows[0], "\x1b[") {
		t.Errorf("a GDScript block should carry ANSI color, got %q", rows[0])
	}
}

// TestChromaCodeBlockUnknownLang: a fence with no language (or one chroma does not
// know) falls back to the reader's muted look rather than erroring.
func TestChromaCodeBlockUnknownLang(t *testing.T) {

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
var sgrSeq = regexp.MustCompile("\x1b\\[[0-9;]*m")

func TestChromaCodeBlockWraps(t *testing.T) {

	line := `"` + strings.Repeat("a", 50) + `" tail`
	rows := chromaCodeBlock("go", []string{line}, 20)
	if len(rows) < 3 {
		t.Fatalf("a %d-cell line at width 20 should fold to several rows, got %d", len(line), len(rows))
	}
	var plain strings.Builder
	for _, row := range rows {
		plain.WriteString(stripANSI(row))
		// The row's LAST sequence being a reset is what says no style leaks past the
		// cut. It need not be the row's final bytes: a fold can land mid-token, leaving
		// plain text after the reset. (lipgloss v1 emitted no escapes at all under
		// `go test`, so this check used to be vacuous here.)
		if seqs := sgrSeq.FindAllString(row, -1); len(seqs) > 0 {
			if last := seqs[len(seqs)-1]; last != "\x1b[m" && last != "\x1b[0m" {
				t.Errorf("row %q ends with %q, want its style closed by a reset", row, last)
			}
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
