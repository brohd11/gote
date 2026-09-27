package app

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/brohd11/bubblestack/components/editor"
	"github.com/brohd11/bubblestack/core"
	"github.com/charmbracelet/colorprofile"
)

// Mode is the active document source: the flat ~/.gote/docs store, an ad-hoc recursive
// scan, a single file opened alone, or a configured named vault.
type Mode int

const (
	ModeHome  Mode = iota // the flat ~/.gote/docs store — the default
	ModeScan              // recursive scan of ScanDir to Depth
	ModeFile              // FilePath alone, in the chrome-less minimal editor
	ModeVault             // a named, configured recursive document root
)

// Ctx is gote's app context (core.Shared.App, read with Of): the seed state and every
// open buffer. Open maps a path to its editor, which owns the buffer, so unsaved edits
// survive switching files.
type Ctx struct {
	Version   string
	Mode      Mode
	ScanDir   string
	FilePath  string // ModeFile's file; the doc the minimal editor boots on
	VaultName string // ModeVault's configured display/lookup name
	Depth     int
	Filter    DocFilter // which files the lists seed from
	NewExt    string    // extension rename appends to an extensionless name
	Preview   bool      // --preview: boot straight into the full-screen reader
	Files     []DocFile
	Config    Config

	// Groups reference retained buffers and survive the screen for session saving.
	groups      []*editorGroup
	activeGroup *editorGroup
	// open owns buffer identities, paths, editors and global opening order.
	open openSet
	lsp  *lspManager

	// activeID is the buffer in the editor pane, kept here because the session save runs
	// after the screen is gone (see session.go).
	activeID string
	// restore holds session positions not yet applied. A buffer never switched to reports a
	// caret at the origin, so saveSession writes its pending entry back instead.
	restore map[string]SessionFile
}

// SetActive records which buffer holds the editor pane, for the session save.
func (c *Ctx) SetActive(id string) { c.activeID = id }

var _ core.Receiver = (*Ctx)(nil)

// Receive forwards broadcasts to every retained editor, including those switched out of
// the pane, so async highlight results always have a live target.
func (c *Ctx) Receive(sh *core.Shared, payload any) core.Action {
	var acts []core.Action
	_, focus := payload.(tea.FocusMsg)
	c.open.each(func(entry *openEntry) {
		ed := entry.editor
		if focus {
			acts = append(acts, core.Async(ed.CheckDiskChanges()))
		}
		act := ed.Receive(sh, payload)
		if act.Msg != nil || act.Cmd != nil {
			acts = append(acts, act)
		}
	})
	switch len(acts) {
	case 0:
		return core.Action{}
	case 1:
		return acts[0]
	default:
		return core.Seq(acts...)
	}
}

// Options is the launch the CLI resolves (cmd.resolveOptions); only the chosen mode's
// fields are read. The zero value is the default launch (Config.Default, else
// ~/.gote/docs). DepthSet and ExtsSet exist because 0 and an empty --ext= are
// meaningful values.
type Options struct {
	Mode     Mode
	Dir      string // ModeScan root, or ModeVault's already-resolved root, absolute
	File     string // ModeFile path, absolute
	Vault    string // ModeVault's configured name; Dir carries its resolved path
	Depth    int    // scan depth, honored only when DepthSet
	DepthSet bool
	Exts     []string // --ext; replaces Config.Extensions, honored only when ExtsSet
	ExtsSet  bool
	Preview  bool // --preview; a request the launch honors only when it opens a document
}

// New builds the context from the loaded config and the CLI's launch options, and
// performs the initial seed so the first screen has rows to show.
func New(version string, cfg Config, opts Options) *Ctx {
	return newWithColorProfile(version, cfg, opts, colorprofile.Unknown)
}

