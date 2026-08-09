package app

import (
	"github.com/brohd11/bubblestack/core"

	tea "github.com/charmbracelet/bubbletea"
)

// editorScreen is gote's root screen — an empty body for now. The router owns the
// global keys (quit, etc.); nothing here handles input yet.
type editorScreen struct {
	width  int
	height int
}

// NewEditorScreen builds the editor screen.
func NewEditorScreen(sh *core.Shared) core.Screen {
	return &editorScreen{}
}

func (s *editorScreen) Init(sh *core.Shared) tea.Cmd { return nil }

func (s *editorScreen) Update(sh *core.Shared, msg tea.Msg) (core.Screen, core.Action) {
	return s, core.Action{}
}

func (s *editorScreen) View(sh *core.Shared) string { return "" }

func (s *editorScreen) HelpView(sh *core.Shared) string { return "" }

func (s *editorScreen) SetSize(sh *core.Shared, width, bodyHeight int) {
	s.width = width
	s.height = bodyHeight
}
