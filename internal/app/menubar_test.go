package app

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/brohd11/bubblestack/components"
	"github.com/brohd11/bubblestack/core"
)

// unmark is a row's label without its mark column (either mark, or the blanks).
func unmark(label string) string {
	label = strings.TrimPrefix(label, menuCheckMark)
	label = strings.TrimPrefix(label, menuRadioMark)
	return strings.TrimSpace(label)
}

// menuLabels are the rows' labels without their mark column, separators skipped.
func menuLabels(items []components.MenuItem) []string {
	var labels []string
	for _, it := range items {
		if !it.Separator {
			labels = append(labels, unmark(it.Label))
		}
	}
	return labels
}

// openHeaderMenu clicks header menu id the way a user would.
func openHeaderMenu(t *testing.T, model tea.Model, s *homeScreen, sh *core.Shared, id string) tea.Model {
	t.Helper()
	_ = model.(core.Router).View() // records the spans
	for _, span := range s.header.menuSpans {
		if span.id == id {
			next, cmd := model.Update(tea.MouseClickMsg{X: span.x0, Y: sh.BodyY(), Button: tea.MouseLeft})
			return pumpModel(next, cmd)
		}
	}
	t.Fatalf("no header menu %q", id)
	return model
}

// chooseMenuRow picks the open menu's row labeled label (mark ignored) with enter.
func chooseMenuRow(t *testing.T, model tea.Model, label string) tea.Model {
	t.Helper()
	menu, ok := model.(core.Router).Top().(*components.MenuScreen)
	if !ok {
		t.Fatalf("expected a menu, top is %T", model.(core.Router).Top())
	}
	for i, it := range menu.Items() {
		if it.Separator || unmark(it.Label) != label {
			continue
		}
		if it.Disabled {
			t.Fatalf("row %q is disabled", label)
		}
		menu.Select(i)
		// Not pumped: picks change state synchronously and the router applies their
		// pushes and pops; pumping would run real timers (a rename box's cursor blink).
		next, _ := model.Update(keyMsg("enter"))
		return next
	}
	t.Fatalf("menu has no row %q: %v", label, menuLabels(menu.Items()))
	return model
}

func TestHeaderMenuContents(t *testing.T) {
	_, s, sh := newHomeRouter(t, Options{})
	defer Of(sh).close()
	for id, want := range map[string]string{
		"file":    "Vaults | Refresh | Update gote",
		"edit":    "Copy | Cut | Paste | Find in Files",
		"view":    "Preview | Tab Groups | File view | Sidebar | Outline | Bottom panel | Wrap | Line numbers | Git gutter | Theme",
		"options": "LSP",
	} {
		if got := strings.Join(menuLabels(s.headerMenuItems(sh, id)), " | "); got != want {
			t.Errorf("%s = %q, want %q", id, got, want)
		}
	}
	if got := strings.Join(menuLabels(s.lspMenuItems(sh)), " | "); got !=
		"Show diagnostics | Diagnostics gutter | Find references | Format document | Restart language servers" {
		t.Errorf("Options → LSP = %q", got)
	}
	edit := s.headerMenuItems(sh, "edit")
	if !edit[0].Disabled || edit[0].Hint != "ctrl+c" {
		t.Errorf("Copy should be disabled without a selection and hint ctrl+c: %+v", edit[0])
	}
}

// TestViewMenuPreviewModes: the Preview submenu checks the current mode, and each row
// reaches its mode from any other, leaving the menus closed.
func TestViewMenuPreviewModes(t *testing.T) {
	model, s, sh := newHomeRouter(t, Options{})
	defer Of(sh).close()
	s.newUnsavedBuffer(sh) // a scratch buffer is markdown-previewable
	for _, step := range []struct {
		label string
		mode  int
	}{
		{"Side by side", previewModeSide},
		{"Full", previewModeFull},
		{"Side by side", previewModeSide},
		{"Full", previewModeFull},
		{"Off", previewModeOff},
	} {
		model = openHeaderMenu(t, model, s, sh, "view")
		model = chooseMenuRow(t, model, "Preview")
		for _, it := range model.(core.Router).Top().(*components.MenuScreen).Items() {
			if strings.HasPrefix(it.Label, menuRadioMark) != strings.HasSuffix(it.Label, []string{"Off", "Side by side", "Full"}[s.previewMode()]) {
				t.Fatalf("the radio mark should mark only the current mode: %q", it.Label)
			}
		}
		model = chooseMenuRow(t, model, step.label)
		if model.(core.Router).Top() != s || s.previewMode() != step.mode {
			t.Fatalf("%s: mode %d with top %T, want mode %d home", step.label, s.previewMode(), model.(core.Router).Top(), step.mode)
		}
	}
}

