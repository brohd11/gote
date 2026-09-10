package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/brohd11/bubblestack/components/editor"
	"github.com/brohd11/bubblestack/core"
)

// sessionHome isolates ~/.gote for a test, the way every other test here does, so a run
// never reads or writes the developer's real session file.
func sessionHome(t *testing.T) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", os.Getenv("HOME"))
}

// writeDoc creates a file with n numbered lines and returns its path.
func writeDoc(t *testing.T, dir, name string, n int) string {
	t.Helper()
	lines := make([]string, n)
	for i := range lines {
		lines[i] = "line " + strings.Repeat("x", i%7)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// loadedEditor is a buffer that has actually read its file and been given a viewport,
// which is what CursorPosition and TopLine need to report anything meaningful.
func loadedEditor(t *testing.T, c *Ctx, path string) *editor.Screen {
	t.Helper()
	ed := c.OpenDoc(path, editor.Opts{})
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	ed.SetText(string(b))
	ed.SetSize(nil, 80, 10)
	return ed
}

func TestSessionKeyPerMode(t *testing.T) {
	sessionHome(t)
	dir := t.TempDir()
	cases := []struct {
		name string
		opts Options
		want string
	}{
		{"scan", Options{Mode: ModeScan, Dir: dir}, filepath.Clean(dir)},
		{"vault", Options{Mode: ModeVault, Dir: dir, Vault: "notes"}, "vault:notes"},
		{"file", Options{Mode: ModeFile, File: filepath.Join(dir, "a.md")}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := sessionKey(New("dev", DefaultConfig(), tc.opts)); got != tc.want {
				t.Fatalf("sessionKey = %q, want %q", got, tc.want)
			}
		})
	}
	// ModeHome resolves through resolveDefault, which can retarget it at a vault; the
	// default config leaves it on the home store.
	if got := sessionKey(New("dev", DefaultConfig(), Options{Mode: ModeHome})); got != homeSessionKey {
		t.Fatalf("home sessionKey = %q, want %q", got, homeSessionKey)
	}
}

func TestSaveSessionRoundTrip(t *testing.T) {
	sessionHome(t)
	dir := t.TempDir()
	first := writeDoc(t, dir, "first.md", 100)
	second := writeDoc(t, dir, "second.md", 100)

	c := New("dev", DefaultConfig(), Options{Mode: ModeScan, Dir: dir})
	loadedEditor(t, c, first)
	ed := loadedEditor(t, c, second)
	ed.Reveal(editor.Position{Line: 42, Column: 3})
	ed.SetTopLine(40)
	c.SetActive(second)

	if err := c.saveSession(); err != nil {
		t.Fatal(err)
	}

	// It must be its own file, not a key smuggled into the user's config.
	path, err := SessionsPath()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("sessions file not written: %v", err)
	}
	cfgPath, err := ConfigPath()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(cfgPath); !os.IsNotExist(err) {
		t.Fatalf("saving a session must not create or touch config.yml (err = %v)", err)
	}

	sessions, err := LoadSessions()
	if err != nil {
		t.Fatal(err)
	}
	got, ok := sessions.Sessions[filepath.Clean(dir)]
	if !ok {
		t.Fatalf("no session for %q in %#v", dir, sessions.Sessions)
	}
	if got.Active != second {
		t.Fatalf("active = %q, want %q", got.Active, second)
	}
	want := []SessionFile{
		{Path: first},
		{Path: second, Line: 42, Col: 3, Top: 40},
	}
	if len(got.Files) != len(want) {
		t.Fatalf("files = %#v, want %#v", got.Files, want)
	}
	for i, w := range want {
		if got.Files[i] != w {
			t.Fatalf("file %d = %#v, want %#v", i, got.Files[i], w)
		}
	}
}

