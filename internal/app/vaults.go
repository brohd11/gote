package app

import (
	"strings"

	"github.com/brohd11/bubblestack/components"
	"github.com/brohd11/bubblestack/core"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/list"
)

// SwitchVaultMsg asks the live home screen to close its session and activate Name.
// The root owns this operation because it alone can see an untracked scratch editor.
type SwitchVaultMsg struct{ Name string }

// VaultsChangedMsg refreshes a live Vaults picker after New Vault persists an entry.
type VaultsChangedMsg struct{}

func vaultsMenu(sh *core.Shared) *components.PickerScreen {
	return components.NewPicker(vaultItems(sh), components.PickerOpts{
		Title: "Vaults",
		Crumb: "Vaults",
		Refresh: func(sh *core.Shared, payload any) ([]list.Item, bool) {
			if _, ok := payload.(VaultsChangedMsg); !ok {
				return nil, false
			}
			return vaultItems(sh), true
		},
	})
}

func vaultItems(sh *core.Shared) []list.Item {
	c := Of(sh)
	items := []list.Item{components.Item{
		Name: "+ New vault",
		Desc: "save and open a document folder",
		Pick: func(*core.Shared) core.Action { return core.Push(newVaultForm()) },
	}}
	for _, entry := range VaultList(c.Config) {
		name := entry.Name
		desc := entry.Path
		if c.Mode == ModeVault && c.VaultName == name {
			desc += "  · active"
		}
		items = append(items, components.Item{
			Name: name,
			Desc: desc,
			Pick: func(*core.Shared) core.Action {
				return core.PropagateAll(SwitchVaultMsg{Name: name})
			},
		})
	}
	return items
}

func newVaultForm() *components.FormScreen {
	return components.NewForm(components.FormOpts{
		Crumb: "New Vault",
		Fields: []components.FormField{
			components.NewHeading("Add vault"),
			components.NewSpacer(),
			components.NewTextField("name", "Name: ", "notes"),
			components.NewTextField("path", "Path: ", "~/Notes"),
		},
		Focus: "name",
		Help: []key.Binding{
			core.Hint("field", core.Keys.PrevField, core.Keys.NextField),
			core.Hint("add", core.Keys.Select),
			core.Hint("cancel", core.Keys.Back),
		},
		OnSubmit: submitNewVault,
	})
}

func submitNewVault(sh *core.Shared, form *components.FormScreen) core.Action {
	name := strings.TrimSpace(form.Value("name"))
	if name == "" {
		return core.Async(form.Focus("name"))
	}
	path := strings.TrimSpace(form.Value("path"))
	if path == "" {
		return core.Async(form.Focus("path"))
	}
	if err := Of(sh).AddVault(name, path); err != nil {
		return core.Push(errPopup("new vault", err))
	}
	return core.Seq(
		core.Pop(),
		core.PropagateAll(VaultsChangedMsg{}),
		core.PropagateAll(SwitchVaultMsg{Name: name}),
	)
}
