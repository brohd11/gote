package app

import (
	"bytes"
	"errors"
	"io"
	"os"
	"unicode/utf8"
)

// sniffLen is how much of a file's head the text test reads. Enough to catch every
// binary format's magic bytes and any realistic UTF-8 breakage, small enough that a
// recursive scan pays one page read per candidate.
const sniffLen = 512

// isTextFile reports whether path holds text gote can edit, judged from its first
// sniffLen bytes: a NUL byte or invalid UTF-8 means binary. This is what makes an
// unconfigured gote list every text file rather than one extension — including files
// with no extension at all (Makefile, LICENSE, .gitignore).
//
// An empty file is text. That is not a nicety: createDoc writes a zero-byte file and
// createFile reseeds immediately after, so calling an empty file binary would make
// every newly created doc vanish from the list it was just created in.
func isTextFile(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false // unreadable, or a symlink pointing at a directory
	}
	defer f.Close()

	var b [sniffLen]byte
	n, err := f.Read(b[:])
	if err != nil && !errors.Is(err, io.EOF) {
		return false
	}
	if n == 0 {
		return true // an empty file reads (0, io.EOF)
	}
	buf := b[:n]
	if bytes.IndexByte(buf, 0) >= 0 {
		return false
	}
	if n == sniffLen {
		// The read boundary can cut a multi-byte rune in half; that is our truncation,
		// not the file's corruption. Drop up to UTFMax-1 trailing bytes that don't
		// decode, then judge what's left.
		for i := 0; i < utf8.UTFMax-1 && len(buf) > 0; i++ {
			if r, size := utf8.DecodeLastRune(buf); r != utf8.RuneError || size != 1 {
				break
			}
			buf = buf[:len(buf)-1]
		}
	}
	return utf8.Valid(buf)
}
