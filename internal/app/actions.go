package app

import (
	"fmt"

	"github.com/brohd11/bubblestack/components"
	"github.com/brohd11/bubblestack/core"

	"charm.land/bubbles/v2/list"
)

// actionsMenu is the small Actions picker opened with core.Keys.Actions ("a", or
// ctrl+alt+a from the editor) — the shared bubblestack menu plus gote's document-root,
// gutter and language-server controls.
//
// The rows are assembled rather than listed because three of the gates that hide them are
// per-launch and permanent: a locked panel, a launch with no language-server manager, and
// minimal mode's missing Open view. A row that cannot act for the whole session is omitted
// rather than shown dead, which is the same call editorLanguageItems makes for the
// right-click menu.
func (s *homeScreen) actionsMenu(sh *core.Shared) *components.PickerScreen {
	lsp := lspEnabled(sh)
	extra := []list.Item{vaultsItem()}
	// Open has no tabs to switch to in minimal mode (tabsVisible is false there), so the
	// row would change a setting with nothing to show for it.
	if !s.minimal {
		extra = append(extra, s.openDocsViewItem())
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
				Name: "Find references", Desc: "every use of the symbol at the cursor (alt+n)",
				Pick: func(sh *core.Shared) core.Action { return s.requestAt(sh, lspReqReferences) },
			},
			components.Item{
				Name: "Format document", Desc: "organize imports and reformat the buffer (alt+m)",
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
		Name: verb + " outline", Desc: "toggle the document-symbol panel (alt+o)",
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

// vaultsItem opens the saved document roots. It supersedes the old home/scan mode
// toggle; ad-hoc scans remain available from gote's CLI.
func vaultsItem() components.Item {
	return components.Item{
		Name: "▣ Vaults",
		Desc: "open or add a saved document folder",
		Pick: func(sh *core.Shared) core.Action { return core.Push(vaultsMenu(sh)) },
	}
}
