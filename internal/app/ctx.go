package app

import (
	"path/filepath"

	"github.com/brohd11/bubblestack/components"
	"github.com/brohd11/bubblestack/core"
)

// Mode is what gote was launched as: a doc list seeded from the flat ~/.gote store or
// from a recursive scan, or a single file opened on its own.
type Mode int

const (
	ModeHome Mode = iota // ~/.gote/*.<ext> — the default
	ModeScan             // recursive scan of ScanDir to Depth
	ModeFile             // FilePath alone, in the chrome-less minimal editor
)

// Ctx is gote's app context, stored on core.Shared.App and recovered with Of. It
// carries the seed state (mode/dir/depth/extension and the last-seeded file list)
// and every open buffer: Open maps a doc's path to its EditorScreen, which owns the
// buffer — so keeping the instance is what preserves unsaved edits across file
// switches. ctrl+x in the editor closes the buffer (CloseDoc).
type Ctx struct {
	Version  string
	Mode     Mode
	ScanDir  string
	FilePath string // ModeFile's file; the doc the minimal editor boots on
	Depth    int
	Ext      string
	Files    []DocFile

	Open      map[string]*components.EditorScreen
	OpenOrder []string // Open's insertion order, for the open-docs list
}

// Options is the launch selection the CLI resolves (see cmd.resolveOptions). Only the
// fields the chosen mode uses are read: Dir for ModeScan, File for ModeFile. A zero
// Options is the default launch — the ~/.gote store at the config's depth.
//
// Depth carries DepthSet rather than treating 0 as "unset", because 0 is a meaningful
// depth: `gote here 0` lists the current directory alone. Unset means the config's
// ScanDepth, which is what the zero value has to mean.
type Options struct {
	Mode     Mode
	Dir      string // ModeScan root, absolute
	File     string // ModeFile path, absolute
	Depth    int    // scan depth, honored only when DepthSet
	DepthSet bool
}

// New builds the context from the loaded config and the CLI's launch options, and
// performs the initial seed so the first screen has rows to show.
func New(version string, cfg Config, opts Options) *Ctx {
	c := &Ctx{
		Version: version,
		Mode:    opts.Mode,
		Ext:     cfg.Extension,
		Depth:   cfg.ScanDepth,
		Open:    map[string]*components.EditorScreen{},
	}
	if opts.DepthSet {
		c.Depth = opts.Depth
	}
	switch opts.Mode {
	case ModeScan:
		c.ScanDir = opts.Dir
	case ModeFile:
		c.FilePath = opts.File
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
	case ModeScan:
		c.Files = ScanDocs(c.ScanDir, c.Depth, c.Ext)
	case ModeFile:
		c.Files = nil
	default:
		dir, err := Dir()
		if err != nil {
			c.Files = nil
			return
		}
		c.Files = HomeDocs(dir, c.Ext)
	}
}

// OpenDoc returns the editor for path, creating and registering it on first open.
// opts carries the host's hooks (Path is filled in here); they are wired only into
// newly created editors, since an already-open doc keeps its existing editor — and its
// buffer — untouched. See homeScreen.editorOpts for what gote passes.
func (c *Ctx) OpenDoc(path string, opts components.EditorOpts) *components.EditorScreen {
	if ed, ok := c.Open[path]; ok {
		return ed
	}
	opts.Path = path
	ed := components.NewEditorScreen(opts)
	c.Open[path] = ed
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
	delete(c.Open, old)
	c.Open[newPath] = ed

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
		docs = append(docs, DocFile{Name: docName(path), Path: path})
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

func docName(path string) string {
	return filepath.Base(path)
}
