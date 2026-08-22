package app

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/brohd11/bubblestack/components"
)

// mdOnly is the filter that reproduces gote's pre-text-discovery behavior, and what
// a user's `extensions: [md]` config produces.
var mdOnly = DocFilter{Exts: []string{"md"}}

// anyText is the default filter: no configured extensions, so content decides.
var anyText = DocFilter{}

// writeTree creates a fixed doc tree under t.TempDir():
//
//	a.md  b.txt  F.MD
//	sub/c.md
//	sub/deep/d.md
//	.hidden/e.md
func writeTree(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	files := []string{
		"a.md", "b.txt", "F.MD",
		"sub/c.md", "sub/deep/d.md",
		".hidden/e.md",
	}
	for _, f := range files {
		p := filepath.Join(root, f)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// write puts content at root/rel, making parents. Returns root for chaining.
func write(t *testing.T, root, rel string, content []byte) {
	t.Helper()
	p := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, content, 0o644); err != nil {
		t.Fatal(err)
	}
}

func docNames(docs []DocFile) []string {
	names := make([]string, 0, len(docs))
	for _, d := range docs {
		names = append(names, d.Name)
	}
	return names
}

func equalNames(got []string, want ...string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range want {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

// TestScanDocsDepth: depth bounds the walk (0 = root only), the extension filter is
// case-insensitive, and dot-directories are skipped at any depth.
func TestScanDocsDepth(t *testing.T) {
	root := writeTree(t)

	if got := docNames(ScanDocs(root, 0, mdOnly)); !equalNames(got, "F.MD", "a.md") {
		t.Fatalf("depth 0 = %v, want root files only", got)
	}
	if got := docNames(ScanDocs(root, 1, mdOnly)); !equalNames(got, "F.MD", "a.md", "c.md") {
		t.Fatalf("depth 1 = %v, want root + one level", got)
	}
	if got := docNames(ScanDocs(root, 2, mdOnly)); !equalNames(got, "F.MD", "a.md", "c.md", "d.md") {
		t.Fatalf("depth 2 = %v, want everything but .hidden", got)
	}
	txt := DocFilter{Exts: []string{"txt"}}
	if got := ScanDocs(root, 3, txt); len(got) != 1 || got[0].Name != "b.txt" {
		t.Fatalf("txt scan = %v, want b.txt only", docNames(got))
	}
	multi := DocFilter{Exts: []string{"md", "txt"}}
	if got := docNames(ScanDocs(root, 0, multi)); !equalNames(got, "F.MD", "a.md", "b.txt") {
		t.Fatalf("multi-extension scan = %v, want both types at the root", got)
	}
}

// TestScanDocsAnyText: the default filter takes any text file — b.txt is in, not out —
// while binaries stay out and extensionless text comes in.
func TestScanDocsAnyText(t *testing.T) {
	root := writeTree(t)
	write(t, root, "Makefile", []byte("all:\n\techo hi\n"))
	write(t, root, "empty", nil)
	write(t, root, "logo.png", []byte("\x89PNG\r\n\x1a\n\x00\x00binary"))

	got := docNames(ScanDocs(root, 0, anyText))
	if !equalNames(got, "F.MD", "Makefile", "a.md", "b.txt", "empty") {
		t.Fatalf("default scan = %v, want every text file and no binary", got)
	}
}

// TestScanDocsSkipsNoiseDirs: with every text file valid, a scan that descended into
// node_modules or vendor would bury the project's own files.
func TestScanDocsSkipsNoiseDirs(t *testing.T) {
	root := t.TempDir()
	write(t, root, "main.go", []byte("package main\n"))
	write(t, root, "node_modules/left-pad/index.js", []byte("module.exports = 1\n"))
	write(t, root, "vendor/dep/dep.go", []byte("package dep\n"))
	write(t, root, "build/out.txt", []byte("artifact\n"))

	if got := docNames(ScanDocs(root, 5, anyText)); !equalNames(got, "main.go") {
		t.Fatalf("scan = %v, want the project's own file only", got)
	}

	// Scanning from inside a pruned directory must still list it: the prune spares the
	// walk root, so `gote here` works wherever it is run.
	inside := filepath.Join(root, "node_modules")
	if got := docNames(ScanDocs(inside, 5, anyText)); !equalNames(got, "index.js") {
		t.Fatalf("scan rooted in node_modules = %v, want index.js", got)
	}
}

// TestScanDocsPaths: the results are sorted by path and carry absolute paths.
func TestScanDocsPaths(t *testing.T) {
	root := writeTree(t)
	docs := ScanDocs(root, 0, mdOnly)
	if len(docs) != 2 {
		t.Fatalf("got %d docs, want 2", len(docs))
	}
	if docs[0].Path != filepath.Join(root, "F.MD") || docs[1].Path != filepath.Join(root, "a.md") {
		t.Fatalf("paths = %v, %v — want sorted absolute paths under root", docs[0].Path, docs[1].Path)
	}
}

// TestHomeDocs: the home store lists only its direct children, and a missing store
// is created rather than an error.
func TestHomeDocs(t *testing.T) {
	root := writeTree(t)
	if got := docNames(HomeDocs(root, mdOnly)); !equalNames(got, "F.MD", "a.md") {
		t.Fatalf("home docs = %v, want the flat md files only", got)
	}
	write(t, root, "logo.png", []byte("\x89PNG\x00\x00"))
	if got := docNames(HomeDocs(root, anyText)); !equalNames(got, "F.MD", "a.md", "b.txt") {
		t.Fatalf("default home docs = %v, want every flat text file and no binary", got)
	}

	missing := filepath.Join(t.TempDir(), "fresh-store")
	docs := HomeDocs(missing, mdOnly)
	if len(docs) != 0 {
		t.Fatalf("a fresh store should be empty, got %v", docNames(docs))
	}
	if st, err := os.Stat(missing); err != nil || !st.IsDir() {
		t.Fatal("HomeDocs should create a missing store directory")
	}
}

// TestSeedModes: the ctx seeds from the store in home mode and from ScanDir in scan
// mode, and reseeding preserves open buffers.
func TestSeedModes(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	store := filepath.Join(home, ".gote", "docs")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(store, "note.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	c := New("dev", DefaultConfig(), Options{})
	if c.Mode != ModeHome {
		t.Fatal("default mode should be home")
	}
	if got := docNames(c.Files); !equalNames(got, "note.md") {
		t.Fatalf("home seed = %v, want note.md", got)
	}

	root := writeTree(t)
	c = New("dev", DefaultConfig(), Options{Mode: ModeScan, Dir: root, Depth: 1, DepthSet: true})
	if c.Mode != ModeScan {
		t.Fatal("scan options should set scan mode")
	}
	// The default config sets no extensions, so the seed is every text file: b.txt
	// is in the depth-1 results now, which is the whole point of the change.
	if got := docNames(c.Files); !equalNames(got, "F.MD", "a.md", "b.txt", "c.md") {
		t.Fatalf("scan seed = %v, want depth-1 results", got)
	}

	ed := c.OpenDoc(c.Files[0].Path, components.EditorOpts{})
	c.Seed()
	if got := c.open.byPath[c.Files[0].Path]; got != ed {
		t.Fatal("reseeding must not drop open buffers")
	}
}

// TestDocFilterMatch: a configured extension set is taken at face value (no content
// sniff, so an unreadable path still matches on name), while the zero filter defers
// entirely to the file's contents.
func TestDocFilterMatch(t *testing.T) {
	root := t.TempDir()
	write(t, root, "a.md", []byte("# hi"))
	write(t, root, "bin.md", []byte("\x00\x00"))

	if !mdOnly.Match(filepath.Join(root, "bin.md"), "bin.md") {
		t.Fatal("a configured extension is the user's word; it should not be second-guessed by a sniff")
	}
	if mdOnly.Match(filepath.Join(root, "a.txt"), "a.txt") {
		t.Fatal("a name outside the configured set should not match")
	}
	if !mdOnly.Match(filepath.Join(root, "A.MD"), "A.MD") {
		t.Fatal("the extension compare should be case-insensitive")
	}
	if !anyText.Match(filepath.Join(root, "a.md"), "a.md") {
		t.Fatal("the default filter should take a text file")
	}
	if anyText.Match(filepath.Join(root, "bin.md"), "bin.md") {
		t.Fatal("the default filter should reject binary content whatever it is named")
	}
}

// TestNewDocFilter: however extensions were written — config.yml or --ext — they mean
// one thing by the time they reach Match, and an all-empty set is no filter at all.
func TestNewDocFilter(t *testing.T) {
	if got := NewDocFilter([]string{"MD", ".Txt", "  yml  "}); !equalNames(got.Exts, "md", "txt", "yml") {
		t.Fatalf("exts = %v, want them lowercased, undotted and trimmed", got.Exts)
	}
	// `gote --ext=` reaches here as [""], and must mean "any text file" rather than a
	// filter matching nothing — which would show an empty list with no way back.
	for _, in := range [][]string{nil, {}, {""}, {"", "  ", "."}} {
		if got := NewDocFilter(in); got.Exts != nil {
			t.Errorf("NewDocFilter(%q).Exts = %v, want nil", in, got.Exts)
		}
	}
}

// TestDefaultExt: "+ new file" follows the filter, so a restricted session cannot
// create a file it would immediately hide.
func TestDefaultExt(t *testing.T) {
	if got := defaultExt(nil); got != "md" {
		t.Errorf("unfiltered default = %q, want md", got)
	}
	if got := defaultExt([]string{"txt", "md"}); got != "txt" {
		t.Errorf("filtered default = %q, want the first configured extension", got)
	}
}

// TestNewDocPath: a plain name gets the configured extension, "/" nests under
// the base, an explicit extension is kept, and blank/absolute/escaping names are
// rejected — the line edit must never write outside the doc store.
func TestNewDocPath(t *testing.T) {
	base := t.TempDir()

	for name, want := range map[string]string{
		"foo":      filepath.Join(base, "foo.md"),
		"a/b":      filepath.Join(base, "a", "b.md"),
		"  pad  ":  filepath.Join(base, "pad.md"),
		"keep.txt": filepath.Join(base, "keep.txt"),
	} {
		got, err := newDocPath(base, name, "md")
		if err != nil || got != want {
			t.Errorf("newDocPath(%q) = (%q, %v), want (%q, nil)", name, got, err, want)
		}
	}

	// "~/x" is neither absolute nor an escape by the prefix rule, so it is refused by
	// name: these boxes stay inside the doc store, and expanding it would write a file
	// the list could never show again.
	for _, name := range []string{"", "   ", ".", "./", "/etc/x", "../escape", "a/../../escape", "~", "~/x.md"} {
		if got, err := newDocPath(base, name, "md"); err == nil {
			t.Errorf("newDocPath(%q) = %q, want an error", name, got)
		}
	}
}

// TestCreateDoc: the file and its parent dirs are created, and a second call on
// an existing file leaves its contents alone.
func TestCreateDoc(t *testing.T) {
	base := t.TempDir()
	path := filepath.Join(base, "notes", "deep", "todo.md")

	if err := createDoc(path); err != nil {
		t.Fatalf("createDoc: %v", err)
	}
	if st, err := os.Stat(path); err != nil || st.IsDir() {
		t.Fatalf("expected a file at %s", path)
	}

	if err := os.WriteFile(path, []byte("keep me"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := createDoc(path); err != nil {
		t.Fatalf("createDoc over an existing file: %v", err)
	}
	b, err := os.ReadFile(path)
	if err != nil || string(b) != "keep me" {
		t.Fatalf("existing file clobbered: %q, %v", b, err)
	}
}

// TestDocItemsMarksCurrent: the doc showing in the editor pane gets a dot prefix —
// the list's only "open here" channel for now — and the marker never leaks into
// filtering. An empty currentPath dots nothing (the docs list's shape).
func TestDocItemsMarksCurrent(t *testing.T) {
	docs := []DocFile{{Name: "a.txt", Path: "/x/a.txt", Root: "/x"}, {Name: "b.txt", Path: "/x/nested/b.txt", Root: "/x"}}

	items := docItems(docs, "/x/nested/b.txt")
	if got := items[1].(docItem).Title(); got != "• b.txt" {
		t.Fatalf("the current doc's title = %q, want %q", got, "• b.txt")
	}
	if got := items[0].(docItem).Title(); got != "a.txt" {
		t.Fatalf("a non-current doc keeps its plain title, got %q", got)
	}
	if got := items[1].(docItem).FilterValue(); got != "b.txt" {
		t.Fatalf("the dot must stay out of filtering, got %q", got)
	}
	if got := items[0].(docItem).SuffixText(); got != "" {
		t.Fatalf("a root-level doc suffix = %q, want empty", got)
	}
	if got := items[1].(docItem).SuffixText(); got != "nested"+string(filepath.Separator) {
		t.Fatalf("a nested doc suffix = %q, want nested plus separator", got)
	}

	items = docItems(docs, "")
	for i, it := range items {
		if got := it.(docItem).Title(); got != docs[i].Name {
			t.Fatalf("an empty currentPath dots nothing, row %d = %q", i, got)
		}
	}
}

// TestDocItemMarkTracksDirty: the unsaved-changes flag is a live probe, not a value baked
// in when the row was built. The open list is rebuilt only on open/save/reseed, so a
// snapshot would go stale the moment the next character was typed.
func TestDocItemMarkTracksDirty(t *testing.T) {
	dirty := false
	row := docItem{doc: DocFile{Name: "a.txt", Path: "/x/a.txt", Root: "/x"}, dirty: func() bool { return dirty }}

	if got := row.Mark(); got != "" {
		t.Fatalf("a clean buffer's mark = %q, want empty", got)
	}
	dirty = true
	if got := row.Mark(); got != " (*)" {
		t.Fatalf("a dirty buffer's mark = %q, want %q", got, " (*)")
	}
	// The flag is a separate reserved piece, so it must not leak into either the text the
	// row prints as its name or the text a filter matches against.
	if got := row.Title(); got != "a.txt" {
		t.Fatalf("the mark leaked into the title: %q", got)
	}
	if got := row.FilterValue(); got != "a.txt" {
		t.Fatalf("the mark leaked into filtering: %q", got)
	}
}

// A docs row has no buffer behind it and so can never be dirty: docItems leaves the probe
// nil, and a nil probe never flags.
func TestDocItemsHaveNoDirtyProbe(t *testing.T) {
	items := docItems([]DocFile{{Name: "a.txt", Path: "/x/a.txt", Root: "/x"}}, "")
	row := items[0].(docItem)
	if row.dirty != nil {
		t.Error("a docs row must not carry a dirty probe")
	}
	if got := row.Mark(); got != "" {
		t.Fatalf("a docs row's mark = %q, want empty", got)
	}
}
