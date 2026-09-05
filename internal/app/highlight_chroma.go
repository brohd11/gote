package app

import (
	"strings"

	"github.com/brohd11/bubblestack/components/editor"

	"charm.land/lipgloss/v2"
	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/lexers"
)

// Syntax coloring for the source files gote gets pointed at in scan mode. It lives here
// rather than in bubblestack because languageForPath is gote's language authority;
// bubblestack receives only a language-neutral Highlighter factory. Markdown has its
// own goldmark highlighter beside this file because headings and emphasis are document
// structure rather than source tokens.
//
// Chroma is the right tokenizer for this because it is lossless — the Values of the
// tokens it emits concatenate back to the exact input — which is precisely the contract
// editor.Span demands (the editor drops to a plain render for any line whose spans
// don't reconstruct it). Nothing here goes near chroma's formatters: those exist to
// write ANSI, and the editor needs styled *runs*, which it composites itself.

// The palette. Fixed ANSI like gote's markdown highlighter, deliberately: the
// editor's syntax colors are not theme-derived, and the two files should not disagree
// about that. The 8 basic slots keep it readable on whatever the terminal's own scheme
// is, which a 256-color palette would not.
var (
	chKeywordStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("5")).Bold(true)
	chTypeStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("6"))
	chFuncStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("4"))
	chStringStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
	chNumberStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("3"))
	chCommentStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Italic(true)
	chOperatorStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
	chInsertedStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
	chDeletedStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
	chErrorStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("1")).Bold(true)
)

// chromaStyles maps token types to styles. Only coarse types are listed; styleFor walks
// a token up to its sub-category and category, so every one of chroma's ~200 leaf types
// resolves to one of these or to nothing (an unstyled run, which is correct for plain
// text, punctuation and whitespace — coloring everything colors nothing).
var chromaStyles = map[chroma.TokenType]lipgloss.Style{
	chroma.Keyword:         chKeywordStyle,
	chroma.KeywordType:     chTypeStyle,
	chroma.NameBuiltin:     chTypeStyle,
	chroma.NameClass:       chTypeStyle,
	chroma.NameFunction:    chFuncStyle,
	chroma.NameDecorator:   chFuncStyle,
	chroma.NameTag:         chKeywordStyle,
	chroma.NameAttribute:   chFuncStyle,
	chroma.LiteralString:   chStringStyle,
	chroma.LiteralNumber:   chNumberStyle,
	chroma.Comment:         chCommentStyle,
	chroma.CommentPreproc:  chKeywordStyle,
	chroma.Operator:        chOperatorStyle,
	chroma.GenericInserted: chInsertedStyle,
	chroma.GenericDeleted:  chDeletedStyle,
	chroma.Error:           chErrorStyle,
}

// styleFor resolves a token type to a style, falling back through chroma's own
// hierarchy: the exact type, then its sub-category (LiteralStringDouble → LiteralString),
// then its category (NameVariableGlobal → Name). A miss is the zero Style, which the
// editor renders unstyled.
func styleFor(tt chroma.TokenType) lipgloss.Style {
	for _, t := range []chroma.TokenType{tt, tt.SubCategory(), tt.Category()} {
		if st, ok := chromaStyles[t]; ok {
			return st
		}
	}
	return lipgloss.Style{}
}

// chromaHighlighter is the editor.Highlighter chroma backs. Parse tokenizes the
// whole document and bakes per-line spans; HighlightLine is then a lookup. The lexer is
// fixed at construction (the language profile chooses it), so no per-parse detection.
type chromaHighlighter struct {
	lexer   chroma.Lexer
	lines   [][]editor.Span
	restart []int // per row, a nearby root-like opener for provisional fragment parses
}

var _ editor.Highlighter = (*chromaHighlighter)(nil)
var _ editor.HighlightRestartProvider = (*chromaHighlighter)(nil)

