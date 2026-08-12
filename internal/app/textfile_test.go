package app

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestIsTextFile covers the sniff's whole decision surface: the ordinary text and
// binary cases, and the three edges that decide whether a real store lists correctly.
func TestIsTextFile(t *testing.T) {
	dir := t.TempDir()

	cases := []struct {
		name    string
		content []byte
		want    bool
		why     string
	}{
		{"plain.md", []byte("# heading\n\nbody\n"), true, "ordinary text"},
		{"code.go", []byte("package main\n\nfunc main() {}\n"), true, "source is text"},
		{"utf8.txt", []byte("héllo — naïve ☃\n"), true, "multi-byte UTF-8 is text"},
		{"crlf.txt", []byte("line\r\nline\r\n"), true, "CRLF is text"},
		{"tabs.tsv", []byte("a\tb\tc\n"), true, "tabs and control whitespace are text"},
		// An empty file must be text: createDoc writes zero bytes and createFile
		// reseeds straight after, so calling this binary would hide every new doc.
		{"empty", nil, true, "an empty file is an empty document"},
		{"png", []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR"), false, "a NUL means binary"},
		{"utf16.txt", []byte("h\x00e\x00l\x00l\x00o\x00"), false, "UTF-16 reads as NUL-laden"},
		{"latin1.txt", []byte("caf\xe9 na\xefve\n"), false, "a lone high byte is not valid UTF-8"},
	}

	for _, tc := range cases {
		p := filepath.Join(dir, tc.name)
		if err := os.WriteFile(p, tc.content, 0o644); err != nil {
			t.Fatal(err)
		}
		if got := isTextFile(p); got != tc.want {
			t.Errorf("isTextFile(%s) = %v, want %v — %s", tc.name, got, tc.want, tc.why)
		}
	}
}

// TestIsTextFileTruncatedRune: the sniff reads a fixed prefix, so a multi-byte rune
// straddling the boundary is our cut, not the file's corruption, and must not condemn
// an otherwise fine document.
func TestIsTextFileTruncatedRune(t *testing.T) {
	dir := t.TempDir()

	// Land a 3-byte rune so it starts at byte 511 and is cut after one byte.
	var buf bytes.Buffer
	buf.WriteString(strings.Repeat("a", sniffLen-1))
	buf.WriteString("☃")
	buf.WriteString(strings.Repeat("b", 64))
	if buf.Len() <= sniffLen {
		t.Fatalf("fixture must exceed the sniff window, got %d bytes", buf.Len())
	}

	p := filepath.Join(dir, "long.md")
	if err := os.WriteFile(p, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	if !isTextFile(p) {
		t.Fatal("a rune truncated by the sniff boundary should not read as binary")
	}
}

// TestIsTextFileUnreadable: what cannot be opened is not a document. A directory
// stands in for the general case (a dangling symlink, a permission-denied file).
func TestIsTextFileUnreadable(t *testing.T) {
	dir := t.TempDir()
	if isTextFile(filepath.Join(dir, "does-not-exist")) {
		t.Fatal("a missing file is not text")
	}
	if isTextFile(dir) {
		t.Fatal("a directory is not text")
	}
}

// TestIsTextFileBinaryPastWindow documents the limit honestly: a file whose binary
// content starts past the sniff window reads as text. That is the price of a fixed
// prefix, and it is the right trade for a recursive scan.
func TestIsTextFileBinaryPastWindow(t *testing.T) {
	p := filepath.Join(t.TempDir(), "late.bin")
	content := append([]byte(strings.Repeat("a", sniffLen)), 0x00, 0x00)
	if err := os.WriteFile(p, content, 0o644); err != nil {
		t.Fatal(err)
	}
	if !isTextFile(p) {
		t.Fatal("the sniff judges the prefix only; this documents that, change it deliberately")
	}
}
