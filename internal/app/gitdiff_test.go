package app

import (
	"strings"
	"testing"
)

func hunkText(h diffHunk) string {
	var lines []string
	for _, line := range h.lines {
		lines = append(lines, string(line.kind)+line.text)
	}
	return strings.Join(lines, "\n")
}

func TestDiffHunks(t *testing.T) {
	for _, tc := range []struct {
		name, base, buf, header, text string
	}{
		{"replace", "a\nb\nc\n", "a\nB\nc\n", "@@ -1,3 +1,3 @@", " a\n-b\n+B\n c"},
		{"insert", "a\nb\n", "a\nX\nb\n", "@@ -1,2 +1,3 @@", " a\n+X\n b"},
		{"delete top", "a\nb\n", "b\n", "@@ -1,2 +1,1 @@", "-a\n b"},
		{"delete end", "a\nb\n", "a\n", "@@ -1,2 +1,1 @@", " a\n-b"},
		{"delete all", "a\nb\n", "", "@@ -1,2 +0,0 @@", "-a\n-b"},
		{"new file", "", "a\nb\n", "@@ -0,0 +1,2 @@", "+a\n+b"},
		{"remove newline", "a\n", "a", "@@ -1,1 +1,1 @@", "-a\n+a\n\\No newline at end of file"},
		{"add newline", "a", "a\n", "@@ -1,1 +1,1 @@", "-a\n\\No newline at end of file\n+a"},
		{"unicode", "α\n猫\n", "α\n犬\n", "@@ -1,2 +1,2 @@", " α\n-猫\n+犬"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			hunks := diffHunks(tc.base, tc.buf)
			if len(hunks) != 1 {
				t.Fatalf("hunks = %+v", hunks)
			}
			if got := hunks[0].header(); got != tc.header {
				t.Errorf("header = %q, want %q", got, tc.header)
			}
			if got := hunkText(hunks[0]); got != tc.text {
				t.Errorf("diff = %q, want %q", got, tc.text)
			}
			// Every drawn marker must open a hunk, including the synthetic EOF line.
			for line := range diffMarkers(tc.base, tc.buf) {
				if !hunks[0].contains(line, bufLines(tc.buf)) {
					t.Errorf("marker on %d cannot open its hunk", line)
				}
			}
		})
	}
	for _, text := range []string{"", "same", "same\n"} {
		if hunks := diffHunks(text, text); len(hunks) != 0 {
			t.Errorf("unchanged %q made hunks: %+v", text, hunks)
		}
	}
}

func TestDiffHunkContextSelectionAndJoining(t *testing.T) {
	base := "0\n1\n2\n3\n4\n5\n6\n7\n8\n9\na\nb\nc\nd\ne\nf\ng\nh\ni\nj\n"
	buf := strings.Replace(base, "4\n", "FOUR\n", 1)
	hunks := diffHunks(base, buf)
	if len(hunks) != 1 || hunks[0].newStart != 1 || hunks[0].newEnd != 8 {
		t.Fatalf("context range = %+v", hunks)
	}
	for line := range bufLines(buf) {
		if got, want := hunks[0].contains(line, bufLines(buf)), line >= 1 && line < 8; got != want {
			t.Errorf("contains(%d) = %v, want %v", line, got, want)
		}
	}
	// Six unchanged lines between edits means their context touches; seven separates.
	if got := len(diffHunks(base, strings.Replace(buf, "b\n", "B\n", 1))); got != 1 {
		t.Errorf("touching context made %d hunks", got)
	}
	far := strings.Replace(buf, "c\n", "C\n", 1)
	if got := len(diffHunks(base, far)); got != 2 {
		t.Errorf("separated context made %d hunks", got)
	}
	// A large insertion shifts the next hunk's current-side coordinates.
	shifted := "extra\nextra2\n" + far
	hunks = diffHunks(base, shifted)
	if len(hunks) != 2 || hunks[1].oldStart != 9 || hunks[1].newStart != 11 {
		t.Fatalf("shifted second hunk = %+v", hunks)
	}
}
