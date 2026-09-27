package app

import (
	"strings"

	"github.com/brohd11/bubblestack/components/editor"

	"charm.land/lipgloss/v2"
	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/lexers"
)

// Syntax coloring for source files, here rather than in bubblestack because
// languageForPath is gote's language authority (bubblestack only gets a Highlighter
// factory). Markdown has its own goldmark highlighter. Chroma suits the editor because it
// is lossless: token values concatenate back to the input, as editor.Span requires. Its
// ANSI formatters are not used.

// The palette, written from Config.SyntaxColors (see palette.go). Only colors are
// configurable; bold and italic mark structure (a keyword is emphatic whatever its color).
var (
	chKeywordStyle  lipgloss.Style
	chTypeStyle     lipgloss.Style
	chFuncStyle     lipgloss.Style
	chStringStyle   lipgloss.Style
	chNumberStyle   lipgloss.Style
	chCommentStyle  lipgloss.Style
	chOperatorStyle lipgloss.Style
	chInsertedStyle lipgloss.Style
	chDeletedStyle  lipgloss.Style
	chErrorStyle    lipgloss.Style
	chBracketStyles []*lipgloss.Style
	chErrorStylePtr *lipgloss.Style
)

// chromaStyles maps coarse token types to styles; styleFor walks leaf types up to them, and
// anything else is unstyled (plain text, punctuation, whitespace). It holds pointers, and
// applyChromaPalette allocates fresh ones on every rebuild, so spans baked earlier keep
// their palette. NameBuiltin is styled as a function: builtins like append are called.
var chromaStyles map[chroma.TokenType]*lipgloss.Style

// applyChromaPalette rebuilds the styles from p, and chromaStyles with fresh pointers.
func applyChromaPalette(p syntaxPalette) {
	chKeywordStyle = lipgloss.NewStyle().Foreground(p.keyword).Bold(true)
	chTypeStyle = lipgloss.NewStyle().Foreground(p.typ)
	chFuncStyle = lipgloss.NewStyle().Foreground(p.fn)
	chStringStyle = lipgloss.NewStyle().Foreground(p.str)
	chNumberStyle = lipgloss.NewStyle().Foreground(p.num)
	chCommentStyle = lipgloss.NewStyle().Foreground(p.comment).Italic(true)
	chOperatorStyle = lipgloss.NewStyle().Foreground(p.operator)
	chInsertedStyle = lipgloss.NewStyle().Foreground(p.inserted)
	chDeletedStyle = lipgloss.NewStyle().Foreground(p.deleted)
	chErrorStyle = lipgloss.NewStyle().Foreground(p.err).Bold(true)
	chBracketStyles = make([]*lipgloss.Style, len(p.brackets))
	for i, color := range p.brackets {
		chBracketStyles[i] = styleRef(lipgloss.NewStyle().Foreground(color))
	}

	chKeywordStyleRef := styleRef(chKeywordStyle)
	chTypeStyleRef := styleRef(chTypeStyle)
	chFuncStyleRef := styleRef(chFuncStyle)
	chStringStyleRef := styleRef(chStringStyle)
	chNumberStyleRef := styleRef(chNumberStyle)
	chCommentStyleRef := styleRef(chCommentStyle)
	chOperatorStyleRef := styleRef(chOperatorStyle)
	chInsertedStyleRef := styleRef(chInsertedStyle)
	chDeletedStyleRef := styleRef(chDeletedStyle)
	chErrorStyleRef := styleRef(chErrorStyle)
	chErrorStylePtr = chErrorStyleRef

	chromaStyles = map[chroma.TokenType]*lipgloss.Style{
		chroma.Keyword:         chKeywordStyleRef,
		chroma.KeywordType:     chTypeStyleRef,
		chroma.NameClass:       chTypeStyleRef,
		chroma.NameBuiltin:     chFuncStyleRef,
		chroma.NameFunction:    chFuncStyleRef,
		chroma.NameDecorator:   chFuncStyleRef,
		chroma.NameTag:         chKeywordStyleRef,
		chroma.NameAttribute:   chFuncStyleRef,
		chroma.LiteralString:   chStringStyleRef,
		chroma.LiteralNumber:   chNumberStyleRef,
		chroma.Comment:         chCommentStyleRef,
		chroma.CommentPreproc:  chKeywordStyleRef,
		chroma.Operator:        chOperatorStyleRef,
		chroma.GenericInserted: chInsertedStyleRef,
		chroma.GenericDeleted:  chDeletedStyleRef,
		chroma.Error:           chErrorStyleRef,
	}
}

