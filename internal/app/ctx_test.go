package app

import (
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
			c.OpenDoc(p, nil, nil)
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
