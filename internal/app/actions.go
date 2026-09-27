package app

import (
	"fmt"

	"github.com/brohd11/bubblestack/components"
	"github.com/brohd11/bubblestack/core"

	"charm.land/bubbles/v2/list"
)

// actionsMenu is the Actions picker ("a", or ctrl+alt+a from the editor).
// Session-wide restrictions omit rows rather than showing them disabled.
func (s *homeScreen) actionsMenu(sh *core.Shared) *components.PickerScreen {
	extra := []list.Item{vaultsItem()}
	if !s.minimal {
		extra = append(extra, components.Item{
			Name: "Editor groups", Desc: "move tabs or close an editor group",
			Pick: func(*core.Shared) core.Action { return core.Push(s.editorGroupsMenu()) },
		})
	}
	extra = append(extra, components.Item{
		Name: "Editor Settings", Desc: "change the git gutter",
		Pick: func(*core.Shared) core.Action { return core.Push(s.editorSettingsMenu()) },
	})
	if lspEnabled(sh) {
		extra = append(extra, components.Item{
			Name: "LSP", Desc: "diagnostics, outline, and language-server actions",
			Pick: func(*core.Shared) core.Action { return core.Push(s.lspActionsMenu()) },
		})
	}
	// Search results open the bottom panel, so this must respect the panel lock.
	if s.panelToggles {
		extra = append(extra, components.Item{
			Name: "⌕ Find in Files", Desc: "search text beneath a folder (ctrl+alt+f)",
			Pick: func(sh *core.Shared) core.Action {
				return core.Seq(core.Pop(), core.Push(s.findFilesForm(sh)))
			},
		})
	}
	return components.NewActionsMenu(selfUpdateHooks(Of(sh).Version),
		"reload the document list", refreshAction, nil, extra...)
}

// actionsSubmenu owns navigation: leaf callbacks only perform their action.
// Escape returns to Actions; selecting a leaf dismisses both picker layers.
func actionsSubmenu(title string, items ...components.Item) *components.PickerScreen {
	rows := make([]list.Item, 0, len(items))
	for _, item := range items {
		if run := item.Pick; run != nil {
			item.Pick = func(sh *core.Shared) core.Action {
				return core.Seq(core.Pop(2), run(sh))
			}
		}
		rows = append(rows, item)
	}
	return components.NewPicker(rows, components.PickerOpts{Title: title, Crumb: title})
}

func (s *homeScreen) editorSettingsMenu() *components.PickerScreen {
	var items []components.Item
	items = append(items, components.Item{
		Name: "Toggle git gutter", Desc: "show or hide changes against HEAD",
		Pick: func(*core.Shared) core.Action {
			on := !s.gitGutter
			return core.Seq(core.Async(s.setGitGutter(on)), core.SetStatus(fmt.Sprintf("git gutter %s", onOff(on))))
		},
	})
	return actionsSubmenu("Editor Settings", items...)
}

func (s *homeScreen) lspActionsMenu() *components.PickerScreen {
	var items []components.Item
	if s.panelToggles {
		items = append(items, components.Item{
			Name: "⚠ Diagnostics", Desc: "show open-file diagnostics in the bottom panel",
			Pick: func(sh *core.Shared) core.Action {
				s.bottom.selectTab(bottomDiagnostics)
				if s.bottomVisible {
					return core.Async(s.modular.FocusSlot(s.panelSlot(s.bottom)))
				}
				return s.toggleBottom(sh)
			},
		}, s.outlineActionItem())
	}
	items = append(items,
		components.Item{
			Name: "Toggle diagnostics gutter", Desc: "show or hide LSP severity markers",
			Pick: func(*core.Shared) core.Action {
				s.setDiagnosticsGutter(!s.diagnosticsGutter)
				return core.SetStatus(fmt.Sprintf("diagnostics gutter %s", onOff(s.diagnosticsGutter)))
			},
		},
		components.Item{
			Name: "Find references", Desc: "every use of the symbol at the cursor (alt+shift+r)",
			Pick: func(sh *core.Shared) core.Action { return s.requestAt(sh, lspReqReferences) },
		},
		components.Item{
			Name: "Format document", Desc: "organize imports and reformat the buffer (alt+shift+m)",
			Pick: func(sh *core.Shared) core.Action { return s.requestAt(sh, lspReqFormat) },
		},
		components.Item{
			Name: "Restart language servers", Desc: "reconnect every active language workspace",
			Pick: s.restartLanguageServers,
		})
	return actionsSubmenu("LSP", items...)
}

func (s *homeScreen) outlineActionItem() components.Item {
	verb := "Show"
	if s.outlineVisible {
		verb = "Hide"
	}
	return components.Item{
		Name: verb + " outline", Desc: "toggle the document-symbol panel (alt+shift+o)",
		Pick: s.toggleOutline,
	}
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

// vaultsItem opens the saved document roots; ad-hoc scans are a CLI launch.
func vaultsItem() components.Item {
	return components.Item{
		Name: "▣ Vaults",
		Desc: "open or add a saved document folder",
		Pick: func(sh *core.Shared) core.Action { return core.Push(vaultsMenu(sh)) },
	}
}

// editorGroupsMenu keeps all three choices visible. An unavailable row explains
// why it cannot act and has no Pick callback; Escape returns to Actions.
func (s *homeScreen) editorGroupsMenu() *components.PickerScreen {
	row := func(name, desc, reason string, run func(*core.Shared) core.Action) components.Item {
		item := components.Item{Name: name, Desc: desc}
		if reason != "" {
			item.Desc = "Unavailable: " + reason
			return item
		}
		item.Pick = func(sh *core.Shared) core.Action { return core.Seq(core.Pop(2), run(sh)) }
		return item
	}
	closeReason := ""
	if len(s.groups()) < 2 {
		closeReason = "only one editor group"
	}
	return components.NewPicker([]list.Item{
		row("Move tab left", "move into the left editor group (ctrl+t)", s.tabMoveUnavailable(-1), func(sh *core.Shared) core.Action { return s.moveTab(sh, -1) }),
		row("Move tab right / split", "move right or create a group (alt+t)", s.tabMoveUnavailable(1), func(sh *core.Shared) core.Action { return s.moveTab(sh, 1) }),
		row("Close editor group", "move its tabs to a neighboring group", closeReason, s.closeEditorGroup),
	}, components.PickerOpts{Title: "Editor groups", Crumb: "Editor groups"})
}