// styleRef is one palette entry: a pointer to its own copy, so the table can be replaced
// wholesale without writing through to spans that already reference it.
func styleRef(st lipgloss.Style) *lipgloss.Style { return &st }

// styleFor resolves a token type through chroma's hierarchy (exact type, sub-category,
// category); nil means unstyled, which lets the render skip lipgloss. Not memoized: Parse
// runs on both the UI and a background goroutine.
func styleFor(tt chroma.TokenType) *lipgloss.Style {
	for _, t := range []chroma.TokenType{tt, tt.SubCategory(), tt.Category()} {
		if st, ok := chromaStyles[t]; ok {
			return st
		}
	}
	return nil
}

// chromaHighlighter backs editor.Highlighter with chroma: Parse tokenizes the document
// into per-line spans, and HighlightLine looks them up. The lexer is fixed by the
// language profile.
type chromaHighlighter struct {
	lexer       chroma.Lexer
	lines       [][]editor.Span
	restart     []int // per row, a nearby root-like opener for provisional fragment parses
	bracketSeed *bracketFrame
	bracketAt   []*bracketFrame // immutable stack at the beginning of each parsed row
	fragment    bool            // a bounded preview: EOF cannot prove an opener unmatched
}

var _ editor.Highlighter = (*chromaHighlighter)(nil)
var _ editor.HighlightRestartProvider = (*chromaHighlighter)(nil)
var _ editor.HighlightPreviewProvider = (*chromaHighlighter)(nil)

type bracketPos struct {
	row, span int
}

// bracketFrame is a persistent stack node, so per-row checkpoints share one chain.
type bracketFrame struct {
	close  rune
	depth  int
	prev   *bracketFrame
	opener bracketPos
	local  bool // opener belongs to this parse rather than a preview seed
}

func chromaHighlighterFactory(lexer chroma.Lexer) func() editor.Highlighter {
	lexer = chroma.Coalesce(lexer)
	return func() editor.Highlighter { return &chromaHighlighter{lexer: lexer} }
}

// NewHighlightPreview returns an independent highlighter seeded with the bracket stack at
// snapshotLine; lexical state still comes from the editor's restart row.
func (h *chromaHighlighter) NewHighlightPreview(snapshotLine int) editor.Highlighter {
	var seed *bracketFrame
	if snapshotLine >= 0 && snapshotLine < len(h.bracketAt) {
		seed = h.bracketAt[snapshotLine]
	}
	return &chromaHighlighter{lexer: h.lexer, bracketSeed: seed, fragment: true}
}

// registerPatchedGDScript fixes Chroma's GDScript lexer, re-registered under the same
// name: its `classname` state (after `extends`) accepts only an identifier, so
// `extends "res://my_file.gd"` opens a string at the closing quote and swallows the rest
// of the file. If a future Chroma changes `classname`, the tests fail and this function
// should be deleted.
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
	// Prepended so `extends Node` still reaches the identifier rule. Both patterns consume at
	// least one rune (a zero-width rule would loop) and stop at a newline, where Chroma's
	// error path resets an unterminated quote.
	rules["classname"] = append([]chroma.Rule{
		{Pattern: `"(?:\\.|[^"\\\n])*"`, Type: chroma.LiteralStringDouble, Mutator: chroma.Pop(1)},
		{Pattern: `'(?:\\.|[^'\\\n])*'`, Type: chroma.LiteralStringSingle, Mutator: chroma.Pop(1)},
	}, rules["classname"]...)

	// Chroma only types the engine classes it lists, so user classes, autoloads and enums
	// render plain. This rule styles PascalCase identifiers (a lowercase letter inside
	// separates them from CONSTANT_CASE) as NameClass, including after "." (Game.PlayerState),
	// and runs before the call rule so MyClass.new() reads as a type.
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

