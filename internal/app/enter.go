package app

import (
	"strconv"
	"strings"

	"github.com/brohd11/bubblestack/components/editor"
)

// Enter handlers: the structured newline for each language profile (list continuation,
// block indentation). Each returns false to fall back to plain indent carrying.

func afterLeadingIndent(ctx editor.EnterContext) bool {
	return strings.HasPrefix(ctx.Before, ctx.LeadingIndent)
}

// markdownItem is a list or blockquote line taken apart. next builds the marker the
// FOLLOWING item gets, which is the only thing an ordered list does differently.
type markdownItem struct {
	indent string // the line's leading whitespace
	marker string // "-", "+", "*", "1.", "2)", or ">"
	gap    string // the spaces between marker and content
	task   bool   // the content opens with a "[ ]" or "[x]" checkbox
	text   string // the item's own words, the checkbox excluded
}

// lead is the item's opening for marker: the marker, its gap, and an unchecked box for a
// task item.
func (it markdownItem) lead(marker string) string {
	out := marker + it.gap
	if it.task {
		out += "[ ] "
	}
	return out
}

// next is the opening the FOLLOWING item gets. Advancing the number is the only thing an
// ordered list does differently.
func (it markdownItem) next() string {
	if n, delim, ok := splitOrderedMarker(it.marker); ok {
		return it.lead(strconv.Itoa(n+1) + delim)
	}
	return it.lead(it.marker)
}

// markdownEnter continues a list or quote, and ends it on an empty item: a nested empty
// item steps out a level, an outer one clears its line. It uses the same markers as the
// highlighter's listMarkerEnd.
func markdownEnter(ctx editor.EnterContext) (editor.EnterAction, bool) {
	if !afterLeadingIndent(ctx) {
		return editor.EnterAction{}, false
	}
	item, ok := parseMarkdownItem(ctx.Before, ctx.LeadingIndent)
	if !ok {
		return editor.EnterAction{}, false
	}
	if item.text == "" && strings.TrimSpace(ctx.After) == "" {
		if out := dropIndentUnit(item.indent, ctx.IndentUnit); out != item.indent {
			// The marker is carried verbatim: renderers renumber ordered lists anyway.
			return editor.EnterAction{Rewrite: true, Line: out + item.lead(item.marker)}, true
		}
		return editor.EnterAction{Rewrite: true, Line: ""}, true
	}
	return editor.EnterAction{Prefix: item.indent + item.next()}, true
}

func parseMarkdownItem(before, indent string) (markdownItem, bool) {
	rest := strings.TrimPrefix(before, indent)
	marker := markdownMarker(rest)
	if marker == "" {
		return markdownItem{}, false
	}
	it := markdownItem{indent: indent, marker: marker}
	rest = rest[len(marker):]
	for len(rest) > 0 && rest[0] == ' ' {
		it.gap += " "
		rest = rest[1:]
	}
	// A bullet or number needs a space to be a marker at all; "-foo" is a word. A
	// blockquote does not: ">quoted" is as valid as "> quoted".
	if it.gap == "" && marker != ">" && rest != "" {
		return markdownItem{}, false
	}
	if marker != ">" {
		if box := markdownTaskBox(rest); box > 0 {
			it.task, rest = true, strings.TrimLeft(rest[box:], " ")
		}
	}
	it.text = strings.TrimSpace(rest)
	return it, true
}

// markdownMarker returns the marker at the head of rest (a bullet, digits closed by "." or
// ")", or ">"), or "": listMarkerEnd's rule over one line.
func markdownMarker(rest string) string {
	if rest == "" {
		return ""
	}
	switch rest[0] {
	case '-', '+', '*', '>':
		return rest[:1]
	}
	i := 0
	for i < len(rest) && rest[i] >= '0' && rest[i] <= '9' {
		i++
	}
	if i > 0 && i < len(rest) && (rest[i] == '.' || rest[i] == ')') {
		return rest[:i+1]
	}
	return ""
}

// splitOrderedMarker takes "12." apart into its number and its delimiter. A bullet or
// blockquote answers false and is repeated verbatim.
func splitOrderedMarker(marker string) (int, string, bool) {
	if len(marker) < 2 {
		return 0, "", false
	}
	n, err := strconv.Atoi(marker[:len(marker)-1])
	if err != nil {
		return 0, "", false
	}
	return n, marker[len(marker)-1:], true
}

