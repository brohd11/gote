package app

import (
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
