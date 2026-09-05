package app

import (
	"bytes"
	"strconv"
	"testing"

	"github.com/charmbracelet/colorprofile"
)

// TestDefaultPaletteDegradesToBasic pins what a 16-color terminal sees: every 256-color
// default must collapse into the hue family of the basic color it replaced — that color
// or its bright variant — so a string stays green and an error stays red however few
// colors the terminal has. Comment is held to 8 exactly, because its bright variant is 7,
// which on most terminals is the body text it is supposed to recede behind.
//
// It holds because the terminal renderer downsamples what it is given rather than passing
// it through: bubbletea parses the view's SGR back into cell styles and re-emits them
// through the detected colorprofile, and ansi256To16 is a fixed lookup table, so which
// basic color an index collapses to is a fact rather than a guess. Picking a default
// without consulting that table is exactly what this catches.
func TestDefaultPaletteDegradesToBasic(t *testing.T) {
	t.Cleanup(func() { applySyntaxPalette(defaultSyntaxColors()) })

	sample := func() map[string]string {
		return map[string]string{
			"keyword":  chKeywordStyle.Render("x"),
			"type":     chTypeStyle.Render("x"),
			"func":     chFuncStyle.Render("x"),
			"string":   chStringStyle.Render("x"),
			"number":   chNumberStyle.Render("x"),
			"comment":  chCommentStyle.Render("x"),
			"operator": chOperatorStyle.Render("x"),
			"inserted": chInsertedStyle.Render("x"),
			"deleted":  chDeletedStyle.Render("x"),
			"error":    chErrorStyle.Render("x"),
			"heading":  mdHeadingStyle.Render("x"),
			"emphasis": mdEmphasisStyle.Render("x"),
			"strong":   mdStrongStyle.Render("x"),
			"code":     mdCodeStyle.Render("x"),
			"quote":    mdQuoteStyle.Render("x"),
			"link":     mdLinkStyle.Render("x"),
			"list":     mdListStyle.Render("x"),
		}
	}

	applySyntaxPalette(defaultSyntaxColors())
	rich := sample()
	// The basic palette rendered twice: once as itself, once with every color swapped for
	// its bright variant. Between them they are the family a default may land in — built
	// from basicSyntaxColors rather than restated, so a slot cannot drift out of the test.
	applySyntaxPalette(SyntaxColors{BasicColors: true})
	dim := sample()
	applySyntaxPalette(brightenedBasicColors())
	bright := sample()

	for slot, want := range dim {
		var buf bytes.Buffer
		w := &colorprofile.Writer{Forward: &buf, Profile: colorprofile.ANSI}
		if _, err := w.Write([]byte(rich[slot])); err != nil {
			t.Fatalf("%s: %v", slot, err)
		}
		got := buf.String()
		// Comment and quote are the greys; 8's bright variant is 7, i.e. body text.
		if slot == "comment" || slot == "quote" {
			if got != want {
				t.Errorf("%s downsamples to %q, want the dim grey %q", slot, got, want)
			}
			continue
		}
		if got != want && got != bright[slot] {
			t.Errorf("%s downsamples to %q, want %q or its bright %q",
				slot, got, want, bright[slot])
		}
	}
}

// brightenedBasicColors is basicSyntaxColors with each color replaced by its bright
// counterpart (5 → 13, and so on), which is the other half of the hue family a default is
// allowed to land in.
func brightenedBasicColors() SyntaxColors {
	sc := basicSyntaxColors()
	for _, slot := range syntaxColorSlots(&sc) {
		n, err := strconv.Atoi(*slot)
		if err != nil || n >= 8 {
			continue
		}
		*slot = strconv.Itoa(n + 8)
	}
	return sc
}

// TestSyntaxColorTolerance covers what a hand-edited config can do to the palette. A bad
// value must not reach lipgloss.Color, which answers one with a no-op color: a typo would
// erase every comment in the buffer rather than mis-color it.
func TestSyntaxColorTolerance(t *testing.T) {
	def := defaultSyntaxColors()
	for _, slot := range syntaxColorSlots(&def) {
		if _, ok := parseSyntaxColor(*slot); !ok {
			t.Fatalf("default color %q does not parse", *slot)
		}
	}
	basic := basicSyntaxColors()
	for _, slot := range syntaxColorSlots(&basic) {
		if _, ok := parseSyntaxColor(*slot); !ok {
			t.Fatalf("basic color %q does not parse", *slot)
		}
	}

	for _, bad := range []string{"", "  ", "nonsense", "256", "-1", "#zzzzzz", "#abcd", "0x10"} {
		if _, ok := parseSyntaxColor(bad); ok {
			t.Errorf("parseSyntaxColor(%q) accepted", bad)
		}
	}
	for _, good := range []string{"0", "255", " 133 ", "#af5faf", "#a5f", "#AF5FAF"} {
		if _, ok := parseSyntaxColor(good); !ok {
			t.Errorf("parseSyntaxColor(%q) rejected", good)
		}
	}

	// Unparseable slots fall back to their default; valid ones and the flag are left as
	// the user wrote them, so toggling basic_colors off restores the file's own palette.
	sc := SyntaxColors{BasicColors: true, Keyword: "nonsense", Type: "", Func: "#a5f", String: "300"}
	normalizeSyntaxColors(&sc)
	if sc.Keyword != def.Keyword || sc.Type != def.Type || sc.String != def.String {
		t.Errorf("bad slots not repaired: %+v", sc)
	}
	if sc.Func != "#a5f" {
		t.Errorf("valid hex rewritten to %q", sc.Func)
	}
	if !sc.BasicColors {
		t.Error("normalizeSyntaxColors cleared BasicColors")
	}
}
