package app

import (
	"strings"
	"testing"

	"github.com/brohd11/bubblestack/components"

	"charm.land/lipgloss/v2"
)

type markdownWantSpan struct {
	text  string
	style lipgloss.Style
}

func markdownRow(doc string, row int) []components.Span {
	hl := newMarkdownHighlighter()
	hl.Parse(doc)
	return hl.HighlightLine(row)
}

func markdownStyleEqual(a, b lipgloss.Style) bool {
	return a.Render("Xy") == b.Render("Xy")
}

func assertMarkdownSpans(t *testing.T, doc string, row int, want ...markdownWantSpan) {
	t.Helper()
	got := markdownRow(doc, row)
	if len(got) != len(want) {
		t.Fatalf("%q row %d: got %d spans, want %d", doc, row, len(got), len(want))
	}
	for i := range want {
		if got[i].Text != want[i].text {
			t.Errorf("%q row %d span %d text = %q, want %q", doc, row, i, got[i].Text, want[i].text)
		}
		if !markdownStyleEqual(got[i].Style, want[i].style) {
			t.Errorf("%q row %d span %d (%q): style mismatch", doc, row, i, got[i].Text)
		}
	}
}

func TestMarkdownSpansReconstructDocument(t *testing.T) {
	doc := "# Héading\n\n- *em* and `code`\n\n```gdscript\nfunc ready():\n\tpass\n```"
	hl := newMarkdownHighlighter()
	hl.Parse(doc)

	styled := 0
	for row, line := range strings.Split(doc, "\n") {
		spans := hl.HighlightLine(row)
		if spans == nil {
			continue
		}
		if got := spanText(spans); got != line {
			t.Fatalf("line %d spans = %q, want %q", row, got, line)
		}
		for _, span := range spans {
			if span.Style.GetForeground() != nil {
				styled++
			}
		}
	}
	if styled == 0 {
		t.Fatal("representative Markdown should produce styled spans")
	}
}

