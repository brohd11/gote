package app

import (
	"path/filepath"
	"strings"

	"github.com/brohd11/bubblestack/components/editor"
	"github.com/brohd11/bubblestack/core"
)

// A markdown doc's mode, which belongs to its buffer until the buffer closes: syntax
// highlighting (Off), rendered in place (Live), or the reader in the editor's pane
// (Reader); ctrl+p cycles the three. Live is the editor's own flag; Reader is
// Ctx.readerDocs. Reader sits over Live rather than replacing it, so leaving the reader
// from a menu returns to whichever the doc had. The side preview is chrome, independent of all three.
const (
	docModeOff = iota
	docModeLive
	docModeReader
)

// The values Config.DefaultDocMode takes. Reader is not a default: it is where a doc opened
// from a rendered link lands.
const (
	docModeOffName  = "off"
	docModeLiveName = "live"
)

// previewablePath reports whether a doc at path can be rendered: markdown, or a pathless
// buffer (the scratch counts as markdown). Rendering other files would mangle them.
func previewablePath(path string) bool {
	if path == "" {
		return true
	}
	switch strings.ToLower(filepath.Ext(path)) {
	case ".md", ".markdown":
		return true
	}
	return false
}

// liveByDefault reports whether new markdown docs start in Live.
func (c Config) liveByDefault() bool {
	return strings.EqualFold(strings.TrimSpace(c.DefaultDocMode), docModeLiveName)
}

// inReader reports whether ed's doc is in Reader mode.
func (c *Ctx) inReader(ed *editor.Screen) bool { return ed != nil && c.readerDocs[ed] }

// setReader records ed's doc entering or leaving Reader. Keyed by the editor, so the flag
// follows a save-as or rename; CloseDoc drops it with the buffer.
func (c *Ctx) setReader(ed *editor.Screen, on bool) {
	if ed == nil {
		return
	}
	if !on {
		delete(c.readerDocs, ed)
		return
	}
	if c.readerDocs == nil {
		c.readerDocs = make(map[*editor.Screen]bool)
	}
	c.readerDocs[ed] = true
}

// docMode is the current doc's mode; Off for a doc that cannot be previewed.
func (s *homeScreen) docMode() int {
	switch {
	case !s.previewable():
		return docModeOff
	case s.readerOn():
		return docModeReader
	case s.editor.LiveRender():
		return docModeLive
	}
	return docModeOff
}

// readerOn reports whether the current doc shows in the reader.
func (s *homeScreen) readerOn() bool {
	return s.sh != nil && s.previewable() && Of(s.sh).inReader(s.editor)
}

// setDocMode moves the current doc to mode. Off clears Live too; Reader keeps it underneath.
func (s *homeScreen) setDocMode(mode int) core.Action {
	if !s.previewable() || mode == s.docMode() {
		return core.Action{}
	}
	switch mode {
	case docModeOff:
		s.editor.SetLiveRender(false)
	case docModeLive:
		s.editor.SetLiveRender(true)
	}
	return s.setReader(mode == docModeReader)
}

// cycleDocMode is ctrl+p: Off → Live → Reader → Off. Nothing on a doc that cannot be
// previewed.
func (s *homeScreen) cycleDocMode() core.Action {
	return s.setDocMode((s.docMode() + 1) % 3)
}

// setDefaultDocMode writes default_doc_mode to config.yml. The file is re-read first so
// the write keeps any edit made to it since launch. Only docs opened afterwards follow it.
func (s *homeScreen) setDefaultDocMode(sh *core.Shared, name string) core.Action {
	c := Of(sh)
	c.Config.DefaultDocMode = name
	cfg, err := LoadConfig()
	if err == nil {
		cfg.DefaultDocMode = name
		err = SaveConfig(cfg)
	}
	if err != nil {
		return core.SetStatus("could not save default doc mode: " + err.Error())
	}
	return core.Action{}
}
