package app

// The semantic-token vocabulary and its mapping to gote's palette. The type names are the
// LSP specification's, shared by gopls, rust-analyzer, pylsp and typescript-language-
// server; LanguageServerConfig.SemanticTokens maps any non-standard ones.

// semanticTokenTypeNames and semanticTokenModifierNames are declared in full at
// initialize: they state what gote can decode, and a narrower list could change what a
// server computes.
var semanticTokenTypeNames = []string{
	"namespace", "type", "class", "enum", "interface", "struct", "typeParameter",
	"parameter", "variable", "property", "enumMember", "event", "function", "method",
	"macro", "keyword", "modifier", "comment", "string", "number", "regexp", "operator",
	"decorator", "label",
}

var semanticTokenModifierNames = []string{
	"declaration", "definition", "readonly", "static", "deprecated", "abstract", "async",
	"modification", "documentation", "defaultLibrary",
}

// defaultSemanticSlots maps token types to syntax_colors slots, by slot name. variable,
// parameter, property, namespace, enumMember, event and label are deliberately unmapped:
// unmapped tokens keep chroma's color, so this corrects chroma rather than recoloring
// every identifier, and unknown names never paint.
var defaultSemanticSlots = map[string]string{
	"type":          "type",
	"class":         "type",
	"enum":          "type",
	"interface":     "type",
	"struct":        "type",
	"typeParameter": "type",

	"function":  "func",
	"method":    "func",
	"macro":     "func",
	"decorator": "func",

	"keyword":  "keyword",
	"modifier": "keyword",

	"comment":  "comment",
	"string":   "string",
	"regexp":   "string",
	"number":   "number",
	"operator": "operator",
}

// semanticSlots merges a server's overrides over the defaults; an empty value turns a type
// off.
func semanticSlots(overrides map[string]string) map[string]string {
	if len(overrides) == 0 {
		return defaultSemanticSlots
	}
	merged := make(map[string]string, len(defaultSemanticSlots)+len(overrides))
	for name, slot := range defaultSemanticSlots {
		merged[name] = slot
	}
	for name, slot := range overrides {
		if slot == "" {
			delete(merged, name)
			continue
		}
		merged[name] = slot
	}
	return merged
}
