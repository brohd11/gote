package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/brohd11/bubblestack/core"
	"github.com/brohd11/gitstack/repo"
)

func noDocsGitTimer(time.Duration, func(time.Time) tea.Msg) tea.Cmd { return nil }

func TestGitFileStates(t *testing.T) {
	for _, tc := range []struct {
		f    repo.FileStatus
		want gitFileState
	}{
		{repo.FileStatus{Index: '.', Worktree: '.'}, gitClean},
		{repo.FileStatus{Index: 'M', Worktree: '.'}, gitStaged},
		{repo.FileStatus{Index: 'A', Worktree: '.'}, gitStaged},
		{repo.FileStatus{Index: 'D', Worktree: '.'}, gitStaged},
		{repo.FileStatus{Index: 'R', Worktree: '.'}, gitStaged},
		{repo.FileStatus{Index: 'M', Worktree: 'M'}, gitModified},
		{repo.FileStatus{Index: 'A', Worktree: 'D'}, gitDeleted},
		{repo.FileStatus{Index: '.', Worktree: 'T'}, gitModified},
		{repo.FileStatus{Untracked: true}, gitUntracked},
		{repo.FileStatus{Conflict: true, Index: 'D', Worktree: 'D'}, gitConflict},
	} {
		if got := fileGitState(tc.f); got != tc.want {
			t.Fatalf("%+v = %v, want %v", tc.f, got, tc.want)
		}
	}
}