// Runtime capability affects styles, never c.Config or a later config save.
func newWithColorProfile(version string, cfg Config, opts Options, profile colorprofile.Profile) *Ctx {
	// Before anything can parse a document: a Highlighter bakes these styles into its
	// spans at Parse time and nothing re-parses to pick up a later palette.
	applySyntaxPalette(syntaxColorsForProfile(cfg.SyntaxColors, profile))
	c := &Ctx{
		Version: version,
		Mode:    opts.Mode,
		Filter:  cfg.Filter(),
		Depth:   cfg.ScanDepth,
		Preview: opts.Preview,
		Config:  cfg,
		open:    newOpenSet(),
	}
	// auto-lsp and the mode's default_allow_lsp are ANDed; a nil manager is handled
	// everywhere.
	if cfg.AutoLSP && cfg.modeDefaults(opts.Mode).AllowLSP {
		c.lsp = newLSPManager(cfg, version)
	}
	if opts.DepthSet {
		c.Depth = opts.Depth
	}
	if opts.ExtsSet {
		c.Filter = NewDocFilter(opts.Exts)
	}
	// Derived after the override, so --ext=txt also decides what a bare "notes" becomes.
	c.NewExt = defaultExt(c.Filter.Exts)
	switch opts.Mode {
	case ModeScan:
		c.ScanDir = opts.Dir
	case ModeFile:
		c.FilePath = opts.File
	case ModeVault:
		// The CLI already resolved and validated this vault.
		c.VaultName, c.ScanDir = opts.Vault, opts.Dir
	case ModeHome:
		c.Mode, c.VaultName, c.ScanDir = resolveDefault(cfg)
	}
	c.Seed()
	return c
}

func (c *Ctx) close() {
	if c != nil && c.lsp != nil {
		c.lsp.Close()
	}
}

// Of recovers the gote context from a Shared. Screens call c := app.Of(sh).
func Of(sh *core.Shared) *Ctx { return core.App[Ctx](sh) }

// Seed re-reads the doc list for the current mode (home store, scan, or nothing for a
// single file). Open buffers are never touched.
func (c *Ctx) Seed() {
	switch c.Mode {
	case ModeScan, ModeVault:
		c.Files = ScanDocs(c.ScanDir, c.Depth, c.Filter)
	case ModeFile:
		c.Files = nil
	default:
		dir, err := DocsDir()
		if err != nil {
			c.Files = nil
			return
		}
		c.Files = HomeDocs(dir, c.Filter)
	}
}

// resolveDefault turns Config.Default into a bare `gote` launch: a path when written as
// one (~, ./, or a separator), otherwise a vault name, as the CLI reads bare arguments.
// ~/.gote/docs resolves to home mode rather than a scan of it. Anything that does not
// resolve falls back to the home store rather than refusing to start.
func resolveDefault(cfg Config) (mode Mode, name, dir string) {
	if cfg.Default == "" {
		return ModeHome, "", ""
	}
	if isPathRef(cfg.Default) {
		path, err := normalizeDirPath(cfg.Default)
		if err != nil {
			return ModeHome, "", ""
		}
		if docs, err := DocsDir(); err == nil && path == docs {
			return ModeHome, "", ""
		}
		return ModeScan, "", path
	}
	path, err := vaultPath(cfg, cfg.Default)
	if err != nil {
		return ModeHome, "", ""
	}
	return ModeVault, cfg.Default, path
}

// isPathRef reports whether a config value is a path rather than a vault name; "./notes"
// reaches a directory whose name a vault has claimed.
func isPathRef(s string) bool {
	return strings.HasPrefix(s, "~") || strings.HasPrefix(s, ".") ||
		strings.ContainsRune(s, '/') || strings.ContainsRune(s, filepath.Separator)
}

// vaultPath resolves and validates one configured vault without changing live state.
func vaultPath(cfg Config, name string) (string, error) {
	v, ok := cfg.Vaults[name]
	if !ok {
		return "", fmt.Errorf("vault %q is not configured", name)
	}
	path, err := normalizeDirPath(v.Path)
	if err != nil {
		return "", fmt.Errorf("vault %q: %w", name, err)
	}
	return path, nil
}

// LookupVault is vaultPath for the CLI, where ok separates "no such vault" (try the next
// reading) from "broken vault" (err).
func LookupVault(cfg Config, name string) (path string, ok bool, err error) {
	if _, ok := cfg.Vaults[name]; !ok {
		return "", false, nil
	}
	path, err = vaultPath(cfg, name)
	return path, true, err
}

// VaultEntry is one configured vault as listed outside the TUI, with its path exactly as
// configured, so a vault whose directory is gone still lists.
type VaultEntry struct {
	Name    string
	Path    string
	Default bool
}

