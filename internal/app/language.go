package app

import (
	"path/filepath"
	"strings"

	"github.com/brohd11/bubblestack/components"

	"github.com/alecthomas/chroma/v2/lexers"
)

// languageProfile is gote's single file-type authority. The editor consumes only the
// language-agnostic component config; id is retained here for the LSP document language
// identifier a later client will need.
type languageProfile struct {
	id     string
	editor components.EditorLanguageConfig
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
		profiles[ext] = &languageProfile{id: id, editor: cfg}
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

func languageForPath(path string) *languageProfile {
	return languageByExt[strings.ToLower(filepath.Ext(path))]
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
