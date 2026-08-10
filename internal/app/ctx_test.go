package app

import (
	"path/filepath"
	"testing"

	"github.com/brohd11/bubblestack/components"
)

// TestCloseDoc covers the open-set removal: the next doc is the one after the closed
// one in open order, falling back to the new last at the tail, "" when none remain,
// and unknown/empty paths are no-ops (the scratch editor's exit rides on the last one).
func TestCloseDoc(t *testing.T) {
	newCtx := func(paths ...string) *Ctx {
		c := &Ctx{Open: map[string]*components.EditorScreen{}}
		for _, p := range paths {
			c.OpenDoc(p, components.EditorOpts{})
		}
		return c
	}
	order := func(c *Ctx) []string { return append([]string{}, c.OpenOrder...) }

	c := newCtx("a", "b", "c")
	if next := c.CloseDoc("b"); next != "c" {
		t.Fatalf("closing the middle doc should switch to the next one, got %q", next)
	}
	if got := order(c); len(got) != 2 || got[0] != "a" || got[1] != "c" {
		t.Fatalf("open order after close = %v, want [a c]", got)
	}
	if _, ok := c.Open["b"]; ok {
		t.Fatal("the closed doc must leave the open set")
	}

	c = newCtx("a", "b", "c")
	if next := c.CloseDoc("c"); next != "b" {
		t.Fatalf("closing the tail should switch to the new last, got %q", next)
	}

	c = newCtx("a")
	if next := c.CloseDoc("a"); next != "" {
		t.Fatalf("closing the only doc should leave nothing, got %q", next)
	}
	if len(c.OpenDocs()) != 0 {
		t.Fatal("the open set should be empty")
	}

	c = newCtx("a")
	if next := c.CloseDoc("zzz"); next != "" || len(order(c)) != 1 {
		t.Fatalf("an unknown path is a no-op: next %q, order %v", next, order(c))
	}
	if next := c.CloseDoc(""); next != "" || len(order(c)) != 1 {
		t.Fatalf("the scratch path is a no-op: next %q, order %v", next, order(c))
	}
}

// TestRekeyDoc covers what a save-as does to the open set: the buffer keeps its
// identity as an editor and changes it as a path, in place, without disturbing the
// rows around it.
func TestRekeyDoc(t *testing.T) {
	newCtx := func(paths ...string) *Ctx {
		c := &Ctx{Open: map[string]*components.EditorScreen{}}
		for _, p := range paths {
			c.OpenDoc(p, components.EditorOpts{})
		}
		return c
	}
	order := func(c *Ctx) []string { return append([]string{}, c.OpenOrder...) }
	eq := func(t *testing.T, got, want []string) {
		t.Helper()
		if len(got) != len(want) {
			t.Fatalf("open order = %v, want %v", got, want)
		}
		for i := range got {
			if got[i] != want[i] {
				t.Fatalf("open order = %v, want %v", got, want)
			}
		}
	}

	// A rename keeps the row where it was, so the selection doesn't jump.
	c := newCtx("a", "b", "c")
	ed := c.Open["b"]
	c.RekeyDoc("b", "b2", ed)
	eq(t, order(c), []string{"a", "b2", "c"})
	if c.Open["b2"] != ed {
		t.Fatal("the renamed path should answer with the same editor")
	}
	if _, ok := c.Open["b"]; ok {
		t.Fatal("the old path must leave the open set")
	}

	// The scratch buffer has no path at all until a save gives it one.
	c = newCtx("a")
	scratch := components.NewEditorScreen(components.EditorOpts{})
	c.RekeyDoc("", "fresh.md", scratch)
	eq(t, order(c), []string{"a", "fresh.md"})
	if c.Open["fresh.md"] != scratch {
		t.Fatal("saving the scratch buffer should register it")
	}

	// Saving onto a path another buffer holds leaves one row for it, not two.
	c = newCtx("a", "b")
	ed = c.Open["a"]
	c.RekeyDoc("a", "b", ed)
	eq(t, order(c), []string{"b"})
	if c.Open["b"] != ed {
		t.Fatal("the saved buffer should be the one the path resolves to")
	}

	// The ordinary same-path save changes nothing.
	c = newCtx("a", "b")
	ed = c.Open["a"]
	c.RekeyDoc("a", "a", ed)
	eq(t, order(c), []string{"a", "b"})
	if c.Open["a"] != ed {
		t.Fatal("a same-path save must leave the entry alone")
	}
}

func TestOpenDocsKeepOriginRoot(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "notes", "todo.md")
	c := &Ctx{
		Mode:      ModeScan,
		ScanDir:   root,
		Files:     []DocFile{{Name: "todo.md", Path: path, Root: root}},
		Open:      map[string]*components.EditorScreen{},
		OpenRoots: map[string]string{},
	}
	ed := c.OpenDoc(path, components.EditorOpts{})

	c.Mode = ModeHome
	docs := c.OpenDocs()
	if len(docs) != 1 || docs[0].Root != root {
		t.Fatalf("open doc root after mode switch = %v, want %q", docs, root)
	}

	renamed := filepath.Join(root, "archive", "todo.md")
	c.RekeyDoc(path, renamed, ed)
	docs = c.OpenDocs()
	if len(docs) != 1 || docs[0].Path != renamed || docs[0].Root != root {
		t.Fatalf("rekeyed open doc = %v, want path %q rooted at %q", docs, renamed, root)
	}
	c.CloseDoc(renamed)
	if _, ok := c.OpenRoots[renamed]; ok {
		t.Fatal("closing a doc must remove its origin root")
	}
}