func TestSaveSessionSkipsUnsavedAndFileMode(t *testing.T) {
	sessionHome(t)
	dir := t.TempDir()
	doc := writeDoc(t, dir, "doc.md", 10)

	// ModeFile does not participate at all.
	fileCtx := New("dev", DefaultConfig(), Options{Mode: ModeFile, File: doc})
	loadedEditor(t, fileCtx, doc)
	if err := fileCtx.saveSession(); err != nil {
		t.Fatal(err)
	}
	if path, err := SessionsPath(); err != nil {
		t.Fatal(err)
	} else if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("ModeFile must write no session file (err = %v)", err)
	}

	// A pathless buffer has nothing to be reopened by, so it is not recorded — and a
	// session of nothing but those writes no row at all.
	c := New("dev", DefaultConfig(), Options{Mode: ModeScan, Dir: dir})
	id, name := c.newUnsavedIdentity()
	c.trackUnsaved(id, name, editor.New(editor.Opts{}))
	c.SetActive(id)
	if err := c.saveSession(); err != nil {
		t.Fatal(err)
	}
	sessions, err := LoadSessions()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := sessions.Sessions[filepath.Clean(dir)]; ok {
		t.Fatalf("unsaved-only session should record nothing, got %#v", sessions.Sessions)
	}
}

// A session whose every buffer was closed must not reopen them next launch.
func TestSaveSessionClosingEverythingClearsTheRow(t *testing.T) {
	sessionHome(t)
	dir := t.TempDir()
	doc := writeDoc(t, dir, "doc.md", 10)

	c := New("dev", DefaultConfig(), Options{Mode: ModeScan, Dir: dir})
	loadedEditor(t, c, doc)
	if err := c.saveSession(); err != nil {
		t.Fatal(err)
	}
	c.CloseDoc(doc)
	if err := c.saveSession(); err != nil {
		t.Fatal(err)
	}
	sessions, err := LoadSessions()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := sessions.Sessions[filepath.Clean(dir)]; ok {
		t.Fatalf("closing every buffer should drop the row, got %#v", sessions.Sessions)
	}
}

// The regression this design exists to prevent: a restored buffer the user never
// switched to has an editor that never read its file and reports a caret at the origin.
// Saving must write back what was restored, not that.
func TestSaveSessionKeepsUnvisitedRestorePosition(t *testing.T) {
	sessionHome(t)
	dir := t.TempDir()
	doc := writeDoc(t, dir, "doc.md", 100)
	rec := SessionFile{Path: doc, Line: 61, Col: 4, Top: 55}

	c := New("dev", DefaultConfig(), Options{Mode: ModeScan, Dir: dir})
	ed := c.OpenDoc(doc, editor.Opts{}) // registered, never Init'd: no text, caret at 0,0
	c.restore = map[string]SessionFile{doc: rec}
	if pos := ed.CursorPosition(); pos.Line != 0 {
		t.Fatalf("precondition: unread buffer should sit at the origin, got %#v", pos)
	}

	if err := c.saveSession(); err != nil {
		t.Fatal(err)
	}
	sessions, err := LoadSessions()
	if err != nil {
		t.Fatal(err)
	}
	got := sessions.Sessions[filepath.Clean(dir)].Files
	if len(got) != 1 || got[0] != rec {
		t.Fatalf("files = %#v, want [%#v]", got, rec)
	}
}

// A pending position has to follow its file through a rename or save-as, which both
// funnel through RekeyDoc.
func TestRekeyDocMovesRestorePosition(t *testing.T) {
	sessionHome(t)
	dir := t.TempDir()
	doc := writeDoc(t, dir, "doc.md", 100)
	next := filepath.Join(dir, "renamed.md")
	rec := SessionFile{Path: doc, Line: 61, Col: 4, Top: 55}

	c := New("dev", DefaultConfig(), Options{Mode: ModeScan, Dir: dir})
	ed := c.OpenDoc(doc, editor.Opts{})
	c.restore = map[string]SessionFile{doc: rec}
	c.RekeyDoc(doc, next, ed)

	if _, stale := c.restore[doc]; stale {
		t.Fatal("the old path should not still hold a pending position")
	}
	want := rec
	want.Path = next
	if got, ok := c.restore[next]; !ok || got != want {
		t.Fatalf("restore[%q] = %#v (ok=%v), want %#v", next, got, ok, want)
	}
}

