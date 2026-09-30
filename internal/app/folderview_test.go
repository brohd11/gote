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
	t.Setenv("USERPROFILE", os.Getenv("HOME"))
	sh := core.NewShared(New("test", cfg, opts))
	s := NewHomeScreen(sh).(*homeScreen)
	s.gitDocs.timer = noDocsGitTimer
	s.Init(sh)
	s.SetSize(sh, 100, 30)
	return s, sh
}

// altKey builds gote's alt chords, which keyMsg-style helpers elsewhere don't cover.
// nextFileView steps to the next docs view, as View → File view does (it used to be alt+f).
func nextFileView(s *homeScreen) { s.setFileView((s.fileView + 1) % fileViewCount) }

func altKey(r rune) tea.KeyMsg {
	return keyMsg("alt+" + string(r))
}

// TestFolderViewToggle: the views cycle flat, folder browser, grouped folders, then flat.
func TestFolderViewToggle(t *testing.T) {
	root := scanTree(t)
	s, _ := newScanHome(t, root)

	if s.fileView != fileViewFlat {
		t.Fatal("gote should start on the flat list")
	}
	flat := rowTitles(s.docsPanel.List())
	if hasRow(flat, "+ new file") {
		t.Fatalf("flat view should only contain documents, got %v", flat)
	}
	if !hasRow(flat, "deep.md") {
		t.Fatalf("the flat list should carry the nested hit, got %v", flat)
	}

	nextFileView(s)
	if s.fileView != fileViewFolder {
		t.Fatal("switching views should enter the folder browser")
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
	if hasRow(folder, "+ new file") {
		t.Fatalf("folder view should only contain filesystem entries, got %v", folder)
	}

	nextFileView(s)
	if s.fileView != fileViewGrouped || !hasRow(rowTitles(s.groupedPanel.List()), "deep.md") {
		t.Fatal("switching views should show grouped scan results")
	}
	nextFileView(s)
	if s.fileView != fileViewFlat {
		t.Fatal("switching views should come back to the flat list")
	}
	if got := rowTitles(s.docsPanel.List()); !hasRow(got, "deep.md") {
		t.Fatalf("the flat list should be exactly what it was, got %v", got)
	}
}

// TestFolderViewFromConfig: folder_view picks which view gote opens on — and only that.
// The scan still runs behind it, so alt+f shows a flat list that is already seeded rather
// than one that has to go and read the tree first.
func TestFolderViewFromConfig(t *testing.T) {
	root := scanTree(t)
	cfg := DefaultConfig()
	cfg.FolderView = true
	s, sh := newHomeCfg(t, cfg, Options{Mode: ModeScan, Dir: root, Depth: 3, DepthSet: true})

	if s.fileView != fileViewFolder {
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

	nextFileView(s)
	nextFileView(s)
	if s.fileView != fileViewFlat {
		t.Fatal("switching views should cycle back to the flat list")
	}
}

// TestFlatViewIsTheDefault: an unset folder_view leaves gote exactly as it was.
func TestFlatViewIsTheDefault(t *testing.T) {
	root := scanTree(t)
	s, _ := newHomeCfg(t, DefaultConfig(), Options{Mode: ModeScan, Dir: root})
	if s.fileView != fileViewFlat {
		t.Fatal("the default config should open on the flat list")
	}
}

// TestFolderViewOpensDoc: enter on a file row goes through openDoc, so the folder view
// reaches the editor by the same road the flat list does — buffers and all.
func TestFolderViewOpensDoc(t *testing.T) {
	root := scanTree(t)
	s, sh := newScanHome(t, root)
	nextFileView(s)

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
	nextFileView(s)

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

func TestFolderViewNavigationKeys(t *testing.T) {
	root := scanTree(t)
	model, s, _ := newHomeRouter(t, Options{Mode: ModeScan, Dir: root, Depth: 3, DepthSet: true})
	nextFileView(s)
	selectRow(t, s.filePanel.List(), "sub/")
	model, _ = model.Update(keyMsg("d"))
	sub := filepath.Join(root, "sub")
	if s.filePanel.Dir() != sub {
		t.Fatal("d should enter the selected folder")
	}
	model, _ = model.Update(keyMsg("backspace"))
	if s.filePanel.Dir() != sub {
		t.Fatal("Backspace must no longer ascend")
	}
	model, _ = model.Update(keyMsg("x"))
	e, ok := s.filePanel.Selected()
	if s.filePanel.Dir() != root || !ok || e.Path != sub {
		t.Fatal("x should return to the parent and select the folder just left")
	}
	model, _ = model.Update(keyMsg("x"))
	if s.filePanel.Dir() != root {
		t.Fatal("x must not escape the scan root")
	}
	model, _ = model.Update(keyMsg("d"))
	selectRow(t, s.filePanel.List(), "..")
	model, _ = model.Update(keyMsg("d"))
	if s.filePanel.Dir() != root {
		t.Fatal("d on .. should enter the parent")
	}
	selectRow(t, s.filePanel.List(), "notes.md")
	before := s.currentID
	model, _ = model.Update(keyMsg("d"))
	if s.filePanel.Dir() != root || s.currentID != before || model.(core.Router).Top() != s {
		t.Fatal("d on a document should neither open it nor raise an overlay")
	}
}

func TestFolderViewKeysRespectInputAndFocus(t *testing.T) {
	for _, state := range []string{"filter", "editor", "flat", "grouped", "hidden", "resizing"} {
		t.Run(state, func(t *testing.T) {
			root := scanTree(t)
			model, s, _ := newHomeRouter(t, Options{Mode: ModeScan, Dir: root, Depth: 3, DepthSet: true})
			nextFileView(s)
			selectRow(t, s.filePanel.List(), "sub/")
			// A nested folder gives both keys somewhere to go: d enters .. and x ascends.
			model, _ = model.Update(keyMsg("d"))
			selectRow(t, s.filePanel.List(), "..")
			switch state {
			case "filter":
				model, _ = model.Update(keyMsg("/"))
			case "editor":
				s.modular.FocusSlot(s.editorSlot())
			case "flat":
				nextFileView(s)
				nextFileView(s)
			case "grouped":
				nextFileView(s)
			case "hidden":
				model, _ = model.Update(keyMsg(`alt+\`))
			case "resizing":
				s.modular.SetResizing(true)
			}
			before := s.editor.Text()
			model, _ = model.Update(keyMsg("d"))
			model, _ = model.Update(keyMsg("x"))
			model, _ = model.Update(keyMsg("."))
			if s.showHidden {
				t.Fatalf("hidden toggle fired while %s", state)
			}
			if s.filePanel.Dir() != filepath.Join(root, "sub") {
				t.Fatalf("folder navigation fired while %s", state)
			}
			if state == "filter" {
				if got := s.filePanel.List().FilterInput.Value(); got != "dx." {
					t.Fatalf("filter input = %q, want dx.", got)
				}
				model.Update(keyMsg("backspace"))
				if got := s.filePanel.List().FilterInput.Value(); got != "dx" {
					t.Fatalf("Backspace should edit the filter, got %q", got)
				}
			}
			if state == "editor" || state == "hidden" {
				if s.editor.Text() == before || !strings.Contains(s.editor.Text(), "dx.") {
					t.Fatal("d, x and . should reach the editor")
				}
			}
		})
	}
}

func TestFolderViewHiddenToggle(t *testing.T) {
	for _, restricted := range []bool{false, true} {
		name := "text files"
		if restricted {
			name = "extensions"
		}
		t.Run(name, func(t *testing.T) {
			root := scanTree(t)
			writeSearchFile(t, root, ".hidden.md", "hidden\n")
			writeSearchFile(t, root, ".secret/note.md", "nested\n")
			writeSearchFile(t, root, ".binary.bin", "\x00\x01\x02")
			writeSearchFile(t, root, ".other.txt", "text\n")
			cfg := DefaultConfig()
			cfg.FolderView = true
			if restricted {
				cfg.Extensions = []string{"md"}
			}
			s, sh := newHomeCfg(t, cfg, Options{Mode: ModeScan, Dir: root})
			flatBefore := strings.Join(rowTitles(s.docsPanel.List()), "\n")
			for _, row := range []string{".hidden.md", ".secret/"} {
				if hasRow(rowTitles(s.filePanel.List()), row) {
					t.Fatalf("%s visible at startup", row)
				}
			}
			s.Update(sh, keyMsg("."))
			rows := rowTitles(s.filePanel.List())
			if !hasRow(rows, ".hidden.md") || !hasRow(rows, ".secret/") {
				t.Fatalf("hidden entries missing after toggle: %v", rows)
			}
			if hasRow(rows, "node_modules/") || hasRow(rows, ".binary.bin") ||
				hasRow(rows, ".other.txt") == restricted {
				t.Fatalf("toggle changed existing filters: %v", rows)
			}
			selectRow(t, s.filePanel.List(), ".secret/")
			s.Update(sh, keyMsg("d"))
			selectRow(t, s.filePanel.List(), "..")
			s.Update(sh, keyMsg("."))
			if s.showHidden || s.filePanel.Dir() != filepath.Join(root, ".secret") {
				t.Fatal("toggle on .. should hide entries without leaving the current folder")
			}
			s.Update(sh, keyMsg("x"))
			if hasRow(rowTitles(s.filePanel.List()), ".secret/") {
				t.Fatal("hidden directory reappeared after navigation")
			}
			s.Update(sh, keyMsg("."))
			nextFileView(s)
			nextFileView(s)
			if got := strings.Join(rowTitles(s.docsPanel.List()), "\n"); got != flatBefore {
				t.Fatal("toggle changed the flat list")
			}
			nextFileView(s)
			s.Receive(sh, ReseedMsg{})
			if !hasRow(rowTitles(s.filePanel.List()), ".hidden.md") {
				t.Fatal("view change or refresh lost hidden visibility")
			}
			vault := t.TempDir()
			writeSearchFile(t, vault, ".vault.md", "vault\n")
			Of(sh).Config.Vaults["hidden-test"] = VaultConfig{Path: vault}
			s.activateVault(sh, "hidden-test")
			if s.filePanel.Dir() != vault || !hasRow(rowTitles(s.filePanel.List()), ".vault.md") {
				t.Fatal("vault switch lost hidden visibility")
			}
			s.Update(sh, keyMsg("."))
			if _, ok := s.filePanel.Selected(); ok {
				t.Fatal("hiding the only file should leave an empty listing")
			}
			s.Update(sh, keyMsg("."))
			if entry, ok := s.filePanel.Selected(); !ok || entry.Name != ".vault.md" {
				t.Fatal("toggle in an empty listing should restore a valid selection")
			}
		})
	}
}

// TestFolderViewRootClamp: the explorer's floor is the scan root, so the sidebar cannot
// wander off into files the rest of gote knows nothing about.
func TestFolderViewRootClamp(t *testing.T) {
	root := scanTree(t)
	s, sh := newScanHome(t, root)
	nextFileView(s)

	if got := rowTitles(s.filePanel.List()); hasRow(got, "..") {
		t.Fatalf("the scan root should offer no way above it, got %v", got)
	}
	s.filePanel.SetDir(sh, filepath.Dir(root))
	if s.filePanel.Dir() != root {
		t.Fatalf("Dir() = %q, want the clamped %q", s.filePanel.Dir(), root)
	}
}

// TestFolderViewReseed: the Refresh broadcast reaches the explorer too, so a file created
// or deleted elsewhere shows up in whichever view is live.
func TestFolderViewReseed(t *testing.T) {
	root := scanTree(t)
	s, sh := newScanHome(t, root)
	nextFileView(s)

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
	nextFileView(s)
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
	for _, want := range []string{"alt+r", "row density", ".", "show or hide dot files (folder view)"} {
		if !strings.Contains(help, want) {
			t.Fatalf("missing %q from the ? overlay:\n%s", want, help)
		}
	}
	for _, line := range strings.Split(help, "\n") {
		fields := strings.Fields(line)
		if strings.Contains(line, "enter selected folder") && fields[0] != "d" {
			t.Fatalf("wrong descend binding in help: %s", line)
		}
		if strings.Contains(line, "up a folder") && fields[0] != "x" {
			t.Fatalf("wrong up binding in help: %s", line)
		}
	}
	for _, want := range []string{"enter selected folder (folder view)", "up a folder (folder view)"} {
		if !strings.Contains(help, want) {
			t.Fatalf("missing folder navigation hint %q", want)
		}
	}
}

// TestFolderViewMinimalModeStaysPut: single-file mode has no sidebar to swap a panel into.
func TestFolderViewMinimalModeStaysPut(t *testing.T) {
	path := filepath.Join(t.TempDir(), "single.md")
	s, _ := newHomeWith(t, Options{Mode: ModeFile, File: path})
	nextFileView(s)
	if s.fileView != fileViewFlat {
		t.Fatal("minimal mode should refuse the swap, as it refuses the sidebar")
	}
}

func TestFileViewSwitchEmptyListsAndDirectories(t *testing.T) {
	model, s, sh := newHomeRouter(t, Options{Mode: ModeScan, Dir: t.TempDir()})
	defer Of(sh).close()
	for i := 0; i < 6; i++ {
		before := s.fileView
		nextFileView(s)
		if s.fileView != (before+1)%fileViewCount || s.focusedPane() != s.docsPane() {
			t.Fatal("empty Docs pane could not toggle or lost focus")
		}
	}
	root := scanTree(t)
	model, s, sh = newHomeRouter(t, Options{Mode: ModeScan, Dir: root, Depth: 3, DepthSet: true})
	defer Of(sh).close()
	nextFileView(s)
	selectRow(t, s.filePanel.List(), "sub/")
	nextFileView(s)
	if s.fileView != fileViewGrouped {
		t.Fatal("directory row blocked toggle")
	}
	nextFileView(s)
	nextFileView(s)
	selectRow(t, s.filePanel.List(), "sub/")
	model, _ = model.Update(keyMsg("d"))
	selectRow(t, s.filePanel.List(), "..")
	nextFileView(s)
	if s.fileView != fileViewGrouped || s.focusedPane() != s.docsPane() {
		t.Fatal("parent-directory row blocked toggle")
	}
	nextFileView(s)
	nextFileView(s)
	if s.filePanel.Dir() != filepath.Join(root, "sub") {
		t.Fatal("toggle reset folder location")
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
