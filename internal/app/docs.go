package app

import (
	"fmt"
	"image/color"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"charm.land/bubbles/v2/list"
	"github.com/brohd11/goutil/strutil"
	"github.com/brohd11/goutil/textfile"
)

// DocFile describes a disk-backed Docs row or an open buffer. ID is populated only
// for open buffers; Path is populated only once the buffer has a filesystem identity.
type DocFile struct {
	ID   string // open-buffer identity; empty on disk-backed Docs rows
	Name string // base name, shown in the list
	Path string // absolute load/save target; empty for an unsaved buffer
	Root string // origin root used to render stable relative path context
}

// DocFilter decides which files seed the lists. Empty Exts means any text file (by
// content sniffing); non-empty Exts (config or --ext) are taken at face value.
type DocFilter struct {
	Exts []string // lowercase, no leading dot; see NewDocFilter
}

// NewDocFilter normalizes extensions ("MD", ".md", " md " all mean md; empties drop). It is
// the one normalization point for config and --ext.
func NewDocFilter(exts []string) DocFilter { return DocFilter{Exts: normalizeExts(exts)} }

// normalizeExts is the canonical form; all-empty input returns nil so a loaded config
// equals the zero Config.
func normalizeExts(exts []string) []string {
	out := make([]string, 0, len(exts))
	for _, e := range exts {
		if e = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(e), ".")); e != "" {
			out = append(out, e)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// defaultExt is what rename appends to a bare name: the first configured extension, else
// "md".
func defaultExt(exts []string) string {
	if len(exts) > 0 {
		return exts[0]
	}
	return "md"
}

// Match reports whether the file at path (with base name name) belongs in a list.
func (f DocFilter) Match(path, name string) bool {
	if len(f.Exts) == 0 {
		return textfile.IsText(path)
	}
	return f.hasExt(name)
}

// hasExt compares name's extension against the configured set, without the dot and
// case-insensitively.
func (f DocFilter) hasExt(name string) bool {
	ext := strings.TrimPrefix(filepath.Ext(name), ".")
	for _, want := range f.Exts {
		if strings.EqualFold(ext, want) {
			return true
		}
	}
	return false
}

// HomeDocs lists the docs stored flat in dir, creating dir if missing. A listing failure
// yields an empty list.
func HomeDocs(dir string, f DocFilter) []DocFile {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var docs []DocFile
	for _, e := range entries {
		path := filepath.Join(dir, e.Name())
		if e.IsDir() || !f.Match(path, e.Name()) {
			continue
		}
		docs = append(docs, DocFile{Name: e.Name(), Path: path, Root: filepath.Clean(dir)})
	}
	sortDocs(docs)
	return docs
}

// skipDirs are trees a scan never enters (vendored code, build output); dot-directories
// are pruned separately.
var skipDirs = map[string]bool{
	"node_modules": true,
	"vendor":       true,
	"target":       true,
	"dist":         true,
	"build":        true,
	"__pycache__":  true,
	"venv":         true,
}

// docsRoot is the folder view's root and floor: the scan or vault root, the doc store in
// home mode, or the file's directory in single-file mode. "" leaves it unclamped.
func docsRoot(c *Ctx) string {
	switch c.Mode {
	case ModeScan, ModeVault:
		return c.ScanDir
	case ModeHome:
		if dir, err := DocsDir(); err == nil {
			return dir
		}
	case ModeFile:
		if c.FilePath != "" {
			return filepath.Dir(c.FilePath)
		}
	}
	return ""
}

// includeDoc applies the folder view's live dot-file visibility and existing file and
// dependency-directory filters independently of the flat scan.
func (s *homeScreen) includeDoc(c *Ctx) func(string, fs.DirEntry) bool {
	return func(path string, d fs.DirEntry) bool {
		name := d.Name()
		if !s.showHidden && strings.HasPrefix(name, ".") {
			return false
		}
		if d.IsDir() {
			return !skipDirs[name]
		}
		return c.Filter.Match(path, name)
	}
}

// ScanDocs walks root to depth levels (0 = root only) collecting accepted files, pruning
// dot-directories and skipDirs (but never root itself). Unreadable subtrees are skipped.
func ScanDocs(root string, depth int, f DocFilter) []DocFile {
	var docs []DocFile
	root = filepath.Clean(root)
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // skip what we can't read
		}
		if d.IsDir() {
			if path != root && (strings.HasPrefix(d.Name(), ".") || skipDirs[d.Name()]) {
				return fs.SkipDir
			}
			if strutil.Depth(root, path) > depth {
				return fs.SkipDir
			}
			return nil
		}
		if f.Match(path, d.Name()) {
			docs = append(docs, DocFile{Name: d.Name(), Path: path, Root: root})
		}
		return nil
	})
	sortDocs(docs)
	return docs
}

