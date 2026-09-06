package app

import (
	"testing"

	"github.com/brohd11/bubblestack/components/editor"

	"charm.land/lipgloss/v2"
)

// TestDecodeSemanticTokensRelativeEncoding pins the wire format. Every tuple is relative
// to the one before it, and deltaStartChar changes meaning depending on whether the row
// advanced — relative to the previous token on the same row, absolute on a new one.
// Getting that second rule wrong is silent: nothing errors, every token after the first
// line break simply lands somewhere else.
func TestDecodeSemanticTokensRelativeEncoding(t *testing.T) {
	legend := []string{"variable", "type", "function"}
	slots := map[string]string{"type": "type", "function": "func"}

	// row 0 col 4 len 3 type; row 0 col 12 len 5 function; row 2 col 2 len 4 type.
	data := []uint32{
		0, 4, 3, 1, 0, // absolute: line 0, char 4
		0, 8, 5, 2, 0, // same line: char 4+8 = 12
		2, 2, 4, 1, 0, // line 0+2 = 2, char resets to absolute 2
	}
	got := decodeSemanticTokens(data, legend, slots)
	want := []semanticToken{
		{line: 0, startUTF16: 4, lenUTF16: 3, slot: "type"},
		{line: 0, startUTF16: 12, lenUTF16: 5, slot: "func"},
		{line: 2, startUTF16: 2, lenUTF16: 4, slot: "type"},
	}
	if len(got) != len(want) {
		t.Fatalf("decoded %d tokens, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("token %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

// TestDecodeSemanticTokensSkipsUnmapped covers the property that makes this an overlay
// rather than a replacement: a type the palette does not map is dropped at decode, so the
// run keeps whatever chroma gave it. It also covers the two malformed answers worth
// surviving — a type index outside the server's legend, and a trailing partial tuple.
func TestDecodeSemanticTokensSkipsUnmapped(t *testing.T) {
	legend := []string{"variable", "type"}
	slots := map[string]string{"type": "type"}
	data := []uint32{
		0, 0, 3, 0, 0, // variable: unmapped, dropped
		0, 4, 3, 1, 0, // type: kept
		0, 4, 3, 9, 0, // index past the legend: dropped
		0, 4, 3, // a partial tuple: ignored, not read past
	}
	got := decodeSemanticTokens(data, legend, slots)
	if len(got) != 1 {
		t.Fatalf("decoded %d tokens, want 1: %+v", len(got), got)
	}
	if got[0].startUTF16 != 4 || got[0].slot != "type" {
		t.Errorf("token = %+v, want the type at char 4", got[0])
	}
	// The relative walk must still have advanced across the dropped tokens: a dropped
	// token is not a skipped tuple, and treating it as one would shift everything after.
	if got[0].line != 0 {
		t.Errorf("line = %d, want 0", got[0].line)
	}
}

// TestOverlaySpansSplitsOnRunes covers the other silent failure: the offsets are UTF-16
// code units, so on a line holding an astral-plane rune they are not rune indices and a
// naive slice cuts in the wrong place — or mid-rune, which would corrupt the text.
func TestOverlaySpansSplitsOnRunes(t *testing.T) {
	applySyntaxPalette(defaultSyntaxColors())

	// The emoji is one rune but TWO UTF-16 code units, so "Foo" starts at rune 5 and at
	// UTF-16 offset 6.
	line := `s := "🚀" + Foo`
	base := []editor.Span{{Text: line}}
	tokens := []lineToken{{startUTF16: 12, lenUTF16: 3, slot: "type"}}

	got, ok := overlaySpans(line, base, tokens)
	if !ok {
		t.Fatal("overlaySpans refused a line its spans reconstruct")
	}
	var rebuilt string
	var typed string
	for _, span := range got {
		rebuilt += span.Text
		if span.Style.Render("z") == chTypeStyle.Render("z") {
			typed += span.Text
		}
	}
	// The span contract: the editor drops any highlighting that does not reconstruct the
	// line exactly, so this is the assertion that keeps the overlay visible at all.
	if rebuilt != line {
		t.Errorf("spans rebuild %q, want %q", rebuilt, line)
	}
	if typed != "Foo" {
		t.Errorf("type-styled text = %q, want %q", typed, "Foo")
	}
}

// TestOverlaySpansRejectsMismatch covers the guard that keeps a stale or fragment parse
// from painting the wrong text: spans that do not cover the line are refused outright
// rather than partially applied.
func TestOverlaySpansRejectsMismatch(t *testing.T) {
	line := "package app"
	short := []editor.Span{{Text: "package"}}
	if _, ok := overlaySpans(line, short, []lineToken{{lenUTF16: 3, slot: "type"}}); ok {
		t.Error("accepted spans that do not cover the line")
	}
	long := []editor.Span{{Text: line + " extra"}}
	if _, ok := overlaySpans(line, long, []lineToken{{lenUTF16: 3, slot: "type"}}); ok {
		t.Error("accepted spans that overrun the line")
	}
}

// TestOverlaySpansPreservesBaseStyles checks the overlay only claims the ranges it names:
// a token re-styles its own run and every other run keeps the color chroma gave it.
func TestOverlaySpansPreservesBaseStyles(t *testing.T) {
	applySyntaxPalette(defaultSyntaxColors())
	line := "var x Foo"
	base := []editor.Span{
		{Text: "var", Style: chKeywordStyle},
		{Text: " x "},
		{Text: "Foo"},
	}
	got, ok := overlaySpans(line, base, []lineToken{{startUTF16: 6, lenUTF16: 3, slot: "type"}})
	if !ok {
		t.Fatal("overlaySpans refused")
	}
	if got[0].Text != "var" || got[0].Style.Render("z") != chKeywordStyle.Render("z") {
		t.Errorf("first span = %+v, want chroma's keyword", got[0])
	}
	last := got[len(got)-1]
	if last.Text != "Foo" || last.Style.Render("z") != chTypeStyle.Render("z") {
		t.Errorf("last span = %+v, want the type style", last)
	}
}

// TestSemanticSlotsOverride covers the per-server escape hatch: a server's own names merge
// over the standard map, and an empty value subtracts a default rather than painting it.
func TestSemanticSlotsOverride(t *testing.T) {
	merged := semanticSlots(map[string]string{"builtinType": "type", "comment": ""})
	if merged["builtinType"] != "type" {
		t.Errorf("override not applied: %q", merged["builtinType"])
	}
	if _, ok := merged["comment"]; ok {
		t.Error("empty override did not disable the type")
	}
	if merged["function"] != "func" {
		t.Errorf("defaults lost through the merge: %q", merged["function"])
	}
	// The defaults must not be mutated by a merge — they are package state shared by
	// every other server.
	if _, ok := defaultSemanticSlots["builtinType"]; ok {
		t.Error("merge wrote through to the shared default map")
	}
	if defaultSemanticSlots["comment"] != "comment" {
		t.Error("merge deleted from the shared default map")
	}
}

// TestSlotStyleNames pins that every slot the semantic map targets actually resolves to a
// style. A typo here is invisible: the token would simply never paint.
func TestSlotStyleNames(t *testing.T) {
	for tokenType, slot := range defaultSemanticSlots {
		if _, ok := slotStyle(slot); !ok {
			t.Errorf("%s maps to slot %q, which is not a palette slot", tokenType, slot)
		}
	}
	if _, ok := slotStyle("not_a_slot"); ok {
		t.Error("slotStyle accepted an unknown name")
	}
	var zero lipgloss.Style
	if style, _ := slotStyle("not_a_slot"); style.Render("z") != zero.Render("z") {
		t.Error("slotStyle returned a non-zero style for an unknown name")
	}
}

// TestOverlayFollowsTheLineNotTheRow is the contract the overlay actually needs. Tokens
// describe TEXT, and the editor re-derives highlighting from two places — the exact parse
// over the whole document, and a fragment covering the caret row to the bottom of the
// viewport that is re-run on every keystroke. Anything keyed by absolute row misses the
// second one entirely, which is a screenful of colour disappearing between keystrokes.
//
// So: a line keeps its colour wherever it moves to, and loses it only when its own text
// changes.
func TestOverlayFollowsTheLineNotTheRow(t *testing.T) {
	applySyntaxPalette(defaultSyntaxColors())
	path := "/tmp/overlay_test.go"
	t.Cleanup(func() { forgetSemanticTokens(path) })

	scored := "type Foo struct{}\nvar x = 1\ntype Bar struct{}\n"
	setSemanticTokens(path, 1, []semanticToken{
		{line: 0, startUTF16: 5, lenUTF16: 3, slot: "type"}, // Foo
		{line: 2, startUTF16: 5, lenUTF16: 3, slot: "type"}, // Bar
	}, hashLines(scored))

	typed := func(h editor.Highlighter, row int) string {
		var out string
		for _, sp := range h.HighlightLine(row) {
			if sp.Style.Render("z") == chTypeStyle.Render("z") {
				out += sp.Text
			}
		}
		return out
	}
	parse := func(text string) editor.Highlighter {
		h := semanticHighlighterFactory(languageForPath(path).editor.NewHighlighter, path)()
		h.Parse(text)
		return h
	}

	h := parse(scored)
	if got := typed(h, 0); got != "Foo" {
		t.Errorf("row 0 type text = %q, want Foo", got)
	}
	if got := typed(h, 2); got != "Bar" {
		t.Errorf("row 2 type text = %q, want Bar", got)
	}

	// A line inserted at the top pushes both scored lines down. Keyed by row this loses
	// every colour on screen; keyed by content the lines simply carry theirs along.
	h = parse("package p\n" + scored)
	if got := typed(h, 1); got != "Foo" {
		t.Errorf("Foo lost its colour when the line moved to row 1: %q", got)
	}
	if got := typed(h, 3); got != "Bar" {
		t.Errorf("Bar lost its colour when the line moved to row 3: %q", got)
	}

	// Editing a line is the one thing that should bare it: the server's description of
	// that text no longer applies. Its neighbours are untouched.
	h = parse("type Foo struct{}\nvar x = 22\ntype Fooo struct{}\n")
	if got := typed(h, 0); got != "Foo" {
		t.Errorf("row 0 lost its colour over an edit on another row: %q", got)
	}
	if got := typed(h, 2); got != "" {
		t.Errorf("the edited row painted %q from a description of its old text", got)
	}
}

// TestOverlayAppliesToAPreviewFragment is the case that was flashing. The editor parses a
// bounded fragment synchronously on every keystroke — starting partway down the document,
// so its row 0 is not the document's — and prefers it over the exact snapshot for every
// row it covers. If the overlay does not apply there, the server's colours vanish from the
// caret to the bottom of the viewport on every character typed.
func TestOverlayAppliesToAPreviewFragment(t *testing.T) {
	applySyntaxPalette(defaultSyntaxColors())
	path := "/tmp/fragment_test.go"
	t.Cleanup(func() { forgetSemanticTokens(path) })

	doc := "package p\n\nvar n = 1\ntype Foo struct{}\n"
	setSemanticTokens(path, 1, []semanticToken{
		{line: 3, startUTF16: 5, lenUTF16: 3, slot: "type"},
	}, hashLines(doc))

	// The fragment is the tail of the document, the way refreshHighlightPreview builds
	// one: its row 1 is the document's row 3.
	h := semanticHighlighterFactory(languageForPath(path).editor.NewHighlighter, path)()
	h.Parse("var n = 1\ntype Foo struct{}\n")

	var typed string
	for _, sp := range h.HighlightLine(1) {
		if sp.Style.Render("z") == chTypeStyle.Render("z") {
			typed += sp.Text
		}
	}
	if typed != "Foo" {
		t.Errorf("fragment row 1 type text = %q, want Foo — the preview path is unpainted", typed)
	}
}

// TestTokensByLineDropsAmbiguousLines: identical text with disagreeing tokens cannot be
// resolved, so neither occurrence is painted. Guessing would put one line's colours on
// another's, which is worse than the chroma-only rendering this overlay improves on.
func TestTokensByLineDropsAmbiguousLines(t *testing.T) {
	doc := "a.Run()\nb = 2\na.Run()\n"
	hashes := hashLines(doc)

	agree := tokensByLine([]semanticToken{
		{line: 0, startUTF16: 2, lenUTF16: 3, slot: "func"},
		{line: 2, startUTF16: 2, lenUTF16: 3, slot: "func"},
	}, hashes)
	if got := agree[hashLine("a.Run()")]; len(got) != 1 || got[0].slot != "func" {
		t.Errorf("agreeing duplicate lines were not kept: %+v", got)
	}

	disagree := tokensByLine([]semanticToken{
		{line: 0, startUTF16: 2, lenUTF16: 3, slot: "func"},
		{line: 2, startUTF16: 2, lenUTF16: 3, slot: "type"},
	}, hashes)
	if _, ok := disagree[hashLine("a.Run()")]; ok {
		t.Error("disagreeing duplicate lines were kept; one line would wear the other's colour")
	}

	// An empty line would collide with every other empty line and carries nothing anyway.
	blank := tokensByLine([]semanticToken{{line: 0, lenUTF16: 1, slot: "type"}},
		hashLines("\nx = 1\n"))
	if _, ok := blank[hashLine("")]; ok {
		t.Error("the empty line was keyed")
	}
}

// TestHashLinesDistinguishesRows guards the change detector itself: a fingerprint that
// collapsed distinct lines would silently keep stale colours alive.
func TestHashLinesDistinguishesRows(t *testing.T) {
	got := hashLines("alpha\nbeta\nalpha")
	if len(got) != 3 {
		t.Fatalf("hashed %d rows, want 3", len(got))
	}
	if got[0] != got[2] {
		t.Error("identical lines hashed differently")
	}
	if got[0] == got[1] {
		t.Error("different lines hashed the same")
	}
	if len(hashLines("")) != 1 {
		t.Error("an empty document should still have one row")
	}
}
