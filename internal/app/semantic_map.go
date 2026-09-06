package app

// The semantic-token vocabulary and how it lands on gote's palette.
//
// The token type names here are the LSP specification's, not any one server's. gopls,
// rust-analyzer, pylsp and typescript-language-server all speak the same twenty-four
// names, which is why one hardcoded map serves every server gote can talk to rather than
// this being work done for a single language. What varies per server is only which subset
// it emits, and whatever non-standard names it invents on top — which is what
// LanguageServerConfig.SemanticTokens is for.

// semanticTokenTypeNames and semanticTokenModifierNames are what gote declares it
// understands at initialize. The full lists are sent rather than only the mapped ones: the
// declaration says what the client can DECODE, and a server is entitled to assume anything
// it omits from its legend is unavailable. Narrowing this to the mapped subset would
// silently change which tokens a server bothers to compute.
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

// defaultSemanticSlots maps a token type to the syntax_colors slot that paints it. The
// values are the config's own slot names, so a per-server override in config.yml is
// written in the same vocabulary as the palette it refers to.
//
// The omissions are the design. variable, parameter, property, namespace, enumMember,
// event and label are deliberately absent: an unmapped token keeps whatever chroma gave
// it, which is what makes this an overlay that CORRECTS chroma rather than a second
// highlighter that overrides it. It is also what makes an unfamiliar server degrade to
// today's rendering instead of painting nonsense — a name gote does not know simply does
// not paint.
//
// Coloring every identifier is a decision this map declines to make on the user's behalf,
// for the reason chromaStyles declines it: coloring everything colors nothing.
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

// semanticSlots merges a server's overrides over the defaults. An override with an empty
// value turns a type OFF rather than painting it, which is the only way a config can
// subtract from a hardcoded default — and the reason the zero value of the map means "no
// opinion" rather than "paint nothing".
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
