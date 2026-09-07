package app

import (
	"strings"
	"testing"

	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"
)

// The projections collapse protocol unions so no screen ever sees one. Every arm a
// server may legitimately answer with is covered here, because the arm that is never
// exercised is the one a particular language server turns out to use.

func atLine(line uint32) protocol.Range {
	return protocol.Range{
		Start: protocol.Position{Line: line},
		End:   protocol.Position{Line: line, Character: 4},
	}
}

func TestProjectDefinitionCoversEveryUnionArm(t *testing.T) {
	path := "/tmp/gote/target.go"
	single := protocol.Location{URI: uri.File(path), Range: atLine(3)}
	for name, result := range map[string]protocol.DefinitionResult{
		"single location": &single,
		"location slice":  protocol.LocationSlice{single},
		"definition links": protocol.DefinitionLinkSlice{{
			TargetURI: uri.File(path),
			// TargetRange is the whole symbol including its doc comment; the
			// selection range is the name, and the name is what a jump wants.
			TargetRange:          atLine(1),
			TargetSelectionRange: atLine(3),
		}},
	} {
		got := projectDefinition(result)
		if len(got) != 1 {
			t.Fatalf("%s: projected %d locations, want 1", name, len(got))
		}
		if !sameFilePath(got[0].Path, path) {
			t.Errorf("%s: path = %q, want %q", name, got[0].Path, path)
		}
		if got[0].Range.Start.Line != 3 {
			t.Errorf("%s: line = %d, want the selection range's 3", name, got[0].Range.Start.Line)
		}
	}
	if got := projectDefinition(nil); len(got) != 0 {
		t.Errorf("a nil result projected %d locations", len(got))
	}
	// A non-file URI has no path to open; dropping it here is what keeps every caller
	// from having to ask.
	if got := projectLocations([]protocol.Location{{URI: uri.URI("jdt://contents/rt.jar")}}); len(got) != 0 {
		t.Errorf("a non-file location projected %d entries, want none", len(got))
	}
}

func TestProjectSymbolsPreservesNestedShapeAndSortsFlatShape(t *testing.T) {
	detail := "func()"
	nested := protocol.DocumentSymbolSlice{{
		Name: "Outer", Kind: protocol.SymbolKindStruct, Range: protocol.Range{Start: protocol.Position{Line: 1}, End: protocol.Position{Line: 8}}, SelectionRange: atLine(1),
		Children: []protocol.DocumentSymbol{
			{Name: "Inner", Kind: protocol.SymbolKindMethod, Detail: &detail, Range: protocol.Range{Start: protocol.Position{Line: 2}, End: protocol.Position{Line: 6}}, SelectionRange: atLine(2)},
		},
	}}
	got := projectSymbols(nested)
	if len(got) != 1 || len(got[0].Children) != 1 {
		t.Fatalf("nested projection = %#v, want one root with one child", got)
	}
	if child := got[0].Children[0]; child.Name != "Inner" || child.Detail != detail || child.Range.End.Line != 6 {
		t.Errorf("child node = %+v", child)
	}

	// The flat shape carries no order guarantee, so the projection imposes one.
	container := "pkg"
	flat := protocol.SymbolInformationSlice{
		{BaseSymbolInformation: protocol.BaseSymbolInformation{Name: "second", ContainerName: &container},
			Location: protocol.Location{Range: atLine(9)}},
		{BaseSymbolInformation: protocol.BaseSymbolInformation{Name: "first"},
			Location: protocol.Location{Range: atLine(2)}},
	}
	rows := projectSymbols(flat)
	if len(rows) != 2 || rows[0].Name != "first" || rows[1].Name != "second" {
		t.Fatalf("flat projection = %#v, want document order", rows)
	}
	if rows[1].Detail != container {
		t.Errorf("container name = %q, want %q", rows[1].Detail, container)
	}
	if got := projectSymbols(nil); got != nil {
		t.Errorf("a nil result projected %#v", got)
	}
}

func TestOutlineNodeAtFindsContainingOrNearestSymbol(t *testing.T) {
	symbols := []lspSymbol{
		{Name: "a", Range: atLine(0), SelectionRange: atLine(0)},
		{Name: "b", Range: atLine(10), SelectionRange: atLine(10)},
		{Name: "c", Range: atLine(20), SelectionRange: atLine(20)},
	}
	nodes := outlineTree("test.go", symbols)
	nameFor := func(id string) string {
		for _, node := range nodes {
			if item := node.Item.(outlineItem); item.id == id {
				return item.name
			}
		}
		return ""
	}
	for _, tc := range []struct {
		line uint32
		want string
	}{{0, "a"}, {5, "a"}, {10, "b"}, {19, "b"}, {200, "c"}} {
		if got := nameFor(outlineNodeAt(nodes, protocol.Position{Line: tc.line})); got != tc.want {
			t.Errorf("caret on line %d selected %q, want %q", tc.line, got, tc.want)
		}
	}
}

