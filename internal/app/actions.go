package app

import (
	"fmt"

	"github.com/brohd11/bubblestack/components"
	"github.com/brohd11/bubblestack/core"

	"charm.land/bubbles/v2/list"
)

// actionsMenu is the Actions picker ("a", or ctrl+alt+a from the editor): the shared
// menu plus gote's document-root, gutter and language-server rows. Rows that cannot act
// for the whole session (locked panels, no language-server manager, minimal mode) are
// left out rather than shown disabled.
func (s *homeScreen) actionsMenu(sh *core.Shared) *components.PickerScreen {
	lsp := lspEnabled(sh)
	extra := []list.Item{vaultsItem()}
	// Open has no tabs to switch to in minimal mode (tabsVisible is false there), so the
	// row would change a setting with nothing to show for it.
	if !s.minimal {
		if len(s.groups()) == 1 {
			extra = append(extra, s.openDocsViewItem())
		}
		extra = append(extra, components.Item{
			Name: "Editor groups", Desc: "move tabs or close an editor group",
			Pick: func(*core.Shared) core.Action { return core.Push(s.editorGroupsMenu()) },
		})
	}
	if s.panelToggles && lsp {
		extra = append(extra, components.Item{
			Name: "⚠ Diagnostics", Desc: "show open-file diagnostics in the bottom panel",
			Pick: func(sh *core.Shared) core.Action {
				s.bottom.selectTab(bottomDiagnostics)
				if s.bottomVisible {
					return core.Seq(core.Pop(), core.Async(s.modular.FocusSlot(s.panelSlot(s.bottom))))
				}
				return core.Seq(core.Pop(), s.toggleBottom(sh))
			},
		})
	}
	// Locked with the panels: a result opens the bottom panel whether or not it was asked
	// for, so a live search would be a way around the lock.
	if s.panelToggles {
		extra = append(extra, components.Item{
			Name: "⌕ Find in Files", Desc: "search text beneath a folder (ctrl+alt+f)",
			Pick: func(sh *core.Shared) core.Action {
				return core.Seq(core.Pop(), core.Push(s.findFilesForm(sh)))
			},
		})
	}
	if lsp {
		extra = append(extra, components.Item{
			Name: "Toggle diagnostics gutter", Desc: "show or hide LSP severity markers",
			Pick: func(*core.Shared) core.Action {
				s.setDiagnosticsGutter(!s.diagnosticsGutter)
				return core.SetStatus(fmt.Sprintf("diagnostics gutter %s", onOff(s.diagnosticsGutter)))
			},
		})
	}
	extra = append(extra, components.Item{
		Name: "Toggle git gutter", Desc: "show or hide changes against HEAD",
		Pick: func(*core.Shared) core.Action {
			on := !s.gitGutter
			return core.Seq(core.Async(s.setGitGutter(on)), core.SetStatus(fmt.Sprintf("git gutter %s", onOff(on))))
		},
	})
	if s.panelToggles {
		extra = append(extra, s.outlineActionItem())
	}
	if lsp {
		extra = append(extra,
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
	}
	return components.NewActionsMenu(selfUpdateHooks(Of(sh).Version),
		"reload the document list", refreshAction, nil, extra...)
}

func (s *homeScreen) outlineActionItem() components.Item {
	verb := "Show"
	if s.outlineVisible {
		verb = "Hide"
	}
	return components.Item{
		Name: verb + " outline", Desc: "toggle the document-symbol panel (alt+shift+o)",
		Pick: func(sh *core.Shared) core.Action { return core.Seq(core.Pop(), s.toggleOutline(sh)) },
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
