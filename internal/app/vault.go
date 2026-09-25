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

// activateVault performs a confirmed switch: clear the open set, give the pane a scratch
// editor, and rebuild vault-specific state before returning to the root.
func (s *homeScreen) activateVault(sh *core.Shared, name string) core.Action {
	c := Of(sh)
	if err := c.SwitchVault(name); err != nil {
		return core.Replace(errPopup("open vault", err))
	}
	s.resetDocsGit()
	s.installScratch(c)
	s.fullPreview = nil // the vault's scratch buffer is the editor, not a reader over it
	cmd := s.editorPanel.SetChild(s.editor)
	s.docsPanel.SetItems(s.docRows(c))
	// Rebuilt, not re-pointed: the new vault brings a new root as well as a new directory,
	// and the explorer's floor is fixed at construction.
	s.filePanel = components.NewFilePanel(s.filePanelOpts(c))
	s.openPanel.SetItems(nil)
	if s.outlineVisible {
		s.prepareOutlineDocument()
	}
	s.preview, s.previewPrior = previewOff, previewOff
	s.resetPreviewCache()
	s.minimal = false
	s.sidebar = true
	// A vault is the full editor, so re-ask every mode default rather than keeping ModeFile's
	// (including the panel lock).
	s.panelToggles = true
	s.setDiagnosticsGutter(c.lsp != nil && c.Config.Project.DiagnosticsGutter)
	gutterCmd := s.setGitGutter(gutterDefault(c.Config, ModeVault))
	if c.lsp != nil {
		c.lsp.Reconcile(c)
	}
	focus := s.rebuildModular(sh, 0)
	return core.Seq(core.Async(tea.Batch(cmd, focus, gutterCmd)), core.ResetToRoot())
}
