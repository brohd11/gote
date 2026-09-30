package app

import (
	tea "charm.land/bubbletea/v2"
	"github.com/brohd11/bubblestack/components"
	"github.com/brohd11/bubblestack/core"
)

// requestVaultSwitch validates the target before consulting dirty state. A broken
// saved path must never make the user discard work for a switch that cannot happen.
func (s *homeScreen) requestVaultSwitch(sh *core.Shared, name string) core.Action {
	s.closeCompletion()
	if _, err := vaultPath(Of(sh).Config, name); err != nil {
		return core.Push(errPopup("open vault", err))
	}
	if dirty := s.dirtyDocs(sh); len(dirty) > 0 {
		return core.Push(dirtyPopup(dirty, "switching vaults", func(sh *core.Shared) core.Action {
			return s.activateVault(sh, name)
		}))
	}
	return s.activateVault(sh, name)
}

// activateVault is activateRoot for a named vault, recording it as visited.
func (s *homeScreen) activateVault(sh *core.Shared, name string) core.Action {
	return s.activateRoot(sh, func(c *Ctx) error { return c.SwitchVault(name) }, name)
}

// requestDefaultSwitch is requestVaultSwitch for File → Vaults → gote: the root a bare
// launch opens. It has nothing to validate — resolveDefault falls back to the home store.
func (s *homeScreen) requestDefaultSwitch(sh *core.Shared) core.Action {
	s.closeCompletion()
	switchTo := func(sh *core.Shared) core.Action {
		return s.activateRoot(sh, func(c *Ctx) error { c.SwitchDefault(); return nil }, "")
	}
	if dirty := s.dirtyDocs(sh); len(dirty) > 0 {
		return core.Push(dirtyPopup(dirty, "switching vaults", switchTo))
	}
	return switchTo(sh)
}

// activateRoot performs a confirmed switch (a vault, or the default root): save the
// outgoing session, restore the incoming root's groups (or a scratch editor), and rebuild
// the shared tools. recent names a vault to record as visited; "" records nothing.
func (s *homeScreen) activateRoot(sh *core.Shared, switchRoot func(*Ctx) error, recent string) core.Action {
	c := Of(sh)
	if err := switchRoot(c); err != nil {
		return core.Replace(errPopup("open vault", err))
	}
	if recent != "" {
		noteRecentVault(recent)
	}
	s.resetDocsGit()
	s.invalidateDocumentTools()
	s.modular.SetFocused(false)
	s.editorGroup = &editorGroup{weight: 1, gitGutter: gutterDefault(c.Config, c.Mode), diagnosticsGutter: c.lsp != nil && c.Config.Project.DiagnosticsGutter}
	c.groups, c.activeGroup = []*editorGroup{s.editorGroup}, s.editorGroup
	s.minimal = false
	if !s.restoreSession(c) {
		s.installScratch(c)
	}
	s.initGroupPanels(c)
	var initCmds []tea.Cmd
	for _, g := range c.groups {
		initCmds = append(initCmds, g.editorPanel.Init(sh))
	}
	cmd := tea.Batch(initCmds...)
	s.docsPanel.SetItems(s.docRows(c))
	// Rebuilt, not re-pointed: the new vault brings a new root as well as a new directory,
	// and the explorer's floor is fixed at construction.
	s.filePanel = components.NewFilePanel(s.filePanelOpts(c))
	s.groupedPanel = s.newGroupedPanel(c)
	if s.outlineVisible {
		s.prepareOutlineDocument()
	}
	s.sidePreview = false
	s.resetPreviewCache()
	s.sidebar = true
	// A vault or the default root is the full editor, so re-ask every mode default rather
	// than keeping ModeFile's (including the panel lock).
	s.panelToggles = true
	s.setDiagnosticsGutter(c.lsp != nil && c.Config.Project.DiagnosticsGutter)
	gutterCmd := s.setGitGutter(gutterDefault(c.Config, c.Mode))
	if c.lsp != nil {
		c.lsp.Reconcile(c)
	}
	focus := s.rebuildModular(sh, 0)
	return core.Seq(core.Async(tea.Batch(cmd, focus, gutterCmd)), core.ResetToRoot())
}
