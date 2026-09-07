package app

import (
	"context"
	"image/color"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/brohd11/bubblestack/components"
	"github.com/brohd11/bubblestack/core"
	"github.com/brohd11/gitstack/repo"
	"github.com/brohd11/goutil/strutil"
)

type gitFileState int

const (
	gitClean gitFileState = iota
	gitIgnored
	gitStaged
	gitUntracked
	gitModified
	gitDeleted
	gitConflict
)

func fileGitState(f repo.FileStatus) gitFileState {
	switch {
	case f.Conflict:
		return gitConflict
	case f.Untracked:
		return gitUntracked
	case f.Worktree == 'D':
		return gitDeleted
	case f.Worktree != 0 && f.Worktree != '.':
		return gitModified
	case f.Index != 0 && f.Index != '.':
		return gitStaged
	default:
		return gitClean
	}
}
func gitStateColor(state gitFileState) color.Color {
	switch state {
	case gitStaged:
		return lipgloss.Color("2")
	case gitUntracked:
		return lipgloss.Color("10")
	case gitModified:
		return lipgloss.Color("11")
	case gitDeleted, gitConflict:
		return lipgloss.Color("1")
	case gitIgnored:
		return lipgloss.Color("8")
	default:
		return nil
	}
}

type gitPathAlias struct{ path, real string }
type docsGitSnapshot struct {
	// Longest paths first: a nested checkout owns even its clean or unreadable files.
	roots   []string
	aliases []gitPathAlias
	repos   map[string]repo.WorktreeStatus
	states  map[string]gitFileState
	dirs    map[string]gitFileState
}

func underPath(path, root string) bool {
	return path == root || strings.HasPrefix(path, root+string(filepath.Separator)) || root == string(filepath.Separator)
}
func (v *docsGitSnapshot) canonical(path string) string {
	path = filepath.Clean(path)
	for _, a := range v.aliases {
		if underPath(path, a.path) {
			rel, _ := filepath.Rel(a.path, path)
			return filepath.Join(a.real, rel)
		}
	}
	return path
}
func (v *docsGitSnapshot) owner(path string) string {
	for _, root := range v.roots {
		if underPath(path, root) {
			return root
		}
	}
	return ""
}
func (v *docsGitSnapshot) state(path string, isDir bool) gitFileState {
	if v == nil {
		return gitClean
	}
	path = v.canonical(path)
	if isDir && v.dirs[path] > gitIgnored {
		return max(v.dirs[path], v.states[path])
	}
	if state := v.states[path]; state != gitClean {
		return state
	}
	root := v.owner(path)
	if root == "" {
		return gitClean
	}
	status := v.repos[root]
	for _, ignored := range status.Ignored {
		p := filepath.Join(root, filepath.FromSlash(strings.TrimSuffix(ignored, "/")))
		if path == p || (strings.HasSuffix(ignored, "/") && underPath(path, p)) {
			return gitIgnored
		}
	}
	return gitClean
}
func (s *homeScreen) docTitleColor(path string) color.Color {
	return gitStateColor(s.gitDocs.snapshot.state(path, false))
}
func (s *homeScreen) fileTitleColor(e components.FileEntry) color.Color {
	if e.Up {
		return nil
	}
	return gitStateColor(s.gitDocs.snapshot.state(e.Path, e.IsDir))
}

// buildDocsGitSnapshot runs entirely in the command lane. Discovery follows the
// document scan's pruning/depth, with the current folder as an additional starting
// point so walking deeper (or through a symlink) discovers its nearest checkout.
func buildDocsGitSnapshot(ctx context.Context, base, current string, depth int) *docsGitSnapshot {
	v := &docsGitSnapshot{repos: map[string]repo.WorktreeStatus{}, states: map[string]gitFileState{}, dirs: map[string]gitFileState{}}
	candidates := map[string]bool{}
	starts := []string{base}
	if current != base {
		starts = append(starts, current)
	}
	for _, start := range starts {
		if start == "" {
			continue
		}
		abs, err := filepath.Abs(start)
		if err != nil {
			continue
		}
		real, err := filepath.EvalSymlinks(abs)
		if err != nil {
			real = abs
		}
		v.aliases = append(v.aliases, gitPathAlias{abs, real})
		probeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		root, _ := repo.RepoRootContext(probeCtx, real)
		cancel()
		if root != "" {
			candidates[root] = true
		}
		_ = filepath.WalkDir(real, func(path string, d fs.DirEntry, err error) error {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if err != nil || !d.IsDir() {
				return nil
			}
			if path != real && (strings.HasPrefix(d.Name(), ".") || skipDirs[d.Name()] || strutil.Depth(real, path) > depth) {
				return fs.SkipDir
			}
			if _, err := os.Stat(filepath.Join(path, ".git")); err == nil {
				candidates[path] = true
			}
			return nil
		})
	}
	sort.Slice(v.aliases, func(i, j int) bool { return len(v.aliases[i].path) > len(v.aliases[j].path) })
	for path := range candidates {
		v.roots = append(v.roots, path)
	}
	sort.Slice(v.roots, func(i, j int) bool { return len(v.roots[i]) > len(v.roots[j]) })
	for _, root := range v.roots {
		if ctx.Err() != nil {
			break
		}
		readCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		status, err := repo.ReadWorktreeStatus(readCtx, root)
		cancel()
		if err == nil {
			v.repos[root] = status
		}
	}
	// Compute ownership after discovery, so outer ignored/untracked entries never
	// override an inner repository. Directory aggregates can cross repo boundaries.
	for root, status := range v.repos {
		for _, f := range status.Files {
			path := filepath.Join(root, filepath.FromSlash(f.Path))
			if owner := v.owner(path); owner != root && (path != owner || f.Untracked) {
				continue
			}
			state := fileGitState(f)
			v.states[path] = max(v.states[path], state)
			v.markParents(path, state)
			// A rename changes the directory it left as well as the one it entered.
			// Copies leave their source untouched; neither adds a synthetic file row.
			if f.OriginalPath != "" && (f.Index == 'R' || f.Worktree == 'R') {
				v.markParents(filepath.Join(root, filepath.FromSlash(f.OriginalPath)), state)
			}
		}
	}
	return v
}

