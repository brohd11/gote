package app

import (
	"strings"
	"testing"

	"github.com/brohd11/bubblestack/components"
	"github.com/brohd11/bubblestack/core"
)

// The Actions picker is retired: its rows live in the menu bar (menubar.go). These pin
// the rows that moved there from its Editor groups submenu, now View → Tab Groups.

func TestTabGroupsMenu(t *testing.T) {
	model, s, sh := tabTestHome(t)
	a, _ := seedDoc(t, s, sh, "a.txt", "a")
	b, _ := seedDoc(t, s, sh, "b.txt", "b")
	s.openDoc(sh, b)
	tabGroups := func() {
		t.Helper()
		model = openHeaderMenu(t, model, s, sh, "view")
		model = chooseMenuRow(t, model, "Tab Groups")
	}

	// One group: moving left and closing a group cannot act, so they are disabled.
	tabGroups()
	rows := model.(core.Router).Top().(*components.MenuScreen).Items()
	if got := strings.Join(menuLabels(rows), " | "); got != "Move tab left | Move tab right / split | Close editor group" {
		t.Fatalf("Tab Groups = %q", got)
	}
	if !rows[0].Disabled || rows[1].Disabled || !rows[2].Disabled {
		t.Fatalf("with one group only the split should be live: %+v", rows)
	}
	model, _ = model.Update(keyMsg("esc"))
	model, _ = model.Update(keyMsg("esc"))

	tabGroups()
	model = chooseMenuRow(t, model, "Move tab right / split")
	if model.(core.Router).Top() != s || len(s.groups()) != 2 || s.currentID != b || !s.editorPanel.Focused() {
		t.Fatal("split should close both menus and return to its editor")
	}
	tabGroups()
	model = chooseMenuRow(t, model, "Move tab left")
	if model.(core.Router).Top() != s || len(s.groups()) != 1 || s.currentID != b {
		t.Fatal("left should close both menus")
	}
	model, _ = model.Update(keyMsg("alt+t"))
	tabGroups()
	model = chooseMenuRow(t, model, "Close editor group")
	if model.(core.Router).Top() != s || len(s.groups()) != 1 || s.currentID != b || len(s.tabs) != 2 || s.tabs[0] != a {
		t.Fatal("close should merge the group and return to the editor")
	}
}

// TestTabGroupsSplitAvailability: the split row is disabled for the same reasons the
// shortcut refuses — one tab, or four groups already.
func TestTabGroupsSplitAvailability(t *testing.T) {
	s, sh := groupHome(t)
	s.newUnsavedBuffer(sh)
	if !s.tabGroupMenuItems()[1].Disabled || !strings.Contains(s.tabMoveUnavailable(1), "second tab") {
		t.Fatal("single-tab split was offered")
	}
	for len(s.groups()) < maxEditorGroups {
		s.newUnsavedBuffer(sh)
		s.moveTab(sh, 1)
	}
	s.newUnsavedBuffer(sh)
	if !s.tabGroupMenuItems()[1].Disabled || !strings.Contains(s.tabMoveUnavailable(1), "four") {
		t.Fatal("fifth group was offered")
	}
}