func chromaHighlighterFactory(lexer chroma.Lexer) func() editor.Highlighter {
	lexer = chroma.Coalesce(lexer)
	return func() editor.Highlighter { return &chromaHighlighter{lexer: lexer} }
}

// registerPatchedGDScript repairs a defect in Chroma's own GDScript lexer, in place, by
// re-registering a patched copy under the same name. The lexer's `classname` state — the
// one `extends` pushes — accepts a bare identifier and nothing else, so the path form
//
//	extends "res://my_file.gd"
//
// dead-ends on the opening quote: Chroma emits a one-rune Error for it and stays in the
// state, `res` then satisfies the identifier rule and pops, and the CLOSING quote is what
// finally opens a string — swallowing the rest of the document, comments included, until
// some later quote happens to close it. Adding the quoted form to the state fixes it at
// the source, so both the editor buffer and a fenced preview block inherit the fix from
// the one registry lookup each already does.
//
// This is upstream's bug, not a policy of gote's: gdscript.xml has been untouched since
// 2023 and the open Godot-4.7 PR does not go near this state. If a future Chroma
// restructures `classname` the patch stops applying cleanly and the tests say so; the
// right response then is to delete this function.
func registerPatchedGDScript() {
	base, ok := lexers.Get("gdscript").(*chroma.RegexLexer)
	if !ok {
		return // upstream changed shape; the stock lexer is still better than none
	}
	rules, err := base.Rules()
	if err != nil {
		return
	}
	// Clone before touching it: Rules hands back the registry lexer's own map.
	rules = rules.Clone()
	// Prepended, so `extends Node` still reaches the identifier rule below. Both patterns
	// consume at least one rune — a zero-width "pop on anything" guard would leave the
	// lexer's position unmoved and spin forever — and both stop at a newline, which leaves
	// an unterminated quote on Chroma's existing error path, where hitting the '\n' resets
	// the stack to root and keeps the damage on one line.
	rules["classname"] = append([]chroma.Rule{
		{Pattern: `"(?:\\.|[^"\\\n])*"`, Type: chroma.LiteralStringDouble, Mutator: chroma.Pop(1)},
		{Pattern: `'(?:\\.|[^'\\\n])*'`, Type: chroma.LiteralStringSingle, Mutator: chroma.Pop(1)},
	}, rules["classname"]...)

	// Chroma types only the engine classes it ships a list of, so a project's own classes,
	// autoload singletons and enum type names fall through to root's `[a-zA-Z_]\w*`
	// catch-all and render unstyled. This rule claims them for the same NameClass the
	// engine list uses — a user class is a type, and reads as one.
	//
	// The lowercase in the middle is what separates PascalCase from CONSTANT_CASE: MyClass,
	// Node2D and HTTPManager match, MAX_SPEED, AABB and a lone X do not. The trailing \w*
	// is greedy, so a match always spans the whole identifier rather than a prefix. The
	// lookbehind blocks a mid-identifier match but deliberately allows one after '.', so
	// Game.PlayerState colors — which is how the engine list already behaves for Foo.Node.
	//
	// It goes ahead of root's call rule so `MyClass.new()` and `MyClass(…)` read as a type
	// rather than a function, matching `Vector2(1, 2)`, whose engine-list rule already
	// outranks that call rule. Everything above the anchor — keywords, annotations,
	// operators, the class/extends rules, the engine types, builtins, numbers — still wins.
	const callRule = `(\b[a-zA-Z_]\w*)([(])`
	for i, rule := range rules["root"] {
		if rule.Pattern != callRule {
			continue
		}
		root := make([]chroma.Rule, 0, len(rules["root"])+1)
		root = append(root, rules["root"][:i]...)
		root = append(root, chroma.Rule{Pattern: `(?<!\w)[A-Z]\w*[a-z]\w*`, Type: chroma.NameClass})
		rules["root"] = append(root, rules["root"][i:]...)
		break
	}

	lexers.Register(chroma.MustNewLexer(base.Config(), func() chroma.Rules { return rules }))
}

