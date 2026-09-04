package app

import (
	"reflect"
	"testing"

	"github.com/brohd11/bubblestack/components/editor"
)

func TestParseLSPSnippetPracticalSubset(t *testing.T) {
	text, stops, ok := parseLSPSnippet(`call(${1:first}, ${2|second,other|})$0`)
	if !ok || text != "call(first, second)" {
		t.Fatalf("parsed snippet = %q, %v", text, ok)
	}
	want := []editor.CompletionStop{
		{Index: 1, Start: 5, End: 10},
		{Index: 2, Start: 12, End: 18},
		{Index: 0, Start: 19, End: 19},
	}
	if !reflect.DeepEqual(stops, want) {
		t.Fatalf("stops = %#v, want %#v", stops, want)
	}

	text, stops, ok = parseLSPSnippet("héllo\\$ ${1:a\\}b}\r\n$0")
	if !ok || text != "héllo$ a}b\n" || !reflect.DeepEqual(stops, []editor.CompletionStop{
		{Index: 1, Start: 7, End: 10}, {Index: 0, Start: 11, End: 11},
	}) {
		t.Fatalf("unicode/escaped snippet = %q %#v %v", text, stops, ok)
	}
}

func TestParseLSPSnippetRejectsUnsupportedOrMalformedForms(t *testing.T) {
	for _, source := range []string{
		`${TM_FILENAME}`, `${1/foo/bar/}`, `${1:${2:nested}}`, `$1 + $1`, `${1:unterminated`,
	} {
		if text, stops, ok := parseLSPSnippet(source); ok {
			t.Errorf("parseLSPSnippet(%q) = %q %#v, want rejected", source, text, stops)
		}
	}
}
