package app

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/lexers"
)

func TestYAMLFlowBrackets(t *testing.T) {
	applySyntaxPalette(defaultSyntaxColors())
	t.Cleanup(func() { applySyntaxPalette(defaultSyntaxColors()) })
	const example = `tool_level_2:
  title: ${area_check} Level 2
  desc: Use this tool 100 times, then unlock level 2 to reveal its chain, advanced efficiency and Day Energy II upgrades.
  icon: area_check
  values: [1, 2]
  tier: 2
  spacing: 160
  prereq: [uses;100]
next: [one, two]`
	for _, tc := range []struct {
		name, doc, brackets string
		depths              []int // -1 means an unmatched delimiter
	}{
		{"reported document", example, "[][][]", []int{0, 0, 0, 0, 0, 0}},
		{"semicolon", "prereq: [uses;100]\nnext: [1, 2]", "[][]", []int{0, 0, 0, 0}},
		{"no semicolon", "prereq: [uses100]", "[]", []int{0, 0}},
		{"words", "x: [one, two]", "[]", []int{0, 0}},
		{"quotes", `x: ["uses;100", 'uses;100', 'it''s [fine]', "[escaped\" ]"]`, "[]", []int{0, 0}},
		{"nested", "x: [one, {name: [uses;100, two]}, [three]]", "[{[]}[]]", []int{0, 1, 2, 2, 1, 1, 1, 0}},
		{"mapping", "x: {name: uses}\nnext: [one]", "{}[]", []int{0, 0, 0, 0}},
		{"flow pair", "x: [name: value]", "[]", []int{0, 0}},
		{"quoted block key", `"x": ["name": value]`, "[]", []int{0, 0}},
		{"brackets in block key", "items[0]: [uses;100]", "[]", []int{0, 0}},
		{"quoted mapping key", `x: {"name":[uses;100], 'other': [two]}`, "{[][]}", []int{0, 1, 1, 1, 1, 0}},
		{"block sequence", "- [uses;100]\n- {name: value}", "[]{}", []int{0, 0, 0, 0}},
		{"multiline", "x: [\n  one, # ] }\n  {name:\n    [two, three]}\n]\nnext: [four]", "[{[]}][]", []int{0, 1, 2, 2, 1, 0, 0, 0}},
		{"multiline quotes", "x: [\"one\\\n  [two\", 'three\n  {four']\nnext: [five]", "[][]", []int{0, 0, 0, 0}},
		{"scalar punctuation", "x: [https://example.test/a#b, 123#tag, true#tag, uses;100]", "[]", []int{0, 0}},
		{"aliases", "base: &base one\nx: [*base, &item two]\nnext: [three]", "[][]", []int{0, 0, 0, 0}},
		{"comments", "x: [one, # ] {\n two] # [\nnext: [three]", "[][]", []int{0, 0, 0, 0}},
		{"literal text", "title: ${area_check} Level 2\ntext: words [and {brackets}\nquoted: \"[}\"\nnext: [one]", "[]", []int{0, 0}},
		{"block scalar", "text: |\n  [unclosed {\n  more ]\nnext: [one]", "[]", []int{0, 0}},
		{"folded scalar", "text: >\n  [unclosed {\n  more ]\nnext: [one]", "[]", []int{0, 0}},
		{"unclosed", "x: [one", "[", []int{-1}},
		{"unexpected closer", "x: ]", "]", []int{-1}},
		{"mismatch", "x: [one}\n]", "[}]", []int{0, -1, 0}},
	} {
		for _, ext := range []string{".yaml", ".yml"} {
			t.Run(tc.name+ext, func(t *testing.T) {
				h := highlighterFor(t, ext)
				h.Parse(tc.doc)
				var brackets strings.Builder
				var styles []*lipgloss.Style
				for row, line := range strings.Split(tc.doc, "\n") {
					spans := h.HighlightLine(row)
					if got := spanText(spans); got != line {
						t.Fatalf("row %d text = %q, want %q", row, got, line)
					}
					for _, span := range spans {
						structural := span.Style == chErrorStylePtr
						for _, style := range chBracketStyles {
							structural = structural || span.Style == style
						}
						if structural {
							brackets.WriteString(span.Text)
							styles = append(styles, span.Style)
						}
					}
				}
				if got := brackets.String(); got != tc.brackets {
					t.Fatalf("structural brackets = %q, want %q", got, tc.brackets)
				}
				for i, depth := range tc.depths {
					want := chErrorStylePtr
					if depth >= 0 {
						want = chBracketStyles[depth%len(chBracketStyles)]
					}
					if styles[i] != want {
						t.Errorf("bracket %d style = %p, want depth %d (%p)", i, styles[i], depth, want)
					}
				}
			})
		}
	}
}

