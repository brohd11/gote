package app

import (
	"image/color"
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"
)

// The syntax palette both highlighters draw from. It lives here rather than in either of
// them because they share it: highlight_chroma.go colors source tokens and
// highlight_markdown.go colors document structure, and the two must not disagree about a
// slot they both use (a keyword and a **strong** run are the same purple). Each file
// still owns its own styles — this file resolves colors and hands them over.
//
// Not theme-derived. The framework palette in bubblestack/core repaints with the theme
// and the detected background; syntax colors deliberately do not, because the editor
// bakes styles into editor.Spans when a document is parsed and nothing re-parses on a
// theme change. Constant colors are what makes that safe, and Config.SyntaxColors is how
// a user who dislikes them says so.

// defaultSyntaxColors is the palette a fresh config.yml is written with, and the
// fallback for any slot whose configured value does not parse.
//
// These colors are used on 256-color and true-color terminals. A terminal
// reporting only 16 colors uses basicSyntaxColors directly; the rich palette
// does not need to quantize into the same hues as that separate palette.
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

// basicSyntaxColors is the eight-slot ANSI palette gote shipped before the config key
// existed, and what SyntaxColors.BasicColors selects. These are the colors the terminal's
// own scheme defines, so they follow it — the one thing a 256-color palette cannot do,
// and the whole reason the opt-out is here.
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

// syntaxPalette is one resolved palette: the color for every slot the highlighters draw.
// applyChromaPalette and applyMarkdownPalette each take one and build their own styles
// from it, which is how the two files stay in agreement without importing each other's
// vars.
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

// parseSyntaxColor accepts the two spellings a config value may take — an ANSI index
// 0-255 ("133") or a hex literal ("#af5faf", "#a5f") — and reports whether it took.
// The check is gote's own rather than lipgloss.Color's because lipgloss answers an
// unparseable string with a no-op color, which renders the token invisible instead of
// wrong: a typo would silently erase every comment in the buffer.
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

// syntaxColorSlots is the config's color fields in a fixed order, as pointers so both
// callers can walk two SyntaxColors in lockstep: normalizeSyntaxColors repairs a loaded
// value against the defaults, resolveSyntaxColors reads them. Adding a slot means adding
// it here, to the struct, and to the two apply functions — the compiler catches the last
// two and this list is what the first depends on.
func syntaxColorSlots(sc *SyntaxColors) []*string {
	return []*string{
		&sc.Keyword, &sc.Type, &sc.Func, &sc.String, &sc.Number,
		&sc.Comment, &sc.Operator, &sc.Inserted, &sc.Deleted, &sc.Error,
		&sc.MdHeading, &sc.MdEmphasis, &sc.MdStrong, &sc.MdCode,
		&sc.MdQuote, &sc.MdLink, &sc.MdList,
	}
}

// normalizeSyntaxColors replaces every empty or unparseable slot with its default, the
// same per-key tolerance GitGutter gets: a typo in one color should not cost the user
// their palette, let alone their editor. Repairing cfg rather than only the resolved
// colors means the next SaveConfig writes a file that parses.
//
// The fallback is always the 256 default even when BasicColors is set, so toggling the
// flag off again returns the user to the colors their file actually names. BasicColors is
// applied at resolve time instead — see resolveSyntaxColors.
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

// resolveSyntaxColors turns the configured strings into colors. BasicColors discards the
// rest of sc for the built-in ANSI palette; it is not a fallback the other keys feed
// into, because a user asking for their terminal's own colors means all of them.
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
	// Every slot still resolves through its default: applySyntaxPalette is reachable
	// without LoadConfig's normalization (init below, and tests), so the guard belongs
	// on both paths rather than only the one that reads a file.
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

// paletteSlots is the slot registry: every config key paired with the style it produces,
// in the order syntaxColorSlots walks. It is the bridge between the config's vocabulary
// and the styles, which is what lets `gote colors` print a legend and what lets a semantic
// token name a slot by the same string the user types in config.yml.
//
// The styles are reached through pointers to the package vars rather than copied, because
// a caller may re-apply the palette after this list is built and a copy would go on
// showing the palette that was replaced.
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

// slotStyle resolves a config slot name to its style. It is how a semantic token type,
// having been mapped to a slot name, becomes a color — and why that mapping is written in
// slot names rather than in colors of its own.
func slotStyle(key string) (lipgloss.Style, bool) {
	for _, slot := range paletteSlots() {
		if slot.key == key {
			return *slot.style, true
		}
	}
	return lipgloss.Style{}, false
}

// slotStylePtrs is slotStyle's answer as the reference an editor.Span holds, rebuilt with
// the palette so the pointers it hands out never change under spans already carrying them.
// paletteSlots points at the mutable package vars themselves, which is exactly what a span
// must not reference.
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

// syntaxColorsForProfile selects the runtime palette without modifying the saved
// configuration. Colorless output is left to the renderer; Unknown lets callers
// without a terminal profile retain the configured palette.
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

// The defaults are installed at init so that a caller which never loads a config — a
// test, or the fenced-code renderer reached before Ctx.New — still highlights with the
// palette gote means to ship rather than with nil colors.
func init() { applySyntaxPalette(defaultSyntaxColors()) }
