package app

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/brohd11/bubblestack/components"
	"github.com/brohd11/bubblestack/core"
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

// Ctx is gote's app context, stored on core.Shared.App and recovered with Of. It
// carries the seed state (mode/dir/depth/filter and the last-seeded file list)
// and every open buffer: Open maps a doc's path to its EditorScreen, which owns the
// buffer — so keeping the instance is what preserves unsaved edits across file
// switches. ctrl+x in the editor closes the buffer (CloseDoc).
type Ctx struct {
	Version   string
	Mode      Mode
	ScanDir   string
	FilePath  string // ModeFile's file; the doc the minimal editor boots on
	VaultName string // ModeVault's configured display/lookup name
	Depth     int
	Filter    DocFilter // which files the lists seed from
	NewExt    string    // extension "+ new file" appends to an extensionless name
	Preview   bool      // --preview: boot straight into the full-screen reader
	Files     []DocFile
	Config    Config

	Open      map[string]*components.EditorScreen
	OpenOrder []string          // Open's insertion order, for the open-docs list
	OpenRoots map[string]string // path's origin root; stable across mode switches
}

// Options is the launch selection the CLI resolves (see cmd.resolveOptions). Only the
// fields the chosen mode uses are read: Dir for ModeScan, File for ModeFile, Vault and
// Dir for ModeVault. A zero Options is the default launch — Config.Default's vault when
// valid, else ~/.gote/docs.
//
// Depth carries DepthSet rather than treating 0 as "unset", because 0 is a meaningful
// depth: `gote here 0` lists the current directory alone. Unset means the config's
// ScanDepth, which is what the zero value has to mean. Exts carries ExtsSet for the
// same reason: `gote --ext=` is an empty set on purpose — it widens a restricting
// config back to every text file for one run.
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
	c := &Ctx{
		Version:   version,
		Mode:      opts.Mode,
		Filter:    cfg.Filter(),
		Depth:     cfg.ScanDepth,
		Preview:   opts.Preview,
		Config:    cfg,
		Open:      map[string]*components.EditorScreen{},
		OpenRoots: map[string]string{},
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
		// Named on the command line. The CLI resolved and validated the path already
		// (cmd.resolveOptions' vault lookup), so a bad name never reaches here — it is
		// a launch error, not a screen that silently lists nothing.
		c.VaultName, c.ScanDir = opts.Vault, opts.Dir
	case ModeHome:
		if cfg.Default != "" {
			if path, err := vaultPath(cfg, cfg.Default); err == nil {
				c.Mode, c.VaultName, c.ScanDir = ModeVault, cfg.Default, path
			}
		}
	}
	c.Seed()
	return c
}

// Of recovers the gote context from a Shared. Screens call c := app.Of(sh).
func Of(sh *core.Shared) *Ctx { return core.App[Ctx](sh) }

// Seed re-reads the doc list for the current mode. Home mode lists its store;
// scan mode walks ScanDir; file mode has no list at all (the minimal screen renders no
// sidebar), so it touches the filesystem not at all. Seeding never touches Open —
// buffers outlive reseeds.
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

// vaultPath resolves and validates one configured vault without changing live state.
func vaultPath(cfg Config, name string) (string, error) {
	v, ok := cfg.Vaults[name]
	if !ok {
		return "", fmt.Errorf("vault %q is not configured", name)
	}
	path, err := normalizeVaultPath(v.Path)
	if err != nil {
		return "", fmt.Errorf("vault %q: %w", name, err)
	}
	return path, nil
}

// LookupVault is vaultPath for the CLI, which must tell "no such vault" (fall through
// to the next reading of the argument) apart from "that vault is broken" (a launch
// error) — an error alone cannot carry that difference, so ok does.
func LookupVault(cfg Config, name string) (path string, ok bool, err error) {
	if _, ok := cfg.Vaults[name]; !ok {
		return "", false, nil
	}
	path, err = vaultPath(cfg, name)
	return path, true, err
}

// VaultEntry is one configured vault as a caller outside the TUI sees it: the name it
// is reached by and the path exactly as config.yml writes it. Unnormalized on purpose —
// a vault whose directory has gone missing must still appear in the listing that
// explains why, which normalizeVaultPath would turn into an error instead.
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

