package app

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/brohd11/bubblestack/components"
)

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

	if got := docNames(ScanDocs(root, 0, "md")); !equalNames(got, "F.MD", "a.md") {
		t.Fatalf("depth 0 = %v, want root files only", got)
	}
	if got := docNames(ScanDocs(root, 1, "md")); !equalNames(got, "F.MD", "a.md", "c.md") {
		t.Fatalf("depth 1 = %v, want root + one level", got)
	}
	if got := docNames(ScanDocs(root, 2, "md")); !equalNames(got, "F.MD", "a.md", "c.md", "d.md") {
		t.Fatalf("depth 2 = %v, want everything but .hidden", got)
	}
	if got := ScanDocs(root, 3, "txt"); len(got) != 1 || got[0].Name != "b.txt" {
		t.Fatalf("txt scan = %v, want b.txt only", docNames(got))
	}
}

// TestScanDocsPaths: the results are sorted by path and carry absolute paths.
func TestScanDocsPaths(t *testing.T) {
	root := writeTree(t)
	docs := ScanDocs(root, 0, "md")
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
	if got := docNames(HomeDocs(root, "md")); !equalNames(got, "F.MD", "a.md") {
		t.Fatalf("home docs = %v, want the flat md files only", got)
	}

	missing := filepath.Join(t.TempDir(), "fresh-store")
	docs := HomeDocs(missing, "md")
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
	store := filepath.Join(home, ".gote")
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
	if got := docNames(c.Files); !equalNames(got, "F.MD", "a.md", "c.md") {
		t.Fatalf("scan seed = %v, want depth-1 results", got)
	}

	ed := c.OpenDoc(c.Files[0].Path, components.EditorOpts{})
	c.Seed()
	if got := c.Open[c.Files[0].Path]; got != ed {
		t.Fatal("reseeding must not drop open buffers")
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

	for _, name := range []string{"", "   ", ".", "./", "/etc/x", "../escape", "a/../../escape"} {
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
	docs := []DocFile{{Name: "a.txt", Path: "/x/a.txt"}, {Name: "b.txt", Path: "/x/b.txt"}}

	items := docItems(docs, "/x/b.txt")
	if got := items[1].(docItem).Title(); got != "• b.txt" {
		t.Fatalf("the current doc's title = %q, want %q", got, "• b.txt")
	}
	if got := items[0].(docItem).Title(); got != "a.txt" {
		t.Fatalf("a non-current doc keeps its plain title, got %q", got)
	}
	if got := items[1].(docItem).FilterValue(); got != "b.txt" {
		t.Fatalf("the dot must stay out of filtering, got %q", got)
	}

	items = docItems(docs, "")
	for i, it := range items {
		if got := it.(docItem).Title(); got != docs[i].Name {
			t.Fatalf("an empty currentPath dots nothing, row %d = %q", i, got)
		}
	}
}
