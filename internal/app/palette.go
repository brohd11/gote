package app

import (
	"image/color"
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"
)

// The syntax palette shared by both highlighters, so a slot they both use (keyword and
// **strong**) is one color. Each file builds its own styles from it.
//
// It is not theme-derived: spans bake their styles at parse time and nothing re-parses on
// a theme change. Config.SyntaxColors is how users change it.

// defaultSyntaxColors is written to a fresh config and backs any unparseable slot. It is
// for 256-color terminals; 16-color terminals use basicSyntaxColors.
func defaultSyntaxColors() SyntaxColors {
	return SyntaxColors{
		Brackets: []string{"178", "176", "32"},
		Keyword:  "176",
		Type:     "41",
		Func:     "32",
		String:   "178",
		Number:   "114",
		Comment:  "8",
		Operator: "117",
		Inserted: "28",
		Deleted:  "132",
		Error:    "196",

		MdHeading:  "167",
		MdEmphasis: "24",
		MdStrong:   "39",
		MdCode:     "100",
		MdQuote:    "102",
		MdLink:     "38",
		MdList:     "136",
	}
}

// basicSyntaxColors is the ANSI palette that BasicColors selects: the terminal's own
// colors, which follow its scheme.
func basicSyntaxColors() SyntaxColors {
	return SyntaxColors{
		Brackets: []string{"3", "5", "4"},
		Keyword:  "5",
		Type:     "6",
		Func:     "4",
		String:   "3",
		Number:   "2",
		Comment:  "8",
		Operator: "1",
		Inserted: "2",
		Deleted:  "1",
		Error:    "1",

		MdHeading:  "1",
		MdEmphasis: "2",
		MdStrong:   "5",
		MdCode:     "3",
		MdQuote:    "8",
		MdLink:     "4",
		MdList:     "6",
	}
}

// syntaxPalette is one resolved palette. Both apply functions build their styles from one,
// which keeps the two highlighters in agreement.
type syntaxPalette struct {
	brackets []color.Color
	keyword  color.Color
	typ      color.Color
	fn       color.Color
	str      color.Color
	num      color.Color
	comment  color.Color
	operator color.Color
	inserted color.Color
	deleted  color.Color
	err      color.Color

	mdHeading  color.Color
	mdEmphasis color.Color
	mdStrong   color.Color
	mdCode     color.Color
	mdQuote    color.Color
	mdLink     color.Color
	mdList     color.Color
}

// parseSyntaxColor accepts an ANSI index ("133") or hex ("#af5faf", "#a5f"). gote checks
// itself because lipgloss renders an unparseable color invisibly, which would erase text.
func parseSyntaxColor(s string) (color.Color, bool) {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "#") {
		if len(s) != 4 && len(s) != 7 {
			return nil, false
		}
		if _, err := strconv.ParseUint(s[1:], 16, 32); err != nil {
			return nil, false
		}
		return lipgloss.Color(s), true
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < 0 || n > 255 {
		return nil, false
	}
	return lipgloss.Color(s), true
}

// syntaxColorSlots lists the config's color fields in fixed order, as pointers so two
// SyntaxColors can be walked in step. A new slot goes here, in the struct and in both apply
// functions.
func syntaxColorSlots(sc *SyntaxColors) []*string {
	return []*string{
		&sc.Keyword, &sc.Type, &sc.Func, &sc.String, &sc.Number,
		&sc.Comment, &sc.Operator, &sc.Inserted, &sc.Deleted, &sc.Error,
		&sc.MdHeading, &sc.MdEmphasis, &sc.MdStrong, &sc.MdCode,
		&sc.MdQuote, &sc.MdLink, &sc.MdList,
	}
}

// normalizeSyntaxColors replaces empty or unparseable slots with defaults, so one typo
// does not cost the palette and the next save writes valid values. It always uses the
// 256 defaults, so turning BasicColors off restores the file's colors.
func normalizeSyntaxColors(sc *SyntaxColors) {
	def := defaultSyntaxColors()
	got, want := syntaxColorSlots(sc), syntaxColorSlots(&def)
	for i := range got {
		if _, ok := parseSyntaxColor(*got[i]); !ok {
			*got[i] = *want[i]
		}
	}
	if sc.Brackets == nil {
		sc.Brackets = append([]string(nil), def.Brackets...)
	}
	for i := range sc.Brackets {
		if _, ok := parseSyntaxColor(sc.Brackets[i]); !ok {
			sc.Brackets[i] = def.Brackets[i%len(def.Brackets)]
		}
	}
}

