package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/brohd11/bubblestack/core"

	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"
)

// scanTree builds root/{notes.md, sub/{deep.md}, node_modules/{junk.md}} and returns root.
func scanTree(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for _, dir := range []string{"sub", "node_modules"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	files := map[string]string{
		"notes.md":             "top\n",
		"sub/deep.md":          "deep\n",
		"node_modules/junk.md": "junk\n",
	}
	for rel, body := range files {
		if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(rel)), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func newScanHome(t *testing.T, root string) (*homeScreen, *core.Shared) {
	t.Helper()
	return newHomeWith(t, Options{Mode: ModeScan, Dir: root, Depth: 3, DepthSet: true})
}

// newHomeCfg is newHomeWith for a specific Config — the launch options carry the mode,
// the config carries the preferences.
func newHomeCfg(t *testing.T, cfg Config, opts Options) (*homeScreen, *core.Shared) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	sh := core.NewShared(New("test", cfg, opts))
	s := NewHomeScreen(sh).(*homeScreen)
	s.Init(sh)
	s.SetSize(sh, 100, 30)
	return s, sh
}

// altKey builds gote's alt chords, which keyMsg-style helpers elsewhere don't cover.
func altKey(r rune) tea.KeyMsg {
	return keyMsg("alt+" + string(r))
}

// TestFolderViewToggle: alt+t swaps the docs slot between the flat scan (every hit in one
// list, nested or not) and the folder view (one directory, folders included).
func TestFolderViewToggle(t *testing.T) {
	root := scanTree(t)
	s, sh := newScanHome(t, root)

	if !s.flat {
		t.Fatal("gote should start on the flat list")
	}
	flat := rowTitles(s.docsPanel.List())
	if !hasRow(flat, "deep.md") {
		t.Fatalf("the flat list should carry the nested hit, got %v", flat)
	}

	s.Update(sh, altKey('t'))
	if s.flat {
		t.Fatal("alt+t should leave the flat list")
	}
	folder := rowTitles(s.filePanel.List())
	if !hasRow(folder, "sub/") {
		t.Fatalf("the folder view should list directories, got %v", folder)
	}
	if hasRow(folder, "deep.md") {
		t.Fatalf("a nested file has no row until you walk into its folder, got %v", folder)
	}
	if hasRow(folder, "node_modules/") {
		t.Fatalf("the scan's pruning rules should apply here too, got %v", folder)
	}
	if !hasRow(folder, "+ new file") {
		t.Fatalf("the action row should survive the swap, got %v", folder)
	}

	s.Update(sh, altKey('t'))
	if !s.flat {
		t.Fatal("alt+t should come back to the flat list")
	}
	if got := rowTitles(s.docsPanel.List()); !hasRow(got, "deep.md") {
		t.Fatalf("the flat list should be exactly what it was, got %v", got)
	}
}

// TestFolderViewFromConfig: folder_view picks which view gote opens on — and only that.
// The scan still runs behind it, so alt+t shows a flat list that is already seeded rather
// than one that has to go and read the tree first.
func TestFolderViewFromConfig(t *testing.T) {
	root := scanTree(t)
	cfg := DefaultConfig()
	cfg.FolderView = true
	s, sh := newHomeCfg(t, cfg, Options{Mode: ModeScan, Dir: root, Depth: 3, DepthSet: true})

	if s.flat {
		t.Fatal("folder_view: true should open on the folder view")
	}
	if got := rowTitles(s.filePanel.List()); !hasRow(got, "sub/") {
		t.Fatalf("the folder view should be the live panel, got %v", got)
	}
	// The half the key must NOT touch.
	var found bool
	for _, d := range Of(sh).Files {
		if d.Name == "deep.md" {
			found = true
		}
	}
	if !found {
		t.Fatalf("the scan must still run; Files = %v", Of(sh).Files)
	}
	if got := rowTitles(s.docsPanel.List()); !hasRow(got, "deep.md") {
		t.Fatalf("the flat list should be seeded behind the folder view, got %v", got)
	}

	s.Update(sh, altKey('t'))
	if !s.flat {
		t.Fatal("alt+t should still swap back to the flat list")
	}
}

// TestFlatViewIsTheDefault: an unset folder_view leaves gote exactly as it was.
func TestFlatViewIsTheDefault(t *testing.T) {
	root := scanTree(t)
	s, _ := newHomeCfg(t, DefaultConfig(), Options{Mode: ModeScan, Dir: root})
	if !s.flat {
		t.Fatal("the default config should open on the flat list")
	}
}

// TestFolderViewOpensDoc: enter on a file row goes through openDoc, so the folder view
// reaches the editor by the same road the flat list does — buffers and all.
func TestFolderViewOpensDoc(t *testing.T) {
	root := scanTree(t)
	s, sh := newScanHome(t, root)
	s.Update(sh, altKey('t'))

	selectRow(t, s.filePanel.List(), "notes.md")
	s.Update(sh, keyMsg("enter"))
	if want := filepath.Join(root, "notes.md"); s.currentPath != want {
		t.Fatalf("currentPath = %q, want %q", s.currentPath, want)
	}
	if _, open := Of(sh).Doc(filepath.Join(root, "notes.md")); !open {
		t.Fatal("the doc should be registered in the open set, as a flat pick registers it")
	}
}

// TestFolderViewWalksIntoFolder: enter on a folder lists it, and the rename/delete row
// keys still reach the files inside.
func TestFolderViewWalksIntoFolder(t *testing.T) {
	root := scanTree(t)
	s, sh := newScanHome(t, root)
	s.Update(sh, altKey('t'))

	selectRow(t, s.filePanel.List(), "sub/")
	s.Update(sh, keyMsg("enter"))
	if want := filepath.Join(root, "sub"); s.filePanel.Dir() != want {
		t.Fatalf("Dir() = %q, want %q", s.filePanel.Dir(), want)
	}
	if got := rowTitles(s.filePanel.List()); !hasRow(got, "deep.md") || !hasRow(got, "..") {
		t.Fatalf("inside sub the rows should be .. and deep.md, got %v", got)
	}

	selectRow(t, s.filePanel.List(), "deep.md")
	if _, handled := s.filePanel.UpdatePanel(sh, keyMsg("ctrl+r")); !handled {
		t.Fatal("ctrl+r should still rename from the folder view")
	}
}

// TestFolderViewRootClamp: the explorer's floor is the scan root, so the sidebar cannot
// wander off into files the rest of gote knows nothing about.
func TestFolderViewRootClamp(t *testing.T) {
	root := scanTree(t)
	s, sh := newScanHome(t, root)
	s.Update(sh, altKey('t'))

	if got := rowTitles(s.filePanel.List()); hasRow(got, "..") {
		t.Fatalf("the scan root should offer no way above it, got %v", got)
	}
	s.filePanel.SetDir(sh, filepath.Dir(root))
	if s.filePanel.Dir() != root {
		t.Fatalf("Dir() = %q, want the clamped %q", s.filePanel.Dir(), root)
	}
}

// TestFolderViewNewFileUsesCurrentFolder: "+ new file" means the folder on screen. In the
// flat list there is no such folder and the scan root is still the answer.
func TestFolderViewNewFileUsesCurrentFolder(t *testing.T) {
	root := scanTree(t)
	s, sh := newScanHome(t, root)
	s.Update(sh, altKey('t'))
	selectRow(t, s.filePanel.List(), "sub/")
	s.Update(sh, keyMsg("enter"))

	s.createFile(sh, "made.md")
	if _, err := os.Stat(filepath.Join(root, "sub", "made.md")); err != nil {
		t.Fatalf("the file should land in the folder on screen: %v", err)
	}

	s.setFlat(true)
	s.createFile(sh, "flat.md")
	if _, err := os.Stat(filepath.Join(root, "flat.md")); err != nil {
		t.Fatalf("the flat list should still create at the scan root: %v", err)
	}
}

// TestFolderViewReseed: the Refresh broadcast reaches the explorer too, so a file created
// or deleted elsewhere shows up in whichever view is live.
func TestFolderViewReseed(t *testing.T) {
	root := scanTree(t)
	s, sh := newScanHome(t, root)
	s.Update(sh, altKey('t'))

	if err := os.WriteFile(filepath.Join(root, "later.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	s.Receive(sh, ReseedMsg{})
	if got := rowTitles(s.filePanel.List()); !hasRow(got, "later.md") {
		t.Fatalf("a reseed should reach the folder view, got %v", got)
	}
}

// TestFolderViewDensity: the explorer's own chord flips its row density in place.
func TestFolderViewDensity(t *testing.T) {
	root := scanTree(t)
	s, sh := newScanHome(t, root)
	s.Update(sh, altKey('t'))
	s.filePanel.Focus()

	if !s.filePanel.Compact() {
		t.Fatal("the sidebar explorer should start compact")
	}
	if _, handled := s.filePanel.UpdatePanel(sh, altKey('r')); !handled {
		t.Fatal("alt+r should be claimed by the explorer")
	}
	if s.filePanel.Compact() {
		t.Fatal("alt+r should have flipped the density")
	}
	if s.filePanel.Dir() != root {
		t.Fatal("the flip must not move the explorer")
	}
}

// TestFolderViewInHelp: the ? overlay is gote's complete reference, so the folder view's
// keys have to be written there or they are written nowhere.
func TestFolderViewInHelp(t *testing.T) {
	root := scanTree(t)
	s, _ := newScanHome(t, root)
	help := s.helpText()
	for _, want := range []string{"alt+t", "folder view", "alt+r", "row density", "backspace"} {
		if !strings.Contains(help, want) {
			t.Fatalf("missing %q from the ? overlay:\n%s", want, help)
		}
	}
}

// TestFolderViewMinimalModeStaysPut: single-file mode has no sidebar to swap a panel into.
func TestFolderViewMinimalModeStaysPut(t *testing.T) {
	path := filepath.Join(t.TempDir(), "single.md")
	s, sh := newHomeWith(t, Options{Mode: ModeFile, File: path})
	s.Update(sh, altKey('t'))
	if !s.flat {
		t.Fatal("minimal mode should refuse the swap, as it refuses the sidebar")
	}
}

// hasRow reports whether one of the rendered row titles is name.
func hasRow(rows []string, name string) bool {
	for _, r := range rows {
		if r == name {
			return true
		}
	}
	return false
}

// selectRow puts the list's cursor on the row titled name.
func selectRow(t *testing.T, l *list.Model, name string) {
	t.Helper()
	for i, r := range rowTitles(l) {
		if r == name {
			l.Select(i)
			return
		}
	}
	t.Fatalf("no row %q in %v", name, rowTitles(l))
}