// Parse tokenizes doc and splits tokens across lines into per-row spans. A tokenizer error
// leaves no spans, and the buffer renders plain.
func (h *chromaHighlighter) Parse(doc string) {
	h.lines = nil
	h.restart = nil
	h.bracketAt = nil
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
	h.bracketAt = make([]*bracketFrame, len(h.lines))
	for row := range h.restart {
		h.restart[row] = row
	}
	row := 0
	stack := h.bracketSeed
	h.bracketAt[0] = stack
	family, familyStart := 0, 0
	isYAML := h.lexer.Config().Name == "YAML"
	var yamlFlow []yamlFlowFrame
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
				// Each '\n' closes a row; some lexers add a trailing newline, so the row can run one past
				// the buffer (the append below guards for it).
				row++
				if row < len(h.bracketAt) {
					h.bracketAt[row] = stack
				}
				if family != 0 && row < len(h.restart) {
					h.restart[row] = familyStart
				}
				if len(yamlFlow) > 0 && row < len(h.restart) {
					h.restart[row] = min(h.restart[row], yamlFlow[0].row)
				}
			}
			if part == "" || row >= len(h.lines) {
				continue
			}
			if isYAML && tok.Type == chroma.Punctuation {
				yamlFlow = advanceYAMLFlow(yamlFlow, row, part)
			}
			if tok.Type == chroma.Punctuation && len(chBracketStyles) > 0 {
				h.appendRainbowPart(row, part, style, &stack)
			} else {
				h.lines[row] = append(h.lines[row], editor.Span{Text: part, Style: style})
			}
		}
	}
	if !h.fragment {
		for frame := stack; frame != nil; frame = frame.prev {
			pos := frame.opener
			if frame.local && pos.row >= 0 && pos.row < len(h.lines) &&
				pos.span >= 0 && pos.span < len(h.lines[pos.row]) {
				h.lines[pos.row][pos.span].Style = chErrorStylePtr
			}
		}
	}
}

func (h *chromaHighlighter) appendRainbowPart(row int, part string, base *lipgloss.Style, stack **bracketFrame) {
	from := 0
	for at, r := range part {
		close, opens := rainbowCloser(r)
		closes := r == ')' || r == ']' || r == '}'
		if !opens && !closes {
			continue
		}
		if at > from {
			h.lines[row] = append(h.lines[row], editor.Span{Text: part[from:at], Style: base})
		}
		style := chErrorStylePtr
		if opens {
			depth := 0
			if *stack != nil {
				depth = (*stack).depth + 1
			}
			style = chBracketStyles[depth%len(chBracketStyles)]
			span := len(h.lines[row])
			*stack = &bracketFrame{
				close: close, depth: depth, prev: *stack,
				opener: bracketPos{row: row, span: span}, local: true,
			}
		} else if *stack != nil && (*stack).close == r {
			style = chBracketStyles[(*stack).depth%len(chBracketStyles)]
			*stack = (*stack).prev
		}
		h.lines[row] = append(h.lines[row], editor.Span{Text: string(r), Style: style})
		from = at + 1 // rainbow delimiters are ASCII, so one byte advances past r
	}
	if from < len(part) {
		h.lines[row] = append(h.lines[row], editor.Span{Text: part[from:], Style: base})
	}
}

func rainbowCloser(r rune) (rune, bool) {
	switch r {
	case '(':
		return ')', true
	case '[':
		return ']', true
	case '{':
		return '}', true
	default:
		return 0, false
	}
}

// chromaRestartFamily groups adjacent leaves of one string or comment, keeping escapes and
// interpolation with the opening delimiter.
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

func spansPlainText(spans []editor.Span) string {
	var b strings.Builder
	for _, span := range spans {
		b.WriteString(span.Text)
	}
	return b.String()
}

func (h *chromaHighlighter) HighlightRestartLine(row int) int {
	if row < 0 || row >= len(h.restart) {
		return row
	}
	return h.restart[row]
}

// chromaExts are the extensions gote hands to Chroma; anything else renders plain. Markdown
// uses gote's own highlighter, and whole-name files (Makefile, Dockerfile) are not matched
// by extension, so they edit literally.
var chromaExts = []string{
	".go", ".py", ".rb", ".rs", ".java", ".lua", ".php", ".pl", ".r",
	".js", ".jsx", ".ts", ".tsx",
	".c", ".h", ".cc", ".cpp", ".hpp", ".hh", ".cs", ".kt", ".swift", ".dart",
	".sh", ".bash", ".zsh", ".fish", ".vim",
	".json", ".yaml", ".yml", ".toml", ".ini", ".xml", ".csv",
	".html", ".css", ".scss", ".sql", ".diff", ".patch",
	".tf", ".gradle", ".proto", ".mk", ".gd", ".glsl",
}
