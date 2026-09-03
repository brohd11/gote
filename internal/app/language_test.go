package app

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/brohd11/bubblestack/components"

	"github.com/alecthomas/chroma/v2/lexers"
)

func editorForLanguage(path, content string) *components.EditorScreen {
	ed := components.NewEditorScreen(components.EditorOpts{
		Path:            path,
		ResolveLanguage: editorLanguageForPath,
	})
	ed.SetText(content)
	return ed
}

func pressEditor(ed *components.EditorScreen, keys ...string) {
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

func hasPair(pairs []components.EditorPair, open, close rune) bool {
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
