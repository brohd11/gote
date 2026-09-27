package app

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/brohd11/bubblestack/components"
	"github.com/brohd11/bubblestack/core"
)

// Choose by label so unrelated additions to Actions do not change the test's target.
func choosePickerRow(t *testing.T, model tea.Model, label string) tea.Model {
	t.Helper()
	picker, ok := model.(core.Router).Top().(*components.PickerScreen)
	if !ok {
		t.Fatal("expected a picker")
	}
	for i, row := range picker.List().Items() {
		if item, ok := row.(components.Item); ok && item.Name == label {
			picker.List().Select(i)
			next, cmd := model.Update(keyMsg("enter"))
			return pumpModel(next, cmd)
		}
	}
	t.Fatalf("picker has no row %q", label)
	return model
}

func TestEditorGroupsSubmenuNavigationAndActions(t *testing.T) {
	model, s, sh := tabTestHome(t)
	a, _ := seedDoc(t, s, sh, "a.txt", "a")
	b, _ := seedDoc(t, s, sh, "b.txt", "b")
	s.openDoc(sh, b)
	model, _ = model.Update(keyMsg("ctrl+alt+a"))
	actions := model.(core.Router).Top().(*components.PickerScreen)
	for _, row := range actions.List().Items() {
		item := row.(components.Item)
		if strings.HasPrefix(item.Name, "Move tab") || item.Name == "Close editor group" {
			t.Fatal("group action still appears at top level")
		}
	}
	model = choosePickerRow(t, model, "Editor groups")
	groups := model.(core.Router).Top().(*components.PickerScreen)
	if len(groups.List().Items()) != 3 {
		t.Fatal("submenu should have exactly three rows")
	}
	for _, index := range []int{0, 2} {
		item := groups.List().Items()[index].(components.Item)
		if item.Pick != nil || !strings.HasPrefix(item.Desc, "Unavailable:") {
			t.Fatal("unavailable row has no explanation or can activate")
		}
		model = choosePickerRow(t, model, item.Name)
		if model.(core.Router).Top() != groups || len(s.groups()) != 1 {
			t.Fatal("unavailable action changed state")
		}
	}
	model, _ = model.Update(keyMsg("esc"))
	if model.(core.Router).Top() != actions {
		t.Fatal("Escape did not return to Actions")
	}
	model = choosePickerRow(t, model, "Editor groups")
	model = choosePickerRow(t, model, "Move tab right / split")
	if model.(core.Router).Top() != s || len(s.groups()) != 2 || s.currentID != b || !s.editorPanel.Focused() {
		t.Fatal("split action did not return to its editor")
	}
	model, _ = model.Update(keyMsg("ctrl+alt+a"))
	model = choosePickerRow(t, model, "Editor groups")
	model = choosePickerRow(t, model, "Move tab left")
	if model.(core.Router).Top() != s || len(s.groups()) != 1 || s.currentID != b {
		t.Fatal("left action did not dismiss both menus")
	}
	model, _ = model.Update(keyMsg("alt+t"))
	model, _ = model.Update(keyMsg("ctrl+alt+a"))
	model = choosePickerRow(t, model, "Editor groups")
	model = choosePickerRow(t, model, "Close editor group")
	if model.(core.Router).Top() != s || len(s.groups()) != 1 || s.currentID != b || len(s.tabs) != 2 || s.tabs[0] != a {
		t.Fatal("close action did not merge and return to editor")
	}
}

func TestEditorGroupsSubmenuSplitAvailability(t *testing.T) {
	s, sh := groupHome(t)
	s.newUnsavedBuffer(sh)
	item := s.editorGroupsMenu().List().Items()[1].(components.Item)
	if item.Pick != nil || !strings.Contains(item.Desc, "second tab") {
		t.Fatal("single-tab split was offered")
	}
	for len(s.groups()) < maxEditorGroups {
		s.newUnsavedBuffer(sh)
		s.moveTab(sh, 1)
	}
	s.newUnsavedBuffer(sh)
	item = s.editorGroupsMenu().List().Items()[1].(components.Item)
	if item.Pick != nil || !strings.Contains(item.Desc, "four") {
		t.Fatal("fifth group was offered")
	}
}