func TestMarkdownStylesStructuralAndInlineText(t *testing.T) {
	hl := newMarkdownHighlighter()
	hl.Parse("# head\n- *em* and `code`")

	for _, tc := range []struct {
		row  int
		text string
	}{
		{0, "# head"},
		{1, "-"},
		{1, "em"},
		{1, "code"},
	} {
		found := false
		for _, span := range hl.HighlightLine(tc.row) {
			if strings.Contains(span.Text, tc.text) && span.Style.GetForeground() != nil {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("row %d has no styled span containing %q", tc.row, tc.text)
		}
	}
}

func TestMarkdownExactInlineSpans(t *testing.T) {
	none := mdStyles[mdStyleNone]
	assertMarkdownSpans(t, "# Heading\n", 0,
		markdownWantSpan{"# Heading", mdHeadingStyle})
	assertMarkdownSpans(t, "a *em* b\n", 0,
		markdownWantSpan{"a *", none}, markdownWantSpan{"em", mdEmphasisStyle}, markdownWantSpan{"* b", none})
	assertMarkdownSpans(t, "a **strong** b\n", 0,
		markdownWantSpan{"a **", none}, markdownWantSpan{"strong", mdStrongStyle}, markdownWantSpan{"** b", none})
	assertMarkdownSpans(t, "a `code` b\n", 0,
		markdownWantSpan{"a `", none}, markdownWantSpan{"code", mdCodeStyle}, markdownWantSpan{"` b", none})
	assertMarkdownSpans(t, "a [text](http://x) b\n", 0,
		markdownWantSpan{"a [", none}, markdownWantSpan{"text", mdLinkStyle}, markdownWantSpan{"](http://x) b", none})
	assertMarkdownSpans(t, "a <https://x.com> b\n", 0,
		markdownWantSpan{"a <", none}, markdownWantSpan{"https://x.com", mdLinkStyle}, markdownWantSpan{"> b", none})

	if got := markdownRow("plain paragraph\n", 0); got != nil {
		t.Errorf("plain paragraph spans = %#v, want nil", got)
	}
}

func TestMarkdownInlineBeatsBlockStyle(t *testing.T) {
	assertMarkdownSpans(t, "# A `b` c\n", 0,
		markdownWantSpan{"# A `", mdHeadingStyle},
		markdownWantSpan{"b", mdCodeStyle},
		markdownWantSpan{"` c", mdHeadingStyle})
}

func TestMarkdownCodeBlocks(t *testing.T) {
	closed := "before\n```go\nfmt.Println()\nx\n```\nafter\n"
	for _, row := range []int{1, 2, 3, 4} {
		assertMarkdownSpans(t, closed, row,
			markdownWantSpan{strings.Split(closed, "\n")[row], mdCodeStyle})
	}
	for _, row := range []int{0, 5} {
		if got := markdownRow(closed, row); got != nil {
			t.Errorf("row %d outside fence = %#v, want nil", row, got)
		}
	}

	unclosed := "before\n```\nunclosed\nmore\n"
	for _, row := range []int{1, 2, 3} {
		assertMarkdownSpans(t, unclosed, row,
			markdownWantSpan{strings.Split(unclosed, "\n")[row], mdCodeStyle})
	}

	indented := "para\n\n    indented\n    code\n\nafter\n"
	for _, row := range []int{2, 3} {
		assertMarkdownSpans(t, indented, row,
			markdownWantSpan{strings.Split(indented, "\n")[row], mdCodeStyle})
	}
}

func TestMarkdownBlockquoteAndLists(t *testing.T) {
	none := mdStyles[mdStyleNone]
	doc := "> quote *em*\n> second\n>\n> > nested\n\nafter\n"
	assertMarkdownSpans(t, doc, 0,
		markdownWantSpan{"> quote *", mdQuoteStyle},
		markdownWantSpan{"em", mdEmphasisStyle},
		markdownWantSpan{"*", mdQuoteStyle})
	for _, row := range []int{1, 2, 3} {
		if got := markdownRow(doc, row); len(got) == 0 {
			t.Errorf("blockquote row %d should be styled", row)
		}
	}
	if got := markdownRow(doc, 5); got != nil {
		t.Errorf("prose after blockquote = %#v, want nil", got)
	}

	assertMarkdownSpans(t, "- *em* item\n", 0,
		markdownWantSpan{"-", mdListStyle},
		markdownWantSpan{" *", none},
		markdownWantSpan{"em", mdEmphasisStyle},
		markdownWantSpan{"* item", none})
	assertMarkdownSpans(t, "1) first\n10) ten\n", 1,
		markdownWantSpan{"10)", mdListStyle}, markdownWantSpan{" ten", none})
	assertMarkdownSpans(t, "- outer\n  - inner\n", 1,
		markdownWantSpan{"  ", none}, markdownWantSpan{"-", mdListStyle}, markdownWantSpan{" inner", none})
	assertMarkdownSpans(t, "> - quoted\n", 0,
		markdownWantSpan{"> ", mdQuoteStyle}, markdownWantSpan{"-", mdListStyle}, markdownWantSpan{" quoted", mdQuoteStyle})
}

func TestMarkdownSetextUnderlineKnownGap(t *testing.T) {
	assertMarkdownSpans(t, "Title\n=====\n\nbody\n", 0,
		markdownWantSpan{"Title", mdHeadingStyle})
	if got := markdownRow("Title\n=====\n\nbody\n", 1); got != nil {
		t.Errorf("setext underline is not styled today; got %#v", got)
	}
}

func TestMarkdownOutOfRange(t *testing.T) {
	hl := newMarkdownHighlighter()
	hl.Parse("# heading")
	for _, row := range []int{-1, 1, 99} {
		if spans := hl.HighlightLine(row); spans != nil {
			t.Errorf("row %d = %#v, want nil", row, spans)
		}
	}
}