func docsGitRepo(t *testing.T) string {
	t.Helper()
	root := scanTree(t)
	gitInit(t, root)
	if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte("ignored.md\nnested/\n"), 0644); err != nil {
		t.Fatal(err)
	}
	gitRun(t, root, "add", ".")
	gitRun(t, root, "commit", "-qm", "initial")
	return root
}
func TestDocsGitSnapshotAndNestedOwnership(t *testing.T) {
	root := docsGitRepo(t)
	writeFile := func(path string) {
		t.Helper()
		if err := os.WriteFile(path, []byte("changed\n"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	writeFile(filepath.Join(root, "notes.md"))
	gitRun(t, root, "add", "notes.md")
	writeFile(filepath.Join(root, "sub", "deep.md"))
	writeFile(filepath.Join(root, "ignored.md"))
	nested := filepath.Join(root, "nested")
	if err := os.Mkdir(nested, 0755); err != nil {
		t.Fatal(err)
	}
	gitInit(t, nested)
	writeFile(filepath.Join(nested, "clean.md"))
	gitRun(t, nested, "add", ".")
	gitRun(t, nested, "commit", "-qm", "initial")
	writeFile(filepath.Join(nested, "untracked.md"))
	link := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(root, link); err != nil {
		t.Skip(err)
	}
	v := buildDocsGitSnapshot(context.Background(), link, link, 3)
	for rel, want := range map[string]gitFileState{"notes.md": gitStaged, "sub/deep.md": gitModified, "ignored.md": gitIgnored, "nested/clean.md": gitClean, "nested/untracked.md": gitUntracked} {
		if got := v.state(filepath.Join(link, rel), false); got != want {
			t.Errorf("%s = %v, want %v", rel, got, want)
		}
	}
	if v.state(filepath.Join(link, "sub"), true) != gitModified || v.state(filepath.Join(link, "nested"), true) != gitUntracked {
		t.Fatal("parent aggregation lost")
	}
	// A removed tracked file remains part of its parent's state.
	if err := os.Remove(filepath.Join(root, "sub", "deep.md")); err != nil {
		t.Fatal(err)
	}
	v = buildDocsGitSnapshot(context.Background(), root, root, 3)
	if v.state(filepath.Join(root, "sub"), true) != gitDeleted {
		t.Fatal("deletion missing from parent")
	}
	// External add/commit clears colors on the next snapshot.
	gitRun(t, root, "add", ".")
	gitRun(t, root, "commit", "-qm", "changes")
	v = buildDocsGitSnapshot(context.Background(), root, root, 3)
	if v.state(filepath.Join(root, "notes.md"), false) != gitClean {
		t.Fatal("commit left stale color")
	}
}

func TestDocsGitColorsAcrossViews(t *testing.T) {
	root := docsGitRepo(t)
	if err := os.WriteFile(filepath.Join(root, "notes.md"), []byte("changed"), 0644); err != nil {
		t.Fatal(err)
	}
	s, sh := newScanHome(t, root)
	s.gitDocs.snapshot = buildDocsGitSnapshot(context.Background(), root, root, 3)
	selectRow(t, s.docsPanel.List(), "notes.md")
	flat := s.docsPanel.List().SelectedItem().(core.ColorItem)
	want := gitStateColor(gitModified)
	if flat.TitleColor() != want {
		t.Fatal("flat missing git color")
	}
	s.Update(sh, keyMsg("alt+t"))
	selectRow(t, s.filePanel.List(), "notes.md")
	folder := s.filePanel.List().SelectedItem().(core.ColorItem)
	if folder.TitleColor() != want {
		t.Fatal("folder missing git color")
	}
	// Cached row objects read the replacement snapshot without rebuilding either list.
	filterList(t, s.filePanel.List(), "note")
	before := s.filePanel.List().Index()
	gitRun(t, root, "add", ".")
	fresh := buildDocsGitSnapshot(context.Background(), root, root, 3)
	s.Receive(sh, docsGitResult{s, s.gitDocs.epoch, s.gitDocs.generation, fresh})
	if flat.TitleColor() != gitStateColor(gitStaged) || folder.TitleColor() != flat.TitleColor() {
		t.Fatal("rows kept stale snapshot")
	}
	if s.filePanel.List().FilterValue() != "note" {
		t.Fatal("refresh reset filter")
	}
	if s.filePanel.List().Index() != before {
		t.Fatal("refresh reset selection")
	}
	s.Receive(sh, docsGitResult{s, s.gitDocs.epoch, s.gitDocs.generation, &docsGitSnapshot{}})
	if flat.TitleColor() != nil || folder.TitleColor() != nil {
		t.Fatal("failed/absent repo left stale colors")
	}
}

func TestDocsGitRefreshLifecycle(t *testing.T) {
	s, sh := newScanHome(t, scanTree(t))
	s.gitDocs.timer = func(delay time.Duration, f func(time.Time) tea.Msg) tea.Cmd {
		// Capture via the real broadcast route when needed; tests below drive its payload.
		return func() tea.Msg { return f(time.Time{}) }
	}
	s.requestDocsGit()
	old := s.gitDocs.generation
	s.requestDocsGit()
	if act, _ := s.receiveDocsGit(docsGitTick{s, s.gitDocs.epoch, old, false}); act.Cmd != nil {
		t.Fatal("stale debounce started read")
	}
	s.gitDocs.busy = true
	if act, _ := s.receiveDocsGit(docsGitTick{s, s.gitDocs.epoch, s.gitDocs.generation, false}); act.Cmd != nil || !s.gitDocs.pending {
		t.Fatal("overlapping read was not coalesced")
	}
	epoch := s.gitDocs.epoch
	snapshot := &docsGitSnapshot{}
	s.setSidebar(false)
	s.syncDocsGit()
	s.Receive(sh, docsGitResult{s, epoch, s.gitDocs.generation, snapshot})
	if s.gitDocs.snapshot != nil || s.gitDocs.visible {
		t.Fatal("hidden view accepted result")
	}
	s.setSidebar(true)
	if s.syncDocsGit() == nil {
		t.Fatal("showing sidebar did not schedule refresh")
	}
	oldEpoch := s.gitDocs.epoch
	s.resetDocsGit()
	s.syncDocsGit()
	s.Receive(sh, docsGitResult{s, oldEpoch, s.gitDocs.generation, snapshot})
	if s.gitDocs.snapshot != nil {
		t.Fatal("old session result accepted")
	}
	s.gitDocs.timer = noDocsGitTimer
	s.gitDocs.snapshot = snapshot
	s.Receive(sh, docsGitTick{s, s.gitDocs.epoch, 0, true})
	if s.gitDocs.polling {
		t.Fatal("poll did not consume timer")
	}
}

// Exercise the actual command/broadcast loop under an overlay, with a manual clock.
func TestDocsGitRefreshThroughRouter(t *testing.T) {
	root := docsGitRepo(t)
	path := filepath.Join(root, "notes.md")
	if err := os.WriteFile(path, []byte("changed\n"), 0644); err != nil {
		t.Fatal(err)
	}
	model, s, _ := newHomeRouter(t, Options{Mode: ModeScan, Dir: root, Depth: 3, DepthSet: true})
	type wake struct {
		delay time.Duration
		msg   tea.Msg
	}
	var wakes []wake
	s.gitDocs.timer = func(delay time.Duration, f func(time.Time) tea.Msg) tea.Cmd {
		wakes = append(wakes, wake{delay, f(time.Time{})})
		return nil
	}
	next := func(want time.Duration) {
		t.Helper()
		if len(wakes) == 0 || wakes[0].delay != want {
			t.Fatalf("missing %v wake: %+v", want, wakes)
		}
		msg := wakes[0].msg
		wakes = wakes[1:]
		var cmd tea.Cmd
		model, cmd = model.Update(msg)
		model = pumpModel(model, cmd)
	}
	s.requestDocsGit()
	model, _ = model.Update(keyMsg("?"))
	if model.(core.Router).Top() == s {
		t.Fatal("help did not open")
	}
	next(250 * time.Millisecond)
	if got := s.docTitleColor(path); got != gitStateColor(gitModified) {
		t.Fatalf("background result under help = %v", got)
	}
	gitRun(t, root, "add", "notes.md")
	next(2 * time.Second)
	next(250 * time.Millisecond)
	if got := s.docTitleColor(path); got != gitStateColor(gitStaged) {
		t.Fatal("poll missed external staging")
	}
	gitRun(t, root, "commit", "-qm", "save")
	next(2 * time.Second)
	next(250 * time.Millisecond)
	if s.docTitleColor(path) != nil {
		t.Fatal("poll missed external commit")
	}
	// The lifecycle has exactly one next poll, and a hidden sidebar consumes it silently.
	s.setSidebar(false)
	s.syncDocsGit()
	next(2 * time.Second)
	if len(wakes) != 0 {
		t.Fatal("hidden sidebar kept polling")
	}
	s.setSidebar(true)
	s.syncDocsGit()
	next(250 * time.Millisecond)
	if len(wakes) != 1 {
		t.Fatal("showing sidebar did not restart one polling loop")
	}
}

func TestDocsGitRenameColorsBothFolders(t *testing.T) {
	root := docsGitRepo(t)
	if err := os.Mkdir(filepath.Join(root, "destination"), 0755); err != nil {
		t.Fatal(err)
	}
	gitRun(t, root, "mv", "sub/deep.md", "destination/moved.md")
	v := buildDocsGitSnapshot(context.Background(), root, root, 3)
	for _, dir := range []string{"sub", "destination"} {
		if got := v.state(filepath.Join(root, dir), true); got != gitStaged {
			t.Fatalf("rename folder %s = %v", dir, got)
		}
	}
	if got := v.state(filepath.Join(root, "destination", "moved.md"), false); got != gitStaged {
		t.Fatal("rename destination missing")
	}
}
