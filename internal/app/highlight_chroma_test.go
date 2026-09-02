package app

import (
	"strings"
	"sync"
	"testing"

	"github.com/brohd11/bubblestack/components"

	"github.com/alecthomas/chroma/v2/lexers"
)

// highlighterFor constructs the same adapter a Chroma-backed language profile does.
func highlighterFor(t *testing.T, ext string) components.Highlighter {
	t.Helper()
	profile := languageForPath("f" + ext)
	if profile == nil || profile.editor.NewHighlighter == nil {
		t.Fatalf("gote has no highlighted profile for %s", ext)
	}
	return profile.editor.NewHighlighter()
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
		".gd":   "extends Node\n\nfunc _ready():\n\tvar greeting = \"hello\"\n\tprint(greeting)\n",
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

// TestChromaExtsAllResolve guards the curated profile list against typos.
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

func TestChromaRestartHintTracksMultilineGDScriptString(t *testing.T) {
	h, ok := highlighterFor(t, ".gd").(*chromaHighlighter)
	if !ok {
		t.Fatal("GDScript should use the Chroma adapter")
	}
	h.Parse("var text = \"\"\"first\nsecond\nthird\"\"\"\nprint(text)")
	for _, row := range []int{1, 2} {
		if got := h.HighlightRestartLine(row); got != 0 {
			t.Fatalf("multiline string row %d restart = %d, want opener row 0", row, got)
		}
	}
	if got := h.HighlightRestartLine(3); got != 3 {
		t.Fatalf("row after multiline string restart = %d, want itself", got)
	}
}

func TestChromaRestartHintTracksBlockComment(t *testing.T) {
	h, ok := highlighterFor(t, ".go").(*chromaHighlighter)
	if !ok {
		t.Fatal("Go should use the Chroma adapter")
	}
	h.Parse("package p\n/* first\nsecond\nthird */\nvar x = 1")
	for _, row := range []int{2, 3} {
		if got := h.HighlightRestartLine(row); got != 1 {
			t.Fatalf("block comment row %d restart = %d, want opener row 1", row, got)
		}
	}
}

func TestChromaFactoryParsesIndependentSnapshotsConcurrently(t *testing.T) {
	profile := languageForPath("concurrent.gd")
	factory := profile.editor.NewHighlighter
	docs := []string{
		strings.Repeat("func one():\n\tprint(\"one\")\n", 20),
		strings.Repeat("func two():\n\tprint(\"two\")\n", 20),
	}
	var wg sync.WaitGroup
	for i := range docs {
		wg.Add(1)
		go func(doc string) {
			defer wg.Done()
			for range 4 {
				h := factory()
				h.Parse(doc)
			}
		}(docs[i])
	}
	wg.Wait()
}

var benchmarkHighlightSpans []components.Span

func BenchmarkChromaHighlighterLargeDocument(b *testing.B) {
	profile := languageForPath("large.gd")
	if profile == nil || profile.editor.NewHighlighter == nil {
		b.Fatal("GDScript highlighter is not configured")
	}
	hl := profile.editor.NewHighlighter()
	doc := strings.Repeat("func update(delta: float) -> void:\n\tposition.x += delta\n", 10_000)
	b.ReportAllocs()
	b.SetBytes(int64(len(doc)))
	b.ResetTimer()
	for b.Loop() {
		hl.Parse(doc)
		benchmarkHighlightSpans = hl.HighlightLine(10_000)
	}
}

func BenchmarkChromaHighlighterViewport(b *testing.B) {
	profile := languageForPath("viewport.gd")
	if profile == nil || profile.editor.NewHighlighter == nil {
		b.Fatal("GDScript highlighter is not configured")
	}
	doc := strings.Repeat("func update(delta: float) -> void:\n\tposition.x += delta\n", 12)
	b.ReportAllocs()
	b.SetBytes(int64(len(doc)))
	b.ResetTimer()
	for b.Loop() {
		hl := profile.editor.NewHighlighter()
		hl.Parse(doc)
		benchmarkHighlightSpans = hl.HighlightLine(20)
	}
}
