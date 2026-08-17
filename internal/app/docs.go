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

// DocFile is one seedable document: a file the configured filter accepts.
type DocFile struct {
	Name string // base name, shown in the list
	Path string // absolute path, the editor's load/save target
	Root string // origin root used to render stable relative path context
}

// DocFilter decides which files seed the lists. An empty Exts means any text file,
// judged by sniffing content (isTextFile) — the default, so a fresh gote lists
// everything it can actually edit. A non-empty Exts is the user's explicit word, from
// config.yml's extensions key or the --ext flag, and is taken at face value without
// sniffing.
type DocFilter struct {
	Exts []string // lowercase, no leading dot; see NewDocFilter
}

// NewDocFilter builds a filter from extension strings however they were written — "MD",
// ".md" and " md " all mean md, and empties drop out. The single normalization point
// for both config.yml's extensions key and the --ext flag, so the two cannot drift.
func NewDocFilter(exts []string) DocFilter { return DocFilter{Exts: normalizeExts(exts)} }

// normalizeExts is NewDocFilter's canonical form, shared with Config so a loaded config
// holds what it means. An all-empty input returns nil, not an empty slice: the two are
// the same filter but only one of them compares equal to a zero Config.
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

// defaultExt is what "+ new file" appends to a name typed without one: the first
// configured extension, so a filtered session cannot create files it would then hide,
// and "md" when nothing is configured.
func defaultExt(exts []string) string {
	if len(exts) > 0 {
		return exts[0]
	}
	return "md"
}

// Match reports whether the file at path (with base name name) belongs in a list.
func (f DocFilter) Match(path, name string) bool {
	if len(f.Exts) == 0 {
		return isTextFile(path)
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

// HomeDocs lists the docs stored flat in dir (the home mode seed), filtered by f.
// The directory is created when missing — the first run of a fresh install should
// still find its store. A listing failure yields an empty list, not an error: the
// TUI shows an empty list and stays usable.
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

// skipDirs are trees a doc scan never descends into. With every text file valid by
// default, a depth-5 scan of any real project would otherwise be mostly vendored
// dependency source and build output. Dot-prefixed names (.git, .venv) need no entry
// here — the dot rule below already covers them.
var skipDirs = map[string]bool{
	"node_modules": true,
	"vendor":       true,
	"target":       true,
	"dist":         true,
	"build":        true,
	"__pycache__":  true,
	"venv":         true,
}

// ScanDocs walks root recursively down to depth directory levels below it (0 = root
// only), collecting the files f accepts. Dot-directories and skipDirs are pruned — a
// scan of ~ has no business descending into .git, and one of a project has none
// descending into node_modules. Both rules spare the root itself, so scanning from
// inside such a directory still lists it. Unreadable subtrees are skipped, not fatal.
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
			if dirDepth(root, path) > depth {
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

// dirDepth counts how many directory levels below the walk root path sits: the root
// itself is 0, its direct subdirectories 1, and so on.
func dirDepth(root, path string) int {
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == "." {
		return 0
	}
	return strings.Count(rel, string(filepath.Separator)) + 1
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
func (newFileItem) Description() string { return "(rel/path)" }
func (newFileItem) FilterValue() string { return "new file" }

// No suffix: the hint about what to type belongs in the line edit this row opens, not in
// the column the doc paths need. It was also the one row whose suffix was pure decoration —
// every other one names a real directory.
func (newFileItem) SuffixText() string { return "" }

// docRows is the docs panel's full row set: the action row, then the seeded docs.
// Every (re)build of the list goes through here so the row survives reseeds.
func docRows(c *Ctx) []list.Item {
	return append([]list.Item{newFileItem{}}, docItems(c.Files, "")...)
}

// newDocPath resolves a name typed into the new-file line edit against base. A name
// without an extension gets ext (the config's default_extension) — a convenience, not
// a necessity now that an extensionless file lists fine, but typing "notes" should
// still land notes.md; "/" in the name nests under base. Absolute names and ones
// escaping base ("..") are rejected — the line edit must never write outside the doc
// store.
func newDocPath(base, name, ext string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", fmt.Errorf("no name given")
	}
	if filepath.IsAbs(name) {
		return "", fmt.Errorf("%q is absolute; give a name relative to the doc store", name)
	}
	// A home path is neither absolute nor an escape by the check below, so without this
	// it would quietly create a directory literally named "~" inside the store. Refused
	// rather than expanded: these boxes are confined to the doc store by design, and a
	// file written to ~ would vanish from the list on the next reseed. (The editor's
	// save-as box is the one that WRITES anywhere, and it does expand "~".)
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

// renameDoc moves a doc from old to newPath, making parent dirs as needed (the rename
// box takes a path, so it nests the same way "+ new file" does). An occupied target is
// refused rather than clobbered — createDoc's non-destructive rule, which matters more
// here: a rename that overwrote would destroy a document that is not the one being
// renamed. Lstat, not Stat, so a dangling symlink still counts as occupied.
func renameDoc(old, newPath string) error {
	if _, err := os.Lstat(newPath); err == nil {
		return fmt.Errorf("%q already exists", filepath.Base(newPath))
	}
	if err := os.MkdirAll(filepath.Dir(newPath), 0o755); err != nil {
		return err
	}
	return os.Rename(old, newPath)
}

// deleteDoc removes a doc from disk — os.Remove, not RemoveAll: the lists hold files,
// and a directory that somehow reached this call must not take its contents with it.
// A missing file reports its error, which the confirm shows: the row promised a
// document, so its absence is news rather than a no-op.
func deleteDoc(path string) error { return os.Remove(path) }

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