// markdownTaskBox is the width of a leading "[ ]"/"[x]" checkbox, or 0 for an ordinary
// item. The box has to be followed by a space or end the line — "[x]y" is text.
func markdownTaskBox(rest string) int {
	if len(rest) < 3 || rest[0] != '[' || rest[2] != ']' {
		return 0
	}
	switch rest[1] {
	case ' ', 'x', 'X':
	default:
		return 0
	}
	if len(rest) > 3 && rest[3] != ' ' {
		return 0
	}
	return 3
}

// yamlEnter carries indentation, opens a level after a key or block scalar, and continues
// a sequence marker. A key under `- name:` hangs off the entry's content column, so
// nesting measures from base, not the raw indent.
func yamlEnter(ctx editor.EnterContext) (editor.EnterAction, bool) {
	if !afterLeadingIndent(ctx) {
		return editor.EnterAction{}, false
	}
	rest := strings.TrimPrefix(ctx.Before, ctx.LeadingIndent)
	base, marker := ctx.LeadingIndent, ""
	if width := yamlMarkerWidth(rest); width > 0 {
		marker, rest = rest[:width], rest[width:]
		base += strings.Repeat(" ", width)
	}
	content := strings.TrimSpace(rest)
	switch {
	case yamlOpensBlock(content):
		return editor.EnterAction{Prefix: base + ctx.IndentUnit}, true
	case marker != "" && content != "":
		return editor.EnterAction{Prefix: ctx.LeadingIndent + marker}, true
	}
	return editor.EnterAction{Prefix: base}, true
}

// yamlMarkerWidth is the width of a leading "- " marker (a bare dash counts), or 0.
func yamlMarkerWidth(rest string) int {
	if !strings.HasPrefix(rest, "-") {
		return 0
	}
	width := 1
	for width < len(rest) && rest[width] == ' ' {
		width++
	}
	if width == 1 && rest != "-" {
		return 0 // "-name" is a scalar, not a sequence entry
	}
	return width
}

// yamlOpensBlock reports a mapping key with no inline value, or a block scalar header ("|"
// or ">" with indicators).
func yamlOpensBlock(content string) bool {
	if strings.HasSuffix(content, ":") {
		return true
	}
	i := strings.LastIndex(content, ": ")
	if i < 0 {
		return false
	}
	value := strings.TrimSpace(content[i+2:])
	return value != "" && (value[0] == '|' || value[0] == '>')
}

// blockBrackets are the pairs that open a block when Enter lands between them (quotes do
// not). Adjacency is the test, as in the editor's deleteEmptyAutoPair.
var blockBrackets = []struct{ open, close string }{{"(", ")"}, {"[", "]"}, {"{", "}"}}

// insideBracket reports whether the caret sits directly between a bracket pair — the
// `{|}` an auto-closing pair leaves behind.
func insideBracket(before, after string) bool {
	for _, pair := range blockBrackets {
		if strings.HasSuffix(before, pair.open) && strings.HasPrefix(after, pair.close) {
			return true
		}
	}
	return false
}

// endsWithOpener reports a bracket left open at line end: indent, but nothing to push
// down.
func endsWithOpener(line string) bool {
	for _, pair := range blockBrackets {
		if strings.HasSuffix(line, pair.open) {
			return true
		}
	}
	return false
}

// bracketBlock moves the closer to its own line and puts the caret on an indented line
// between.
func bracketBlock(ctx editor.EnterContext) editor.EnterAction {
	return editor.EnterAction{
		Prefix: ctx.LeadingIndent + ctx.IndentUnit,
		Block:  true,
		Closer: ctx.LeadingIndent,
	}
}

// blockEndStatements are the statements nothing can follow inside their own block, so the
// line after one steps back out. Python and GDScript agree on every word here.
var blockEndStatements = map[string]bool{
	"pass": true, "break": true, "continue": true, "return": true, "raise": true,
}

// colonBlockEnter serves Python and GDScript (only their indent unit differs, which
// IndentUnit carries).
func colonBlockEnter(ctx editor.EnterContext) (editor.EnterAction, bool) {
	if !afterLeadingIndent(ctx) {
		return editor.EnterAction{}, false
	}
	if insideBracket(ctx.Before, ctx.After) {
		return bracketBlock(ctx), true
	}
	trimmed := strings.TrimSpace(ctx.Before)
	switch {
	case strings.HasSuffix(trimmed, ":"), strings.HasSuffix(trimmed, "\\"), endsWithOpener(trimmed):
		return editor.EnterAction{Prefix: ctx.LeadingIndent + ctx.IndentUnit}, true
	case blockEndStatements[firstWord(trimmed)]:
		return editor.EnterAction{Prefix: dropIndentUnit(ctx.LeadingIndent, ctx.IndentUnit)}, true
	}
	return editor.EnterAction{Prefix: ctx.LeadingIndent}, true
}

