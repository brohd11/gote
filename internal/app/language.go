package app

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/brohd11/bubblestack/components/editor"

	"github.com/alecthomas/chroma/v2/lexers"
)

// languageProfile is gote's single file-type authority. The editor consumes only the
// language-agnostic component config; id is retained here for the LSP document language
// identifier a later client will need.
type languageProfile struct {
	id     string
	editor editor.LanguageConfig
	lsp    *languageLSP
}

// languageLSP is Gote-owned activation metadata. Bubblestack receives only editor;
// server selection and workspace discovery stay beside the rest of the app's language
// policy.
type languageLSP struct {
	server string
	// workspaceMarkers name a root that OUTRANKS a nearer rootMarkers hit, for languages
	// where an inner project is part of an outer one. Go is the case: a module inside a
	// go.work belongs to the workspace, and one gopls over the whole thing is both more
	// correct (cross-module definitions resolve) and far cheaper than a process per module.
	workspaceMarkers []string
	rootMarkers      []string
	requireRootHit   bool
}

var (
	codePairs = []editor.Pair{
		{Open: '(', Close: ')'},
		{Open: '[', Close: ']'},
		{Open: '{', Close: '}'},
		// {Open: '\'', Close: '\''},
		{Open: '"', Close: '"'},
		{Open: '`', Close: '`'},
	}
	markdownSurroundPairs = append(append([]editor.Pair{}, codePairs...),
		editor.Pair{Open: '*', Close: '*'},
		editor.Pair{Open: '_', Close: '_'},
	)
	yamlPairs = []editor.Pair{
		{Open: '[', Close: ']'},
		{Open: '{', Close: '}'},
		{Open: '\'', Close: '\''},
		{Open: '"', Close: '"'},
	}
	gdscriptPairs = []editor.Pair{
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
	shellPairs = []editor.Pair{
		{Open: '(', Close: ')'},
		{Open: '[', Close: ']'},
		{Open: '{', Close: '}'},
		{Open: '\'', Close: '\''},
		{Open: '"', Close: '"'},
	}

	languageByExt = buildLanguageProfiles()
)

// braceIndent are the languages whose blocks are delimited by braces, mapped to the indent
// unit each one's formatter reaches for — 0 meaning a literal tab. Membership here IS the
// profile: an extension listed gets braceBlockEnter and this unit, and nothing else about it
// has to be said. Ruby, Lua, Perl, R, vim, TOML, INI, HTML, XML, SQL, CSV, make and diff are
// absent deliberately — none is brace-delimited, and each wants its own grammar rather than
// this one. A language server is a separate question: .lua has one and no entry here.
var braceIndent = map[string]int{
	// Tabs. gofmt's answer for Go, and the C family keeps the literal tab it already
	// defaulted to rather than picking a side in a split convention — alt+i overrides per
	// buffer, and a project that disagrees says so in its own .editorconfig.
	".go": 0, ".c": 0, ".h": 0, ".cc": 0, ".cpp": 0, ".hpp": 0, ".hh": 0,
	".cs": 4, ".java": 4, ".rs": 4, ".kt": 4, ".swift": 4, ".php": 4,
	".js": 2, ".jsx": 2, ".ts": 2, ".tsx": 2, ".json": 2, ".css": 2, ".scss": 2,
	".dart": 2, ".proto": 2, ".tf": 2, ".gradle": 2, ".glsl": 2,
}

// forcedLexers name the chroma lexer an extension must use instead of whatever
// lexers.Match answers for it. Two different problems land here. Some extensions are
// claimed by SEVERAL lexers and Match's tiebreak picks the wrong one. Others are claimed by
// NONE, even though the language's lexer exists under its own name — GLSL registers itself
// for *.vert, *.frag and *.geo only, so listing *.glsl in chromaExts achieves nothing on its
// own: the nil check below would drop it right back out.
//
// Naming the lexer also keeps a source buffer and a fenced preview block on one tokenizer,
// since fences resolve by lexer name rather than by extension.
var forcedLexers = map[string]string{
	".gd":   "gdscript", // Match also offers the legacy gdscript3
	".h":    "cpp",      // C and Objective-C both claim *.h; the name tiebreak picks C
	".glsl": "glsl",     // the GLSL lexer claims only *.vert, *.frag and *.geo
}

// lineComments name each file type's line-comment delimiter, and blockComments cover the
// few that have no line form at all. They span every entry in chromaExts rather than only
// the brace languages: the comment toggle is worth having in TOML or vimscript even though
// neither gets an Enter handler, and the two concerns are independent.
//
// Absent on purpose: .json has no comment in the format at all, and a diff's or CSV's "#"
// is content rather than syntax. Those types get no gesture, which is the right answer.
var lineComments = map[string]string{
	".go": "//", ".c": "//", ".h": "//", ".cc": "//", ".cpp": "//", ".hpp": "//", ".hh": "//",
	".cs": "//", ".java": "//", ".rs": "//", ".kt": "//", ".swift": "//", ".dart": "//",
	".php": "//", ".js": "//", ".jsx": "//", ".ts": "//", ".tsx": "//",
	".scss": "//", ".proto": "//", ".glsl": "//", ".gradle": "//",

	".py": "#", ".rb": "#", ".sh": "#", ".bash": "#", ".zsh": "#", ".fish": "#",
	".yaml": "#", ".yml": "#", ".toml": "#", ".ini": "#", ".r": "#", ".pl": "#",
	".tf": "#", ".mk": "#", ".gd": "#",

	".lua": "--", ".sql": "--",

	".vim": "\"",
}

var blockComments = map[string][2]string{
	".css":  {"/*", "*/"},
	".html": {"<!--", "-->"},
	".xml":  {"<!--", "-->"},
}

func buildLanguageProfiles() map[string]*languageProfile {
	// Before the first lexers.Get below, and before anything else in the process can
	// resolve either language: see registerPatchedGDScript for the upstream defect it
	// repairs, and registerPatchedGo for the type names it claims.
	registerPatchedGDScript()
	registerPatchedGo()
	profiles := make(map[string]*languageProfile, len(chromaExts)+2)
	for _, ext := range chromaExts {
		lexer := lexers.Match("file" + ext)
		if name, ok := forcedLexers[ext]; ok {
			if forced := lexers.Get(name); forced != nil {
				lexer = forced
			}
		}
		if lexer == nil {
			continue
		}
		id := strings.TrimPrefix(ext, ".")
		if aliases := lexer.Config().Aliases; len(aliases) > 0 {
			id = aliases[0]
		}
		cfg := editor.LanguageConfig{
			NewHighlighter:   chromaHighlighterFactory(lexer),
			AutoClosingPairs: codePairs,
			SurroundingPairs: codePairs,
		}
		cfg.LineComment = lineComments[ext]
		cfg.BlockComment = blockComments[ext]
		if spaces, ok := braceIndent[ext]; ok {
			cfg.IndentSpaces = spaces
			cfg.OnEnter = braceBlockEnter
		}
		switch ext {
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
			cfg.AutoClosingPairs = gdscriptPairs
			cfg.SurroundingPairs = gdscriptPairs
			cfg.OnEnter = colonBlockEnter
		}
		profile := &languageProfile{id: id, editor: cfg}
		switch ext {
		case ".py":
			profile.id = "python"
			profile.lsp = &languageLSP{server: "python", rootMarkers: []string{
				"pyproject.toml", "setup.cfg", "setup.py", ".git",
			}}
		case ".go":
			// No id pin: Chroma's first alias for *.go is already "go", which is the LSP
			// language identifier too.
			profile.lsp = &languageLSP{
				server:           "go",
				workspaceMarkers: []string{"go.work"},
				rootMarkers:      []string{"go.mod", ".git"},
			}
		case ".c", ".cc", ".cpp", ".hpp", ".hh", ".h":
			// .h needs no id pin: forcedLexers already put it on the C++ lexer, whose first
			// alias is "cpp". clangd reads the compilation database for the real language
			// anyway, so the identifier only has to be one it recognizes.
			profile.lsp = &languageLSP{server: "clangd", rootMarkers: []string{
				"compile_commands.json", ".clangd", "compile_flags.txt", "CMakeLists.txt", ".git",
			}}
		case ".cs":
			profile.lsp = &languageLSP{server: "csharp", rootMarkers: []string{
				"*.sln", "*.csproj", ".git",
			}}
		case ".rs":
			profile.lsp = &languageLSP{server: "rust", rootMarkers: []string{"Cargo.toml", ".git"}}
		case ".lua":
			profile.lsp = &languageLSP{server: "lua", rootMarkers: []string{
				".luarc.json", ".luarc.jsonc", ".git",
			}}
		// Chroma's first aliases here are "js" and "ts"; the LSP identifiers are these.
		// typescript-language-server dispatches on them, so the spelling is load-bearing.
		case ".js", ".jsx", ".ts", ".tsx":
			profile.id = map[string]string{
				".js": "javascript", ".jsx": "javascriptreact",
				".ts": "typescript", ".tsx": "typescriptreact",
			}[ext]
			profile.lsp = &languageLSP{server: "typescript", rootMarkers: []string{
				"tsconfig.json", "jsconfig.json", "package.json", ".git",
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
		editor: editor.LanguageConfig{
			NewHighlighter:   newMarkdownHighlighter,
			AutoClosingPairs: codePairs,
			SurroundingPairs: markdownSurroundPairs,
			IndentSpaces:     2,
			OnEnter:          markdownEnter,
			// Markdown's only comment is HTML's, and it has no line form — which also keeps
			// Enter out of the way of the list continuation markdownEnter owns.
			BlockComment: [2]string{"<!--", "-->"},
		},
	}
	profiles[".md"] = markdown
	profiles[".markdown"] = markdown
	return profiles
}

// lspRootForPath finds the workspace a file belongs to. workspaceMarkers get a full walk
// of their own first: an outer marker outranking a nearer ordinary one is the only way to
// say "this project is part of a larger one", which Go's go.work needs and would otherwise
// lose to the module's own go.mod several directories below it. Falling through costs
// nothing — with no workspace marker the ordinary rootMarkers walk runs exactly as it did.
//
// After both walks miss, GDScript has no useful server workspace at all without
// project.godot, while pylsp and the rest can still operate from the file's own directory.
func lspRootForPath(path string, profile *languageProfile) string {
	if profile == nil || profile.lsp == nil {
		return ""
	}
	dir := filepath.Dir(path)
	if root := nearestMarker(dir, profile.lsp.workspaceMarkers); root != "" {
		return root
	}
	if root := nearestMarker(dir, profile.lsp.rootMarkers); root != "" {
		return root
	}
	if profile.lsp.requireRootHit {
		return ""
	}
	return dir
}

// nearestMarker walks from dir to the filesystem root and answers the first directory
// holding any of markers. An empty list never matches, which is what lets a profile leave
// workspaceMarkers unset and keep its original single-walk behavior.
func nearestMarker(dir string, markers []string) string {
	if len(markers) == 0 {
		return ""
	}
	for {
		for _, marker := range markers {
			// A marker carrying a wildcard is matched as a pattern: some ecosystems name the
			// project file after the project (C#'s *.sln) rather than by convention, so there
			// is nothing exact to stat for.
			if strings.ContainsAny(marker, "*?[") {
				if hits, _ := filepath.Glob(filepath.Join(dir, marker)); len(hits) > 0 {
					return dir
				}
				continue
			}
			if _, err := os.Stat(filepath.Join(dir, marker)); err == nil {
				return dir
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
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

func editorLanguageForPath(path string) *editor.LanguageConfig {
	profile := languageForPath(path)
	if profile == nil {
		return nil
	}
	return &profile.editor
}

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
			// The marker is carried out verbatim rather than advanced: this is the same
			// item moved a level left, and its number in the list it lands in cannot be
			// read off one line. Renderers renumber an ordered list from its first item
			// anyway, so the digits here are for the writer, not the output.
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

// colonBlockEnter is the Enter both Python and GDScript want — their block grammars agree,
// and the one place they differ (GDScript indents with a tab, Python with four spaces) is
// a profile setting rather than a rule, so IndentUnit already carries it. alt+i still
// overrides that unit for the current editor.
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

// braceBlockEnter serves every brace-delimited language in braceIndent. It is
// colonBlockEnter without the dedent, and the omission is the point: Python needs
// `return`/`pass` to step back out because nothing closes a Python block, while a brace
// language closes with a `}` the bracket rule has already placed BELOW the caret — so Enter
// under a `return err` should land inside the block, above the brace already sitting there.
//
// The trailing colon covers switch cases, defaults and labels across the whole family, and
// in JS/TS an object key whose value starts on the next line.
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