func TestFlattenHoverCoversEveryUnionArm(t *testing.T) {
	for name, tc := range map[string]struct {
		contents protocol.HoverContents
		want     string
	}{
		"markup":      {&protocol.MarkupContent{Kind: protocol.MarkupKindMarkdown, Value: "  doc  "}, "doc"},
		"plain":       {protocol.String(" plain "), "plain"},
		"marked":      {&protocol.MarkedStringWithLanguage{Language: "go", Value: "func f()"}, "```go\nfunc f()\n```"},
		"marked nil":  {(*protocol.MarkedStringWithLanguage)(nil), ""},
		"markup nil":  {(*protocol.MarkupContent)(nil), ""},
		"unknown arm": {nil, ""},
		"marked slice": {protocol.MarkedStringSlice{
			&protocol.MarkedStringWithLanguage{Language: "go", Value: "func f()"},
			protocol.String("does a thing"),
		}, "```go\nfunc f()\n```\n\ndoes a thing"},
	} {
		if got := flattenHover(tc.contents); got != tc.want {
			t.Errorf("%s: flattenHover = %q, want %q", name, got, tc.want)
		}
	}
}

// TestHoverBodyFitsATooltip: a server's markdown is clipped to something a floating box
// can hold — fences dropped, blank runs collapsed, long lines wrapped.
func TestHoverBodyFitsATooltip(t *testing.T) {
	body := hoverBody("```go\nfunc Reveal(p EditorPosition) bool\n```\n\n\n\nMoves the caret.\n", 20)
	lines := strings.Split(body, "\n")
	if strings.Contains(body, "```") {
		t.Errorf("the fence survived:\n%s", body)
	}
	for i, line := range lines {
		if len([]rune(line)) > 20 {
			t.Errorf("line %d is %d cells wide, want it wrapped to 20: %q", i, len([]rune(line)), line)
		}
	}
	if strings.Contains(body, "\n\n\n") {
		t.Errorf("a run of blank lines survived:\n%s", body)
	}
	if body == "" || !strings.Contains(body, "Moves the caret.") {
		t.Errorf("the prose was lost:\n%s", body)
	}
	if hoverBody("   ", 20) != "" {
		t.Error("blank hover content should produce no tooltip")
	}
	// A long answer is clipped rather than allowed to fill the screen.
	long := strings.Repeat("a line of documentation\n", 40)
	if got := len(strings.Split(hoverBody(long, 40), "\n")); got > hoverMaxHeight {
		t.Errorf("a long hover rendered %d lines, want at most %d", got, hoverMaxHeight)
	}
}

// TestProjectSignatureUsesLabelOffsets: the active parameter is found by the offsets the
// server sent, not by searching the label — which is what makes "f(a int, b int)"
// highlight the right "int".
func TestProjectSignatureUsesLabelOffsets(t *testing.T) {
	help := &protocol.SignatureHelp{
		Signatures: []protocol.SignatureInformation{{
			Label:           "f(a int, b int)",
			Documentation:   protocol.String("does a thing"),
			ActiveParameter: protocol.NewNullable(uint32(1)),
			Parameters: []protocol.ParameterInformation{
				{Label: protocol.ParameterInformationLabelTuple{2, 7}},
				{Label: protocol.ParameterInformationLabelTuple{9, 14}},
			},
		}},
	}
	got := projectSignature(help)
	if got == nil {
		t.Fatal("a signature with one overload projected nothing")
	}
	if got.Active != 1 {
		t.Fatalf("active parameter = %d, want 1", got.Active)
	}
	if got.Parameters[1].Label != "b int" {
		t.Errorf("active parameter label = %q, want %q", got.Parameters[1].Label, "b int")
	}
	if got.Doc != "does a thing" {
		t.Errorf("doc = %q", got.Doc)
	}

	// The string form is the older shape and still has to resolve to offsets.
	help.Signatures[0].Parameters = []protocol.ParameterInformation{{Label: protocol.String("b int")}}
	help.Signatures[0].ActiveParameter = protocol.NewNullable(uint32(0))
	if got := projectSignature(help); got.Parameters[0].Start != 9 || got.Parameters[0].End != 14 {
		t.Errorf("string-form parameter resolved to [%d,%d), want [9,14)", got.Parameters[0].Start, got.Parameters[0].End)
	}
	if projectSignature(nil) != nil || projectSignature(&protocol.SignatureHelp{}) != nil {
		t.Error("an empty signature help should project nothing")
	}
}

func TestUTF16OffsetToRune(t *testing.T) {
	runes := []rune("𝄞ab") // the clef is two UTF-16 units
	for _, tc := range []struct{ offset, want int }{{0, 0}, {2, 1}, {3, 2}, {4, 3}, {1, -1}, {9, -1}} {
		if got := utf16OffsetToRune(runes, tc.offset); got != tc.want {
			t.Errorf("offset %d = %d, want %d", tc.offset, got, tc.want)
		}
	}
}

func TestLSPProvidedReadsCapabilityUnions(t *testing.T) {
	if lspProvided(nil) || lspProvided(protocol.Boolean(false)) {
		t.Error("an absent or false capability must read as unsupported")
	}
	if !lspProvided(protocol.Boolean(true)) || !lspProvided(&protocol.HoverOptions{}) {
		t.Error("a true flag and an options struct both mean supported")
	}
}
