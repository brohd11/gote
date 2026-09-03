package app

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/brohd11/bubblestack/components"

	"github.com/alecthomas/chroma/v2/lexers"
)

// languageProfile is gote's single file-type authority. The editor consumes only the
// language-agnostic component config; id is retained here for the LSP document language
// identifier a later client will need.
type languageProfile struct {
	id     string
	editor components.EditorLanguageConfig
	lsp    *languageLSP
}

// languageLSP is Gote-owned activation metadata. Bubblestack receives only editor;
// server selection and workspace discovery stay beside the rest of the app's language
// policy.
type languageLSP struct {
	server         string
	rootMarkers    []string
	requireRootHit bool
}

var (
	codePairs = []components.EditorPair{
		{Open: '(', Close: ')'},
		{Open: '[', Close: ']'},
		{Open: '{', Close: '}'},
		// {Open: '\'', Close: '\''},
		{Open: '"', Close: '"'},
		{Open: '`', Close: '`'},
	}
	markdownSurroundPairs = append(append([]components.EditorPair{}, codePairs...),
		components.EditorPair{Open: '*', Close: '*'},
		components.EditorPair{Open: '_', Close: '_'},
	)
	yamlPairs = []components.EditorPair{
		{Open: '[', Close: ']'},
		{Open: '{', Close: '}'},
		{Open: '\'', Close: '\''},
		{Open: '"', Close: '"'},
	}
	gdscriptPairs = []components.EditorPair{
		{Open: '(', Close: ')'},
		{Open: '[', Close: ']'},
		{Open: '{', Close: '}'},
		{Open: '\'', Close: '\''},
		{Open: '"', Close: '"'},
	}
	// shellPairs adds the single quote the generic codePairs deliberately leaves out: in
	// shell a '...' is a literal string, not an apostrophe in prose. Backticks are excluded
	// the other way — legacy command substitution, where $( ) is what the parens already
	// cover, so a lone backtick is more often quoted text than an opener.
	shellPairs = []components.EditorPair{
		{Open: '(', Close: ')'},
		{Open: '[', Close: ']'},
		{Open: '{', Close: '}'},
		{Open: '\'', Close: '\''},
		{Open: '"', Close: '"'},
	}

	languageByExt = buildLanguageProfiles()
)