func TestLoadSessionForDropsVanishedFiles(t *testing.T) {
	sessionHome(t)
	dir := t.TempDir()
	kept := writeDoc(t, dir, "kept.md", 10)
	gone := writeDoc(t, dir, "gone.md", 10)

	c := New("dev", DefaultConfig(), Options{Mode: ModeScan, Dir: dir})
	loadedEditor(t, c, kept)
	loadedEditor(t, c, gone)
	c.SetActive(gone)
	if err := c.saveSession(); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(gone); err != nil {
		t.Fatal(err)
	}

	session, ok := loadSessionFor(New("dev", DefaultConfig(), Options{Mode: ModeScan, Dir: dir}))
	if !ok {
		t.Fatal("a session with one surviving file should still restore")
	}
	if len(session.Files) != 1 || session.Files[0].Path != kept {
		t.Fatalf("files = %#v, want just %q", session.Files, kept)
	}
	// Active named the deleted file; restoreSession is what falls back, but the record
	// itself is left as written rather than quietly rewritten here.
	if session.Active != gone {
		t.Fatalf("active = %q, want the recorded %q", session.Active, gone)
	}

	// Every file gone means nothing to restore.
	if err := os.Remove(kept); err != nil {
		t.Fatal(err)
	}
	if _, ok := loadSessionFor(New("dev", DefaultConfig(), Options{Mode: ModeScan, Dir: dir})); ok {
		t.Fatal("a session whose every file is gone should not restore")
	}
}

func TestSessionsPrune(t *testing.T) {
	live := t.TempDir()
	sessions := Sessions{Sessions: map[string]Session{
		live:                       {Files: []SessionFile{{Path: "a"}}},
		filepath.Join(live, "no"):  {Files: []SessionFile{{Path: "b"}}},
		homeSessionKey:             {Files: []SessionFile{{Path: "c"}}},
		vaultKeyPrefix + "missing": {Files: []SessionFile{{Path: "d"}}},
	}}
	sessions.prune()
	if _, ok := sessions.Sessions[filepath.Join(live, "no")]; ok {
		t.Fatal("a root that is no longer a directory should be dropped")
	}
	for _, key := range []string{live, homeSessionKey, vaultKeyPrefix + "missing"} {
		if _, ok := sessions.Sessions[key]; !ok {
			t.Fatalf("%q should have survived: symbolic keys name no directory", key)
		}
	}

	// Past the cap, the least recently used go first.
	sessions = Sessions{Sessions: map[string]Session{}}
	base := time.Now()
	for i := 0; i < sessionLimit+10; i++ {
		sessions.Sessions[vaultKeyPrefix+string(rune('a'+i%26))+string(rune('a'+i/26))] =
			Session{Updated: base.Add(time.Duration(i) * time.Minute)}
	}
	total := len(sessions.Sessions)
	sessions.prune()
	if len(sessions.Sessions) != sessionLimit {
		t.Fatalf("pruned to %d entries, want %d (from %d)", len(sessions.Sessions), sessionLimit, total)
	}
	for _, s := range sessions.Sessions {
		if s.Updated.Before(base.Add(time.Duration(total-sessionLimit) * time.Minute)) {
			t.Fatalf("prune kept a stale entry: %v", s.Updated)
		}
	}
}

// A file gote cannot parse costs a restore, never a launch — and never blocks the next
// save, or one bad byte would be permanent.
func TestCorruptSessionsFileIsSurvivable(t *testing.T) {
	sessionHome(t)
	dir := t.TempDir()
	doc := writeDoc(t, dir, "doc.md", 10)

	stateDir, err := StateDir()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	path, err := SessionsPath()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("\tnot: [valid\nyaml"), 0o644); err != nil {
		t.Fatal(err)
	}

	c := New("dev", DefaultConfig(), Options{Mode: ModeScan, Dir: dir})
	if _, ok := loadSessionFor(c); ok {
		t.Fatal("a corrupt file should restore nothing rather than fail")
	}
	loadedEditor(t, c, doc)
	if err := c.saveSession(); err != nil {
		t.Fatalf("a corrupt file must be overwritten, not fatal: %v", err)
	}
	sessions, err := LoadSessions()
	if err != nil {
		t.Fatal(err)
	}
	if got := sessions.Sessions[filepath.Clean(dir)].Files; len(got) != 1 {
		t.Fatalf("files = %#v, want one", got)
	}
}

// restoredHome relaunches into dir the way NewHomeScreen does, without newHomeWith's
// fresh HOME — the point of the test is the session file already sitting in this one.
func restoredHome(t *testing.T, dir string) (*homeScreen, *core.Shared) {
	t.Helper()
	cfg := DefaultConfig()
	cfg.OpenDocsView = "list"
	sh := core.NewShared(New("test", cfg, Options{Mode: ModeScan, Dir: dir}))
	s := NewHomeScreen(sh).(*homeScreen)
	s.gitDocs.timer = noDocsGitTimer
	s.Init(sh)
	s.SetSize(sh, 100, 30)
	return s, sh
}