func (v *docsGitSnapshot) markParents(path string, state gitFileState) {
	for dir := filepath.Dir(path); ; dir = filepath.Dir(dir) {
		v.dirs[dir] = max(v.dirs[dir], state)
		if dir == filepath.Dir(dir) {
			break
		}
	}
}

type docsGit struct {
	snapshot                        *docsGitSnapshot
	epoch, generation               uint64
	visible, busy, pending, polling bool
	cancel                          context.CancelFunc
	// Tests drive refresh messages explicitly rather than waiting for wall time.
	timer func(time.Duration, func(time.Time) tea.Msg) tea.Cmd
}
type docsGitTick struct {
	target            *homeScreen
	epoch, generation uint64
	poll              bool
}
type docsGitResult struct {
	target            *homeScreen
	epoch, generation uint64
	snapshot          *docsGitSnapshot
}

func (s *homeScreen) gitDocsTimer(delay time.Duration, msg docsGitTick) tea.Cmd {
	tick := s.gitDocs.timer
	if tick == nil {
		tick = tea.Tick
	}
	return tick(delay, func(time.Time) tea.Msg { return core.PropagateAll(msg) })
}
func (s *homeScreen) resetDocsGit() {
	if s.gitDocs.cancel != nil {
		s.gitDocs.cancel()
	}
	s.gitDocs = docsGit{epoch: s.gitDocs.epoch + 1, timer: s.gitDocs.timer}
}
func (s *homeScreen) syncDocsGit() tea.Cmd {
	visible := s.sidebar && !s.minimal
	if visible == s.gitDocs.visible {
		return nil
	}
	if !visible {
		if s.gitDocs.cancel != nil {
			s.gitDocs.cancel()
		}
		s.gitDocs.epoch++
		s.gitDocs.visible, s.gitDocs.busy, s.gitDocs.pending, s.gitDocs.polling = false, false, false, false
		return nil
	}
	s.gitDocs.visible = true
	return s.requestDocsGit()
}
func (s *homeScreen) requestDocsGit() tea.Cmd {
	if !s.sidebar || s.minimal {
		return nil
	}
	s.gitDocs.generation++
	return s.gitDocsTimer(250*time.Millisecond, docsGitTick{target: s, epoch: s.gitDocs.epoch, generation: s.gitDocs.generation})
}
func (s *homeScreen) startDocsGit() tea.Cmd {
	if s.gitDocs.busy {
		s.gitDocs.pending = true
		return nil
	}
	c := Of(s.sh)
	base, current, depth := docsRoot(c), s.filePanel.Dir(), c.Depth
	epoch, generation := s.gitDocs.epoch, s.gitDocs.generation
	ctx, cancel := context.WithCancel(context.Background())
	s.gitDocs.cancel, s.gitDocs.busy = cancel, true
	return func() tea.Msg {
		defer cancel()
		snapshot := buildDocsGitSnapshot(ctx, base, current, depth)
		return core.PropagateAll(docsGitResult{s, epoch, generation, snapshot})
	}
}
func (s *homeScreen) receiveDocsGit(payload any) (core.Action, bool) {
	switch m := payload.(type) {
	case docsGitTick:
		if m.target != s || m.epoch != s.gitDocs.epoch || !s.gitDocs.visible {
			return core.Action{}, true
		}
		if m.poll {
			s.gitDocs.polling = false
			return core.Async(s.requestDocsGit()), true
		}
		if m.generation != s.gitDocs.generation {
			return core.Action{}, true
		}
		return core.Async(s.startDocsGit()), true
	case docsGitResult:
		if m.target != s || m.epoch != s.gitDocs.epoch || !s.gitDocs.visible {
			return core.Action{}, true
		}
		s.gitDocs.busy, s.gitDocs.cancel = false, nil
		if m.generation == s.gitDocs.generation {
			s.gitDocs.snapshot = m.snapshot
		}
		if s.gitDocs.pending || m.generation != s.gitDocs.generation {
			s.gitDocs.pending = false
			return core.Async(s.requestDocsGit()), true
		}
		if !s.gitDocs.polling {
			s.gitDocs.polling = true
			return core.Async(s.gitDocsTimer(2*time.Second, docsGitTick{target: s, epoch: s.gitDocs.epoch, poll: true})), true
		}
		return core.Action{}, true
	}
	return core.Action{}, false
}
