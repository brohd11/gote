package app

import (
	"os"

	"github.com/brohd11/bubblestack/components"
	"github.com/brohd11/bubblestack/core"
)

// actionsMenu is the small Actions picker opened with "a" — the shared bubblestack
// menu (theme, self-update, refresh); gote only adds its home/scan mode toggle.
func actionsMenu(sh *core.Shared) *components.PickerScreen {
	return components.NewActionsMenu(selfUpdateHooks(Of(sh).Version),
		"reload the document list", refreshAction, nil, modeToggleItem())
}

// refreshAction reseeds the doc list — the action the Actions ▸ Refresh row fires.
// The home screen does the reload on the broadcast.
func refreshAction(sh *core.Shared) core.Action {
	return core.PropagateAll(ReseedMsg{})
}

// modeToggleItem flips the doc list between the ~/.gote store and the directory
// scan. Toggling into scan mode without a CLI-supplied directory scans the cwd at
// the configured depth.
func modeToggleItem() components.Item {
	return components.Item{
		Name: "⇄ Switch mode",
		Desc: "toggle the doc list between the ~/.gote store and a directory scan",
		Pick: func(sh *core.Shared) core.Action {
			c := Of(sh)
			if c.Mode == ModeHome {
				c.Mode = ModeScan
				if c.ScanDir == "" {
					c.ScanDir, _ = os.Getwd()
				}
			} else {
				c.Mode = ModeHome
			}
			return core.PropagateAll(ReseedMsg{})
		},
	}
}
