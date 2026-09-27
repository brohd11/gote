package app

import (
	"strings"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/lexers"
)

// registerPatchedYAML repairs Chroma 2.24.1's flow collections: its block plain-scalar
// rule consumes everything through end of line, including the closing bracket in
// [uses;100]. Keep that rule for block plain scalars (e.g. ${area_check}), but use separate
// states inside collections. This can be removed when upstream handles these cases.
func registerPatchedYAML() {
	base, ok := lexers.Get("yaml").(*chroma.RegexLexer)
	if !ok {
		return
	}
	rules, err := base.Rules()
	if err != nil {
		return
	}
	rules = rules.Clone()
	openers := []chroma.Rule{
		{Pattern: `\[`, Type: chroma.Punctuation, Mutator: chroma.Push("flow-sequence")},
		{Pattern: `\{`, Type: chroma.Punctuation, Mutator: chroma.Push("flow-mapping")},
	}
	rules["root"] = append(openers, rules["root"]...)
	for i, rule := range rules["root"] {
		if rule.Pattern == `[?:,\[\]]` {
			rules["root"][i].Pattern = `[?:,\[\]{}]`
		}
	}
	const doubleQuoted = `"(?:\\[\s\S]|[^"\\])*"`
	const singleQuoted = `'(?:''|[^'])*'`
	// Stop block keys at their first separator: the upstream greedy rule can swallow
	// "x: [name:" in "x: [name: value]" before reaching the collection's opener.
	// Brackets within a block key itself (e.g. items[0]:) are still literal text.
	for i, rule := range rules["key"] {
		rules["key"][i].Pattern = strings.ReplaceAll(rule.Pattern, `[^"\n{]*`, `[^"\n{]*?`)
	}
	rules["key"] = append([]chroma.Rule{
		{Pattern: `(` + doubleQuoted + `|` + singleQuoted + `)([ \t]*)(:)(?=\s|$)`, Type: chroma.ByGroups(chroma.NameTag, chroma.TextWhitespace, chroma.Punctuation)},
	}, rules["key"]...)

	// A colon without separation and a hash without preceding whitespace are plain
	// text (URLs and fragments). Flow separators always end a plain scalar.
	const plain = `(?:[^\s\[\]{},:#"']|:(?![\s\[\]{},]))(?:[^\[\]{},:#\r\n]|:(?![\s\[\]{},])|(?<!\s)#)*`
	flow := []chroma.Rule{
		chroma.Include("whitespace"),
		{Pattern: `#[^\r\n]*`, Type: chroma.Comment},
		{Pattern: `(?:!!?|&|\*)[^\s\[\]{},]+`, Type: chroma.CommentPreproc},
		{Pattern: `(` + doubleQuoted + `|` + singleQuoted + `)([ \t]*)(:)`, Type: chroma.ByGroups(chroma.NameTag, chroma.TextWhitespace, chroma.Punctuation)},
		{Pattern: `(` + plain + `)(:)(?=[\s\[\]{},]|$)`, Type: chroma.ByGroups(chroma.NameTag, chroma.Punctuation)},
		{Pattern: doubleQuoted, Type: chroma.LiteralStringDouble},
		{Pattern: singleQuoted, Type: chroma.LiteralStringSingle},
	}
	flow = append(flow, openers...)
	flow = append(flow, chroma.Rule{Pattern: `[,:?\]}]`, Type: chroma.Punctuation})
	// Retain upstream number/date/constant styling, but require a flow boundary so a
	// prefix such as "123" in "123#tag" cannot turn the rest into a comment.
	for _, rule := range rules["value"] {
		switch rule.Type {
		case chroma.KeywordConstant, chroma.LiteralDate, chroma.LiteralNumber:
			rule.Pattern = `(?:` + rule.Pattern + `)(?=[\s,\]}]|$)`
			flow = append(flow, rule)
		}
	}
	flow = append(flow,
		chroma.Rule{Pattern: plain, Type: chroma.Literal},
		chroma.Rule{Pattern: `.`, Type: chroma.Text},
	)
	// Only the matching closer pops lexical state. The rainbow stack independently
	// marks mismatches, and incomplete collections remain highlightable during edits.
	rules["flow-sequence"] = append([]chroma.Rule{
		{Pattern: `\]`, Type: chroma.Punctuation, Mutator: chroma.Pop(1)},
	}, flow...)
	rules["flow-mapping"] = append([]chroma.Rule{
		{Pattern: `\}`, Type: chroma.Punctuation, Mutator: chroma.Pop(1)},
	}, flow...)
	lexers.Register(chroma.MustNewLexer(base.Config(), func() chroma.Rules { return rules }))
}

// YAML previews restart at the outer flow opener, since bracket seeds alone do not
// restore the lexer's collection state. Track this even with rainbow colors disabled.
type yamlFlowFrame struct {
	row   int
	close rune
}

func advanceYAMLFlow(stack []yamlFlowFrame, row int, punctuation string) []yamlFlowFrame {
	for _, r := range punctuation {
		switch r {
		case '[', '{':
			close, _ := rainbowCloser(r)
			stack = append(stack, yamlFlowFrame{row: row, close: close})
		case ']', '}':
			if len(stack) > 0 && stack[len(stack)-1].close == r {
				stack = stack[:len(stack)-1]
			}
		}
	}
	return stack
}
