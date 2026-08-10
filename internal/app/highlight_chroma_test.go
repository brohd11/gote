package app

import (
	"strings"
	"testing"

	"github.com/brohd11/bubblestack/components"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/lexers"
)

// highlighterFor builds the highlighter init registers for ext. components' registry
// lookup is unexported, so the tests construct the same thing the factory does; what
// registration itself depends on — that every listed extension resolves to a lexer —
// is asserted separately in TestChromaExtsAllResolve.
func highlighterFor(t *testing.T, ext string) components.Highlighter {
	t.Helper()
	lexer := lexers.Match("f" + ext)
	if lexer == nil {
		t.Fatalf("chroma has no lexer for %s", ext)
	}
	return &chromaHighlighter{lexer: chroma.Coalesce(lexer)}
}

// spanText is the concatenation the editor validates a line's spans against.
func spanText(spans []components.Span) string {
	var b strings.Builder
	for _, sp := range spans {
		b.WriteString(sp.Text)
	}
	return b.String()
}

// TestChromaSpansReconstructLines is the contract test: for every line of a document,
// the spans must concatenate back to that line exactly. The editor drops to a plain
// render when they don't, so a break here shows up as silently lost color rather than
// as a crash — which is why it is worth asserting directly, across a spread of
// languages and over the shapes that make tokens cross lines (block comments, multi-
// line strings, tabs, blank lines, a document with no trailing newline).
func TestChromaSpansReconstructLines(t *testing.T) {
	docs := map[string]string{
		".go":   "package main\n\n/* a block\n   comment */\nfunc main() {\n\tx := `raw\nstring`\n\tprintln(x) // trailing\n}",
		".py":   "import os\n\ndef f(a, b=1):\n    \"\"\"doc\n    string\"\"\"\n    return a + b  # note\n",
		".sh":   "#!/bin/sh\nset -eu\nfor f in *.txt; do\n\techo \"$f\"\ndone\n",
		".json": "{\n  \"a\": [1, 2.5, null],\n  \"b\": {\"c\": \"d\"}\n}\n",
		".css":  "body {\n  color: #fff; /* white */\n}\n",
	}
	for ext, doc := range docs {
		t.Run(ext, func(t *testing.T) {
			hl := highlighterFor(t, ext)
			hl.Parse(doc)
			for i, line := range strings.Split(doc, "\n") {
				spans := hl.HighlightLine(i)
				if spans == nil {
					continue // an unstyled row renders plain, which is always valid
				}
				if got := spanText(spans); got != line {
					t.Fatalf("line %d spans = %q, want %q", i, got, line)
				}
			}
		})
	}
}

// TestChromaHighlightsSomething guards against the contract test passing vacuously: a
// highlighter that answered nil for every row would satisfy it and color nothing.
func TestChromaHighlightsSomething(t *testing.T) {
	hl := highlighterFor(t, ".go")
	hl.Parse("package main\n\nfunc main() {}\n")
	styled := 0
	for i := 0; i < 3; i++ {
		for _, sp := range hl.HighlightLine(i) {
			if sp.Style.GetForeground() != nil {
				styled++
			}
		}
	}
	if styled == 0 {
		t.Fatal("a Go buffer should come back with some colored spans")
	}
}

// TestChromaOutOfRange: rows past the parsed document answer nil rather than panicking.
// Lexers that append a newline of their own (Config.EnsureNL) make the token stream
// walk one row past the buffer, which is the case this pins down.
func TestChromaOutOfRange(t *testing.T) {
	hl := highlighterFor(t, ".go")
	hl.Parse("package main") // no trailing newline: the Go lexer adds one
	if got := spanText(hl.HighlightLine(0)); got != "package main" {
		t.Fatalf("row 0 = %q", got)
	}
	for _, row := range []int{-1, 1, 99} {
		if hl.HighlightLine(row) != nil {
			t.Fatalf("row %d should be nil", row)
		}
	}
}

// TestChromaExtsAllResolve: init silently skips an extension chroma has no lexer for,
// which is the right behavior at runtime and a typo the list should not keep.
func TestChromaExtsAllResolve(t *testing.T) {
	for _, ext := range chromaExts {
		if lexers.Match("f"+ext) == nil {
			t.Errorf("no chroma lexer matches %q — drop it from chromaExts", ext)
		}
	}
}

// TestChromaNilLexer: a highlighter with no lexer parses to nothing instead of
// panicking. init cannot build one, but the zero value should still be safe.
func TestChromaNilLexer(t *testing.T) {
	hl := &chromaHighlighter{}
	hl.Parse("anything at all")
	if hl.HighlightLine(0) != nil {
		t.Fatal("a lexer-less highlighter should answer nothing")
	}
}
