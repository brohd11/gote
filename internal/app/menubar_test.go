package app

import (
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
		"file":    "Nothing here yet",
		"edit":    "Copy | Cut | Paste",
		"view":    "Preview | Sidebar | Outline | Bottom panel | Wrap | Line numbers | Git gutter",
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
