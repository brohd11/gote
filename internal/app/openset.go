package app

import (
	"path/filepath"

	"github.com/brohd11/bubblestack/components/editor"
)

// openEntry is one retained editor buffer. id is its stable in-memory identity; path is
// empty until an unsaved buffer is written for the first time. Keeping those facts apart
// lets the Open list retain pathless buffers without handing a made-up filename to the
// editor, LSP, git gutter, or filesystem.
type openEntry struct {
	id     string
	name   string
	path   string
	root   string
	editor *editor.Screen
}

// openSet owns every retained buffer, in opening order. Saved buffers use their path as
// their id; unsaved buffers use an opaque id allocated by Ctx. byPath indexes real files
// so Ctx.OpenDoc can still answer "already open" by filename.
type openSet struct {
	byID   map[string]*openEntry
	byPath map[string]string // real path -> id
	order  []string          // ids, in opening order
}

func newOpenSet() openSet {
	return openSet{byID: map[string]*openEntry{}, byPath: map[string]string{}}
}

func (o *openSet) reset() { *o = newOpenSet() }

func (o *openSet) get(id string) (*openEntry, bool) {
	entry, ok := o.byID[id]
	return entry, ok
}

func (o *openSet) getPath(path string) (*openEntry, bool) {
	id, ok := o.byPath[path]
	if !ok {
		return nil, false
	}
	return o.get(id)
}

func (o *openSet) len() int { return len(o.order) }

func (o *openSet) addFile(path, root string, ed *editor.Screen) {
	o.add(openEntry{id: path, name: docName(path), path: path, root: root, editor: ed})
}

func (o *openSet) addUnsaved(id, name string, ed *editor.Screen) {
	o.add(openEntry{id: id, name: name, editor: ed})
}

func (o *openSet) add(entry openEntry) {
	if entry.id == "" || entry.editor == nil {
		return
	}
	if o.byID == nil {
		o.byID = map[string]*openEntry{}
	}
	if o.byPath == nil {
		o.byPath = map[string]string{}
	}
	copy := entry
	o.byID[entry.id] = &copy
	if entry.path != "" {
		o.byPath[entry.path] = entry.id
	}
	o.order = append(o.order, entry.id)
}

// rekey gives a buffer its saved-file identity. A tracked buffer keeps its own slot and
// displaces any other buffer already holding newPath. An untracked startup buffer adopts
// an existing target's slot, or appends when the target was not already open.
func (o *openSet) rekey(oldID, newPath string, ed *editor.Screen) {
	if newPath == "" || ed == nil {
		return
	}
	if entry, ok := o.get(oldID); ok && entry.path == newPath && entry.editor == ed {
		return
	}

	old, tracked := o.get(oldID)
	targetID := o.byPath[newPath]
	root := filepath.Dir(newPath)
	if tracked && old.root != "" {
		root = old.root
	}

	if tracked {
		delete(o.byID, oldID)
		if old.path != "" {
			delete(o.byPath, old.path)
		}
	}
	if targetID != "" {
		delete(o.byID, targetID)
		delete(o.byPath, newPath)
	}

	entry := &openEntry{id: newPath, name: docName(newPath), path: newPath, root: root, editor: ed}
	if o.byID == nil {
		o.byID = map[string]*openEntry{}
	}
	if o.byPath == nil {
		o.byPath = map[string]string{}
	}
	o.byID[newPath] = entry
	o.byPath[newPath] = newPath

	placed := false
	filtered := o.order[:0]
	for _, id := range o.order {
		switch {
		case tracked && id == oldID:
			if !placed {
				filtered = append(filtered, newPath)
				placed = true
			}
		case id == targetID:
			if !tracked && !placed {
				filtered = append(filtered, newPath)
				placed = true
			}
		default:
			filtered = append(filtered, id)
		}
	}
	o.order = filtered
	if !placed {
		o.order = append(o.order, newPath)
	}
}

// remove drops id and returns the buffer id to show next: the one after it, else the
// new last, else empty when no retained buffers remain.
func (o *openSet) remove(id string) (next string) {
	entry, ok := o.get(id)
	if !ok {
		return ""
	}
	delete(o.byID, id)
	if entry.path != "" {
		delete(o.byPath, entry.path)
	}
	for i, candidate := range o.order {
		if candidate != id {
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

func (o *openSet) docs() []DocFile {
	docs := make([]DocFile, 0, len(o.order))
	for _, id := range o.order {
		entry := o.byID[id]
		if entry == nil {
			continue
		}
		docs = append(docs, DocFile{
			ID: entry.id, Name: entry.name, Path: entry.path, Root: entry.root,
		})
	}
	return docs
}

func (o *openSet) each(fn func(*openEntry)) {
	for _, id := range o.order {
		if entry := o.byID[id]; entry != nil {
			fn(entry)
		}
	}
}