func TestYAMLFlowTokens(t *testing.T) {
	iter, err := lexers.Get("yaml").Tokenise(nil, "prereq: [uses;100]\n")
	if err != nil {
		t.Fatal(err)
	}
	var literal, punctuation string
	for _, tok := range iter.Tokens() {
		switch tok.Type {
		case chroma.Literal:
			literal += tok.Value
		case chroma.Punctuation:
			punctuation += tok.Value
		}
	}
	if literal != "uses;100" || punctuation != ":[]" {
		t.Fatalf("literal = %q, punctuation = %q; want uses;100 and :[]", literal, punctuation)
	}
}

func TestYAMLFlowPreviewRestart(t *testing.T) {
	t.Cleanup(func() { applySyntaxPalette(defaultSyntaxColors()) })
	for _, enabled := range []bool{true, false} {
		sc := defaultSyntaxColors()
		if !enabled {
			sc.Brackets = []string{}
		}
		applySyntaxPalette(sc)
		lines := []string{"title: ${area_check}", "x: [", "  one,", "  {name: [", "    two", "  ]}", "]", "next: [three]"}
		exact := highlighterFor(t, ".yaml").(*chromaHighlighter)
		exact.Parse(strings.Join(lines, "\n"))
		for row, want := range []int{0, 1, 1, 1, 1, 1, 1, 7} {
			if got := exact.HighlightRestartLine(row); got != want {
				t.Fatalf("rainbow=%v row %d restart = %d, want %d", enabled, row, got, want)
			}
		}
		anchor := exact.HighlightRestartLine(4)
		preview := exact.NewHighlightPreview(anchor)
		preview.Parse(strings.Join(lines[anchor:6], "\n"))
		for row := anchor; row < 6; row++ {
			got, want := preview.HighlightLine(row-anchor), exact.HighlightLine(row)
			if len(got) != len(want) {
				t.Fatalf("preview row %d spans = %v, want %v", row, got, want)
			}
			for i := range got {
				if got[i] != want[i] {
					t.Errorf("preview row %d span %d = %v, want %v", row, i, got[i], want[i])
				}
			}
		}
		if !enabled {
			for _, span := range delimiterSpans(exact, len(lines)) {
				if span.style != nil {
					t.Errorf("disabled YAML bracket %q still has style %p", span.r, span.style)
				}
			}
		}
	}
}

func TestYAMLCodeBlockFlowBrackets(t *testing.T) {
	applySyntaxPalette(defaultSyntaxColors())
	t.Cleanup(func() { applySyntaxPalette(defaultSyntaxColors()) })
	code := []string{"prereq: [uses;100]", "next: [one, two]"}
	rows := chromaCodeBlock("yaml", code, 80)
	if len(rows) != len(code) {
		t.Fatalf("got %d rows, want %d", len(rows), len(code))
	}
	for i, row := range rows {
		if stripANSI(row) != code[i] {
			t.Errorf("row %d changed text: %q", i, row)
		}
		for _, bracket := range []string{"[", "]"} {
			if !strings.Contains(row, chBracketStyles[0].Render(bracket)) {
				t.Errorf("row %d missing depth-zero %s: %q", i, bracket, row)
			}
		}
	}
}
