package app

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/brohd11/bubblestack/components/editor"
	"github.com/brohd11/goutil/configdir"
)

// Session state is what gote writes for ITSELF between runs: which files were open under
// a given root, which one held the pane, and where the caret and viewport sat in each.
// It lives in ~/.gote/state rather than in config.yml because the two have opposite
// owners — config.yml is the user's to edit and plausibly to commit to a dotfiles repo,
// while this file changes on every quit and is nobody's to read.
//
// Nothing here is load-bearing. Every failure is swallowed by the callers: a sessions
// file that cannot be read costs a restore, never a launch, the same bargain the shared
// theme makes in bubblestack/config.
const (
	sessionsFile = "sessions.yml"
	// homeSessionKey names the ~/.gote/docs store, which has no launch directory to be
	// keyed by. The prefix does the same for vaults, whose configured path can be
	// repointed while the name stays the thing the user opened.
	homeSessionKey = "home"
	vaultKeyPrefix = "vault:"
	// sessionLimit caps how many roots the file remembers. Every directory gote is ever
	// launched in would otherwise accumulate a row forever.
	sessionLimit = 50
)

// Sessions is the whole file: roots to what was open under them.
type Sessions struct {
	Sessions map[string]Session `yaml:"sessions"`
}

// Session is one root's last state. Active is a path rather than a buffer id, so an
// unsaved buffer — which has an id but no path — simply never becomes one.
type Session struct {
	Active  string        `yaml:"active,omitempty"`
	Updated time.Time     `yaml:"updated,omitempty"`
	Files   []SessionFile `yaml:"files"`
}

// SessionFile is one open document and where the user was in it. Line and Col are the
// editor's own rune-based position; Top is the buffer line that was at the top of the
// viewport, which is what makes the restored view the one that was left rather than one
// merely containing the caret.
type SessionFile struct {
	Path string `yaml:"path"`
	Line int    `yaml:"line,omitempty"`
	Col  int    `yaml:"col,omitempty"`
	Top  int    `yaml:"top,omitempty"`
}

// SessionsPath is ~/.gote/state/sessions.yml.
func SessionsPath() (string, error) {
	dir, err := StateDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, sessionsFile), nil
}

// LoadSessions reads the sessions file. A missing one is not an error — configdir.Load
// already treats first run as an empty document — while malformed YAML is reported, so
// callers can decide for themselves whether to care.
func LoadSessions() (Sessions, error) {
	var s Sessions
	path, err := SessionsPath()
	if err != nil {
		return Sessions{}, err
	}
	if err := configdir.Load(path, &s); err != nil {
		return Sessions{}, err
	}
	return s, nil
}

// SaveSessions writes the sessions file atomically, creating ~/.gote/state on the way.
func SaveSessions(s Sessions) error {
	dir, err := StateDir()
	if err != nil {
		return err
	}
	return configdir.SaveAtomic(dir, sessionsFile, s)
}

// sessionKey identifies the root a session belongs to. An empty key means this launch
// does not participate: ModeFile is a single named file in chrome-less minimal mode, and
// reopening a pile of unrelated buffers into it would be nothing the user asked for.
func sessionKey(c *Ctx) string {
	switch c.Mode {
	case ModeScan:
		if c.ScanDir == "" {
			return ""
		}
		return filepath.Clean(c.ScanDir)
	case ModeVault:
		if c.VaultName == "" {
			return ""
		}
		return vaultKeyPrefix + c.VaultName
	case ModeHome:
		return homeSessionKey
	}
	return ""
}

// sessionDir is the directory a key names, when it names one. The home and vault keys
// are symbolic — a vault's directory is reachable only through the config that defines
// it — so neither is something the filesystem can be asked about.
func sessionDir(key string) (string, bool) {
	if key == homeSessionKey || strings.HasPrefix(key, vaultKeyPrefix) {
		return "", false
	}
	return key, true
}

// prune keeps the file from growing without bound: a root that is no longer a directory
// is gone for good, and past sessionLimit the least recently used of what is left goes
// too. Ties break on the key so the result does not depend on map order.
func (s *Sessions) prune() {
	for key := range s.Sessions {
		dir, ok := sessionDir(key)
		if !ok {
			continue
		}
		if info, err := os.Stat(dir); err != nil || !info.IsDir() {
			delete(s.Sessions, key)
		}
	}
	if len(s.Sessions) <= sessionLimit {
		return
	}
	keys := make([]string, 0, len(s.Sessions))
	for key := range s.Sessions {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		a, b := s.Sessions[keys[i]].Updated, s.Sessions[keys[j]].Updated
		if a.Equal(b) {
			return keys[i] < keys[j]
		}
		return a.Before(b)
	})
	for _, key := range keys[:len(keys)-sessionLimit] {
		delete(s.Sessions, key)
	}
}