func TestViewMenuTogglesPanels(t *testing.T) {
	model, s, sh := newHomeRouter(t, Options{})
	defer Of(sh).close()
	for _, tc := range []struct {
		label string
		state func() bool
	}{
		{"Sidebar", func() bool { return s.sidebar }},
		{"Bottom panel", func() bool { return s.bottomVisible }},
		{"Outline", func() bool { return s.outlineVisible }},
		{"Wrap", func() bool { return s.editor.WrapMode() }},
	} {
		before := tc.state()
		model = openHeaderMenu(t, model, s, sh, "view")
		model = chooseMenuRow(t, model, tc.label)
		if model.(core.Router).Top() != s || tc.state() == before {
			t.Fatalf("View → %s did not toggle and close the menu", tc.label)
		}
	}
}

func TestOptionsLSPNeedsAServer(t *testing.T) {
	_, s, sh := tabTestHome(t) // no language-server manager
	items := s.headerMenuItems(sh, "options")
	if len(items) != 1 || !items[0].Disabled {
		t.Fatalf("Options → LSP should be disabled without a server: %+v", items)
	}
}

// headerLabelX is the first column of header menu id's label.
func headerLabelX(t *testing.T, model tea.Model, s *homeScreen, id string) int {
	t.Helper()
	_ = model.(core.Router).View()
	for _, span := range s.header.menuSpans {
		if span.id == id {
			return span.x0
		}
	}
	t.Fatalf("no header menu %q", id)
	return 0
}

// TestMenuBarSwitches: with one header menu open, pointing at another label opens that
// one in its place — from a submenu too — and clicking the open label closes it.
func TestMenuBarSwitches(t *testing.T) {
	model, s, sh := newHomeRouter(t, Options{})
	defer Of(sh).close()
	s.newUnsavedBuffer(sh) // previewable, so View → Preview is live
	topLabels := func() string {
		menu, ok := model.(core.Router).Top().(*components.MenuScreen)
		if !ok {
			return ""
		}
		return strings.Join(menuLabels(menu.Items()), " | ")
	}
	underTop := func() core.Screen {
		// Closing the top menu must reveal home: the old menu is gone, not buried.
		model, _ = model.Update(core.Pop())
		return model.(core.Router).Top()
	}

	model = openHeaderMenu(t, model, s, sh, "file")
	model, _ = model.Update(tea.MouseMotionMsg{X: headerLabelX(t, model, s, "edit"), Y: sh.BodyY()})
	if got := topLabels(); got != "Copy | Cut | Paste | Find in Files" {
		t.Fatalf("pointing at Edit should switch to it, top menu = %q", got)
	}
	if underTop() != s {
		t.Fatal("the File menu should have closed, not stayed under Edit")
	}

	model = openHeaderMenu(t, model, s, sh, "view")
	model = chooseMenuRow(t, model, "Preview") // the submenu is open
	model, _ = model.Update(tea.MouseMotionMsg{X: headerLabelX(t, model, s, "options"), Y: sh.BodyY()})
	if got := topLabels(); got != "LSP" {
		t.Fatalf("pointing at Options from a submenu should switch, top menu = %q", got)
	}
	if underTop() != s {
		t.Fatal("the View cascade should have closed entirely")
	}

	model = openHeaderMenu(t, model, s, sh, "file")
	model, _ = model.Update(tea.MouseClickMsg{X: headerLabelX(t, model, s, "view"), Y: sh.BodyY(), Button: tea.MouseLeft})
	if !strings.HasPrefix(topLabels(), "Preview") {
		t.Fatalf("a click on View should switch in one click, top menu = %q", topLabels())
	}
	model, _ = model.Update(tea.MouseClickMsg{X: headerLabelX(t, model, s, "view"), Y: sh.BodyY(), Button: tea.MouseLeft})
	if model.(core.Router).Top() != s {
		t.Fatal("a click on the open menu's own label should close it")
	}
}

// TestMenuBarOpensAppScreens: the rows that came from the Actions picker push the same
// screens it did — the theme picker, the update check, the vaults list, the search form.
func TestMenuBarOpensAppScreens(t *testing.T) {
	for _, tc := range []struct {
		menu, row string
		want      func(core.Screen) bool
	}{
		{"view", "Theme", func(sc core.Screen) bool { _, ok := sc.(*components.PickerScreen); return ok }},
		{"file", "Update gote", func(sc core.Screen) bool { _, ok := sc.(*components.LoadingScreen); return ok }},
		{"edit", "Find in Files", func(sc core.Screen) bool { _, ok := sc.(*components.FormScreen); return ok }},
	} {
		model, s, sh := newHomeRouter(t, Options{})
		model = openHeaderMenu(t, model, s, sh, tc.menu)
		model = chooseMenuRow(t, model, tc.row)
		if top := model.(core.Router).Top(); !tc.want(top) {
			t.Errorf("%s → %s pushed %T", tc.menu, tc.row, top)
		}
		Of(sh).close()
	}
}

