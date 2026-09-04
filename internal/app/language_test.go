package app

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/brohd11/bubblestack/components/editor"

	"github.com/alecthomas/chroma/v2/lexers"
)

func editorForLanguage(path, content string) *editor.Screen {
	ed := editor.New(editor.Opts{
		Path:            path,
		ResolveLanguage: editorLanguageForPath,
	})
	ed.SetText(content)
	return ed
}

func pressEditor(ed *editor.Screen, keys ...string) {
	for _, key := range keys {
		ed.Update(nil, keyMsg(key))
	}
}

func TestLanguageForPath(t *testing.T) {
	if got := languageForPath("README"); got != nil {
		t.Fatalf("extensionless file resolved to %#v, want literal editing", got)
	}
	if got := languageForPath("notes.unknown"); got != nil {
		t.Fatalf("unknown extension resolved to %#v, want literal editing", got)
	}

	profile := languageForPath("PLAYER.GD")
	if profile == nil || profile.id != "gdscript" {
		t.Fatalf("PLAYER.GD profile = %#v, want gdscript", profile)
	}
	highlighter, ok := profile.editor.NewHighlighter().(*chromaHighlighter)
	if !ok {
		t.Fatalf("gdscript editor highlighter = %T, want chromaHighlighter", profile.editor.NewHighlighter())
	}
	fenceLexer := lexers.Get("gdscript")
	if fenceLexer == nil || highlighter.lexer.Config().Name != fenceLexer.Config().Name {
		t.Fatalf("editor lexer %q and fenced lexer %v should be the same Chroma language",
			highlighter.lexer.Config().Name, fenceLexer)
	}
}

func hasPair(pairs []editor.Pair, open, close rune) bool {
	for _, pair := range pairs {
		if pair.Open == open && pair.Close == close {
			return true
		}
	}
	return false
}

func TestLanguagePairProfiles(t *testing.T) {
	markdown := languageForPath("notes.md").editor
	if hasPair(markdown.AutoClosingPairs, '*', '*') ||
		!hasPair(markdown.SurroundingPairs, '*', '*') ||
		!hasPair(markdown.AutoClosingPairs, '`', '`') {
		t.Fatal("Markdown should keep * surrounding-only and backticks auto-closing")
	}

	yaml := languageForPath("config.yaml").editor
	if hasPair(yaml.AutoClosingPairs, '(', ')') || !hasPair(yaml.AutoClosingPairs, '[', ']') {
		t.Fatal("YAML should keep collection/quote pairs without parentheses")
	}

	gdscript := languageForPath("player.gd").editor
	if !hasPair(gdscript.AutoClosingPairs, '(', ')') || hasPair(gdscript.AutoClosingPairs, '`', '`') {
		t.Fatal("GDScript should pair parentheses but leave backticks literal")
	}
}

func TestUnknownLanguageEditsLiterally(t *testing.T) {
	ed := editorForLanguage("scratch.unknown", "")
	pressEditor(ed, "(", "enter")
	if got := ed.Text(); got != "(\n" {
		t.Fatalf("unknown-language edit = %q, want literal pair and newline", got)
	}
}