// End to end: a launch into a root with a banked session reopens its buffers, lands on
// the one that was focused, and puts the caret and viewport back once the file's
// asynchronous read arrives.
func TestRestoreSessionReopensBuffers(t *testing.T) {
	sessionHome(t)
	dir := t.TempDir()
	first := writeDoc(t, dir, "first.md", 100)
	second := writeDoc(t, dir, "second.md", 100)

	// Bank a session the way the previous run's exit would have.
	prev := New("test", DefaultConfig(), Options{Mode: ModeScan, Dir: dir})
	loadedEditor(t, prev, first)
	ed := loadedEditor(t, prev, second)
	ed.Reveal(editor.Position{Line: 42, Column: 3})
	ed.SetTopLine(40)
	prev.SetActive(second)
	if err := prev.saveSession(); err != nil {
		t.Fatal(err)
	}

	s, sh := restoredHome(t, dir)
	c := Of(sh)
	if got := len(c.OpenDocs()); got != 2 {
		t.Fatalf("restored %d buffers, want 2", got)
	}
	if s.currentPath != second {
		t.Fatalf("pane is on %q, want the recorded active %q", s.currentPath, second)
	}
	if c.activeID != second {
		t.Fatalf("activeID = %q, want %q", c.activeID, second)
	}

	// Nothing has read a file yet, so nothing has been applied.
	if _, pending := c.restore[second]; !pending {
		t.Fatal("the caret should still be pending before the read lands")
	}

	// The read arriving is what finishHomeUpdate is waiting for.
	b, err := os.ReadFile(second)
	if err != nil {
		t.Fatal(err)
	}
	s.editor.SetText(string(b))
	s.finishHomeUpdate(sh, core.Action{})

	if pos := s.editor.CursorPosition(); pos.Line != 42 || pos.Column != 3 {
		t.Fatalf("caret = %#v, want line 42 col 3", pos)
	}
	if got := s.editor.TopLine(); got != 40 {
		t.Fatalf("top line = %d, want the recorded 40 — Reveal's centering must not win", got)
	}
	if _, pending := c.restore[second]; pending {
		t.Fatal("an applied position should be drained")
	}
	// The buffer never switched to keeps its entry, which is what lets the next save
	// write the real position back rather than an unread editor's origin.
	if _, pending := c.restore[first]; !pending {
		t.Fatal("an unvisited buffer should still hold its pending position")
	}
}

// A root with no session behaves exactly as gote always has: one scratch buffer.
func TestRestoreSessionAbsentInstallsScratch(t *testing.T) {
	sessionHome(t)
	s, sh := restoredHome(t, t.TempDir())
	if s.currentPath != "" || len(Of(sh).OpenDocs()) != 0 {
		t.Fatalf("want the usual scratch buffer, got path %q and %d open",
			s.currentPath, len(Of(sh).OpenDocs()))
	}
}

// A restored buffer sits in the open set without having read its file, which is a state
// that could not previously exist. Opening one while the full-screen reader holds the
// pane must still seed it: the reader would otherwise swallow the editor's asynchronous
// load, leaving an empty buffer aimed at a file that is not empty — and the first save
// would truncate it. That is the loss seedForPreview exists to prevent.
func TestRestoredBufferIsSeededUnderTheReader(t *testing.T) {
	sessionHome(t)
	dir := t.TempDir()
	first := writeDoc(t, dir, "first.md", 30)
	second := writeDoc(t, dir, "second.md", 30)

	prev := New("test", DefaultConfig(), Options{Mode: ModeScan, Dir: dir})
	loadedEditor(t, prev, first)
	loadedEditor(t, prev, second)
	prev.SetActive(second)
	if err := prev.saveSession(); err != nil {
		t.Fatal(err)
	}

	s, sh := restoredHome(t, dir)
	if !Of(sh).unread(first) {
		t.Fatal("setup: the unvisited buffer should be registered but unread")
	}
	// The active buffer's own read lands the ordinary way.
	b, err := os.ReadFile(second)
	if err != nil {
		t.Fatal(err)
	}
	s.editor.SetText(string(b))

	s.Update(sh, altP)
	if s.fullPreview == nil {
		t.Fatal("setup: alt+p should put the reader in the editor pane")
	}

	s.openDoc(sh, first)
	want, err := os.ReadFile(first)
	if err != nil {
		t.Fatal(err)
	}
	if got := s.editor.Text(); got != string(want) {
		t.Fatalf("restored buffer opened under the reader holds %q, want the file's %d bytes",
			got, len(want))
	}
}

