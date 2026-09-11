package app

import (
	"testing"

	"github.com/brohd11/bubblestack/components/editor"

	"charm.land/lipgloss/v2"
)

// Every semantic-token tuple is relative to the one before it. deltaStart changes
// meaning when the row advances: it is relative on the same row and absolute on a new
// row. Getting that rule wrong silently shifts the rest of an answer.
func TestDecodeSemanticTokensRelativeEncoding(t *testing.T) {
	legend := []string{"variable", "type", "function"}
	slots := map[string]string{"type": "type", "function": "func"}
	data := []uint32{
		0, 4, 3, 1, 0,
		0, 8, 5, 2, 0,
		2, 2, 4, 1, 0,
	}
	want := []semanticToken{
		{line: 0, startUTF16: 4, lenUTF16: 3, slot: "type"},
		{line: 0, startUTF16: 12, lenUTF16: 5, slot: "func"},
		{line: 2, startUTF16: 2, lenUTF16: 4, slot: "type"},
	}
	got := decodeSemanticTokens(data, legend, slots)
	if len(got) != len(want) {
		t.Fatalf("decoded %d tokens, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("token %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestDecodeSemanticTokensSkipsUnmapped(t *testing.T) {
	legend := []string{"variable", "type"}
	slots := map[string]string{"type": "type"}
	data := []uint32{
		0, 0, 3, 0, 0,
		0, 4, 3, 1, 0,
		0, 4, 3, 9, 0,
		0, 4, 3,
	}
	got := decodeSemanticTokens(data, legend, slots)
	if len(got) != 1 {
		t.Fatalf("decoded %d tokens, want 1: %+v", len(got), got)
	}
	if got[0].line != 0 || got[0].startUTF16 != 4 || got[0].slot != "type" {
		t.Errorf("token = %+v, want the type at row 0 char 4", got[0])
	}
}

func TestSemanticHighlightRangesConvertsUTF16ToRunes(t *testing.T) {
	applySyntaxPalette(defaultSyntaxColors())
	ed := editor.New(editor.Opts{})
	ed.SetText(`s := "🚀" + Foo`)

	got := semanticHighlightRanges(ed, []semanticToken{
		{line: 0, startUTF16: 12, lenUTF16: 3, slot: "type"},
	})
	if len(got) != 1 {
		t.Fatalf("got %d ranges, want 1: %+v", len(got), got)
	}
	if got[0].Range.Start != (editor.Position{Line: 0, Column: 11}) ||
		got[0].Range.End != (editor.Position{Line: 0, Column: 14}) {
		t.Errorf("range = %+v, want row 0 columns [11,14)", got[0].Range)
	}
	if got[0].Style == nil || got[0].Style.Render("z") != chTypeStyle.Render("z") {
		t.Error("range did not resolve the type slot")
	}
}

// Positional ranges allow identical lines to carry different semantic meaning. The old
// line-content cache had to drop this case as ambiguous.
func TestSemanticHighlightRangesKeepIdenticalRowsDistinct(t *testing.T) {
	applySyntaxPalette(defaultSyntaxColors())
	ed := editor.New(editor.Opts{})
	ed.SetText("a.Run()\na.Run()")

	got := semanticHighlightRanges(ed, []semanticToken{
		{line: 0, startUTF16: 2, lenUTF16: 3, slot: "func"},
		{line: 1, startUTF16: 2, lenUTF16: 3, slot: "type"},
	})
	if len(got) != 2 {
		t.Fatalf("got %d ranges, want 2: %+v", len(got), got)
	}
	if got[0].Range.Start.Line != 0 || got[1].Range.Start.Line != 1 {
		t.Errorf("rows = %d, %d; want 0, 1", got[0].Range.Start.Line, got[1].Range.Start.Line)
	}
	if got[0].Style.Render("z") == got[1].Style.Render("z") {
		t.Error("distinct semantic slots on identical lines collapsed to one style")
	}
}

func TestSemanticHighlightRangesDropMalformedTokens(t *testing.T) {
	ed := editor.New(editor.Opts{})
	ed.SetText("Foo")
	got := semanticHighlightRanges(ed, []semanticToken{
		{line: 1, lenUTF16: 1, slot: "type"},
		{line: 0, startUTF16: 4, lenUTF16: 1, slot: "type"},
		{line: 0, lenUTF16: 0, slot: "type"},
		{line: 0, lenUTF16: 3, slot: "unknown"},
	})
	if len(got) != 0 {
		t.Errorf("kept malformed ranges: %+v", got)
	}
}

func TestSemanticSlotsOverride(t *testing.T) {
	merged := semanticSlots(map[string]string{"builtinType": "type", "comment": ""})
	if merged["builtinType"] != "type" {
		t.Errorf("override not applied: %q", merged["builtinType"])
	}
	if _, ok := merged["comment"]; ok {
		t.Error("empty override did not disable the type")
	}
	if merged["function"] != "func" {
		t.Errorf("defaults lost through the merge: %q", merged["function"])
	}
	if _, ok := defaultSemanticSlots["builtinType"]; ok {
		t.Error("merge wrote through to the shared default map")
	}
	if defaultSemanticSlots["comment"] != "comment" {
		t.Error("merge deleted from the shared default map")
	}
}

func TestSlotStyleNames(t *testing.T) {
	for tokenType, slot := range defaultSemanticSlots {
		if _, ok := slotStyle(slot); !ok {
			t.Errorf("%s maps to slot %q, which is not a palette slot", tokenType, slot)
		}
	}
	if _, ok := slotStyle("not_a_slot"); ok {
		t.Error("slotStyle accepted an unknown name")
	}
	var zero lipgloss.Style
	if style, _ := slotStyle("not_a_slot"); style.Render("z") != zero.Render("z") {
		t.Error("slotStyle returned a non-zero style for an unknown name")
	}
}
