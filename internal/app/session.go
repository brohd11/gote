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

// Session state is what gote writes for itself between runs: open files per root, which
// held the pane, and each caret and viewport. It lives in ~/.gote/state, not the
// user-edited config. Every failure is swallowed: a bad sessions file costs a restore,
// never a launch.
const (
	sessionsFile = "sessions.yml"
	// homeSessionKey names the ~/.gote/docs store; vaults are keyed by name, which survives a
	// changed path.
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

// SessionFile is one open document's position: Line and Col (runes) and Top, the line at
// the top of the viewport.
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

// LoadSessions reads the sessions file; missing is fine, malformed is an error.
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

// sessionKey identifies the session's root. "" means no session (ModeFile).
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

// sessionDir is the directory a key names; home and vault keys are symbolic.
func sessionDir(key string) (string, bool) {
	if key == homeSessionKey || strings.HasPrefix(key, vaultKeyPrefix) {
		return "", false
	}
	return key, true
}

// prune drops roots that are no longer directories, then the least recently used past
// sessionLimit (ties by key).
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

// loadSessionFor returns what was open under c's root, minus files that no longer exist.
// false means nothing to restore.
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

// saveSession stores the open set under c's root, after the TUI has exited (hence
// Ctx.activeID).
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
			// A restored buffer never switched to reports a caret at the origin; keep its original
			// record.
			session.Files = append(session.Files, rec)
			return
		}
		pos := entry.editor.CursorPosition()
		session.Files = append(session.Files, SessionFile{
			Path: entry.path, Line: pos.Line, Col: pos.Column, Top: entry.editor.TopLine(),
		})
	})
	// Only real paths become Active; an unsaved buffer's id is opaque.
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

// restoreSession reopens the last run's buffers and focuses the one that held the pane;
// false means install a scratch buffer. Carets are applied later (applyRestore), since
// buffers read their files lazily.
func (s *homeScreen) restoreSession(c *Ctx) bool {
	session, ok := loadSessionFor(c)
	if !ok {
		return false
	}
	c.restore = make(map[string]SessionFile, len(session.Files))
	for _, f := range session.Files {
		c.OpenDoc(f.Path, s.editorOpts(c))
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

// applyRestore restores the caret and viewport of the buffer in the pane, retrying from
// finishHomeUpdate until its async read lands, and giving up if the line never appears.
// Entries for buffers never shown stay, so saveSession writes them back.
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