// VaultList returns every configured vault sorted by name. Sorted here rather than at
// each call site so the CLI listing and the TUI vault menu cannot drift apart.
func VaultList(cfg Config) []VaultEntry {
	entries := make([]VaultEntry, 0, len(cfg.Vaults))
	for name, v := range cfg.Vaults {
		entries = append(entries, VaultEntry{Name: name, Path: v.Path, Default: name == cfg.Default})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })
	return entries
}

// AddVault validates and saves a new vault, creating or adopting its directory. Config
// changes in memory only after the atomic write; the directory is created after the other
// checks, so a rejected add leaves nothing behind.
func (c *Ctx) AddVault(name, rawPath string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("name is required")
	}
	if _, exists := c.Config.Vaults[name]; exists {
		return fmt.Errorf("vault %q already exists", name)
	}
	path, err := resolveVaultPath(rawPath)
	if err != nil {
		return err
	}
	// Compared by resolution alone: a saved vault whose directory has vanished still
	// owns its path, and must still block a second name claiming it.
	for other, v := range c.Config.Vaults {
		otherPath, err := resolveVaultPath(v.Path)
		if err == nil && otherPath == path {
			return fmt.Errorf("%q is already saved as vault %q", path, other)
		}
	}
	if err := ensureVaultDir(path); err != nil {
		return err
	}
	next := c.Config
	next.Vaults = make(map[string]VaultConfig, len(c.Config.Vaults)+1)
	for k, v := range c.Config.Vaults {
		next.Vaults[k] = v
	}
	next.Vaults[name] = VaultConfig{Path: path}
	if err := SaveConfig(next); err != nil {
		return err
	}
	c.Config = next
	return nil
}

// SwitchVault closes the current session and activates name. Validation happens
// first, so a vanished or malformed vault never destroys open buffers.
func (c *Ctx) SwitchVault(name string) error {
	path, err := vaultPath(c.Config, name)
	if err != nil {
		return err
	}
	// Banked before the buffers go: the outgoing vault's session is only knowable while
	// its open set is still standing. Best-effort, like every other session write.
	_ = c.saveSession()
	c.open.reset()
	// The incoming vault gets a clean slate rather than the old root's leftovers, which
	// would otherwise be written back under ITS key on quit.
	c.activeID, c.restore = "", nil
	c.Mode, c.VaultName, c.ScanDir = ModeVault, name, path
	c.FilePath = ""
	// A single-file launch whose settings denied a manager may be allowed one as a vault;
	// create it now, since nothing else reconsiders.
	if c.lsp == nil && c.Config.AutoLSP && c.Config.Project.AllowLSP {
		c.lsp = newLSPManager(c.Config, c.Version)
	}
	c.Seed()
	return nil
}

// OpenDoc returns path's editor, creating and registering it on first open with opts
// (see homeScreen.editorOpts). An already-open doc keeps its editor and buffer.
func (c *Ctx) OpenDoc(path string, opts editor.Opts) *editor.Screen {
	if entry, ok := c.open.getPath(path); ok {
		return entry.editor
	}
	opts.Path = path
	ed := c.newEditor(opts)
	c.open.addFile(path, c.rootForPath(path), ed)
	c.assignGroup(path)
	return ed
}

// newEditor binds save/exit hooks to their owner: an asynchronous write may finish
// after another document has taken the pane.
func (c *Ctx) newEditor(opts editor.Opts) *editor.Screen {
	var ed *editor.Screen
	onSaved, onExit := opts.OnSaved, opts.OnExit
	owner := func() *openEntry {
		var found *openEntry
		c.open.each(func(entry *openEntry) {
			if entry.editor == ed {
				found = entry
			}
		})
		return found
	}
	opts.OnSaved = func(sh *core.Shared, path string) core.Action {
		entry := owner()
		if entry == nil || entry.id == c.activeID {
			if onSaved != nil {
				return onSaved(sh, path)
			}
			return core.Action{}
		}
		forgetSniffedLanguage(entry.path)
		forgetSniffedLanguage(path)
		c.RekeyDoc(entry.id, path, ed)
		if c.lsp != nil {
			c.lsp.DidSave(path)
		}
		return core.PropagateAll(ReseedMsg{})
	}
	opts.OnExit = func(sh *core.Shared) core.Action {
		if entry := owner(); entry != nil && entry.id != c.activeID {
			c.CloseDoc(entry.id)
			return core.PropagateAll(ReseedMsg{})
		}
		if onExit != nil {
			return onExit(sh)
		}
		return core.Pop()
	}
	ed = editor.New(opts)
	return ed
}

