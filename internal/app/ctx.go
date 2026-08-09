package app

import (
	"path/filepath"

	"github.com/brohd11/bubblestack/components"
	"github.com/brohd11/bubblestack/core"
)

// Mode is the doc-list seed source: the flat ~/.gote store, or a recursive scan.
type Mode int

const (
	ModeHome Mode = iota // ~/.gote/*.<ext> — the default
	ModeScan             // recursive scan of ScanDir to Depth
)

// Ctx is gote's app context, stored on core.Shared.App and recovered with Of. It
// carries the seed state (mode/dir/depth/extension and the last-seeded file list)
// and every open buffer: Open maps a doc's path to its EditorScreen, which owns the
// buffer — so keeping the instance is what preserves unsaved edits across file
// switches. ctrl+x in the editor closes the buffer (CloseDoc).
type Ctx struct {
	Version string
	Mode    Mode
	ScanDir string
	Depth   int
	Ext     string
	Files   []DocFile

	Open      map[string]*components.EditorScreen
	OpenOrder []string // Open's insertion order, for the open-docs list
}

// New builds the context from the loaded config and the CLI's mode selection, and
// performs the initial seed so the first screen has rows to show.
func New(version string, cfg Config, scan bool, dir string, depth int) *Ctx {
	c := &Ctx{
		Version: version,
		Mode:    ModeHome,
		Ext:     cfg.Extension,
		Depth:   cfg.ScanDepth,
		Open:    map[string]*components.EditorScreen{},
	}
	if depth > 0 {
		c.Depth = depth
	}
	if scan {
		c.Mode = ModeScan
		c.ScanDir = dir
	}
	c.Seed()
	return c
}

// Of recovers the gote context from a Shared. Screens call c := app.Of(sh).
func Of(sh *core.Shared) *Ctx { return core.App[Ctx](sh) }

// Seed re-reads the doc list for the current mode. Home mode lists its store;
// scan mode walks ScanDir. Seeding never touches Open — buffers outlive reseeds.
func (c *Ctx) Seed() {
	switch c.Mode {
	case ModeScan:
		c.Files = ScanDocs(c.ScanDir, c.Depth, c.Ext)
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
// onExit is wired only into newly created editors; an already-open doc keeps its
// existing editor (and its buffer) untouched.
func (c *Ctx) OpenDoc(path string, onExit func(*core.Shared) core.Action) *components.EditorScreen {
	if ed, ok := c.Open[path]; ok {
		return ed
	}
	ed := components.NewEditorScreen(components.EditorOpts{Path: path, OnExit: onExit})
	c.Open[path] = ed
	c.OpenOrder = append(c.OpenOrder, path)
	return ed
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