// braceBlockEnter serves the braceIndent languages: colonBlockEnter without the dedent
// after return/pass, since the closing brace is already below the caret. A trailing colon
// covers case labels and JS/TS object keys.
func braceBlockEnter(ctx editor.EnterContext) (editor.EnterAction, bool) {
	if !afterLeadingIndent(ctx) {
		return editor.EnterAction{}, false
	}
	if insideBracket(ctx.Before, ctx.After) {
		return bracketBlock(ctx), true
	}
	trimmed := strings.TrimSpace(ctx.Before)
	// A trailing colon in Go is a switch case, a default, or a label — each of which opens
	// a body one level in.
	if endsWithOpener(trimmed) || strings.HasSuffix(trimmed, ":") {
		return editor.EnterAction{Prefix: ctx.LeadingIndent + ctx.IndentUnit}, true
	}
	return editor.EnterAction{Prefix: ctx.LeadingIndent}, true
}

// firstWord is the line's leading whitespace-delimited token, or "" for a blank line.
func firstWord(line string) string {
	if fields := strings.Fields(line); len(fields) > 0 {
		return fields[0]
	}
	return ""
}

// dropIndentUnit removes one trailing indent level (a tab, else up to a unit of spaces),
// matching the editor's alt+, dedent.
func dropIndentUnit(indent, unit string) string {
	if strings.HasSuffix(indent, "\t") {
		return indent[:len(indent)-1]
	}
	trimmed := strings.TrimRight(indent, " ")
	drop := len(indent) - len(trimmed)
	if n := len(unit); drop > n {
		drop = n
	}
	return indent[:len(indent)-drop]
}

// shellBranchEnd holds case-branch terminators. Only a line that is just a terminator
// dedents: `--check) ;;` is already at its pattern's level.
var shellBranchEnd = map[string]bool{";;": true, ";&": true, ";;&": true}

// shellEnter carries indentation, indents after a block opener and dedents after a case
// branch ends. Closers need no rule: they already sit where the next line belongs.
func shellEnter(ctx editor.EnterContext) (editor.EnterAction, bool) {
	if !afterLeadingIndent(ctx) {
		return editor.EnterAction{}, false
	}
	trimmed := strings.TrimSpace(ctx.Before)
	switch {
	case shellBranchEnd[trimmed]:
		return editor.EnterAction{Prefix: dropIndentUnit(ctx.LeadingIndent, ctx.IndentUnit)}, true
	case shellOpensBlock(trimmed):
		return editor.EnterAction{Prefix: ctx.LeadingIndent + ctx.IndentUnit}, true
	}
	return editor.EnterAction{Prefix: ctx.LeadingIndent}, true
}

// shellOpensBlock checks the line's last word without parsing (no comment or quote
// handling): `echo then` indents, but the common shapes work.
func shellOpensBlock(line string) bool {
	if strings.HasSuffix(line, "\\") || strings.HasSuffix(line, "{") || strings.HasSuffix(line, "(") {
		return true
	}
	fields := strings.Fields(line)
	if len(fields) == 0 {
		return false
	}
	switch fields[len(fields)-1] {
	case "then", "do", "else":
		return true
	case "in":
		// Only `case x in` opens on "in". A `for f in *` whose `do` is on the next line
		// would otherwise indent twice for one block.
		return fields[0] == "case"
	}
	// A case pattern is the one bare ")" that opens a block; a line with "(" is a
	// substitution or function header.
	return strings.HasSuffix(line, ")") && !strings.Contains(line, "(")
}

// fishBlockOpeners are fish's block keywords (the first word). `case` is left out: fish
// has no branch terminator to dedent on, so it would add a level per branch.
var fishBlockOpeners = map[string]bool{
	"begin": true, "if": true, "else": true, "for": true,
	"while": true, "function": true, "switch": true,
}

func fishEnter(ctx editor.EnterContext) (editor.EnterAction, bool) {
	if !afterLeadingIndent(ctx) {
		return editor.EnterAction{}, false
	}
	trimmed := strings.TrimSpace(ctx.Before)
	prefix := ctx.LeadingIndent
	fields := strings.Fields(trimmed)
	if strings.HasSuffix(trimmed, "\\") || (len(fields) > 0 && fishBlockOpeners[fields[0]]) {
		prefix += ctx.IndentUnit
	}
	return editor.EnterAction{Prefix: prefix}, true
}