func buildLanguageProfiles() map[string]*languageProfile {
	profiles := make(map[string]*languageProfile, len(chromaExts)+2)
	for _, ext := range chromaExts {
		lexer := lexers.Match("file" + ext)
		if lexer == nil {
			continue
		}
		id := strings.TrimPrefix(ext, ".")
		if aliases := lexer.Config().Aliases; len(aliases) > 0 {
			id = aliases[0]
		}
		cfg := components.EditorLanguageConfig{
			NewHighlighter:   chromaHighlighterFactory(lexer),
			AutoClosingPairs: codePairs,
			SurroundingPairs: codePairs,
		}
		switch ext {
		case ".json":
			cfg.IndentSpaces = 2
		case ".py":
			cfg.IndentSpaces = 4
			cfg.OnEnter = colonBlockEnter
		case ".sh", ".bash", ".zsh":
			cfg.AutoClosingPairs = shellPairs
			cfg.SurroundingPairs = shellPairs
			cfg.IndentSpaces = 2
			cfg.OnEnter = shellEnter
		case ".fish":
			cfg.AutoClosingPairs = shellPairs
			cfg.SurroundingPairs = shellPairs
			cfg.IndentSpaces = 4
			cfg.OnEnter = fishEnter
		case ".yaml", ".yml":
			cfg.AutoClosingPairs = yamlPairs
			cfg.SurroundingPairs = yamlPairs
			cfg.IndentSpaces = 2
			cfg.OnEnter = yamlEnter
		case ".gd":
			id = "gdscript"
			cfg.AutoClosingPairs = gdscriptPairs
			cfg.SurroundingPairs = gdscriptPairs
			cfg.OnEnter = colonBlockEnter
			// Chroma has both current "gdscript" and legacy "gdscript3"
			// lexers claiming *.gd. Pick the named current lexer deliberately so
			// source buffers and ```gdscript preview fences share one tokenizer.
			if gdscript := lexers.Get("gdscript"); gdscript != nil {
				cfg.NewHighlighter = chromaHighlighterFactory(gdscript)
			}
		}
		profile := &languageProfile{id: id, editor: cfg}
		switch ext {
		case ".py":
			profile.id = "python"
			profile.lsp = &languageLSP{server: "python", rootMarkers: []string{
				"pyproject.toml", "setup.cfg", "setup.py", ".git",
			}}
		case ".zsh":
			// Chroma tokenizes zsh with its Bash lexer, so the alias this profile inherited
			// is "bash". Pin the real name: nothing reads it while zsh has no server, and a
			// profile that misreports its own language is a trap for whoever adds one.
			profile.id = "zsh"
		case ".sh", ".bash":
			// The LSP language identifier is "shellscript"; Chroma's alias for these files
			// is "bash", which is the wrong name to send a server.
			profile.id = "shellscript"
			profile.lsp = &languageLSP{server: "bash", rootMarkers: []string{
				".git", ".shellcheckrc", ".editorconfig",
			}}
		case ".gd":
			profile.lsp = &languageLSP{server: "gdscript", rootMarkers: []string{"project.godot"}, requireRootHit: true}
		}
		profiles[ext] = profile
	}

	markdown := &languageProfile{
		id: "markdown",
		editor: components.EditorLanguageConfig{
			NewHighlighter:   newMarkdownHighlighter,
			AutoClosingPairs: codePairs,
			SurroundingPairs: markdownSurroundPairs,
			IndentSpaces:     2,
			OnEnter:          markdownEnter,
		},
	}
	profiles[".md"] = markdown
	profiles[".markdown"] = markdown
	return profiles
}