// resolveSyntaxColors turns the configured strings into colors. BasicColors replaces all
// of them with the ANSI palette.
func resolveSyntaxColors(sc SyntaxColors) syntaxPalette {
	def := defaultSyntaxColors()
	bracketsEnabled := sc.Brackets == nil || len(sc.Brackets) > 0
	if sc.Brackets == nil {
		sc.Brackets = append([]string(nil), def.Brackets...)
	}
	if sc.BasicColors {
		def = basicSyntaxColors()
		sc = def
		if !bracketsEnabled {
			sc.Brackets = []string{}
		}
	}
	// Resolve through the defaults here too: init and tests reach this without LoadConfig.
	col := func(v, fallback string) color.Color {
		if c, ok := parseSyntaxColor(v); ok {
			return c
		}
		c, _ := parseSyntaxColor(fallback)
		return c
	}
	brackets := make([]color.Color, len(sc.Brackets))
	for i, value := range sc.Brackets {
		brackets[i] = col(value, def.Brackets[i%len(def.Brackets)])
	}
	return syntaxPalette{
		brackets: brackets,
		keyword:  col(sc.Keyword, def.Keyword),
		typ:      col(sc.Type, def.Type),
		fn:       col(sc.Func, def.Func),
		str:      col(sc.String, def.String),
		num:      col(sc.Number, def.Number),
		comment:  col(sc.Comment, def.Comment),
		operator: col(sc.Operator, def.Operator),
		inserted: col(sc.Inserted, def.Inserted),
		deleted:  col(sc.Deleted, def.Deleted),
		err:      col(sc.Error, def.Error),

		mdHeading:  col(sc.MdHeading, def.MdHeading),
		mdEmphasis: col(sc.MdEmphasis, def.MdEmphasis),
		mdStrong:   col(sc.MdStrong, def.MdStrong),
		mdCode:     col(sc.MdCode, def.MdCode),
		mdQuote:    col(sc.MdQuote, def.MdQuote),
		mdLink:     col(sc.MdLink, def.MdLink),
		mdList:     col(sc.MdList, def.MdList),
	}
}

// paletteSlots pairs every config key with its style, in syntaxColorSlots' order, for the
// `gote colors` legend and for mapping semantic tokens to slots by name. Styles are
// pointers to the package vars so re-applied palettes show through.
func paletteSlots() []struct {
	key   string
	style *lipgloss.Style
} {
	return []struct {
		key   string
		style *lipgloss.Style
	}{
		{"keyword", &chKeywordStyle},
		{"type", &chTypeStyle},
		{"func", &chFuncStyle},
		{"string", &chStringStyle},
		{"number", &chNumberStyle},
		{"comment", &chCommentStyle},
		{"operator", &chOperatorStyle},
		{"inserted", &chInsertedStyle},
		{"deleted", &chDeletedStyle},
		{"error", &chErrorStyle},
		{"md_heading", &mdHeadingStyle},
		{"md_emphasis", &mdEmphasisStyle},
		{"md_strong", &mdStrongStyle},
		{"md_code", &mdCodeStyle},
		{"md_quote", &mdQuoteStyle},
		{"md_link", &mdLinkStyle},
		{"md_list", &mdListStyle},
	}
}

// slotStyle resolves a config slot name to its style.
func slotStyle(key string) (lipgloss.Style, bool) {
	for _, slot := range paletteSlots() {
		if slot.key == key {
			return *slot.style, true
		}
	}
	return lipgloss.Style{}, false
}

// slotStylePtrs are the style pointers spans hold, rebuilt with the palette so existing
// spans keep theirs (paletteSlots points at mutable vars, which spans must not).
var slotStylePtrs map[string]*lipgloss.Style

func rebuildSlotStylePtrs() {
	next := make(map[string]*lipgloss.Style, len(paletteSlots()))
	for _, slot := range paletteSlots() {
		next[slot.key] = styleRef(*slot.style)
	}
	slotStylePtrs = next
}

func slotStylePtr(key string) (*lipgloss.Style, bool) {
	st, ok := slotStylePtrs[key]
	return st, ok
}

// syntaxColorsForProfile picks the runtime palette for a color profile without touching
// the saved config. Unknown keeps the configured palette.
func syntaxColorsForProfile(sc SyntaxColors, profile colorprofile.Profile) SyntaxColors {
	if profile == colorprofile.ANSI {
		sc.BasicColors = true
	}
	return sc
}

// applySyntaxPalette installs styles before documents are parsed. Highlighters
// bake styles into their spans, so terminal capability must be resolved at startup.
func applySyntaxPalette(sc SyntaxColors) {
	p := resolveSyntaxColors(sc)
	applyChromaPalette(p)
	applyMarkdownPalette(p)
	rebuildSlotStylePtrs() // after both: it copies what they just installed
}

// Install the defaults at init, for callers that never load a config (tests, fenced
// code before Ctx.New).
func init() { applySyntaxPalette(defaultSyntaxColors()) }