func TestGDScriptEnter(t *testing.T) {
	for _, tc := range []struct {
		name, content, want string
	}{
		{"block opener", "func ready():", "func ready():\n\t"},
		{"nested block opener", "\tif ready:", "\tif ready:\n\t\t"},
		{"carry indentation", "\tprint(\"ready\")", "\tprint(\"ready\")\n\t"},
		{"plain line", "pass", "pass\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ed := editorForLanguage("player.gd", tc.content)
			pressEditor(ed, "end", "enter")
			if got := ed.Text(); got != tc.want {
				t.Fatalf("GDScript Enter = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestGDScriptPairsAndUndo(t *testing.T) {
	ed := editorForLanguage("player.gd", "")
	pressEditor(ed, "(")
	if got := ed.Text(); got != "()" {
		t.Fatalf("GDScript pair = %q, want ()", got)
	}
	pressEditor(ed, "backspace")
	if got := ed.Text(); got != "" {
		t.Fatalf("GDScript paired Backspace = %q, want empty", got)
	}
	pressEditor(ed, "ctrl+z")
	if got := ed.Text(); got != "()" {
		t.Fatalf("undo GDScript paired Backspace = %q, want ()", got)
	}
	pressEditor(ed, "ctrl+y")
	if got := ed.Text(); got != "" {
		t.Fatalf("redo GDScript paired Backspace = %q, want empty", got)
	}

	pressEditor(ed, "`")
	if got := ed.Text(); got != "`" {
		t.Fatalf("GDScript backtick = %q, want a literal backtick", got)
	}

	ed = editorForLanguage("player.gd", "func ready():")
	pressEditor(ed, "end", "enter", "ctrl+z")
	if got := ed.Text(); got != "func ready():" {
		t.Fatalf("undo structured GDScript Enter = %q, want original line", got)
	}
}

func TestMigratedMarkdownAndYAMLEnter(t *testing.T) {
	for _, tc := range []struct {
		path, content, want string
	}{
		{"notes.md", "  - item", "  - item\n  - "},
		{"config.yaml", "  key: value", "  key: value\n  "},
	} {
		ed := editorForLanguage(tc.path, tc.content)
		pressEditor(ed, "end", "enter")
		if got := ed.Text(); got != tc.want {
			t.Errorf("%s Enter = %q, want %q", tc.path, got, tc.want)
		}
	}
}

func TestShellPairProfiles(t *testing.T) {
	shell := languageForPath("deploy.sh").editor
	if !hasPair(shell.AutoClosingPairs, '\'', '\'') || !hasPair(shell.AutoClosingPairs, '"', '"') {
		t.Fatal("shell should pair both quotes: '...' is a literal string, not an apostrophe")
	}
	if hasPair(shell.AutoClosingPairs, '`', '`') {
		t.Fatal("shell should leave backticks literal; $( ) is the pair that matters")
	}
	if shell.IndentSpaces != 2 {
		t.Fatalf("shell IndentSpaces = %d, want 2", shell.IndentSpaces)
	}
	if fish := languageForPath("config.fish").editor; fish.IndentSpaces != 4 {
		t.Fatalf("fish IndentSpaces = %d, want 4", fish.IndentSpaces)
	}
}

func TestShellLSPProfiles(t *testing.T) {
	for _, path := range []string{"deploy.sh", "lib.bash"} {
		profile := languageForPath(path)
		if profile == nil || profile.lsp == nil || profile.lsp.server != "bash" {
			t.Fatalf("%s lsp = %#v, want the bash server", path, profile)
		}
		if profile.id != "shellscript" {
			t.Fatalf("%s language id = %q, want shellscript", path, profile.id)
		}
		if profile.lsp.requireRootHit {
			t.Fatalf("%s should still open a session from its own directory", path)
		}
	}
	for _, path := range []string{"prompt.zsh", "config.fish"} {
		if profile := languageForPath(path); profile == nil || profile.lsp != nil {
			t.Fatalf("%s lsp = %#v, want editing behavior with no server", path, profile)
		}
	}
}

func TestShellEnter(t *testing.T) {
	for _, tc := range []struct {
		name, content, want string
	}{
		{"then", "if [ -f x ]; then", "if [ -f x ]; then\n  "},
		{"elif", "elif [ -f y ]; then", "elif [ -f y ]; then\n  "},
		{"else", "else", "else\n  "},
		{"do", "for f in *; do", "for f in *; do\n  "},
		{"case", `case "$1" in`, "case \"$1\" in\n  "},
		{"case pattern", "  --check)", "  --check)\n    "},
		{"branch end", "    ;;", "    ;;\n  "},
		{"one line branch", "  --check) ;;", "  --check) ;;\n  "},
		{"function brace", "run() {", "run() {\n  "},
		{"subshell", "  (", "  (\n    "},
		{"continuation", "  grep foo \\", "  grep foo \\\n    "},
		{"carry indentation", "  echo hi", "  echo hi\n  "},
		{"closer", "  fi", "  fi\n  "},
		{"for without do", "for f in *", "for f in *\n"},
		{"substitution", `  out="$(date)"`, "  out=\"$(date)\"\n  "},
		{"plain line", "set -e", "set -e\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ed := editorForLanguage("deploy.sh", tc.content)
			pressEditor(ed, "end", "enter")
			if got := ed.Text(); got != tc.want {
				t.Fatalf("shell Enter = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestFishEnter(t *testing.T) {
	for _, tc := range []struct {
		name, content, want string
	}{
		{"if", "if test -f x", "if test -f x\n    "},
		{"function", "function greet", "function greet\n    "},
		{"end carries", "    end", "    end\n    "},
		{"plain line", "set -x foo bar", "set -x foo bar\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ed := editorForLanguage("config.fish", tc.content)
			pressEditor(ed, "end", "enter")
			if got := ed.Text(); got != tc.want {
				t.Fatalf("fish Enter = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestShellFilenameProfiles(t *testing.T) {
	for path, want := range map[string]string{
		"/home/me/.bashrc":       "shellscript",
		"/home/me/.bash_profile": "shellscript",
		"/home/me/.profile":      "shellscript",
		"/home/me/.zshrc":        "zsh",
		"/home/me/.zshenv":       "zsh",
	} {
		profile := languageForPath(path)
		if profile == nil || profile.id != want {
			t.Errorf("%s profile = %#v, want id %q", path, profile, want)
		}
		if profile != nil && profile.editor.OnEnter == nil {
			t.Errorf("%s should carry the shell Enter handler", path)
		}
	}
	if _, ok := sniffedLanguages.Load(filepath.Clean("/home/me/.bashrc")); ok {
		t.Fatal("a filename hit should never reach the disk sniff")
	}
}

func TestShebangLanguage(t *testing.T) {
	dir := t.TempDir()
	for _, tc := range []struct {
		name, content, want string
	}{
		{"env bash", "#!/usr/bin/env bash\nset -e\n", "shellscript"},
		{"env dash S", "#!/usr/bin/env -S bash -e\n", "shellscript"},
		{"absolute sh", "#!/bin/sh\n", "shellscript"},
		{"zsh", "#!/bin/zsh -f\n", "zsh"},
		{"fish", "#!/usr/bin/env fish\n", "fish"},
		{"python", "#!/usr/bin/env python3\n", "python"},
		{"no shebang", "just some notes\n", ""},
		{"bare hash", "# a comment\n", ""},
		{"unknown interpreter", "#!/usr/bin/env tclsh\n", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(dir, tc.name)
			if err := os.WriteFile(path, []byte(tc.content), 0o644); err != nil {
				t.Fatal(err)
			}
			sniffedLanguages.Delete(filepath.Clean(path))
			profile := languageForPath(path)
			if tc.want == "" {
				if profile != nil {
					t.Fatalf("%q resolved to %#v, want literal editing", tc.content, profile)
				}
				return
			}
			if profile == nil || profile.id != tc.want {
				t.Fatalf("%q resolved to %#v, want id %q", tc.content, profile, tc.want)
			}
		})
	}

	missing := filepath.Join(dir, "never-written")
	if profile := languageForPath(missing); profile != nil {
		t.Fatalf("unreadable path resolved to %#v, want literal editing", profile)
	}
}

func TestShebangSniffIsMemoized(t *testing.T) {
	path := filepath.Join(t.TempDir(), "deploy")
	if err := os.WriteFile(path, []byte("#!/bin/bash\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if profile := languageForPath(path); profile == nil || profile.id != "shellscript" {
		t.Fatalf("first resolve = %#v, want the shell profile", profile)
	}
	// Reconcile resolves every open buffer on every keystroke, so the answer has to come
	// from the memo rather than the disk — rewriting the file must not change it.
	if err := os.WriteFile(path, []byte("plain text now\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if profile := languageForPath(path); profile == nil || profile.id != "shellscript" {
		t.Fatalf("memoized resolve = %#v, want the cached shell profile", profile)
	}
	forgetSniffedLanguage(path)
	if profile := languageForPath(path); profile != nil {
		t.Fatalf("resolve after a save = %#v, want the re-sniffed literal answer", profile)
	}
}

func TestPythonEnter(t *testing.T) {
	for _, tc := range []struct {
		name, content, want string
	}{
		{"block opener", "def f():", "def f():\n    "},
		{"nested block opener", "    if x:", "    if x:\n        "},
		{"dedent after return", "        return 1", "        return 1\n    "},
		{"dedent after pass", "    pass", "    pass\n"},
		{"dedent at outer level", "pass", "pass\n"},
		{"unclosed opener", "data = [", "data = [\n    "},
		{"continuation", "x = 1 + \\", "x = 1 + \\\n    "},
		{"carry indentation", "    x = 1", "    x = 1\n    "},
		{"plain line", "import os", "import os\n"},
		{"return in a string is not a statement", `    print("return")`, "    print(\"return\")\n    "},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ed := editorForLanguage("main.py", tc.content)
			pressEditor(ed, "end", "enter")
			if got := ed.Text(); got != tc.want {
				t.Fatalf("Python Enter = %q, want %q", got, tc.want)
			}
		})
	}
}

// A caret between a bracket pair opens a block: the closer takes its own line at the
// current indentation and the caret waits on an indented line between the two.
func TestBracketBlockEnter(t *testing.T) {
	for _, tc := range []struct {
		name, path, content, want string
	}{
		{"python braces", "main.py", "data = {}", "data = {\n    \n}"},
		{"python nested brackets", "main.py", "    xs = []", "    xs = [\n        \n    ]"},
		{"python parens", "main.py", "f()", "f(\n    \n)"},
		{"gdscript uses its tab unit", "player.gd", "var d = {}", "var d = {\n\t\n}"},
		{"go composite literal", "main.go", "\tx := T{}", "\tx := T{\n\t\t\n\t}"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ed := editorForLanguage(tc.path, tc.content)
			pressEditor(ed, "end", "left", "enter")
			if got := ed.Text(); got != tc.want {
				t.Fatalf("bracket Enter = %q, want %q", got, tc.want)
			}
		})
	}

	// Three splices, still one gesture.
	ed := editorForLanguage("main.py", "data = {}")
	pressEditor(ed, "end", "left", "enter", "ctrl+z")
	if got := ed.Text(); got != "data = {}" {
		t.Fatalf("undo bracket Enter = %q, want the original line", got)
	}

	// A quote pair has no inner level, so it stays an ordinary split.
	ed = editorForLanguage("main.py", `s = ""`)
	pressEditor(ed, "end", "left", "enter")
	if got, want := ed.Text(), "s = \"\n\""; got != want {
		t.Fatalf("quote Enter = %q, want a plain split %q", got, want)
	}
}

func TestGoEnter(t *testing.T) {
	for _, tc := range []struct {
		name, content, want string
	}{
		{"func brace", "func f() {", "func f() {\n\t"},
		{"nested brace", "\tif err != nil {", "\tif err != nil {\n\t\t"},
		{"switch", "\tswitch x {", "\tswitch x {\n\t\t"},
		{"case", "\tcase 1:", "\tcase 1:\n\t\t"},
		{"default", "\tdefault:", "\tdefault:\n\t\t"},
		{"label", "loop:", "loop:\n\t"},
		{"multiline call", "\tfmt.Println(", "\tfmt.Println(\n\t\t"},
		// The one place Go parts ways with colonBlockEnter. Go's block closes with a brace
		// the bracket rule has already put below the caret, so Enter under a return belongs
		// INSIDE the block; dedenting here would step over the "}" that is already there.
		{"return carries, never dedents", "\t\treturn err", "\t\treturn err\n\t\t"},
		{"break carries", "\t\tbreak", "\t\tbreak\n\t\t"},
		{"closing brace carries", "\t}", "\t}\n\t"},
		{"complete literal carries", "\txs := []int{1, 2}", "\txs := []int{1, 2}\n\t"},
		{"plain line", "package main", "package main\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ed := editorForLanguage("main.go", tc.content)
			pressEditor(ed, "end", "enter")
			if got := ed.Text(); got != tc.want {
				t.Fatalf("Go Enter = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestGoProfile(t *testing.T) {
	profile := languageForPath("main.go")
	if profile == nil || profile.id != "go" {
		t.Fatalf("Go profile = %#v, want id \"go\"", profile)
	}
	if profile.lsp == nil || profile.lsp.server != "go" {
		t.Fatalf("Go lsp = %#v, want the go server", profile.lsp)
	}
	// gofmt indents with tabs, which is what IndentSpaces 0 already means.
	if profile.editor.IndentSpaces != 0 {
		t.Fatalf("Go IndentSpaces = %d, want a literal tab", profile.editor.IndentSpaces)
	}
	if !hasPair(profile.editor.AutoClosingPairs, '`', '`') {
		t.Fatal("Go should pair backticks: raw string literals")
	}
	if hasPair(profile.editor.AutoClosingPairs, '\'', '\'') {
		t.Fatal("Go should leave single quotes literal: apostrophes in comments beat rune literals")
	}
}

// Every extension in braceIndent has to have actually produced a profile. This is the guard
// against the bug that motivated forcedLexers: chroma matching no lexer for an extension
// drops it from languageByExt silently, which is how *.glsl sat in chromaExts editing as
// plain text. A table entry with no profile behind it is the failure to catch.
func TestBraceLanguageProfiles(t *testing.T) {
	for ext, spaces := range braceIndent {
		profile := languageForPath("file" + ext)
		if profile == nil {
			t.Errorf("%s has no profile — chroma matched no lexer, so it edits literally", ext)
			continue
		}
		if profile.editor.OnEnter == nil {
			t.Errorf("%s should carry the brace Enter handler", ext)
		}
		if profile.editor.IndentSpaces != spaces {
			t.Errorf("%s IndentSpaces = %d, want %d", ext, profile.editor.IndentSpaces, spaces)
		}
		if profile.editor.NewHighlighter == nil {
			t.Errorf("%s should carry a highlighter", ext)
		}
	}
	// Not brace languages: each closes its blocks with a keyword, not a "}".
	for _, ext := range []string{".rb", ".lua", ".pl", ".r", ".vim", ".toml", ".ini", ".html", ".xml", ".sql"} {
		if profile := languageForPath("file" + ext); profile != nil && profile.editor.OnEnter != nil {
			t.Errorf("%s should not have picked up the brace Enter handler", ext)
		}
	}
}

func TestBraceLanguageEnter(t *testing.T) {
	for _, tc := range []struct {
		name, path, content, want string
	}{
		{"c++ brace", "os.cpp", "void f() {", "void f() {\n\t"},
		{"c++ nested", "os.cpp", "\tif (x) {", "\tif (x) {\n\t\t"},
		{"c++ case", "os.cpp", "\t\tcase 1:", "\t\tcase 1:\n\t\t\t"},
		{"c++ access specifier", "os.h", "public:", "public:\n\t"},
		// The family-wide reason there is no dedent: the "}" is already below the caret.
		{"c++ return carries", "os.cpp", "\t\treturn err;", "\t\treturn err;\n\t\t"},
		{"c++ closing brace carries", "os.cpp", "\t}", "\t}\n\t"},
		{"c# four spaces", "Gen.cs", "    if (x) {", "    if (x) {\n        "},
		{"rust four spaces", "main.rs", "fn main() {", "fn main() {\n    "},
		{"typescript two spaces", "app.ts", "function f() {", "function f() {\n  "},
		{"typescript object key", "app.ts", "  key:", "  key:\n    "},
		{"json two spaces", "pkg.json", `  "deps": {`, "  \"deps\": {\n    "},
		{"css two spaces", "app.css", ".btn {", ".btn {\n  "},
		{"glsl two spaces", "sky.glsl", "void main() {", "void main() {\n  "},
		{"plain line carries", "os.cpp", "\tint x = 1;", "\tint x = 1;\n\t"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ed := editorForLanguage(tc.path, tc.content)
			pressEditor(ed, "end", "enter")
			if got := ed.Text(); got != tc.want {
				t.Fatalf("brace Enter = %q, want %q", got, tc.want)
			}
		})
	}

	// Bracket block-open reaches the whole family, with each language's own unit.
	for _, tc := range []struct{ name, path, content, want string }{
		{"c++ tab", "os.cpp", "\tState s = {}", "\tState s = {\n\t\t\n\t}"},
		{"c# four", "Gen.cs", "    var s = []", "    var s = [\n        \n    ]"},
		{"json two", "pkg.json", `"deps": {}`, "\"deps\": {\n  \n}"},
	} {
		t.Run(tc.name+" block", func(t *testing.T) {
			ed := editorForLanguage(tc.path, tc.content)
			pressEditor(ed, "end", "left", "enter")
			if got := ed.Text(); got != tc.want {
				t.Fatalf("bracket Enter = %q, want %q", got, tc.want)
			}
		})
	}
}

// The C family's highlighting and server wiring, including the two extensions that were
// broken or missing before forcedLexers and the chromaExts addition.
func TestCFamilyProfiles(t *testing.T) {
	cppLexer := lexers.Get("cpp")
	if cppLexer == nil {
		t.Fatal("chroma should ship a C++ lexer")
	}
	for _, path := range []string{"os.cpp", "os.hpp", "os.cc", "os.hh", "os.h"} {
		profile := languageForPath(path)
		if profile == nil {
			t.Fatalf("%s has no profile", path)
		}
		if profile.lsp == nil || profile.lsp.server != "clangd" {
			t.Errorf("%s lsp = %#v, want clangd", path, profile.lsp)
		}
	}
	// *.h is claimed by both the C and Objective-C lexers; forcedLexers takes C++ instead.
	header, ok := languageForPath("os.h").editor.NewHighlighter().(*chromaHighlighter)
	if !ok || header.lexer.Config().Name != cppLexer.Config().Name {
		t.Fatalf(".h lexer = %v, want C++", header.lexer.Config().Name)
	}
	if got := languageForPath("os.h").id; got != "cpp" {
		t.Errorf(".h language id = %q, want cpp", got)
	}
	if got := languageForPath("main.c").id; got != "c" {
		t.Errorf(".c language id = %q, want c", got)
	}
	// *.glsl is claimed by NO lexer by filename; without forcedLexers it has no profile.
	shader := languageForPath("sky.glsl")
	if shader == nil || shader.id != "glsl" {
		t.Fatalf("glsl profile = %#v, want id glsl", shader)
	}
	if shader.lsp != nil {
		t.Errorf("glsl should have no language server, got %#v", shader.lsp)
	}
}

// typescript-language-server dispatches on these identifiers, and chroma's first aliases
// ("js", "ts") are not them.
func TestJSLanguageIDs(t *testing.T) {
	for path, want := range map[string]string{
		"app.js": "javascript", "app.jsx": "javascriptreact",
		"app.ts": "typescript", "app.tsx": "typescriptreact",
	} {
		profile := languageForPath(path)
		if profile == nil || profile.id != want {
			t.Errorf("%s id = %#v, want %q", path, profile, want)
		}
		if profile != nil && (profile.lsp == nil || profile.lsp.server != "typescript") {
			t.Errorf("%s lsp = %#v, want the typescript server", path, profile.lsp)
		}
	}
}

// Every profile that should carry a comment delimiter does, and nothing claims a bogus one.
func TestCommentDelimiters(t *testing.T) {
	for _, tc := range []struct{ path, line string }{
		{"main.go", "//"}, {"os.cpp", "//"}, {"os.h", "//"}, {"Gen.cs", "//"},
		{"App.java", "//"}, {"main.rs", "//"}, {"app.ts", "//"}, {"app.jsx", "//"},
		{"sky.glsl", "//"}, {"app.scss", "//"},
		{"main.py", "#"}, {"deploy.sh", "#"}, {"config.yaml", "#"}, {"conf.toml", "#"},
		{"player.gd", "#"}, {"main.tf", "#"},
		{"init.lua", "--"}, {"q.sql", "--"},
		{"vimrc.vim", "\""},
	} {
		profile := languageForPath(tc.path)
		if profile == nil || profile.editor.LineComment != tc.line {
			t.Errorf("%s LineComment = %#v, want %q", tc.path, profile, tc.line)
		}
	}

	// No line form, so the toggle falls back to wrapping.
	for _, tc := range []struct {
		path  string
		block [2]string
	}{
		{"app.css", [2]string{"/*", "*/"}},
		{"page.html", [2]string{"<!--", "-->"}},
		{"notes.md", [2]string{"<!--", "-->"}},
	} {
		profile := languageForPath(tc.path)
		if profile == nil || profile.editor.BlockComment != tc.block {
			t.Errorf("%s BlockComment = %#v, want %v", tc.path, profile, tc.block)
		}
		if profile != nil && profile.editor.LineComment != "" {
			t.Errorf("%s should have no line comment, got %q", tc.path, profile.editor.LineComment)
		}
	}

	// JSON has no comment in the format at all; a diff's "#" is content.
	for _, path := range []string{"pkg.json", "rows.csv", "fix.patch", "fix.diff"} {
		profile := languageForPath(path)
		if profile == nil {
			continue
		}
		if profile.editor.LineComment != "" || profile.editor.BlockComment != [2]string{} {
			t.Errorf("%s should declare no comment, got %q / %v",
				path, profile.editor.LineComment, profile.editor.BlockComment)
		}
	}
}

// End-to-end through the real key path: both bindings, a multi-line span, and one undo.
func TestCommentToggleInEditor(t *testing.T) {
	ed := editorForLanguage("main.go", "\tif err != nil {\n\t\treturn err\n\t}")
	pressEditor(ed, "shift+down", "shift+down", "shift+end", "ctrl+_")
	want := "\t// if err != nil {\n\t// \treturn err\n\t// }"
	if got := ed.Text(); got != want {
		t.Fatalf("ctrl+/ over a selection = %q, want %q", got, want)
	}
	pressEditor(ed, "ctrl+z")
	if got, want := ed.Text(), "\tif err != nil {\n\t\treturn err\n\t}"; got != want {
		t.Fatalf("undo = %q, want %q", got, want)
	}

	// alt+/ is the same gesture for terminals that swallow ctrl+/.
	ed = editorForLanguage("main.py", "value = 1")
	pressEditor(ed, "alt+/")
	if got, want := ed.Text(), "# value = 1"; got != want {
		t.Fatalf("alt+/ = %q, want %q", got, want)
	}
	pressEditor(ed, "alt+/")
	if got, want := ed.Text(), "value = 1"; got != want {
		t.Fatalf("alt+/ again = %q, want %q", got, want)
	}
}

// Comment continuation reaches every language that declares a delimiter, and the languages
// whose Enter handlers already own the line are unaffected.
func TestCommentEnterAcrossLanguages(t *testing.T) {
	for _, tc := range []struct{ path, content, want string }{
		{"main.go", "// takes a path", "// takes a path\n// "},
		{"os.cpp", "\t// note", "\t// note\n\t// "},
		{"main.py", "# note", "# note\n# "},
		{"init.lua", "-- note", "-- note\n-- "},
		{"deploy.sh", "#!/bin/bash", "#!/bin/bash\n"},
		// A comment outranks the language's block structure: no indent after this colon.
		{"main.py", "# if x:", "# if x:\n# "},
		// Markdown has no line comment, so its list continuation is untouched.
		{"notes.md", "- item", "- item\n- "},
	} {
		ed := editorForLanguage(tc.path, tc.content)
		pressEditor(ed, "end", "enter")
		if got := ed.Text(); got != tc.want {
			t.Errorf("%s Enter = %q, want %q", tc.path, got, tc.want)
		}
	}
}

func TestYAMLEnter(t *testing.T) {
	for _, tc := range []struct {
		name, content, want string
	}{
		{"top level key", "key:", "key:\n  "},
		{"nested key", "  key:", "  key:\n    "},
		{"key under a sequence entry", "- name:", "- name:\n    "},
		{"block scalar", "script: |", "script: |\n  "},
		{"folded scalar with chomping", "text: >-", "text: >-\n  "},
		{"sequence entry", "- one", "- one\n- "},
		{"nested sequence entry", "  - one", "  - one\n  - "},
		{"inline value carries", "key: value", "key: value\n"},
		{"nested inline value carries", "  key: value", "  key: value\n  "},
		{"scalar starting with a dash", "-name", "-name\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ed := editorForLanguage("config.yaml", tc.content)
			pressEditor(ed, "end", "enter")
			if got := ed.Text(); got != tc.want {
				t.Fatalf("YAML Enter = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestMarkdownEnter(t *testing.T) {
	for _, tc := range []struct {
		name, content, want string
	}{
		{"dash bullet", "- item", "- item\n- "},
		{"star bullet", "* item", "* item\n* "},
		{"plus bullet", "+ item", "+ item\n+ "},
		{"ordered increments", "1. first", "1. first\n2. "},
		{"ordered wraps a paren delimiter", "9) ninth", "9) ninth\n10) "},
		{"nested keeps its indent", "  - item", "  - item\n  - "},
		{"task item", "- [ ] todo", "- [ ] todo\n- [ ] "},
		{"finished task continues unchecked", "- [x] done", "- [x] done\n- [ ] "},
		{"blockquote", "> quoted", "> quoted\n> "},
		{"blockquote without a space", ">quoted", ">quoted\n>"},
		{"heading is not a list", "# Title", "# Title\n"},
		{"prose is not a list", "just words", "just words\n"},
		{"a dash needs its space", "-notalist", "-notalist\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ed := editorForLanguage("notes.md", tc.content)
			pressEditor(ed, "end", "enter")
			if got := ed.Text(); got != tc.want {
				t.Fatalf("Markdown Enter = %q, want %q", got, tc.want)
			}
		})
	}
}

// An empty item ends the list instead of adding another one: nested items step out a
// level per press, and an item at the outer level clears its line.
func TestMarkdownEnterEndsAList(t *testing.T) {
	ed := editorForLanguage("notes.md", "- a\n  - b\n  - ")
	pressEditor(ed, "down", "down", "end", "enter")
	if got, want := ed.Text(), "- a\n  - b\n- "; got != want {
		t.Fatalf("first Enter = %q, want the item outdented to %q", got, want)
	}
	pressEditor(ed, "enter")
	if got, want := ed.Text(), "- a\n  - b\n"; got != want {
		t.Fatalf("second Enter = %q, want the marker gone: %q", got, want)
	}
	pressEditor(ed, "ctrl+z")
	if got, want := ed.Text(), "- a\n  - b\n- "; got != want {
		t.Fatalf("undo the list exit = %q, want %q", got, want)
	}

	for _, tc := range []struct {
		name, content, want string
	}{
		{"top level bullet", "- ", ""},
		{"ordered", "1. ", ""},
		{"task", "- [ ] ", ""},
		{"blockquote", "> ", ""},
		{"nested ordered outdents", "  1. ", "1. "},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ed := editorForLanguage("notes.md", tc.content)
			pressEditor(ed, "end", "enter")
			if got := ed.Text(); got != tc.want {
				t.Fatalf("Enter on an empty item = %q, want %q", got, tc.want)
			}
		})
	}

	// An empty marker with text still to its right is a split, not an exit.
	ed = editorForLanguage("notes.md", "- foo")
	pressEditor(ed, "end", "left", "enter")
	if got, want := ed.Text(), "- fo\n- o"; got != want {
		t.Fatalf("mid-item Enter = %q, want %q", got, want)
	}
}
