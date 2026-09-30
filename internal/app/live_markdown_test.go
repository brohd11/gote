package app

import (
	"image/color"
	"strings"
	"testing"

	"github.com/brohd11/bubblestack/components"
	"github.com/brohd11/bubblestack/components/editor"
	"github.com/brohd11/bubblestack/core"
	"github.com/brohd11/bubblestack/tuitest"

	"charm.land/lipgloss/v2"
)

// liveWidth is the render width the construct tests draw at.
const liveWidth = 20

func liveRow(t *testing.T, doc string, row int) (string, []int) {
	t.Helper()
	hl := newMarkdownHighlighter().(*markdownHighlighter)
	hl.Parse(doc)
	spans := hl.RenderLine(row, editor.LiveContext{Width: liveWidth})
	if spans == nil {
		return "<source>", nil
	}
	return spanText(spans), hl.liveCols[row]
}

func TestLiveMarkdownRendersEachConstruct(t *testing.T) {
	for _, tc := range []struct {
		name, doc string
		row       int
		want      string
	}{
		{"atx heading", "### Title", 0, "Title"},
		{"h1", "# Title", 0, "Title"},
		{"closing hashes", "## Title ##", 0, "Title"},
		{"heading alone", "#", 0, ""},
		{"setext keeps its underline", "Title\n===", 1, "<source>"},
		{"bullet", "- item", 0, "• item"},
		{"nested bullet", "- a\n  - b", 1, "  ◦ b"},
		{"quoted fence closer keeps the bar", "> ```\n> a\n> ```", 2, "│ ```"},
		{"a quote does not swallow the next paragraph", "> ```\n> a\ntext", 2, "<source>"},
		{"ordered stays", "1. first", 0, "<source>"},
		{"open task", "- [ ] todo", 0, "• ☐ todo"},
		{"done task", "* [x] done", 0, "• ☑ done"},
		{"quote", "> said", 0, "│ said"},
		{"nested quote", "> > deep", 0, "│ │ deep"},
		{"emphasis", "a *b* **c** d", 0, "a b c d"},
		{"nested emphasis", "***both*** and **a *b* c**", 0, "both and a b c"},
		{"multi-line strong", "**one\ntwo**", 1, "two"},
		{"code span", "run `go test` now", 0, "run  go test  now"},
		{"padded code span", "``a ` b``", 0, " a ` b "},
		{"link", "see [the docs](http://x.y (t)) here", 0, "see the docs here"},
		{"reference link", "[text][r]\n\n[r]: http://x.y", 0, "text"},
		{"autolink", "<http://x.y>", 0, "http://x.y"},
		{"image stays", "![alt](x.png)", 0, "<source>"},
		{"rule", "---", 0, strings.Repeat("─", liveWidth)},
		{"fence opener", "```go\nx\n```", 0, "─── go " + strings.Repeat("─", liveWidth-7)},
		{"bare fence opener", "```\nx\n```", 0, strings.Repeat("─", liveWidth)},
		{"fence body", "```go\nx\n```", 1, "<source>"},
		{"fence closer", "```go\nx\n```", 2, strings.Repeat("─", liveWidth)},
		{"rule in a quote keeps the bar", "> ---", 0, "│ ---"},
		{"plain", "just text", 0, "<source>"},
		{"table header", "| a | bbb |\n|:-|--:|\n| cc | *d* |", 0, "│ a  │ bbb │"},
		{"table delimiter", "| a | bbb |\n|:-|--:|\n| cc | *d* |", 1, "├────┼─────┤"},
		{"table body aligns", "| a | bbb |\n|:-|--:|\n| cc | *d* |", 2, "│ cc │   d │"},
		{"table short row", "| a | b |\n|-|-|\n| c |", 2, "│ c │   │"},
	} {
		if got, _ := liveRow(t, tc.doc, tc.row); got != tc.want {
			t.Errorf("%s: row %d = %q, want %q", tc.name, tc.row, got, tc.want)
		}
	}
}

