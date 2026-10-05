package app

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/brohd11/bubblestack/components"
	"github.com/brohd11/bubblestack/core"
)

// bodyRows is the screen's body as plain text rows.
func bodyRows(s *homeScreen, sh *core.Shared) []string {
	return strings.Split(stripANSI(s.View(sh)), "\n")
}

// TestMinimalMenuBarHiddenByDefault: minimal mode opens on the bare title bar, with the
// text where the editor's own title used to put it; a menu chord reveals the menus beside
// the title, for the rest of the run, without moving the body.
func TestMinimalMenuBarHiddenByDefault(t *testing.T) {
	model, s, sh := newHomeRouterCfg(t, DefaultConfig(), Options{Mode: ModeFile, File: soloFile(t)})
	defer Of(sh).close()
	_ = s.editor.LoadFile() // the async read never runs through the test router
	rows := bodyRows(s, sh)
	if !strings.Contains(rows[0], "solo.go") || strings.Contains(rows[0], "Edit") {
		t.Fatalf("hidden bar should be the bare title, got %q", rows[0])
	}
	want, _, _ := strings.Cut(core.RenderTitleBar("solo.go"), "\n")
	if got := strings.Split(s.View(sh), "\n")[0]; !strings.HasPrefix(got, want) {
		t.Fatalf("the focused editor's title bar should be drawn as the editor drew it:\n got %q\nwant %q", got, want)
	}
	if strings.TrimSpace(rows[1]) != "" || !strings.Contains(rows[2], "package main") {
		t.Fatalf("the body should start under the title bar's blank row, got %q / %q", rows[1], rows[2])
	}

	model, _ = model.Update(keyMsg("alt+e"))
	if labels := topMenuLabels(model.(core.Router)); len(labels) == 0 || labels[0] != "Undo" {
		t.Fatalf("alt+e should reveal the bar and open Edit, got %v", labels)
	}
	model, _ = model.Update(keyMsg("esc"))
	rows = bodyRows(s, sh)
	if !strings.Contains(rows[0], "File  Edit  View  Options") || !strings.Contains(rows[0], "solo.go") {
		t.Fatalf("revealed bar should show the minimal menus beside the title, got %q", rows[0])
	}
	if !strings.Contains(rows[1], "─") || !strings.Contains(rows[2], "package main") {
		t.Fatalf("revealing should draw the rule without moving the body, got %q / %q", rows[1], rows[2])
	}
}

// TestMinimalMenuBarChords: with menu_bar on, the bar shows from launch and the digit
// chords count the menus it draws, wrapping across them.
func TestMinimalMenuBarChords(t *testing.T) {
	cfg := DefaultConfig()
	cfg.SingleFile.MenuBar = true
	model, s, sh := newHomeRouterCfg(t, cfg, Options{Mode: ModeFile, File: soloFile(t)})
	defer Of(sh).close()
	if row := bodyRows(s, sh)[0]; !strings.HasPrefix(row, " File  Edit  View  Options │") {
		t.Fatalf("menu_bar should show the menus from launch, got %q", row)
	}
	first := func() string {
		if labels := topMenuLabels(model.(core.Router)); len(labels) > 0 {
			return labels[0]
		}
		return ""
	}
	for _, step := range []struct{ key, want string }{
		{"alt+1", "Save"},
		{"alt+2", "Undo"},
		{"alt+3", "Preview"},
		{"alt+4", "LSP"},
		{"left", "Preview"},
		{"left", "Undo"},
		{"left", "Save"},
		{"left", "LSP"}, // File → Options, wrapping
	} {
		model, _ = model.Update(keyMsg(step.key))
		if got := first(); got != step.want {
			t.Fatalf("%s should open the menu starting %q, got %q", step.key, step.want, got)
		}
	}
	model, _ = model.Update(keyMsg("esc"))
	model, _ = model.Update(keyMsg("alt+5"))
	if model.(core.Router).Top() != s {
		t.Fatal("alt+5 names no menu")
	}
}