// lspRootForPath finds the nearest project marker. GDScript has no useful server
// workspace without project.godot, while pylsp can still operate from the file's own
// directory when a standalone script has no project marker.
func lspRootForPath(path string, profile *languageProfile) string {
	if profile == nil || profile.lsp == nil {
		return ""
	}
	dir := filepath.Dir(path)
	for {
		for _, marker := range profile.lsp.rootMarkers {
			if _, err := os.Stat(filepath.Join(dir, marker)); err == nil {
				return dir
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	if profile.lsp.requireRootHit {
		return ""
	}
	return filepath.Dir(path)
}

// extByFilename resolves the dotfiles that carry a language with no extension of their
// own. filepath.Ext(".bashrc") is ".bashrc", so none of these can ever reach languageByExt;
// they name the extension whose profile they share instead of duplicating it.
var extByFilename = map[string]string{
	".bashrc":       ".bash",
	".bash_profile": ".bash",
	".bash_aliases": ".bash",
	".bash_logout":  ".bash",
	".profile":      ".sh",
	".zshrc":        ".zsh",
	".zshenv":       ".zsh",
	".zprofile":     ".zsh",
	".zlogin":       ".zsh",
	".zlogout":      ".zsh",
}

// extByInterpreter maps a shebang's interpreter onto the extension whose profile its files
// use. Seeded past shell on purpose: the sniff is a general seam for scripts that carry
// their language in line one rather than in their name, not a bash special case.
var extByInterpreter = map[string]string{
	"sh":      ".sh",
	"dash":    ".sh",
	"ash":     ".sh",
	"ksh":     ".sh",
	"bash":    ".bash",
	"zsh":     ".zsh",
	"fish":    ".fish",
	"python":  ".py",
	"python3": ".py",
	"node":    ".js",
	"ruby":    ".rb",
	"perl":    ".pl",
}

// shebangLimit bounds the sniff read. A shebang is line one or it is not a shebang, and a
// binary that happens to start with "#!" is not worth reading further to disprove.
const shebangLimit = 256

// sniffedLanguages memoizes shebang results per path, including the misses — a nil profile
// is a real answer here, not an empty cache slot. The memo is what makes the sniff usable
// at all: lspManager.Reconcile resolves every open buffer on every update, so an uncached
// read would be disk IO on the UI goroutine once per keystroke per document.
var sniffedLanguages sync.Map // cleaned path -> *languageProfile

// languageForPath asks the three questions in order of confidence: the extension, then the
// whole filename, then the file's own first line. Only the last one touches the disk, and
// only for a path the first two could not answer.
func languageForPath(path string) *languageProfile {
	if profile := languageByExt[strings.ToLower(filepath.Ext(path))]; profile != nil {
		return profile
	}
	if ext, ok := extByFilename[strings.ToLower(filepath.Base(path))]; ok {
		if profile := languageByExt[ext]; profile != nil {
			return profile
		}
	}
	return sniffLanguage(path)
}

func sniffLanguage(path string) *languageProfile {
	if path == "" {
		return nil
	}
	key := filepath.Clean(path)
	if cached, ok := sniffedLanguages.Load(key); ok {
		profile, _ := cached.(*languageProfile)
		return profile
	}
	profile := languageByExt[shebangExt(key)]
	sniffedLanguages.Store(key, profile)
	return profile
}

// forgetSniffedLanguage drops path's memoized first line. A save is the only way a file's
// shebang changes under gote, so that is the only place this needs calling.
func forgetSniffedLanguage(path string) {
	if path != "" {
		sniffedLanguages.Delete(filepath.Clean(path))
	}
}

// shebangExt reads line one and answers the extension its interpreter stands for. An
// unreadable path, a file with no "#!", and an interpreter nothing is registered for all
// answer "" — which languageByExt turns into literal editing, the same as an unknown
// extension does.
func shebangExt(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	buf := make([]byte, shebangLimit)
	n, _ := f.Read(buf)
	line := string(buf[:n])
	if i := strings.IndexAny(line, "\r\n"); i >= 0 {
		line = line[:i]
	}
	if !strings.HasPrefix(line, "#!") {
		return ""
	}
	fields := strings.Fields(line[2:])
	if len(fields) == 0 {
		return ""
	}
	// #!/usr/bin/env bash names the interpreter in the next field — past any of env's own
	// flags, so the -S form used to pass arguments through resolves to bash and not to -S.
	if filepath.Base(fields[0]) == "env" {
		fields = fields[1:]
		for len(fields) > 0 && strings.HasPrefix(fields[0], "-") {
			fields = fields[1:]
		}
		if len(fields) == 0 {
			return ""
		}
	}
	return extByInterpreter[strings.ToLower(filepath.Base(fields[0]))]
}

func editorLanguageForPath(path string) *components.EditorLanguageConfig {
	profile := languageForPath(path)
	if profile == nil {
		return nil
	}
	return &profile.editor
}

func afterLeadingIndent(ctx components.EditorEnterContext) bool {
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

// lead assembles the item's opening for a given marker: the marker, the gap that followed
// it, and an unchecked box when this is a task item — a finished item does not continue
// as another finished one.
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

// markdownEnter continues a list or blockquote onto the next line, and ends one when the
// item being left is empty: a nested empty item steps out a level, an empty item already
// at the outer level clears its line. Two presses to leave a nested list, which is what
// makes nesting comfortable and what stops a list continuing forever.
//
// The markers are exactly the set the highlighter's listMarkerEnd paints — the two should
// not disagree about what a list is.
func markdownEnter(ctx components.EditorEnterContext) (components.EditorEnterAction, bool) {
	if !afterLeadingIndent(ctx) {
		return components.EditorEnterAction{}, false
	}
	item, ok := parseMarkdownItem(ctx.Before, ctx.LeadingIndent)
	if !ok {
		return components.EditorEnterAction{}, false
	}
	if item.text == "" && strings.TrimSpace(ctx.After) == "" {
		if out := dropIndentUnit(item.indent, ctx.IndentUnit); out != item.indent {
			// The marker is carried out verbatim rather than advanced: this is the same
			// item moved a level left, and its number in the list it lands in cannot be
			// read off one line. Renderers renumber an ordered list from its first item
			// anyway, so the digits here are for the writer, not the output.
			return components.EditorEnterAction{Rewrite: true, Line: out + item.lead(item.marker)}, true
		}
		return components.EditorEnterAction{Rewrite: true, Line: ""}, true
	}
	return components.EditorEnterAction{Prefix: item.indent + item.next()}, true
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

// markdownMarker returns the marker at the head of rest: one bullet character, a run of
// digits closed by "." or ")", or a ">" blockquote. Anything else answers "". This is
// listMarkerEnd's rule (highlight_markdown.go), expressed over a line rather than an
// offset into the whole document.
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

// yamlEnter carries the block's indentation, opens a level after a mapping key or a block
// scalar, and continues a sequence's marker.
//
// The subtlety is the dash. A sequence entry's CONTENT column, not its marker's, is what a
// key nested under it hangs off — so `- name:` at column zero opens at column four, not
// two. Everything below the marker therefore measures from base rather than from the raw
// leading indent.
func yamlEnter(ctx components.EditorEnterContext) (components.EditorEnterAction, bool) {
	if !afterLeadingIndent(ctx) {
		return components.EditorEnterAction{}, false
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
		return components.EditorEnterAction{Prefix: base + ctx.IndentUnit}, true
	case marker != "" && content != "":
		return components.EditorEnterAction{Prefix: ctx.LeadingIndent + marker}, true
	}
	return components.EditorEnterAction{Prefix: base}, true
}

// yamlMarkerWidth is the width of a leading sequence marker — the dash and the spaces
// separating it from the entry's content — or 0 when the line does not start one. A dash
// with nothing after it is still a marker: an entry whose value is on the next line.
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

// yamlOpensBlock reports the line shapes a new nesting level hangs off: a mapping key with
// no inline value, and a block scalar header — "|" or ">" with any chomping or indentation
// indicator after it.
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

// blockBrackets are the pairs that open a block when Enter lands between them. Quotes are
// excluded on purpose: a string literal has no inner level to open. Adjacency alone is the
// test, the same rule the editor's own deleteEmptyAutoPair uses — pair provenance is not
// tracked anywhere, so a hand-typed empty pair behaves like an auto-closed one.
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

// endsWithOpener reports a bracket left open at the end of the line — the case where the
// closer is somewhere below rather than under the caret, so there is a level to indent
// into but nothing to push down.
func endsWithOpener(line string) bool {
	for _, pair := range blockBrackets {
		if strings.HasSuffix(line, pair.open) {
			return true
		}
	}
	return false
}

// bracketBlock is the action for a caret between a bracket pair: the closer moves down to
// its own line at the current indentation and the caret lands on an indented line between
// the two.
func bracketBlock(ctx components.EditorEnterContext) components.EditorEnterAction {
	return components.EditorEnterAction{
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

// colonBlockEnter is the Enter both Python and GDScript want — their block grammars agree,
// and the one place they differ (GDScript indents with a tab, Python with four spaces) is
// a profile setting rather than a rule, so IndentUnit already carries it. alt+i still
// overrides that unit for the current editor.
func colonBlockEnter(ctx components.EditorEnterContext) (components.EditorEnterAction, bool) {
	if !afterLeadingIndent(ctx) {
		return components.EditorEnterAction{}, false
	}
	if insideBracket(ctx.Before, ctx.After) {
		return bracketBlock(ctx), true
	}
	trimmed := strings.TrimSpace(ctx.Before)
	switch {
	case strings.HasSuffix(trimmed, ":"), strings.HasSuffix(trimmed, "\\"), endsWithOpener(trimmed):
		return components.EditorEnterAction{Prefix: ctx.LeadingIndent + ctx.IndentUnit}, true
	case blockEndStatements[firstWord(trimmed)]:
		return components.EditorEnterAction{Prefix: dropIndentUnit(ctx.LeadingIndent, ctx.IndentUnit)}, true
	}
	return components.EditorEnterAction{Prefix: ctx.LeadingIndent}, true
}

// firstWord is the line's leading whitespace-delimited token, or "" for a blank line.
func firstWord(line string) string {
	if fields := strings.Fields(line); len(fields) > 0 {
		return fields[0]
	}
	return ""
}

// dropIndentUnit removes one trailing indent level, taking whatever is actually there: a
// tab, else up to one unit of spaces. It mirrors the editor's own shiftLineIndent(-1) so
// alt+, and a structured Enter agree about what one level is.
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

// shellBranchEnd holds the case-branch terminators. Only a line that is *nothing but* a
// terminator dedents: `--check) ;;` is a one-line branch already sitting at its pattern's
// own level, and dedenting after it would walk back out of the case.
var shellBranchEnd = map[string]bool{";;": true, ";&": true, ";;&": true}

// shellEnter carries the current block indentation, adds one live indent unit after a block
// opener, and drops one after a case branch closes. Closers themselves — fi, done, esac, }
// — need no rule: they already sit at the level the next line wants, so carrying is right.
// Dedenting one as you type it is the editor's alt+, and deliberately not this handler's
// job, the same division gdscriptEnter draws.
func shellEnter(ctx components.EditorEnterContext) (components.EditorEnterAction, bool) {
	if !afterLeadingIndent(ctx) {
		return components.EditorEnterAction{}, false
	}
	trimmed := strings.TrimSpace(ctx.Before)
	switch {
	case shellBranchEnd[trimmed]:
		return components.EditorEnterAction{Prefix: dropIndentUnit(ctx.LeadingIndent, ctx.IndentUnit)}, true
	case shellOpensBlock(trimmed):
		return components.EditorEnterAction{Prefix: ctx.LeadingIndent + ctx.IndentUnit}, true
	}
	return components.EditorEnterAction{Prefix: ctx.LeadingIndent}, true
}

// shellOpensBlock tests the line's last word rather than parsing it. Nothing here strips
// trailing comments or tracks quoting — the same modest scope the YAML and GDScript
// handlers keep. The cost is that `echo then` indents; the benefit is that the common
// shapes (if/elif, loops, case, functions, subshells, continuations) all work.
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
	// A case pattern — `*)`, `start|stop)` — is the one bare `)` that opens a block. A line
	// with a `(` of its own is a substitution or a function header, which the suffix tests
	// above already answered.
	return strings.HasSuffix(line, ")") && !strings.Contains(line, "(")
}

// fishBlockOpeners are fish's block keywords. fish closes every block with `end`, so unlike
// POSIX shell the opener is the line's FIRST word. `case` is deliberately absent: fish has
// no `;;` equivalent, so there is no Enter-visible signal to dedent between branches and
// including it would add a level per branch. Left out, the body of a switch sits level with
// its cases — one alt+. to fix, and it never compounds.
var fishBlockOpeners = map[string]bool{
	"begin": true, "if": true, "else": true, "for": true,
	"while": true, "function": true, "switch": true,
}

func fishEnter(ctx components.EditorEnterContext) (components.EditorEnterAction, bool) {
	if !afterLeadingIndent(ctx) {
		return components.EditorEnterAction{}, false
	}
	trimmed := strings.TrimSpace(ctx.Before)
	prefix := ctx.LeadingIndent
	fields := strings.Fields(trimmed)
	if strings.HasSuffix(trimmed, "\\") || (len(fields) > 0 && fishBlockOpeners[fields[0]]) {
		prefix += ctx.IndentUnit
	}
	return components.EditorEnterAction{Prefix: prefix}, true
}
