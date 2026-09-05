package app

import (
	"strings"
	"sync"
	"testing"

	"github.com/brohd11/bubblestack/components/editor"

	"charm.land/lipgloss/v2"
	"github.com/alecthomas/chroma/v2/lexers"
)

// highlighterFor constructs the same adapter a Chroma-backed language profile does.
func highlighterFor(t *testing.T, ext string) editor.Highlighter {
	t.Helper()
	profile := languageForPath("f" + ext)
	if profile == nil || profile.editor.NewHighlighter == nil {
		t.Fatalf("gote has no highlighted profile for %s", ext)
	}
	return profile.editor.NewHighlighter()
}

// spanText is the concatenation the editor validates a line's spans against.
func spanText(spans []editor.Span) string {
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
		".gd":   "extends \"res://my_file.gd\"\n\nfunc _ready():\n\tvar greeting = \"hello\"\n\tprint(greeting)\n\tvar p := PlayerState.new() # a \" in a comment\n",
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

// TestChromaExtsAllResolve guards the curated profile list against typos. An extension
// resolves either because chroma matches a lexer to its filename or because forcedLexers
// names one for it — the second is how *.glsl earns its place, since GLSL registers itself
// only for *.vert, *.frag and *.geo. Either way the outcome that matters is the same: the
// extension must come out of buildLanguageProfiles with a profile, because an entry that
// resolves to nothing is dropped silently and edits as plain text.
func TestChromaExtsAllResolve(t *testing.T) {
	for _, ext := range chromaExts {
		if lexers.Match("f"+ext) == nil && forcedLexers[ext] == "" {
			t.Errorf("no chroma lexer matches %q — name one in forcedLexers or drop it", ext)
		}
		if languageForPath("f"+ext) == nil {
			t.Errorf("%q built no profile, so it edits literally", ext)
		}
	}
	for ext, name := range forcedLexers {
		if lexers.Get(name) == nil {
			t.Errorf("forcedLexers[%q] names %q, which chroma does not have", ext, name)
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

var benchmarkHighlightSpans []editor.Span

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

// spanFor finds the span whose Text is exactly want. Under the unpatched Chroma lexer a
// quoted extends path is shredded into an Error rune and a handful of names and
// operators, so "the whole path came back as one span" is itself half the assertion.
func spanFor(spans []editor.Span, want string) (editor.Span, bool) {
	for _, sp := range spans {
		if sp.Text == want {
			return sp, true
		}
	}
	return editor.Span{}, false
}

// TestGDScriptExtendsPathIsAString pins the fix registerPatchedGDScript makes. Chroma's
// GDScript lexer dead-ends on the path form of extends: the opening quote becomes an
// Error, the closing one opens a string, and every line after it — comments included —
// is swallowed by it. The leak is what actually ruins the buffer, so it is asserted on a
// later line as well as on line 0.
func TestGDScriptExtendsPathIsAString(t *testing.T) {
	hl := highlighterFor(t, ".gd")
	hl.Parse("extends \"res://my_file.gd\"\n\nfunc _ready():\n\tprint(\"hi\") # a \" in a comment\n")

	line0 := hl.HighlightLine(0)
	path, ok := spanFor(line0, "\"res://my_file.gd\"")
	if !ok {
		t.Fatalf("extends path is not one span: %q", line0)
	}
	if got := path.Style.GetForeground(); got != chStringStyle.GetForeground() {
		t.Errorf("extends path foreground = %v, want the string color %v", got, chStringStyle.GetForeground())
	}
	for _, sp := range line0 {
		if sp.Style.GetForeground() == chErrorStyle.GetForeground() && sp.Style.GetBold() {
			t.Errorf("extends line has an error span %q", sp.Text)
		}
	}

	// The string must not have leaked: row 3's trailing "# a " in a comment" is a comment,
	// and under the bug the whole row reads as one string span instead.
	comment, ok := spanFor(hl.HighlightLine(3), "# a \" in a comment")
	if !ok {
		t.Fatalf("row 3 lost its comment span: %q", hl.HighlightLine(3))
	}
	if got := comment.Style.GetForeground(); got != chCommentStyle.GetForeground() {
		t.Errorf("trailing comment foreground = %v, want the comment color %v", got, chCommentStyle.GetForeground())
	}
}

// TestGDScriptExtendsIdentifierStillTyped is the other half: the patched rules are
// prepended to the classname state, so the plain form must still reach the identifier
// rule underneath them.
func TestGDScriptExtendsIdentifierStillTyped(t *testing.T) {
	hl := highlighterFor(t, ".gd")
	hl.Parse("extends Node\n")
	node, ok := spanFor(hl.HighlightLine(0), "Node")
	if !ok {
		t.Fatalf("extends Node lost its class span: %q", hl.HighlightLine(0))
	}
	if got := node.Style.GetForeground(); got != chTypeStyle.GetForeground() {
		t.Errorf("extends Node foreground = %v, want the type color %v", got, chTypeStyle.GetForeground())
	}
}

// TestGDScriptClassExtendsPath pins the other two keywords that reach the classname state.
// Chroma routes class, class_name and extends through one root rule into one state, so the
// quoted-path patch covers all three — this is what keeps that true rather than implied.
func TestGDScriptClassExtendsPath(t *testing.T) {
	hl := highlighterFor(t, ".gd")
	hl.Parse("class_name Outer\nclass Inner extends \"res://inner.gd\":\n\tvar n := 1 # a \" in a comment\n")

	line1 := hl.HighlightLine(1)
	path, ok := spanFor(line1, "\"res://inner.gd\"")
	if !ok {
		t.Fatalf("inner class extends path is not one span: %q", line1)
	}
	if got := path.Style.GetForeground(); got != chStringStyle.GetForeground() {
		t.Errorf("extends path foreground = %v, want the string color %v", got, chStringStyle.GetForeground())
	}
	for _, sp := range line1 {
		if sp.Style.GetForeground() == chErrorStyle.GetForeground() && sp.Style.GetBold() {
			t.Errorf("class/extends line has an error span %q", sp.Text)
		}
	}
	comment, ok := spanFor(hl.HighlightLine(2), "# a \" in a comment")
	if !ok {
		t.Fatalf("row 2 lost its comment span: %q", hl.HighlightLine(2))
	}
	if got := comment.Style.GetForeground(); got != chCommentStyle.GetForeground() {
		t.Errorf("trailing comment foreground = %v, want the comment color %v", got, chCommentStyle.GetForeground())
	}
}

// TestGDScriptPascalCaseIsTyped covers the rule that gives a project's own classes the same
// color as Godot's. The negative half matters as much as the positive: CONSTANT_CASE is
// deliberately left alone, because the lexer cannot tell a bare enum member from a member
// access and would render the same symbol two ways.
func TestGDScriptPascalCaseIsTyped(t *testing.T) {
	hl := highlighterFor(t, ".gd")
	hl.Parse("extends Node\n\nconst MAX_SPEED = 400.0\nvar p: PlayerState\nvar g := Game.PlayerState\nvar q := PlayerState.new()\nvar speed := 0.0\n")

	typed := map[int]string{
		0: "Node",        // engine class, unchanged
		3: "PlayerState", // user class in a type hint
		4: "PlayerState", // after a dot: member access still colors
		5: "PlayerState", // ahead of the call rule, so not a function
	}
	for row, name := range typed {
		sp, ok := spanFor(hl.HighlightLine(row), name)
		if !ok {
			t.Errorf("row %d has no %q span: %q", row, name, hl.HighlightLine(row))
			continue
		}
		if got := sp.Style.GetForeground(); got != chTypeStyle.GetForeground() {
			t.Errorf("row %d %q foreground = %v, want the type color %v", row, name, got, chTypeStyle.GetForeground())
		}
	}

	for row, name := range map[int]string{2: "MAX_SPEED", 6: "speed"} {
		sp, ok := spanFor(hl.HighlightLine(row), name)
		if !ok {
			t.Errorf("row %d has no %q span: %q", row, name, hl.HighlightLine(row))
			continue
		}
		// An unstyled span is the zero Style, whose foreground is lipgloss's own
		// "no color" rather than a nil interface — compare against that, not nil.
		if got, plain := sp.Style.GetForeground(), (lipgloss.Style{}).GetForeground(); got != plain {
			t.Errorf("row %d %q foreground = %v, want it left unstyled (%v)", row, name, got, plain)
		}
	}
}
