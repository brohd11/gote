package app

import (
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
		docs = append(docs, DocFile{Name: e.Name(), Path: filepath.Join(dir, e.Name())})
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
			docs = append(docs, DocFile{Name: d.Name(), Path: path})
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

// docItem adapts a DocFile to a list.Item for the picker panels.
type docItem struct{ doc DocFile }

func (i docItem) Title() string       { return i.doc.Name }
func (i docItem) Description() string { return i.doc.Path }
func (i docItem) FilterValue() string { return i.doc.Name }

// docItems wraps a seed result as list rows.
func docItems(docs []DocFile) []list.Item {
	items := make([]list.Item, 0, len(docs))
	for _, d := range docs {
		items = append(items, docItem{doc: d})
	}
	return items
}