// AddVault validates and persists a new named vault. Config is replaced in memory
// only after the atomic write succeeds, so an error cannot leave the menu ahead of
// config.yml.
func (c *Ctx) AddVault(name, rawPath string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("name is required")
	}
	if _, exists := c.Config.Vaults[name]; exists {
		return fmt.Errorf("vault %q already exists", name)
	}
	path, err := normalizeVaultPath(rawPath)
	if err != nil {
		return err
	}
	for other, v := range c.Config.Vaults {
		otherPath, err := normalizeVaultPath(v.Path)
		if err == nil && otherPath == path {
			return fmt.Errorf("%q is already saved as vault %q", path, other)
		}
	}
	next := c.Config
	next.Vaults = make(map[string]VaultConfig, len(c.Config.Vaults)+1)
	for k, v := range c.Config.Vaults {
		next.Vaults[k] = v
	}
	next.Vaults[name] = VaultConfig{Path: path, Open: []string{}}
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
	c.Open = map[string]*components.EditorScreen{}
	c.OpenOrder = nil
	c.OpenRoots = map[string]string{}
	c.Mode, c.VaultName, c.ScanDir = ModeVault, name, path
	c.FilePath = ""
	c.Seed()
	return nil
}

// OpenDoc returns the editor for path, creating and registering it on first open.
// opts carries the host's hooks (Path is filled in here); they are wired only into
// newly created editors, since an already-open doc keeps its existing editor — and its
// buffer — untouched. See homeScreen.editorOpts for what gote passes.
func (c *Ctx) OpenDoc(path string, opts components.EditorOpts) *components.EditorScreen {
	if ed, ok := c.Open[path]; ok {
		return ed
	}
	if c.OpenRoots == nil {
		c.OpenRoots = map[string]string{}
	}
	opts.Path = path
	ed := components.NewEditorScreen(opts)
	c.Open[path] = ed
	c.OpenRoots[path] = c.rootForPath(path)
	c.OpenOrder = append(c.OpenOrder, path)
	return ed
}

// RekeyDoc re-files ed under newPath after a save-as, keeping Open and OpenOrder
// consistent with a buffer that renamed itself. old == "" registers a buffer that was
// not tracked at all (the scratch editor, which saving is exactly what gives an
// identity); old == newPath is the ordinary same-path save and does nothing. The entry
// keeps its slot in OpenOrder so the open-docs list doesn't jump under the selection.
// Saving over a path that some OTHER buffer already holds drops that buffer's entry:
// the list is keyed by path and two rows for one file would both claim to be it.
func (c *Ctx) RekeyDoc(old, newPath string, ed *components.EditorScreen) {
	if newPath == "" || ed == nil {
		return
	}
	if old == newPath && c.Open[newPath] == ed {
		return
	}
	root := c.OpenRoots[old]
	if root == "" {
		root = filepath.Dir(newPath)
	}
	delete(c.Open, old)
	delete(c.OpenRoots, old)
	c.Open[newPath] = ed
	if c.OpenRoots == nil {
		c.OpenRoots = map[string]string{}
	}
	c.OpenRoots[newPath] = root

	at := -1
	filtered := c.OpenOrder[:0]
	for _, p := range c.OpenOrder {
		switch p {
		case old:
			at = len(filtered)
			filtered = append(filtered, newPath) // in place, so the row stays put
		case newPath:
			if at < 0 && old == "" {
				at = len(filtered) // an untracked buffer taking an open path's slot
				filtered = append(filtered, newPath)
			}
		default:
			filtered = append(filtered, p)
		}
	}
	c.OpenOrder = filtered
	if at < 0 {
		c.OpenOrder = append(c.OpenOrder, newPath)
	}
}

// OpenDocs lists the open buffers in opening order, for the open-docs list.
func (c *Ctx) OpenDocs() []DocFile {
	docs := make([]DocFile, 0, len(c.OpenOrder))
	for _, path := range c.OpenOrder {
		docs = append(docs, DocFile{Name: docName(path), Path: path, Root: c.OpenRoots[path]})
	}
	return docs
}

// CloseDoc removes path from the open set (an empty or unknown path is a no-op,
// which makes the scratch editor's exit free) and returns the doc to switch to:
// the one after it in open order, else the new last, else "" when none remain.
func (c *Ctx) CloseDoc(path string) (next string) {
	if _, ok := c.Open[path]; !ok {
		return ""
	}
	delete(c.Open, path)
	delete(c.OpenRoots, path)
	for i, p := range c.OpenOrder {
		if p != path {
			continue
		}
		c.OpenOrder = append(c.OpenOrder[:i], c.OpenOrder[i+1:]...)
		switch {
		case i < len(c.OpenOrder):
			return c.OpenOrder[i]
		case len(c.OpenOrder) > 0:
			return c.OpenOrder[len(c.OpenOrder)-1]
		}
		return ""
	}
	return ""
}

// rootForPath records where a document came from when it first enters Open. Seeded
// docs carry an exact root; other paths use the active mode, while a standalone or
// otherwise unseeded file is treated as flat in its containing directory.
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
