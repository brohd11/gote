package app

import (
	"path/filepath"

	"github.com/brohd11/bubblestack/components"
)

// openSet is the set of open buffers: the editor keyed by path, the order they were opened
// in (what the open-docs list shows), and each path's origin root (which mode switches must
// not disturb).
//
// The three move together on every operation — an add touches all three, a close removes
// from all three, and a save-as has to rewrite a key in two maps while holding a slot in the
// slice. Keeping them in step was previously the caller's job at each site; making them one
// type is what stops a future operation from updating two of the three.
type openSet struct {
	byPath map[string]*components.EditorScreen
	order  []string          // insertion order; the open-docs list reads it
	roots  map[string]string // path → the root it was discovered under
}

func newOpenSet() openSet {
	return openSet{
		byPath: map[string]*components.EditorScreen{},
		roots:  map[string]string{},
	}
}

// reset empties the set, for a vault switch closing the whole session.
func (o *openSet) reset() { *o = newOpenSet() }

// get returns the editor registered for path, if any.
func (o *openSet) get(path string) (*components.EditorScreen, bool) {
	ed, ok := o.byPath[path]
	return ed, ok
}

// len reports how many buffers are open.
func (o *openSet) len() int { return len(o.order) }

// add registers ed under path at the end of the order. The caller supplies root because
// only the Ctx knows how a path was discovered (see Ctx.rootForPath).
func (o *openSet) add(path, root string, ed *components.EditorScreen) {
	if o.byPath == nil {
		o.byPath = map[string]*components.EditorScreen{}
	}
	if o.roots == nil {
		o.roots = map[string]string{}
	}
	o.byPath[path] = ed
	o.roots[path] = root
	o.order = append(o.order, path)
}

// rekey re-files ed from old to newPath after a save-as. old == "" registers a buffer that
// was not tracked at all (the scratch editor, which saving is exactly what gives an
// identity). The entry keeps its slot in the order so the open-docs list doesn't jump under
// the selection. Saving over a path some OTHER buffer already holds drops that buffer's
// entry: the list is keyed by path, and two rows for one file would both claim to be it.
func (o *openSet) rekey(old, newPath string, ed *components.EditorScreen) {
	root := o.roots[old]
	if root == "" {
		root = filepath.Dir(newPath)
	}
	delete(o.byPath, old)
	delete(o.roots, old)
	if o.byPath == nil {
		o.byPath = map[string]*components.EditorScreen{}
	}
	if o.roots == nil {
		o.roots = map[string]string{}
	}
	o.byPath[newPath] = ed
	o.roots[newPath] = root

	at := -1
	filtered := o.order[:0]
	for _, p := range o.order {
		switch p {
		case old:
			at = len(filtered)
			filtered = append(filtered, newPath) // in place, so the row stays put
		case newPath:
			if at < 0 && old == "" {
				at = len(filtered) // an untracked buffer taking an open path's slot
				filtered = append(filtered, newPath)
			}
		default:
			filtered = append(filtered, p)
		}
	}
	o.order = filtered
	if at < 0 {
		o.order = append(o.order, newPath)
	}
}

// remove drops path and reports the doc to switch to: the one after it in open order, else
// the new last, else "" when none remain. An unknown path is a no-op returning "".
func (o *openSet) remove(path string) (next string) {
	if _, ok := o.byPath[path]; !ok {
		return ""
	}
	delete(o.byPath, path)
	delete(o.roots, path)
	for i, p := range o.order {
		if p != path {
			continue
		}
		o.order = append(o.order[:i], o.order[i+1:]...)
		switch {
		case i < len(o.order):
			return o.order[i]
		case len(o.order) > 0:
			return o.order[len(o.order)-1]
		}
		return ""
	}
	return ""
}

// docs lists the open buffers in opening order, for the open-docs list.
func (o *openSet) docs() []DocFile {
	docs := make([]DocFile, 0, len(o.order))
	for _, path := range o.order {
		docs = append(docs, DocFile{Name: docName(path), Path: path, Root: o.roots[path]})
	}
	return docs
}

// each visits every open buffer in opening order.
func (o *openSet) each(fn func(path string, ed *components.EditorScreen)) {
	for _, path := range o.order {
		fn(path, o.byPath[path])
	}
}