// unread reports whether path is open but has never read its file: a restored buffer not
// yet switched to.
func (c *Ctx) unread(path string) bool {
	_, pending := c.restore[path]
	return pending
}

// Doc returns the editor open for path, if there is one.
func (c *Ctx) Doc(path string) (*editor.Screen, bool) {
	entry, ok := c.open.getPath(path)
	if !ok {
		return nil, false
	}
	return entry.editor, true
}

// buffer returns the retained editor identified by id, whether saved or unsaved.
func (c *Ctx) buffer(id string) (*editor.Screen, bool) {
	entry, ok := c.open.get(id)
	if !ok {
		return nil, false
	}
	return entry.editor, true
}

// bufferInfo returns the open-buffer metadata for id.
func (c *Ctx) bufferInfo(id string) (DocFile, bool) {
	entry, ok := c.open.get(id)
	if !ok {
		return DocFile{}, false
	}
	return DocFile{ID: entry.id, Name: entry.name, Path: entry.path, Root: entry.root}, true
}

// newUnsavedIdentity finds the lowest free unsaved_N; the NUL in the id cannot clash with
// a real path.
func (c *Ctx) newUnsavedIdentity() (id, name string) {
	for n := 1; ; n++ {
		id = fmt.Sprintf("\x00gote:unsaved:%d", n)
		if _, used := c.open.get(id); used {
			continue
		}
		return id, fmt.Sprintf("unsaved_%d", n)
	}
}

// trackUnsaved promotes a pathless editor into the retained Open set.
func (c *Ctx) trackUnsaved(id, name string, ed *editor.Screen) {
	if _, exists := c.open.get(id); exists {
		return
	}
	c.open.addUnsaved(id, name, ed)
	c.assignGroup(id)
}

// EachDoc visits every open buffer that speaks for a file, in opening order, skipping
// pathless buffers and restored buffers not yet read, whose empty text would mislead
// consumers (it made gopls report a whole package undefined). open.each and OpenDocs
// visit everything.
func (c *Ctx) EachDoc(fn func(path string, ed *editor.Screen)) {
	c.open.each(func(entry *openEntry) {
		if entry.path != "" && !c.unread(entry.path) {
			fn(entry.path, entry.editor)
		}
	})
}

// RekeyDoc re-files ed under newPath after a save-as, keeping its Open slot (or
// registering an untracked startup buffer). Another buffer already holding newPath is
// dropped, since the list is keyed by path.
func (c *Ctx) RekeyDoc(oldID, newPath string, ed *editor.Screen) {
	if newPath == "" || ed == nil {
		return
	}
	if cur, ok := c.open.getPath(newPath); oldID == newPath && ok && cur.editor == ed {
		return
	}
	// A restored buffer renamed before being opened carries its pending position with it.
	if rec, ok := c.restore[oldID]; ok {
		delete(c.restore, oldID)
		rec.Path = newPath
		c.restore[newPath] = rec
	}
	c.rekeyGroups(oldID, newPath, ed)
	c.open.rekey(oldID, newPath, ed)
	c.assignGroup(newPath)
}

// OpenDocs lists the open buffers in opening order.
func (c *Ctx) OpenDocs() []DocFile { return c.open.docs() }

// CloseDoc removes id from the open set (unknown ids are ignored) and returns the buffer
// to show next: the one after it, else the last, else "".
func (c *Ctx) CloseDoc(id string) (next string) {
	next = c.open.remove(id)
	if g := c.groupFor(id); g != nil {
		next = g.remove(id)
	}
	return next
}

// rootForPath records a document's origin root on first open: exact for seeded docs, the
// mode's root otherwise, or its own directory for a standalone file.
func (c *Ctx) rootForPath(path string) string {
	for _, doc := range c.Files {
		if doc.Path == path && doc.Root != "" {
			return doc.Root
		}
	}
	switch c.Mode {
	case ModeScan, ModeVault:
		if c.ScanDir != "" {
			return filepath.Clean(c.ScanDir)
		}
	case ModeHome:
		if dir, err := DocsDir(); err == nil {
			return filepath.Clean(dir)
		}
	}
	return filepath.Dir(path)
}

func docName(path string) string {
	return filepath.Base(path)
}
