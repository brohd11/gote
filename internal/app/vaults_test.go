package app

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/brohd11/bubblestack/components"
	tea "github.com/charmbracelet/bubbletea"
)

func TestVaultItemsSortedAndActive(t *testing.T) {
	_, sh := newHome(t)
	c := Of(sh)
	c.Config.Vaults["zeta"] = VaultConfig{Path: "/zeta", Open: []string{}}
	c.Config.Vaults["alpha"] = VaultConfig{Path: "/alpha", Open: []string{}}
	c.Mode, c.VaultName = ModeVault, "alpha"

	items := vaultItems(sh)
	if got := items[0].(components.Item).Name; got != "+ New vault" {
		t.Fatalf("first row = %q", got)
	}
	if got := items[1].(components.Item).Name; got != "alpha" {
		t.Fatalf("first saved vault = %q, want alpha", got)
	}
	if got := items[2].(components.Item).Name; got != "zeta" {
		t.Fatalf("second saved vault = %q, want zeta", got)
	}
	if got := items[1].(components.Item).Desc; got != "/alpha  · active" {
		t.Fatalf("active vault description = %q", got)
	}
}

func TestSubmitNewVaultPersistsBeforeSwitch(t *testing.T) {
	s, sh := newHome(t)
	vault := filepath.Join(t.TempDir(), "Notes")
	if err := os.Mkdir(vault, 0o755); err != nil {
		t.Fatal(err)
	}
	// A dirty scratch buffer proves submitting the form does not discard anything;
	// the returned switch broadcast is what will subsequently raise the gate.
	s.modular.FocusSlot(s.editorSlot())
	s.editor.Update(sh, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("draft")})

	form := newVaultForm()
	form.SetValue("name", "notes")
	form.SetValue("path", vault)
	act := submitNewVault(sh, form)
	if msgType(act) != "core.seqMsg" {
		t.Fatalf("successful submit should pop, refresh and request a switch, got %s", msgType(act))
	}
	if !s.editor.Dirty() {
		t.Fatal("saving the vault entry must not discard before switch confirmation")
	}
	got := Of(sh).Config.Vaults["notes"]
	if got.Path != vault || got.Open == nil || len(got.Open) != 0 {
		t.Fatalf("persisted vault = %#v", got)
	}
	loaded, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := loaded.Vaults["notes"]; !ok {
		t.Fatal("new vault was not written to config.yml")
	}
}

func TestSubmitNewVaultValidation(t *testing.T) {
	_, sh := newHome(t)
	form := newVaultForm()
	if act := submitNewVault(sh, form); act.Msg != nil || act.Cmd == nil {
		t.Fatalf("blank name should refocus without navigating, got %+v", act)
	}
	form.SetValue("name", "notes")
	if act := submitNewVault(sh, form); act.Msg != nil || act.Cmd == nil {
		t.Fatalf("blank path should refocus without navigating, got %+v", act)
	}
	form.SetValue("path", filepath.Join(t.TempDir(), "missing"))
	if got := msgType(submitNewVault(sh, form)); got != "core.pushMsg" {
		t.Fatalf("missing folder should push an error popup, got %s", got)
	}
}
