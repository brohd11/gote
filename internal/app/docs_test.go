package app

import (
	"os"
	"path/filepath"
	"testing"
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

	c := New("dev", DefaultConfig(), false, "", 0)
	if c.Mode != ModeHome {
		t.Fatal("default mode should be home")
	}
	if got := docNames(c.Files); !equalNames(got, "note.md") {
		t.Fatalf("home seed = %v, want note.md", got)
	}

	root := writeTree(t)
	c = New("dev", DefaultConfig(), true, root, 1)
	if c.Mode != ModeScan {
		t.Fatal("scan flag should set scan mode")
	}
	if got := docNames(c.Files); !equalNames(got, "F.MD", "a.md", "c.md") {
		t.Fatalf("scan seed = %v, want depth-1 results", got)
	}

	ed := c.OpenDoc(c.Files[0].Path, nil)
	c.Seed()
	if got := c.Open[c.Files[0].Path]; got != ed {
		t.Fatal("reseeding must not drop open buffers")
	}
}