// loadSessionFor returns what was open under c's root, minus any file that has since
// been deleted, moved, or replaced by a directory. Reporting false means there is
// nothing worth restoring and the caller should start the way it always has.
func loadSessionFor(c *Ctx) (Session, bool) {
	key := sessionKey(c)
	if key == "" {
		return Session{}, false
	}
	sessions, err := LoadSessions()
	if err != nil {
		return Session{}, false
	}
	session, ok := sessions.Sessions[key]
	if !ok {
		return Session{}, false
	}
	kept := make([]SessionFile, 0, len(session.Files))
	for _, f := range session.Files {
		if f.Path == "" {
			continue
		}
		if info, err := os.Stat(f.Path); err != nil || info.IsDir() {
			continue
		}
		kept = append(kept, f)
	}
	session.Files = kept
	return session, len(kept) > 0
}

// saveSession banks the open set under c's root. It runs after the TUI is gone, off the
// context, which is why Ctx has to carry activeID at all: by then no screen is left to
// ask which buffer held the pane.
func (c *Ctx) saveSession() error {
	key := sessionKey(c)
	if key == "" {
		return nil
	}
	sessions, err := LoadSessions()
	if err != nil {
		// A file that cannot be parsed is one to overwrite, not one to give up on:
		// refusing to save would let a single bad byte cost every run from here on.
		sessions = Sessions{}
	}
	if sessions.Sessions == nil {
		sessions.Sessions = map[string]Session{}
	}

	session := Session{Updated: time.Now()}
	c.open.each(func(entry *openEntry) {
		if entry.path == "" {
			return // an unsaved buffer has no path to be reopened by
		}
		if rec, ok := c.restore[entry.path]; ok {
			// Restored last launch but never switched to, so its editor never read the
			// file and reports a caret at the origin. The record that outlived the run
			// is the true position; reading the editor here would quietly flatten it.
			session.Files = append(session.Files, rec)
			return
		}
		pos := entry.editor.CursorPosition()
		session.Files = append(session.Files, SessionFile{
			Path: entry.path, Line: pos.Line, Col: pos.Column, Top: entry.editor.TopLine(),
		})
	})
	// Only a real path becomes Active: an unsaved buffer's id is opaque (and contains a
	// NUL), which is not something to write into a YAML file that means nothing on the
	// way back in.
	for _, f := range session.Files {
		if f.Path == c.activeID {
			session.Active = f.Path
			break
		}
	}

	if len(session.Files) == 0 {
		// Closing every buffer is a decision too. Leaving the old row would reopen files
		// the user has just finished putting away.
		delete(sessions.Sessions, key)
	} else {
		sessions.Sessions[key] = session
	}
	sessions.prune()
	return SaveSessions(sessions)
}

// restoreSession reopens what the last run left open and puts the pane on whichever
// buffer was focused. False means there was nothing to restore, and the caller installs
// the usual scratch buffer instead.
//
// Only registration happens here. Carets are deferred to applyRestore because gote reads
// a document lazily — Init reaches only the editor currently holding the pane — so every
// buffer but one has no text to put a caret into yet.
func (s *homeScreen) restoreSession(c *Ctx) bool {
	session, ok := loadSessionFor(c)
	if !ok {
		return false
	}
	c.restore = make(map[string]SessionFile, len(session.Files))
	for _, f := range session.Files {
		c.OpenDoc(f.Path, s.editorOpts())
		c.restore[f.Path] = f
	}
	active := session.Active
	ed, ok := c.Doc(active)
	if !ok {
		// The focused file is the one most likely to have been deleted since. Falling
		// back to the last opened keeps the rest of the restore rather than dropping it.
		active = session.Files[len(session.Files)-1].Path
		ed, ok = c.Doc(active)
	}
	if !ok {
		c.restore = nil
		return false
	}
	s.currentID, s.currentPath, s.currentName = active, active, docName(active)
	s.editor = ed
	// Seeded here because nothing else does it before the first pick, and a restored
	// session that showed an empty Open list would look like it had failed.
	s.openPanel.SetItems(openDocItems(c, s.currentID))
	return true
}

// applyRestore puts the caret and viewport back for the buffer now holding the pane. It
// runs from finishHomeUpdate — the seam applyPendingJump already uses — because the file
// read is asynchronous: Reveal rejects a line the buffer does not have yet, so this just
// tries again on the next message until the text lands.
//
// It gives up once the buffer has content but not the line asked for, the same rule
// applyPendingJump follows: a file that changed on disk should cost one wrong caret, not
// a retry that never ends. An entry for a buffer never switched to is never drained at
// all, which is exactly what lets saveSession write the original position back.
func (s *homeScreen) applyRestore(c *Ctx) {
	if len(c.restore) == 0 || s.editor == nil || s.currentPath == "" {
		return
	}
	rec, ok := c.restore[s.currentPath]
	if !ok {
		return
	}
	if s.editor.Reveal(editor.Position{Line: rec.Line, Column: rec.Col}) {
		// After Reveal, which centers a caret it had to scroll to reach: the recorded
		// offset is the view the user actually left, so it gets the last word.
		s.editor.SetTopLine(rec.Top)
		delete(c.restore, s.currentPath)
		return
	}
	if s.editor.Text() != "" {
		delete(c.restore, s.currentPath)
	}
}