func sortDocs(docs []DocFile) {
	sort.Slice(docs, func(i, j int) bool { return docs[i].Path < docs[j].Path })
}

// docItem adapts a DocFile to a list row. current marks the doc in the editor with a dot.
type docItem struct {
	doc        DocFile
	current    bool
	titleColor func(string) color.Color
}

func (i docItem) Title() string {
	if i.current {
		return "• " + i.doc.Name
	}
	return i.doc.Name
}
func (i docItem) TitleColor() color.Color {
	if i.titleColor != nil {
		return i.titleColor(i.doc.Path)
	}
	return nil
}

// KeepColor preserves git colors under the Docs panel cursor.
func (i docItem) KeepColor() bool     { return i.titleColor != nil }
func (i docItem) Description() string { return i.doc.Path }
func (i docItem) FilterValue() string { return i.doc.Name }

func (i docItem) identity() string {
	if i.doc.ID != "" {
		return i.doc.ID
	}
	return i.doc.Path
}

func (i docItem) SuffixText() string {
	if i.doc.Path == "" {
		return ""
	}
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

// docRows is the docs panel's full row set, rebuilt from the scan on each reseed.
func (s *homeScreen) docRows(c *Ctx) []list.Item {
	items := docItems(c.Files, "")
	for n, item := range items {
		row := item.(docItem)
		row.titleColor = s.docTitleColor
		items[n] = row
	}
	return items
}

// newDocPath resolves a rename name against base. A name without an extension gets ext;
// "/" nests under base. Absolute names and ".." escapes are rejected: the box must never
// write outside the doc store.
func newDocPath(base, name, ext string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", fmt.Errorf("no name given")
	}
	// Refuse rooted names by either separator and drive/UNC volumes explicitly:
	// filepath.IsAbs on Windows does not treat "/etc/x" as absolute.
	if filepath.IsAbs(name) || filepath.VolumeName(name) != "" ||
		strings.HasPrefix(name, "/") || strings.HasPrefix(name, `\`) {
		return "", fmt.Errorf("%q is absolute; give a name relative to the doc store", name)
	}
	// Refuse "~": it would create a literal "~" directory, and these boxes are confined to the
	// store (the editor's save-as box is the one that expands it).
	if strings.HasPrefix(name, "~") {
		return "", fmt.Errorf("%q is a home path; give a name relative to the doc store", name)
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

// renameDoc moves a doc from old to newPath, making parent dirs as needed. An occupied
// target is refused to protect the other document. Lstat counts dangling symlinks too.
func renameDoc(old, newPath string) error {
	if _, err := os.Lstat(newPath); err == nil {
		return fmt.Errorf("%q already exists", filepath.Base(newPath))
	}
	if err := os.MkdirAll(filepath.Dir(newPath), 0o755); err != nil {
		return err
	}
	return os.Rename(old, newPath)
}

// deleteDoc removes one file (os.Remove, never RemoveAll). A missing file is an error the
// confirm reports.
func deleteDoc(path string) error { return os.Remove(path) }