// TestFileVaultsSubmenu: File → Vaults lists the recently visited vaults newest first,
// marks the active one, and ends in More, the full picker; picking one switches to it
// and moves it to the front.
func TestFileVaultsSubmenu(t *testing.T) {
	model, s, sh := newHomeRouter(t, Options{}) // sets a temp HOME: the state file is scratch
	defer Of(sh).close()
	c := Of(sh)
	vaultsItems := func() []components.MenuItem {
		t.Helper()
		for _, it := range s.fileMenuItems() {
			if it.Label == "Vaults" {
				return it.Submenu()
			}
		}
		t.Fatal("File has no Vaults row")
		return nil
	}

	// No vaults yet: gote (the default root, active in this home-store launch) and More.
	first := vaultsItems()
	if got := strings.Join(menuLabels(first), " | "); got != "gote | "+menuMoreLabel {
		t.Fatalf("with no vaults the submenu is gote and More, got %q", got)
	}
	if !strings.HasPrefix(first[0].Label, menuRadioMark) || first[0].Hint != defaultDocsRef {
		t.Fatalf("gote should be marked while on the default root, hinting it: %+v", first[0])
	}

	for _, name := range []string{"alpha", "beta", "gamma"} {
		if err := c.AddVault(name, filepath.Join(t.TempDir(), name)); err != nil {
			t.Fatal(err)
		}
	}
	// Never visited, the vaults still fill the list, by name.
	if got := strings.Join(menuLabels(vaultsItems()), " | "); got != "gote | alpha | beta | gamma | "+menuMoreLabel {
		t.Fatalf("unvisited vaults should fill the list by name: %q", got)
	}
	noteRecentVault("gamma")
	noteRecentVault("beta")
	if got := strings.Join(menuLabels(vaultsItems()), " | "); got != "gote | beta | gamma | alpha | "+menuMoreLabel {
		t.Fatalf("recent vaults should lead newest first, the rest filling after: %q", got)
	}

	// Pick alpha through the real menus: it becomes the active vault and moves to the front.
	model = openHeaderMenu(t, model, s, sh, "file")
	model = chooseMenuRow(t, model, "Vaults")
	model = chooseMenuRow(t, model, "alpha")
	model = pumpModel(model, nil)
	if model.(core.Router).Top() != s {
		t.Fatalf("picking a vault should close both menus, top %T", model.(core.Router).Top())
	}
	if c.Mode != ModeVault || c.VaultName != "alpha" {
		t.Fatalf("switch did not happen: mode %v vault %q", c.Mode, c.VaultName)
	}
	items := vaultsItems()
	if got := strings.Join(menuLabels(items), " | "); got != "gote | alpha | beta | gamma | "+menuMoreLabel {
		t.Fatalf("the vault just visited should lead: %q", got)
	}
	if strings.HasPrefix(items[0].Label, menuRadioMark) || !strings.HasPrefix(items[2].Label, menuRadioMark) ||
		strings.HasPrefix(items[3].Label, menuRadioMark) {
		t.Fatalf("only the active vault is marked: %q / %q / %q", items[0].Label, items[2].Label, items[3].Label)
	}

	// gote goes back to the default root (the home store here) and takes the mark.
	model = openHeaderMenu(t, model, s, sh, "file")
	model = chooseMenuRow(t, model, "Vaults")
	model = chooseMenuRow(t, model, "gote")
	if model.(core.Router).Top() != s || c.Mode != ModeHome || c.VaultName != "" {
		t.Fatalf("gote should switch to the default root: mode %v vault %q", c.Mode, c.VaultName)
	}
	if items := vaultsItems(); !strings.HasPrefix(items[0].Label, menuRadioMark) {
		t.Fatalf("gote should be marked after switching to it: %q", items[0].Label)
	}

	// A default that names a vault is the gote row, not a second row below it.
	c.Config.Default = "beta"
	if got := strings.Join(menuLabels(vaultsItems()), " | "); got != "gote | alpha | gamma | "+menuMoreLabel {
		t.Fatalf("the default vault should not repeat below gote: %q", got)
	}

	model = openHeaderMenu(t, model, s, sh, "file")
	model = chooseMenuRow(t, model, "Vaults")
	model = chooseMenuRow(t, model, menuMoreLabel)
	if _, ok := model.(core.Router).Top().(*components.PickerScreen); !ok {
		t.Fatalf("More should open the Vaults picker, top %T", model.(core.Router).Top())
	}
}

// TestFileVaultsFillCaps: filling from the remaining vaults stops at maxRecentVaults, so
// the rest are reached through More.
func TestFileVaultsFillCaps(t *testing.T) {
	_, s, sh := newHomeRouter(t, Options{})
	defer Of(sh).close()
	for i := range maxRecentVaults + 3 {
		name := fmt.Sprintf("v%02d", i)
		if err := Of(sh).AddVault(name, filepath.Join(t.TempDir(), name)); err != nil {
			t.Fatal(err)
		}
	}
	noteRecentVault("v12")
	labels := menuLabels(s.vaultMenuItems())
	if len(labels) != maxRecentVaults+2 || labels[0] != "gote" || labels[1] != "v12" || labels[2] != "v00" || labels[len(labels)-1] != menuMoreLabel {
		t.Fatalf("expected gote, v12, then v00… up to %d vaults, then More: %v", maxRecentVaults, labels)
	}
}