// TestMinimalMenuItems: minimal mode keeps only what acts on its one file; the panel rows
// follow allow_panel_toggle, and no row left out strands a separator.
func TestMinimalMenuItems(t *testing.T) {
	for _, tc := range []struct {
		name      string
		cfg       Config
		view, lsp []string
	}{
		{"locked", DefaultConfig(),
			[]string{"Preview", "Wrap", "Line numbers", "Git gutter", "Theme", "Help"},
			[]string{"Diagnostics gutter", "Restart language servers"}},
		{"panels", minimalCfg(),
			[]string{"Preview", "Outline", "Bottom panel", "Wrap", "Line numbers", "Git gutter", "Theme", "Help"},
			[]string{"Show diagnostics", "Diagnostics gutter", "Restart language servers"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, sh := newHomeCfg(t, tc.cfg, Options{Mode: ModeFile, File: soloFile(t)})
			defer Of(sh).close()
			if got := menuLabels(s.headerMenuItems(sh, "file")); !slices.Equal(got, []string{"Save", "Save As", "Close", "Quit"}) {
				t.Fatalf("File = %v", got)
			}
			if got := menuLabels(s.headerMenuItems(sh, "edit")); !slices.Equal(got, []string{"Undo", "Redo", "Copy", "Cut", "Paste", "Select All", "Search", "Indent", "Dedent", "Toggle comment"}) {
				t.Fatalf("Edit = %v", got)
			}
			view := s.headerMenuItems(sh, "view")
			if got := menuLabels(view); !slices.Equal(got, tc.view) {
				t.Fatalf("View = %v, want %v", got, tc.view)
			}
			if got := menuLabels(s.lspMenuItems(sh)); !slices.Equal(got, tc.lsp) {
				t.Fatalf("LSP = %v, want %v", got, tc.lsp)
			}
			for i, it := range view {
				if it.Separator && (i == 0 || i == len(view)-1 || view[i-1].Separator) {
					t.Fatalf("View strands a separator at %d", i)
				}
			}
		})
	}
}

// TestMenuHelpCloseAndReader: View → Help is the ? page, File → Close is alt+w (a clean
// single file quits), and the editor rows grey out while the reader holds the pane.
func TestMenuHelpCloseAndReader(t *testing.T) {
	cfg := DefaultConfig()
	cfg.SingleFile.MenuBar = true
	model, s, sh := newHomeRouterCfg(t, cfg, Options{Mode: ModeFile, File: soloFile(t)})
	defer Of(sh).close()
	_ = s.editor.LoadFile()

	model, _ = model.Update(keyMsg("alt+3"))
	model = chooseMenuRow(t, model, "Help")
	if d, ok := model.(core.Router).Top().(*components.DocScreen); !ok || d.Title != "gote · shortcuts" {
		t.Fatalf("View → Help should open the shortcuts page, top %T", model.(core.Router).Top())
	}
	model, _ = model.Update(keyMsg("esc"))

	model, _ = model.Update(keyMsg("alt+1"))
	var quit tea.Cmd
	menu := model.(core.Router).Top().(*components.MenuScreen)
	for i, it := range menu.Items() {
		if it.Label == "Close" {
			menu.Select(i)
			_, quit = model.Update(keyMsg("enter"))
		}
	}
	if quit == nil {
		t.Fatal("File → Close on a clean single file should quit")
	}
	if _, ok := quit().(tea.QuitMsg); !ok {
		t.Fatal("File → Close on a clean single file should quit")
	}

	md := filepath.Join(t.TempDir(), "notes.md")
	if err := os.WriteFile(md, []byte("# hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	reader, rsh := newHomeCfg(t, offDocConfig(), Options{Mode: ModeFile, File: md})
	defer Of(rsh).close()
	reader.Update(rsh, keyMsg("ctrl+p"))
	reader.Update(rsh, keyMsg("ctrl+p"))
	if !reader.readerOn() {
		t.Fatal("two ctrl+p should reach the reader")
	}
	for _, it := range reader.headerMenuItems(rsh, "edit") {
		if !it.Separator && !it.Disabled {
			t.Fatalf("%q should be disabled under the reader", it.Label)
		}
	}
}