// Every display rune maps back to a source column inside the line, never backwards, so a
// click or vertical move lands in order.
func TestLiveMarkdownSourceColumns(t *testing.T) {
	doc := "- item **bold** [link](u) `c`\n| a | bbb |\n|-|-|\n# Head"
	hl := newMarkdownHighlighter().(*markdownHighlighter)
	hl.Parse(doc)
	for row, line := range strings.Split(doc, "\n") {
		spans := hl.RenderLine(row, editor.LiveContext{Width: 40})
		if spans == nil {
			continue
		}
		n := len([]rune(spanText(spans)))
		prev := 0
		for i := 0; i <= n; i++ {
			col := hl.SourceCol(row, i)
			if col < prev || col > len([]rune(line)) {
				t.Fatalf("row %d rune %d → col %d (prev %d, line len %d)", row, i, col, prev, len([]rune(line)))
			}
			prev = col
		}
		if hl.SourceCol(row, n) != len([]rune(line)) {
			t.Errorf("row %d: the end maps to %d, want the line's end", row, hl.SourceCol(row, n))
		}
	}
	// "**bold**": rendered "b" is source column 9.
	if got := hl.SourceCol(0, len([]rune("• item "))); got != 9 {
		t.Errorf("rendered 'b' maps to %d, want 9", got)
	}
}

func TestLivePreviewToggleIsMarkdownOnly(t *testing.T) {
	s, sh := newHome(t)
	if s.Update(sh, tuitest.KeyMsg("alt+m")); s.editor.LiveRender() {
		t.Fatal("alt+m is retired; ctrl+p cycles the doc mode")
	}
	s.setDocMode(docModeLive)
	if !s.editor.LiveRender() {
		t.Fatal("Live on the markdown scratch buffer should turn live preview on")
	}
	s.setDocMode(docModeOff)
	if s.editor.LiveRender() {
		t.Fatal("Off should turn it off")
	}
	s.currentPath = "notes.txt"
	s.setDocMode(docModeLive)
	if s.editor.LiveRender() {
		t.Fatal("live preview is markdown-only")
	}
}

func TestLiveMarkdownRuleFollowsWidth(t *testing.T) {
	hl := newMarkdownHighlighter().(*markdownHighlighter)
	hl.Parse("---")
	for _, w := range []int{1, 8, 60} {
		got := spanText(hl.RenderLine(0, editor.LiveContext{Width: w}))
		if want := strings.Repeat("─", max(w, liveMinRule)); got != want {
			t.Errorf("width %d: rule = %q, want %q", w, got, want)
		}
	}
}

func TestLiveMarkdownBulletGlyphs(t *testing.T) {
	hl := newMarkdownHighlighter().(*markdownHighlighter)
	hl.Parse("- a\n  * b\n1. c\n+ [ ] d\ntext")
	want := map[int]editor.Glyph{
		0: {Col: 0, From: '-', Text: '•'},
		1: {Col: 2, From: '*', Text: '◦'},
		3: {Col: 0, From: '+', Text: '•'},
	}
	for row := 0; row < 5; row++ {
		gs := hl.ActiveGlyphs(row)
		w, ok := want[row]
		if !ok {
			if len(gs) != 0 {
				t.Errorf("row %d: glyphs %v, want none", row, gs)
			}
			continue
		}
		if len(gs) != 1 || gs[0].Col != w.Col || gs[0].From != w.From || gs[0].Text != w.Text || gs[0].Style != nil {
			t.Errorf("row %d: glyphs %+v, want %+v", row, gs, w)
		}
	}
}

func TestLiveMarkdownHeadingLevels(t *testing.T) {
	hl := newMarkdownHighlighter().(*markdownHighlighter)
	hl.Parse("# One\n## Two\n### Three")
	ctx := editor.LiveContext{Width: 40}
	styles := make([]lipgloss.Style, 3)
	for row := range styles {
		spans := hl.RenderLine(row, ctx)
		styles[row] = spanStyle(spans[0])
		for _, sp := range spans {
			if !markdownStyleEqual(spanStyle(sp), styles[row]) {
				t.Errorf("row %d span %q: mixed styles on one heading", row, sp.Text)
			}
		}
	}
	h1, h2, h3 := styles[0], styles[1], styles[2]
	if !h1.GetUnderline() || !h1.GetBold() || hasColor(h1.GetBackground()) {
		t.Error("h1 should be the previewer's underlined heading, with no background")
	}
	if !h2.GetBold() || h2.GetUnderline() || !hasColor(h2.GetForeground()) {
		t.Error("h2 should be bold in the heading color, not underlined")
	}
	if !h3.GetBold() || markdownStyleEqual(h2, h3) {
		t.Error("h3 should be bold and dimmer than h2")
	}
	// The source row keeps the plain heading style: levels are a preview effect.
	if raw := hl.HighlightLine(0); !markdownStyleEqual(spanStyle(raw[0]), mdHeadingStyle) {
		t.Error("the source row of an h1 should keep the plain heading style")
	}
}

