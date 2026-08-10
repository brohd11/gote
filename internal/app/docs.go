package app

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/charmbracelet/bubbles/list"
)

// DocFile is one seedable document: a file matching the configured extension.
type DocFile struct {
	Name string // base name, shown in the list
	Path string // absolute path, the editor's load/save target
	Root string // origin root used to render stable relative path context
}

// HomeDocs lists the docs stored flat in dir (the home mode seed), filtered to ext.
// The directory is created when missing — the first run of a fresh install should
// still find its store. A listing failure yields an empty list, not an error: the
// TUI shows an empty list and stays usable.
func HomeDocs(dir, ext string) []DocFile {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var docs []DocFile
	for _, e := range entries {
		if e.IsDir() || !matchesExt(e.Name(), ext) {
			continue
		}
		docs = append(docs, DocFile{Name: e.Name(), Path: filepath.Join(dir, e.Name()), Root: filepath.Clean(dir)})
	}
	sortDocs(docs)
	return docs
}

// ScanDocs walks root recursively down to depth directory levels below it (0 = root
// only), collecting files matching ext. Dot-directories are skipped — a scan of ~
// has no business descending into .git or .config. Unreadable subtrees are skipped,
// not fatal.
func ScanDocs(root string, depth int, ext string) []DocFile {
	var docs []DocFile
	root = filepath.Clean(root)
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // skip what we can't read
		}
		if d.IsDir() {
			if path != root && strings.HasPrefix(d.Name(), ".") {
				return fs.SkipDir
			}
			if dirDepth(root, path) > depth {
				return fs.SkipDir
			}
			return nil
		}
		if matchesExt(d.Name(), ext) {
			docs = append(docs, DocFile{Name: d.Name(), Path: path, Root: root})
		}
		return nil
	})
	sortDocs(docs)
	return docs
}

// dirDepth counts how many directory levels below the walk root path sits: the root
// itself is 0, its direct subdirectories 1, and so on.
func dirDepth(root, path string) int {
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == "." {
		return 0
	}
	return strings.Count(rel, string(filepath.Separator)) + 1
}

// matchesExt reports whether name carries the configured extension (compared
// without the dot, case-insensitively).
func matchesExt(name, ext string) bool {
	return strings.EqualFold(strings.TrimPrefix(filepath.Ext(name), "."), ext)
}

func sortDocs(docs []DocFile) {
	sort.Slice(docs, func(i, j int) bool { return docs[i].Path < docs[j].Path })
}

// docItem adapts a DocFile to a list.Item for the picker panels. current marks the
// doc showing in the editor pane: its row gets a dot prefix (the list has no other
// "open here" channel — selection is focus, not state).
type docItem struct {
	doc     DocFile
	current bool
}

func (i docItem) Title() string {
	if i.current {
		return "• " + i.doc.Name
	}
	return i.doc.Name
}
func (i docItem) Description() string { return i.doc.Path }
func (i docItem) FilterValue() string { return i.doc.Name }
func (i docItem) SuffixText() string {
	rel, err := filepath.Rel(i.doc.Root, i.doc.Path)
	if err != nil {
		return ""
	}
	dir := filepath.Dir(rel)
	if dir == "." {
		return ""
	}
	return dir + string(filepath.Separator)
}

// docItems wraps a seed result as list rows, dotting the row whose path is
// currentPath ("" dots nothing — the docs list never has a current doc).
func docItems(docs []DocFile, currentPath string) []list.Item {
	items := make([]list.Item, 0, len(docs))
	for _, d := range docs {
		items = append(items, docItem{doc: d, current: d.Path == currentPath && currentPath != ""})
	}
	return items
}

// newFileItem is the docs list's first row — an action, not a doc: enter opens
// the floating line edit that creates a file. A distinct type (not docItem) so
// pickDoc can route it; the panel's OnSelect bypasses per-item Pick.
type newFileItem struct{}

func (newFileItem) Title() string       { return "+ new file" }
func (newFileItem) Description() string { return "type a name (a/b.md nests dirs)" }
func (newFileItem) FilterValue() string { return "new file" }
func (newFileItem) SuffixText() string  { return "type a name (a/b.md nests dirs)" }

// docRows is the docs panel's full row set: the action row, then the seeded docs.
// Every (re)build of the list goes through here so the row survives reseeds.
func docRows(c *Ctx) []list.Item {
	return append([]list.Item{newFileItem{}}, docItems(c.Files, "")...)
}

// newDocPath resolves a name typed into the new-file line edit against base. A
// name without an extension gets the configured one (the docs list filters to it,
// so an extensionless file would be invisible); "/" in the name nests under base.
// Absolute names and ones escaping base ("..") are rejected — the line edit must
// never write outside the doc store.
func newDocPath(base, name, ext string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", fmt.Errorf("no name given")
	}
	if filepath.IsAbs(name) {
		return "", fmt.Errorf("%q is absolute; give a name relative to the doc store", name)
	}
	path := filepath.Join(base, name)
	// path == base happens for "." / "./" — rejecting it also covers the ext
	// append below, which would otherwise produce a sibling of base, outside it.
	if path == base || !strings.HasPrefix(path, base+string(filepath.Separator)) {
		return "", fmt.Errorf("%q escapes the doc store", name)
	}
	if filepath.Ext(path) == "" {
		path += "." + ext
	}
	return path, nil
}

// createDoc writes an empty doc at path, making parent dirs as needed, without
// clobbering: an existing file is left alone (the editor opens it either way).
func createDoc(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if os.IsExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	return f.Close()
}
