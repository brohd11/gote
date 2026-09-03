package app

import (
	"os"
	"path/filepath"
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
			cfg.OnEnter = gdscriptEnter
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

// markdownEnter preserves the editor's existing deliberately small behavior: only a
// leading dash-space list marker continues, including its exact indentation.
func markdownEnter(ctx components.EditorEnterContext) (components.EditorEnterAction, bool) {
	if !afterLeadingIndent(ctx) {
		return components.EditorEnterAction{}, false
	}
	rest := strings.TrimPrefix(ctx.Before, ctx.LeadingIndent)
	if !strings.HasPrefix(rest, "- ") {
		return components.EditorEnterAction{}, false
	}
	return components.EditorEnterAction{Prefix: ctx.LeadingIndent + "- "}, true
}

// yamlEnter carries existing indentation only. Inferring a new YAML nesting level is a
// separate language feature; this migration preserves the behavior users have today.
func yamlEnter(ctx components.EditorEnterContext) (components.EditorEnterAction, bool) {
	if ctx.LeadingIndent == "" || !afterLeadingIndent(ctx) {
		return components.EditorEnterAction{}, false
	}
	return components.EditorEnterAction{Prefix: ctx.LeadingIndent}, true
}

// gdscriptEnter carries the current block indentation and adds exactly one live indent
// unit after a colon. GDScript's profile defaults that unit to a literal tab, while
// alt+i remains able to override it for the current editor.
func gdscriptEnter(ctx components.EditorEnterContext) (components.EditorEnterAction, bool) {
	if !afterLeadingIndent(ctx) {
		return components.EditorEnterAction{}, false
	}
	prefix := ctx.LeadingIndent
	if strings.HasSuffix(strings.TrimSpace(ctx.Before), ":") {
		prefix += ctx.IndentUnit
	}
	return components.EditorEnterAction{Prefix: prefix}, true
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
