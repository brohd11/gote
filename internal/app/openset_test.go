package app

import (
	"path/filepath"
	"testing"

	"github.com/brohd11/bubblestack/components/editor"
)

// seeded builds an openSet holding a fresh editor per path, in the given order.
func seeded(paths ...string) *openSet {
	o := newOpenSet()
	for _, p := range paths {
		o.addFile(p, "/root", editor.New(editor.Opts{}))
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
	if len(o.byID) != len(o.order) {
		t.Fatalf("structures disagree: order=%v byID=%d", o.order, len(o.byID))
	}
	for _, id := range o.order {
		entry, ok := o.byID[id]
		if !ok || entry == nil || entry.editor == nil {
			t.Errorf("%q is in the order but has no editor", id)
		}
		if entry != nil && entry.path != "" && o.byPath[entry.path] != id {
			t.Errorf("%q has no matching path index", id)
		}
	}
}

// TestOpenSetRekeyKeepsSlot is the save-as case: the row must not move, or the selection
// jumps out from under the user mid-save.
func TestOpenSetRekeyKeepsSlot(t *testing.T) {
	o := seeded("a", "b", "c")
	ed := o.byID["b"].editor

	o.rekey("b", "b2", ed)

	wantOrder(t, o, "a", "b2", "c")
	wantConsistent(t, o)
	if got, _ := o.get("b2"); got.editor != ed {
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
	scratch := editor.New(editor.Opts{})
	o.rekey("", "fresh.md", scratch)
	wantOrder(t, o, "a", "fresh.md")
	wantConsistent(t, o)

	// Saving the scratch buffer onto a path another buffer holds: one row, not two, and
	// the row resolves to the buffer that was just written.
	o = seeded("a", "b")
	scratch = editor.New(editor.Opts{})
	o.rekey("", "b", scratch)
	wantOrder(t, o, "a", "b")
	wantConsistent(t, o)
	if got, _ := o.get("b"); got.editor != scratch {
		t.Error("the saved buffer should be the one the path resolves to")
	}
}

func TestOpenSetRetainsAndRekeysUnsaved(t *testing.T) {
	o := newOpenSet()
	first := editor.New(editor.Opts{})
	second := editor.New(editor.Opts{})
	o.addUnsaved("u1", "unsaved_1", first)
	o.addUnsaved("u2", "unsaved_2", second)

	wantOrder(t, &o, "u1", "u2")
	wantConsistent(t, &o)
	docs := o.docs()
	if len(docs) != 2 || docs[0].Name != "unsaved_1" || docs[0].Path != "" || docs[1].Name != "unsaved_2" {
		t.Fatalf("unsaved docs = %+v", docs)
	}

	o.rekey("u1", "/root/saved.md", first)
	wantOrder(t, &o, "/root/saved.md", "u2")
	wantConsistent(t, &o)
	entry, ok := o.getPath("/root/saved.md")
	if !ok || entry.editor != first || entry.name != "saved.md" {
		t.Fatalf("saved entry = %+v, ok=%v", entry, ok)
	}
}

// TestOpenSetRekeyInheritsRoot: a renamed buffer keeps the root it was discovered under,
// so a mode switch doesn't relocate it. A previously untracked one falls back to its
// containing directory.
func TestOpenSetRekeyInheritsRoot(t *testing.T) {
	o := newOpenSet()
	ed := editor.New(editor.Opts{})
	o.addFile("/vault/notes/a.md", "/vault", ed)
	o.rekey("/vault/notes/a.md", "/vault/notes/b.md", ed)
	if got := o.byID["/vault/notes/b.md"].root; got != "/vault" {
		t.Errorf("root after rename = %q, want the original /vault", got)
	}

	scratch := editor.New(editor.Opts{})
	path := filepath.Join(string(filepath.Separator), "elsewhere", "new.md")
	o.rekey("", path, scratch)
	if got := o.byID[path].root; got != filepath.Dir(path) {
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
	if o.len() != 0 || len(o.byID) != 0 || len(o.byPath) != 0 {
		t.Fatalf("reset left state: order=%v byID=%v byPath=%v", o.order, o.byID, o.byPath)
	}
	// The zeroed set must still be usable — reset is not a teardown.
	o.addFile("c", "/root", editor.New(editor.Opts{}))
	wantOrder(t, o, "c")
	wantConsistent(t, o)
}

// TestOpenSetEachInOrder: the open-docs list reads through each, so it must visit in
// opening order and hand back the editor registered for the path.
func TestOpenSetEachInOrder(t *testing.T) {
	o := seeded("a", "b", "c")
	var seen []string
	o.each(func(entry *openEntry) {
		if entry.editor == nil {
			t.Errorf("%q visited with a nil editor", entry.id)
		}
		if entry != o.byID[entry.id] {
			t.Errorf("%q visited with the wrong entry", entry.id)
		}
		seen = append(seen, entry.id)
	})
	if len(seen) != 3 || seen[0] != "a" || seen[1] != "b" || seen[2] != "c" {
		t.Errorf("each visited %v, want [a b c]", seen)
	}
}
