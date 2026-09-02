package app

import (
	"fmt"

	"github.com/brohd11/bubblestack/components"
	"github.com/brohd11/bubblestack/core"
)

// actionsMenu is the small Actions picker opened with "a" — the shared bubblestack
// menu plus Gote's document-root, gutter and language-server controls.
func (s *homeScreen) actionsMenu(sh *core.Shared) *components.PickerScreen {
	return components.NewActionsMenu(selfUpdateHooks(Of(sh).Version),
		"reload the document list", refreshAction, nil,
		vaultsItem(),
		components.Item{
			Name: "⚠ Diagnostics", Desc: "show diagnostics for every open file",
			Pick: func(sh *core.Shared) core.Action { return core.Push(s.diagnosticsScreen(sh)) },
		},
		components.Item{
			Name: "Toggle diagnostics gutter", Desc: "show or hide LSP severity markers",
			Pick: func(*core.Shared) core.Action {
				s.setDiagnosticsGutter(!s.diagnosticsGutter)
				return core.SetStatus(fmt.Sprintf("diagnostics gutter %s", onOff(s.diagnosticsGutter)))
			},
		},
		components.Item{
			Name: "Toggle git gutter", Desc: "show or hide changes against HEAD",
			Pick: func(*core.Shared) core.Action {
				on := !s.gitGutter
				return core.Seq(core.Async(s.setGitGutter(on)), core.SetStatus(fmt.Sprintf("git gutter %s", onOff(on))))
			},
		},
		components.Item{
			Name: "Restart language servers", Desc: "reconnect active GDScript and Python workspaces",
			Pick: s.restartLanguageServers,
		},
	)
}

func onOff(on bool) string {
	if on {
		return "on"
	}
	return "off"
}

// refreshAction reseeds the doc list — the action the Actions ▸ Refresh row fires.
// The home screen does the reload on the broadcast.
func refreshAction(sh *core.Shared) core.Action {
	return core.PropagateAll(ReseedMsg{})
}

// vaultsItem opens the saved document roots. It supersedes the old home/scan mode
// toggle; ad-hoc scans remain available from gote's CLI.
func vaultsItem() components.Item {
	return components.Item{
		Name: "▣ Vaults",
		Desc: "open or add a saved document folder",
		Pick: func(sh *core.Shared) core.Action { return core.Push(vaultsMenu(sh)) },
	}
}