// A restored buffer is in the open set without having read its file, which is a state
// nothing but session restore can produce. EachDoc holds it back until its text is real,
// because every consumer downstream falls back to the file on disk — and the disk is
// exactly what an unread buffer contains.
func TestEachDocSkipsUnreadRestoredBuffers(t *testing.T) {
	sessionHome(t)
	dir := t.TempDir()
	read := writeDoc(t, dir, "read.md", 5)
	unread := writeDoc(t, dir, "unread.md", 5)

	c := New("test", DefaultConfig(), Options{Mode: ModeScan, Dir: dir})
	loadedEditor(t, c, read)
	c.OpenDoc(unread, editor.Opts{})
	c.restore = map[string]SessionFile{unread: {Path: unread}}

	var got []string
	c.EachDoc(func(path string, _ *editor.Screen) { got = append(got, path) })
	if len(got) != 1 || got[0] != read {
		t.Fatalf("EachDoc yielded %v, want only the buffer that has read its file", got)
	}

	// Switching to it drains the restore entry, which is what makes its text speak for
	// the file from then on.
	delete(c.restore, unread)
	got = nil
	c.EachDoc(func(path string, _ *editor.Screen) { got = append(got, path) })
	if len(got) != 2 {
		t.Fatalf("EachDoc yielded %v once the read landed, want both buffers", got)
	}
}

// The reported bug: relaunching onto a test file reported every symbol in its package
// undefined, because the restored implementation file went to gopls as an empty
// document. An unread buffer must never be published — with no didOpen, the server reads
// the real file itself.
func TestReconcileOmitsUnreadRestoredBuffer(t *testing.T) {
	sessionHome(t)
	dir := t.TempDir()
	impl := filepath.Join(dir, "impl.py")
	if err := os.WriteFile(impl, []byte("def helper():\n    return 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	active := filepath.Join(dir, "main.py")
	if err := os.WriteFile(active, []byte("helper()\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := DefaultConfig()
	// An address nothing answers on: Reconcile does no IO, and the actor's failure to
	// dial is beside the point of the assertion.
	cfg.LanguageServers["python"] = LanguageServerConfig{Address: "127.0.0.1:1"}
	c := New("test", cfg, Options{Mode: ModeScan, Dir: dir})
	t.Cleanup(c.close)

	// The state a restore leaves behind: both registered, only the active one read.
	c.OpenDoc(impl, editor.Opts{})
	ed := c.OpenDoc(active, editor.Opts{})
	c.restore = map[string]SessionFile{impl: {Path: impl}, active: {Path: active}}
	ed.SetText("helper()\n")
	delete(c.restore, active) // what applyRestore does once the read lands

	c.lsp.Reconcile(c)
	c.lsp.mu.Lock()
	defer c.lsp.mu.Unlock()
	if _, published := c.lsp.desired[filepath.Clean(impl)]; published {
		t.Fatal("an unread restored buffer was published: the server would take the empty " +
			"editor as the file's contents and call every symbol in it undefined")
	}
	doc, ok := c.lsp.desired[filepath.Clean(active)]
	if !ok || doc.text != "helper()\n" {
		t.Fatalf("the buffer that HAS read its file must still be published, got %#v (ok=%v)", doc, ok)
	}
}

// The same bug's second symptom: the project search takes open buffers as content
// overrides, so an unread restored buffer used to make its file search as empty.
func TestSearchFindsUnreadRestoredBufferOnDisk(t *testing.T) {
	sessionHome(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "notes.md")
	if err := os.WriteFile(path, []byte("a needle in here\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	c := New("test", DefaultConfig(), Options{Mode: ModeScan, Dir: dir})
	c.OpenDoc(path, editor.Opts{})
	c.restore = map[string]SessionFile{path: {Path: path}}

	// The snapshot map beginFindFiles builds.
	snapshots := map[string]string{}
	c.EachDoc(func(p string, ed *editor.Screen) { snapshots[filepath.Clean(p)] = ed.Text() })

	got, _, err := searchFiles(context.Background(), dir, "needle", snapshots, searchResultLimit)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("search returned %#v, want the match read from disk — an unread buffer "+
			"must not override the file with its empty text", got)
	}
}
