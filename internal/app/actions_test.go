package app

import (
	"slices"
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

func pickerLabels(picker *components.PickerScreen) []string {
	var labels []string
	for _, row := range picker.List().Items() {
		labels = append(labels, row.(components.Item).Name)
	}
	return labels
}

func TestActionsSubmenuMembershipAndNavigation(t *testing.T) {
	model, s, sh := newHomeRouter(t, Options{})
	defer Of(sh).close()
	model, _ = model.Update(keyMsg("a"))
	actions := model.(core.Router).Top().(*components.PickerScreen)
	top := pickerLabels(actions)
	for _, name := range []string{"Editor groups", "Editor Settings", "LSP", "⌕ Find in Files", "▣ Vaults"} {
		if !slices.Contains(top, name) {
			t.Fatalf("Actions missing %q: %v", name, top)
		}
	}
	for _, tc := range []struct {
		name string
		want []string
	}{
		{"LSP", []string{"⚠ Diagnostics", "Show outline", "Toggle diagnostics gutter", "Find references", "Format document", "Restart language servers"}},
		{"Editor Settings", []string{"Show open documents as tabs", "Toggle git gutter"}},
	} {
		model = choosePickerRow(t, model, tc.name)
		picker := model.(core.Router).Top().(*components.PickerScreen)
		if got := pickerLabels(picker); !slices.Equal(got, tc.want) {
			t.Fatalf("%s rows = %v, want %v", tc.name, got, tc.want)
		}
		for _, leaf := range tc.want {
			if slices.Contains(top, leaf) {
				t.Errorf("%q still appears at top level", leaf)
			}
		}
		model, _ = model.Update(keyMsg("esc"))
		if model.(core.Router).Top() != actions {
			t.Fatalf("Escape from %s did not return to Actions", tc.name)
		}
	}
	model, _ = model.Update(keyMsg("esc"))
	if model.(core.Router).Top() != s {
		t.Fatal("Escape from Actions did not return home")
	}
}

func TestActionsSubmenuTogglesReturnHome(t *testing.T) {
	model, s, sh := newHomeRouter(t, Options{})
	defer Of(sh).close()
	s.newUnsavedBuffer(sh)
	id, ed := s.currentID, s.editor
	selectAction := func(menu, name string) {
		t.Helper()
		model, _ = model.Update(keyMsg("ctrl+alt+a"))
		model = choosePickerRow(t, model, menu)
		model = choosePickerRow(t, model, name)
		if model.(core.Router).Top() != s || s.currentID != id || s.editor != ed {
			t.Fatalf("%s did not dismiss both menus or changed the active document", name)
		}
	}
	for _, name := range []string{"Show open documents as tabs", "Show open documents as list"} {
		before := s.openDocsTabs
		selectAction("Editor Settings", name)
		if s.openDocsTabs == before || !s.editorPanel.Focused() {
			t.Fatal("view toggle did not change layout and retain editor focus")
		}
	}
	for range 2 {
		before := s.gitGutter
		selectAction("Editor Settings", "Toggle git gutter")
		if s.gitGutter == before || !s.editorPanel.Focused() {
			t.Fatal("git gutter did not toggle and retain editor focus")
		}
		before = s.diagnosticsGutter
		selectAction("LSP", "Toggle diagnostics gutter")
		if s.diagnosticsGutter == before || !s.editorPanel.Focused() {
			t.Fatal("diagnostics gutter did not toggle and retain editor focus")
		}
	}
	selectAction("LSP", "Show outline")
	if !s.outlineVisible {
		t.Fatal("outline was not shown")
	}
	selectAction("LSP", "Hide outline")
	if s.outlineVisible {
		t.Fatal("outline was not hidden")
	}
	for _, name := range []string{"Find references", "Format document", "Restart language servers"} {
		selectAction("LSP", name)
	}
}

func TestEditorSettingsSubmenuWithMultipleGroups(t *testing.T) {
	model, s, sh := tabTestHome(t)
	s.newUnsavedBuffer(sh)
	s.newUnsavedBuffer(sh)
	s.moveTab(sh, 1)
	model, _ = model.Update(keyMsg("ctrl+alt+a"))
	if slices.Contains(pickerLabels(model.(core.Router).Top().(*components.PickerScreen)), "LSP") {
		t.Fatal("LSP submenu is available without a manager")
	}
	model = choosePickerRow(t, model, "Editor Settings")
	if got := pickerLabels(model.(core.Router).Top().(*components.PickerScreen)); !slices.Equal(got, []string{"Toggle git gutter"}) {
		t.Fatalf("multi-group settings rows = %v", got)
	}
	active := s.editorGroup
	other := s.groups()[0]
	before, otherBefore := active.gitGutter, other.gitGutter
	model = choosePickerRow(t, model, "Toggle git gutter")
	if model.(core.Router).Top() != s || active.gitGutter == before || other.gitGutter != otherBefore {
		t.Fatal("gutter toggle did not apply only to the active group and return home")
	}
}