// hasColor reports whether a style getter returned a set color (unset is NoColor).
func hasColor(c color.Color) bool {
	_, none := c.(lipgloss.NoColor)
	return c != nil && !none
}

// liveSpan finds the rendered span on row whose text is text.
func liveSpan(t *testing.T, hl *markdownHighlighter, row int, text string) lipgloss.Style {
	t.Helper()
	for _, sp := range hl.RenderLine(row, editor.LiveContext{Width: 40}) {
		if sp.Text == text {
			return spanStyle(sp)
		}
	}
	t.Fatalf("row %d has no span %q: %q", row, text, spanText(hl.RenderLine(row, editor.LiveContext{Width: 40})))
	return lipgloss.Style{}
}

func TestLiveMarkdownInlineStyles(t *testing.T) {
	hl := newMarkdownHighlighter().(*markdownHighlighter)
	hl.Parse("a **b** *c* `d`\n# x **y**\n> q **r**")

	b, c := liveSpan(t, hl, 0, "b"), liveSpan(t, hl, 0, "c")
	if !b.GetBold() || hasColor(b.GetForeground()) {
		t.Error("bold should render as weight only, without a color")
	}
	if !c.GetItalic() || hasColor(c.GetForeground()) {
		t.Error("italic should render as slant only, without a color")
	}
	chip := liveSpan(t, hl, 0, " d ")
	if !hasColor(chip.GetBackground()) || !hasColor(chip.GetForeground()) {
		t.Error("a code span should render as a tinted chip")
	}
	if y := liveSpan(t, hl, 1, "y"); !y.GetBold() || !y.GetUnderline() || !hasColor(y.GetForeground()) {
		t.Error("bold inside an h1 should keep the h1's color and underline")
	}
	if r := liveSpan(t, hl, 2, "r"); !r.GetBold() || !hasColor(r.GetForeground()) {
		t.Error("bold inside a quote should keep the quote's color")
	}
	// The source rows keep the highlight palette.
	for _, sp := range hl.HighlightLine(0) {
		if sp.Text == "**b**" && !markdownStyleEqual(spanStyle(sp), mdStrongStyle) {
			t.Error("the source row's bold should keep its color")
		}
	}
}

// Live rows draw in the full previewer's palette; source rows keep md_*.
func TestLiveMarkdownUsesPreviewerPalette(t *testing.T) {
	hl := newMarkdownHighlighter().(*markdownHighlighter)
	hl.Parse("## Two\n### Three\nsee [l](u) and `c`\n> q\n\n---")
	ms := components.CurrentMarkdownStyles()
	for _, c := range []struct {
		name string
		row  int
		text string
		want lipgloss.Style
	}{
		{"h2", 0, "Two", ms.Heading},
		{"h3", 1, "Three", ms.Subheading},
		{"link", 2, "l", ms.Link},
		{"code chip", 2, " c ", ms.CodeSpan},
		{"quote bar", 3, components.MarkdownQuoteBar, ms.Rule},
		{"quote text", 3, " q", ms.QuoteText},
	} {
		if got := liveSpan(t, hl, c.row, c.text); !markdownStyleEqual(got, c.want) {
			t.Errorf("%s: live style differs from the previewer's", c.name)
		}
	}
	rule := hl.RenderLine(5, editor.LiveContext{Width: 10})
	if !markdownStyleEqual(spanStyle(rule[0]), ms.Rule) {
		t.Error("a rule should draw in the previewer's rule color")
	}
	if raw := hl.HighlightLine(0); !markdownStyleEqual(spanStyle(raw[0]), mdHeadingStyle) {
		t.Error("source rows keep the md_* heading color")
	}
}

// A theme switch repaints already-baked live spans on the next RenderLine, no reparse.
func TestLiveMarkdownFollowsThemeChange(t *testing.T) {
	prev := core.CurrentTheme()
	t.Cleanup(func() { core.SetTheme(prev) })
	core.SetTheme("red")
	hl := newMarkdownHighlighter().(*markdownHighlighter)
	hl.Parse("## Two")
	span := hl.RenderLine(0, editor.LiveContext{Width: 20})[0]
	before := spanStyle(span).Render("x")
	core.SetTheme("green")
	hl.RenderLine(0, editor.LiveContext{Width: 20})
	if after := spanStyle(span).Render("x"); after == before {
		t.Fatal("the baked heading span kept the old theme's color")
	}
	if !markdownStyleEqual(spanStyle(span), components.CurrentMarkdownStyles().Heading) {
		t.Error("the heading should now match the new theme's previewer heading")
	}
}
