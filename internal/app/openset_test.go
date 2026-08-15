package app

import (
	"testing"

	"github.com/brohd11/bubblestack/components"
)

// seeded builds an openSet holding a fresh editor per path, in the given order.
func seeded(paths ...string) *openSet {
	o := newOpenSet()
	for _, p := range paths {
		o.add(p, "/root", components.NewEditorScreen(components.EditorOpts{}))
	}
	return &o
}

func wantOrder(t *testing.T, o *openSet, want ...string) {
	t.Helper()
	if len(o.order) != len(want) {
		t.Fatalf("order = %v, want %v", o.order, want)
	}
	for i := range want {
		if o.order[i] != want[i] {
			t.Fatalf("order = %v, want %v", o.order, want)
		}
	}
}

// wantConsistent is the invariant the whole type exists to hold: the three structures
// describe the same set of paths. Every test below asserts it after mutating.
func wantConsistent(t *testing.T, o *openSet) {
	t.Helper()
	if len(o.byPath) != len(o.order) || len(o.roots) != len(o.order) {
		t.Fatalf("structures disagree: order=%v byPath=%d roots=%d", o.order, len(o.byPath), len(o.roots))
	}
	for _, p := range o.order {
		if _, ok := o.byPath[p]; !ok {
			t.Errorf("%q is in the order but has no editor", p)
		}
		if _, ok := o.roots[p]; !ok {
			t.Errorf("%q is in the order but has no root", p)
		}
	}
}

// TestOpenSetRekeyKeepsSlot is the save-as case: the row must not move, or the selection
// jumps out from under the user mid-save.
func TestOpenSetRekeyKeepsSlot(t *testing.T) {
	o := seeded("a", "b", "c")
	ed := o.byPath["b"]

	o.rekey("b", "b2", ed)

	wantOrder(t, o, "a", "b2", "c")
	wantConsistent(t, o)
	if got, _ := o.get("b2"); got != ed {
		t.Error("the renamed path should answer with the same editor")
	}
	if _, ok := o.get("b"); ok {
		t.Error("the old path must leave the set")
	}
}

// TestOpenSetRekeyUntracked covers the scratch buffer, which has no path at all until a
// save gives it one — and the case where that save lands on a path already open.
func TestOpenSetRekeyUntracked(t *testing.T) {
	o := seeded("a")
	scratch := components.NewEditorScreen(components.EditorOpts{})
	o.rekey("", "fresh.md", scratch)
	wantOrder(t, o, "a", "fresh.md")
	wantConsistent(t, o)

	// Saving the scratch buffer onto a path another buffer holds: one row, not two, and
	// the row resolves to the buffer that was just written.
	o = seeded("a", "b")
	scratch = components.NewEditorScreen(components.EditorOpts{})
	o.rekey("", "b", scratch)
	wantOrder(t, o, "a", "b")
	wantConsistent(t, o)
	if got, _ := o.get("b"); got != scratch {
		t.Error("the saved buffer should be the one the path resolves to")
	}
}

// TestOpenSetRekeyInheritsRoot: a renamed buffer keeps the root it was discovered under,
// so a mode switch doesn't relocate it. A previously untracked one falls back to its
// containing directory.
func TestOpenSetRekeyInheritsRoot(t *testing.T) {
	o := newOpenSet()
	ed := components.NewEditorScreen(components.EditorOpts{})
	o.add("/vault/notes/a.md", "/vault", ed)
	o.rekey("/vault/notes/a.md", "/vault/notes/b.md", ed)
	if got := o.roots["/vault/notes/b.md"]; got != "/vault" {
		t.Errorf("root after rename = %q, want the original /vault", got)
	}

	scratch := components.NewEditorScreen(components.EditorOpts{})
	o.rekey("", "/elsewhere/new.md", scratch)
	if got := o.roots["/elsewhere/new.md"]; got != "/elsewhere" {
		t.Errorf("root for a newly identified buffer = %q, want its directory", got)
	}
}

// TestOpenSetRemove covers which doc the UI switches to after a close: the one after it,
// else the new last, else nothing.
func TestOpenSetRemove(t *testing.T) {
	o := seeded("a", "b", "c")
	if next := o.remove("b"); next != "c" {
		t.Errorf("closing the middle doc = %q, want the next one (c)", next)
	}
	wantOrder(t, o, "a", "c")
	wantConsistent(t, o)

	o = seeded("a", "b", "c")
	if next := o.remove("c"); next != "b" {
		t.Errorf("closing the tail = %q, want the new last (b)", next)
	}

	o = seeded("a")
	if next := o.remove("a"); next != "" {
		t.Errorf("closing the only doc = %q, want nothing", next)
	}
	wantConsistent(t, o)
	if o.len() != 0 {
		t.Error("the set should be empty")
	}

	// An unknown path and the scratch path are both no-ops — the scratch editor's exit
	// rides on the second one.
	o = seeded("a")
	if next := o.remove("zzz"); next != "" || o.len() != 1 {
		t.Errorf("unknown path: next %q, len %d", next, o.len())
	}
	if next := o.remove(""); next != "" || o.len() != 1 {
		t.Errorf("scratch path: next %q, len %d", next, o.len())
	}
}

// TestOpenSetResetClearsAll: a vault switch closes the whole session, and must not leave
// one of the three structures populated.
func TestOpenSetResetClearsAll(t *testing.T) {
	o := seeded("a", "b")
	o.reset()
	wantConsistent(t, o)
	if o.len() != 0 || len(o.byPath) != 0 || len(o.roots) != 0 {
		t.Fatalf("reset left state: order=%v byPath=%v roots=%v", o.order, o.byPath, o.roots)
	}
	// The zeroed set must still be usable — reset is not a teardown.
	o.add("c", "/root", components.NewEditorScreen(components.EditorOpts{}))
	wantOrder(t, o, "c")
	wantConsistent(t, o)
}

// TestOpenSetEachInOrder: the open-docs list reads through each, so it must visit in
// opening order and hand back the editor registered for the path.
func TestOpenSetEachInOrder(t *testing.T) {
	o := seeded("a", "b", "c")
	var seen []string
	o.each(func(path string, ed *components.EditorScreen) {
		if ed == nil {
			t.Errorf("%q visited with a nil editor", path)
		}
		if ed != o.byPath[path] {
			t.Errorf("%q visited with the wrong editor", path)
		}
		seen = append(seen, path)
	})
	if len(seen) != 3 || seen[0] != "a" || seen[1] != "b" || seen[2] != "c" {
		t.Errorf("each visited %v, want [a b c]", seen)
	}
}