// Parse tokenizes doc and splits the token stream into per-line spans. Tokens cross line
// boundaries — a block comment is one token, a string may contain newlines — so each
// token's Value is cut on '\n' and its pieces distributed, which is what turns chroma's
// flat stream into the row-addressed answer the editor asks for.
//
// A tokenizer error leaves lines nil: HighlightLine then answers nothing for every row
// and the buffer renders plain, the same as a profile with no highlighter.
func (h *chromaHighlighter) Parse(doc string) {
	h.lines = nil
	h.restart = nil
	if h.lexer == nil {
		return
	}
	iter, err := h.lexer.Tokenise(nil, doc)
	if err != nil {
		return
	}
	// One row per source line up front, so a token that touches no line (and a document
	// whose stream ends early) still leaves the rows addressable.
	h.lines = make([][]editor.Span, strings.Count(doc, "\n")+1)
	h.restart = make([]int, len(h.lines))
	for row := range h.restart {
		h.restart[row] = row
	}
	row := 0
	family, familyStart := 0, 0
	for _, tok := range iter.Tokens() {
		nextFamily := chromaRestartFamily(tok.Type)
		if nextFamily == 0 || nextFamily != family {
			familyStart = row
		}
		family = nextFamily
		style := styleFor(tok.Type)
		parts := strings.Split(tok.Value, "\n")
		for i, part := range parts {
			if i > 0 {
				// Every '\n' in the token closes the current row. Some lexers append a
				// trailing newline of their own (Config.EnsureNL), so this can walk one
				// row past the buffer — the append below is guarded for it.
				row++
				if family != 0 && row < len(h.restart) {
					h.restart[row] = familyStart
				}
			}
			if part == "" || row >= len(h.lines) {
				continue
			}
			h.lines[row] = append(h.lines[row], editor.Span{Text: part, Style: style})
		}
	}
}

// chromaRestartFamily joins adjacent token leaves that are still part of one string or
// comment. Coalesce already handles identical leaves; grouping their token hierarchy as
// well keeps escapes/interpolation attached to the opening delimiter in common lexers.
func chromaRestartFamily(tt chroma.TokenType) int {
	if tt.InSubCategory(chroma.LiteralString) {
		return 1
	}
	if tt.InCategory(chroma.Comment) {
		return 2
	}
	return 0
}

// HighlightLine returns the baked spans for row, or nil when the row is outside what was
// parsed (the editor renders those plain).
func (h *chromaHighlighter) HighlightLine(row int) []editor.Span {
	if row < 0 || row >= len(h.lines) {
		return nil
	}
	return h.lines[row]
}

func (h *chromaHighlighter) HighlightRestartLine(row int) int {
	if row < 0 || row >= len(h.restart) {
		return row
	}
	return h.restart[row]
}

// chromaExts are the extensions gote hands to Chroma. A curated list rather than every
// filename pattern Chroma knows: these are what a scan-mode gote actually opens, and
// adding a profile is one line. Anything not listed keeps rendering plain.
//
// Two exclusions are deliberate. ".md"/".markdown" use gote's structural Markdown
// highlighter. Files Chroma matches by whole name rather than extension — Makefile,
// Dockerfile — currently resolve to literal editing because this seam is path-extension
// based; they still list and edit normally.
var chromaExts = []string{
	".go", ".py", ".rb", ".rs", ".java", ".lua", ".php", ".pl", ".r",
	".js", ".jsx", ".ts", ".tsx",
	".c", ".h", ".cc", ".cpp", ".hpp", ".hh", ".cs", ".kt", ".swift", ".dart",
	".sh", ".bash", ".zsh", ".fish", ".vim",
	".json", ".yaml", ".yml", ".toml", ".ini", ".xml", ".csv",
	".html", ".css", ".scss", ".sql", ".diff", ".patch",
	".tf", ".gradle", ".proto", ".mk", ".gd", ".glsl",
}
