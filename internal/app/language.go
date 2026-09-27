package app

import (
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/brohd11/bubblestack/components/editor"

	"github.com/alecthomas/chroma/v2/lexers"
)

// languageProfile is gote's file-type authority. The editor only sees the language
// config; id is the LSP language identifier.
type languageProfile struct {
	id     string
	editor editor.LanguageConfig
	lsp    *languageLSP
}

// languageLSP is gote's server selection and workspace discovery for a language.
type languageLSP struct {
	server string
	// workspaceMarkers name an outer root that outranks a nearer rootMarkers hit: a Go module
	// inside a go.work belongs to the workspace, and one gopls for it is more correct and
	// cheaper.
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
	// shellPairs adds the single quote (a literal string in shell). Backticks are left out;
	// $( ) covers substitution.
	shellPairs = []editor.Pair{
		{Open: '(', Close: ')'},
		{Open: '[', Close: ']'},
		{Open: '{', Close: '}'},
		{Open: '\'', Close: '\''},
		{Open: '"', Close: '"'},
	}

	languageByExt = buildLanguageProfiles()
)

// braceIndent lists the brace-delimited languages and their indent unit (0 = tab). Being
// listed is the whole profile: braceBlockEnter plus the unit. Languages that are not
// brace-delimited are absent.
var braceIndent = map[string]int{
	// Tabs, as gofmt uses; the C family keeps tabs too. alt+i overrides per buffer.
	".go": 0, ".c": 0, ".h": 0, ".cc": 0, ".cpp": 0, ".hpp": 0, ".hh": 0,
	".cs": 4, ".java": 4, ".rs": 4, ".kt": 4, ".swift": 4, ".php": 4,
	".js": 2, ".jsx": 2, ".ts": 2, ".tsx": 2, ".json": 2, ".css": 2, ".scss": 2,
	".dart": 2, ".proto": 2, ".tf": 2, ".gradle": 2, ".glsl": 2,
}

// forcedLexers pin the chroma lexer for an extension: some extensions are claimed by
// several lexers and Match picks wrong, others (.glsl) by none. Naming the lexer also
// keeps source buffers and fenced previews on one tokenizer.
var forcedLexers = map[string]string{
	".gd":   "gdscript", // Match also offers the legacy gdscript3
	".h":    "cpp",      // C and Objective-C both claim *.h; the name tiebreak picks C
	".glsl": "glsl",     // the GLSL lexer claims only *.vert, *.frag and *.geo
}

// lineComments and blockComments give each file type's comment delimiters (every chromaExts
// entry, not just brace languages). JSON, diff and CSV have none on purpose.
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
	// resolve these languages: see the registration helpers for what they repair.
	registerPatchedGDScript()
	registerPatchedYAML()
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
			// .h is already on the C++ lexer ("cpp"); clangd reads the real language from the
			// compilation database.
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
			// Chroma lexes zsh as Bash; pin the real name for when a server is added.
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

// lspRootForPath finds a file's workspace: first by workspaceMarkers (so a go.work beats
// a nearer go.mod), then rootMarkers. GDScript needs project.godot; other servers fall
// back to the file's directory.
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

// nearestMarker returns the first directory from dir upward holding any of markers; an
// empty list never matches.
func nearestMarker(dir string, markers []string) string {
	if len(markers) == 0 {
		return ""
	}
	for {
		for _, marker := range markers {
			// Wildcard markers are globbed (C#'s *.sln is named after the project).
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

// extByFilename maps extensionless dotfiles to the extension whose profile they share.
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

// extByInterpreter maps a shebang interpreter to the extension whose profile it uses.
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

// sniffedLanguages caches shebang results per path, misses included. Reconcile resolves
// every open buffer on every update, so uncached sniffs would read disk per keystroke.
var sniffedLanguages sync.Map // cleaned path -> *languageProfile

// languageForPath checks the extension, then the whole filename, then (reading disk only
// then) the shebang.
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

// shebangExt returns the extension a file's shebang interpreter stands for, or "".
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

// editorLanguageForPath is the editor's LanguageResolver. Semantic tokens go to the
// editor's overlay, so highlighter factories stay path-independent.
func editorLanguageForPath(path string) *editor.LanguageConfig {
	profile := languageForPath(path)
	if profile == nil {
		return nil
	}
	return &profile.editor
}
