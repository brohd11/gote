package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/brohd11/bubblestack/core"
)

// soloFile is the document a single-file launch opens. Go source rather than markdown so
// the language-server questions have a file a server would actually claim.
func soloFile(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "solo.go")
	if err := os.WriteFile(path, []byte("package main\n\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func contextLabels(s *homeScreen, sh *core.Shared) []string {
	var out []string
	for _, item := range s.editorContextItems(sh) {
		out = append(out, item.Label)
	}
	return out
}

func hasLabel(labels []string, want string) bool {
	for _, l := range labels {
		if l == want {
			return true
		}
	}
	return false
}

// TestSingleFilePanelLockKeys: with allow_panel_toggle off, the three keys that summon a
// panel do nothing. Find in files is in the set because a result forces the bottom panel
// open on its own, which would be a way around the lock.
func TestSingleFilePanelLockKeys(t *testing.T) {
	s, sh := newHomeCfg(t, DefaultConfig(), Options{Mode: ModeFile, File: soloFile(t)})
	if s.panelToggles {
		t.Fatal("the shipped single_file_mode should lock the panels")
	}
	for _, k := range []string{"alt+\\", "alt+shift+o", "ctrl+alt+f"} {
		if _, act := s.Update(sh, keyMsg(k)); act.Msg != nil {
			t.Errorf("%s should be inert while the panels are locked, got %T", k, act.Msg)
		}
	}
	if s.bottomVisible || s.outlineVisible {
		t.Errorf("a locked launch opened a panel anyway: bottom=%v outline=%v",
			s.bottomVisible, s.outlineVisible)
	}
	// ctrl+p is deliberately NOT locked: the preview is the editor's own reader, and
	// --preview is a single-file feature. Checked on markdown, since previewable() refuses
	// anything else whatever the lock says.
	md := filepath.Join(t.TempDir(), "notes.md")
	if err := os.WriteFile(md, []byte("# hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	reader, rsh := newHomeCfg(t, DefaultConfig(), Options{Mode: ModeFile, File: md})
	reader.Update(rsh, keyMsg("ctrl+p"))
	if reader.preview == previewOff {
		t.Error("ctrl+p should still cycle the preview under the panel lock")
	}
}

// TestSingleFilePanelUnlockKeys is the other half: the same launch with the key set to true
// behaves as it always did, so the lock is what silenced them and not minimal mode itself.
func TestSingleFilePanelUnlockKeys(t *testing.T) {
	s, sh := newHomeCfg(t, minimalCfg(), Options{Mode: ModeFile, File: soloFile(t)})
	if !s.panelToggles {
		t.Fatal("allow_panel_toggle: true should leave the panels reachable")
	}
	s.Update(sh, keyMsg("alt+\\"))
	if !s.bottomVisible {
		t.Error("alt+\\ should open the bottom panel when the panels are unlocked")
	}
	s.Update(sh, keyMsg("alt+shift+o"))
	if !s.outlineVisible {
		t.Error("alt+shift+o should open the outline when the panels are unlocked")
	}
}

// TestSingleFilePanelLockMenus: the rows the lock takes away are omitted from the menu,
// and the rows beside them are untouched.
func TestSingleFilePanelLockMenus(t *testing.T) {
	locked, lsh := newHomeCfg(t, DefaultConfig(), Options{Mode: ModeFile, File: soloFile(t)})
	labels := contextLabels(locked, lsh)
	for _, gone := range []string{"Show outline", "Hide outline", "Toggle diagnostics panel"} {
		if hasLabel(labels, gone) {
			t.Errorf("the right-click menu still offers %q under the lock: %v", gone, labels)
		}
	}
	for _, kept := range []string{"Toggle preview", "Toggle wrap", "Toggle git gutter"} {
		if !hasLabel(labels, kept) {
			t.Errorf("the lock removed %q, which is not a panel: %v", kept, labels)
		}
	}

	// Unlocked, every removed row comes back.
	open, osh := newHomeCfg(t, minimalCfg(), Options{Mode: ModeFile, File: soloFile(t)})
	if labels := contextLabels(open, osh); !hasLabel(labels, "Show outline") ||
		!hasLabel(labels, "Toggle diagnostics panel") {
		t.Errorf("unlocking did not restore the right-click panel rows: %v", labels)
	}
}

// TestSingleFileAllowLSP: default_allow_lsp: false denies the launch a manager, which is
// the same state auto-lsp: false produces — so every LSP-derived row falls away from the
// right-click menu with it.
func TestSingleFileAllowLSP(t *testing.T) {
	cfg := minimalCfg()
	cfg.SingleFile.AllowLSP = false
	s, sh := newHomeCfg(t, cfg, Options{Mode: ModeFile, File: soloFile(t)})
	if Of(sh).lsp != nil {
		t.Fatal("single_file_mode.default_allow_lsp: false should leave the launch without a manager")
	}
	if s.diagnosticsGutter {
		t.Error("a launch with no manager has nothing to draw in the diagnostics column")
	}

	labels := contextLabels(s, sh)
	for _, gone := range []string{"Hover info", "Go to definition", "Find references",
		"Format document", "Toggle diagnostics gutter", "Restart language servers"} {
		if hasLabel(labels, gone) {
			t.Errorf("the right-click menu still offers %q with no manager: %v", gone, labels)
		}
	}
	// The git column is not an LSP feature and must survive.
	if !hasLabel(labels, "Toggle git gutter") {
		t.Error("the git gutter row should survive a launch with no language server")
	}

	// The project section still speaks for a project launch.
	if p, _ := newHomeCfg(t, cfg, Options{}); p.diagnosticsGutter == false {
		t.Error("turning single-file LSP off should not reach project_mode")
	}
}

// TestProjectAllowLSP: the project section carries the same switch, and auto-lsp remains
// the master — either one alone denies the manager.
func TestProjectAllowLSP(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Project.AllowLSP = false
	if _, sh := newHomeCfg(t, cfg, Options{}); Of(sh).lsp != nil {
		t.Error("project_mode.default_allow_lsp: false should deny the manager")
	}
	// And the single-file launch is unaffected by the project key.
	if _, sh := newHomeCfg(t, cfg, Options{Mode: ModeFile, File: soloFile(t)}); Of(sh).lsp == nil {
		t.Error("the project key should not reach a single-file launch")
	}

	master := DefaultConfig()
	master.AutoLSP = false
	if _, sh := newHomeCfg(t, master, Options{}); Of(sh).lsp != nil {
		t.Error("auto-lsp: false is the master switch and should still deny the manager")
	}
}

// TestLSPDisabledReason: the message names the key that actually turned the servers off.
// It used to always say auto-lsp, which would send the user to edit a key already true.
func TestLSPDisabledReason(t *testing.T) {
	off := DefaultConfig()
	off.AutoLSP = false
	cases := []struct {
		name string
		cfg  Config
		mode Mode
		want string
	}{
		{"master", off, ModeHome, "auto-lsp: false"},
		{"master beats mode", off, ModeFile, "auto-lsp: false"},
		{"single file", DefaultConfig(), ModeFile, "single_file_mode.default_allow_lsp: false"},
		{"project", DefaultConfig(), ModeHome, "project_mode.default_allow_lsp: false"},
	}
	for _, c := range cases {
		if got := lspDisabledReason(&Ctx{Config: c.cfg, Mode: c.mode}); got != c.want {
			t.Errorf("%s: reason = %q, want %q", c.name, got, c.want)
		}
	}
	if got := lspDisabledReason(nil); got == "" {
		t.Error("a nil ctx should still name something rather than an empty reason")
	}
}

// TestSingleFileHelpMarksLockedKeys: the ? overlay LISTS a locked key with the setting
// responsible rather than dropping it — a binding that silently vanished would read as a
// gote bug. alt+shift+b is marked by neither gate: find-in-files pushes onto the same jump
// stack, so it outlives the language server.
func TestSingleFileHelpMarksLockedKeys(t *testing.T) {
	cfg := DefaultConfig()
	cfg.SingleFile.AllowLSP = false
	s, _ := newHomeCfg(t, cfg, Options{Mode: ModeFile, File: soloFile(t)})
	text := s.helpText()

	for _, k := range []string{"alt+\\", "alt+shift+o", "ctrl+alt+f"} {
		if !strings.Contains(text, k) {
			t.Errorf("the overlay dropped %q instead of marking it:\n%s", k, text)
		}
	}
	if !strings.Contains(text, "off (single_file_mode.allow_panel_toggle)") {
		t.Error("no locked key named the panel setting")
	}
	if !strings.Contains(text, "off (single_file_mode.default_allow_lsp: false)") {
		t.Error("no silenced language-server key named the LSP setting")
	}
	for _, line := range strings.Split(text, "\n") {
		if strings.Contains(line, "alt+shift+b") && strings.Contains(line, "off (") {
			t.Errorf("alt+shift+b survives without a language server and must not be marked off: %q", line)
		}
	}

	// A full, LSP-enabled launch marks nothing.
	if full, _ := newHome(t); strings.Contains(full.helpText(), "off (") {
		t.Error("an unrestricted launch should mark no keys off")
	}
}

// TestSingleFileVaultPromotionLiftsRestrictions: Actions ▸ Vaults is the one route out of
// minimal mode, and everything single_file_mode decided has to be re-asked on the way
// through — the panel lock belonged to the minimal mode being left behind, and the manager
// the launch was denied is one project_mode may well allow.
func TestSingleFileVaultPromotionLiftsRestrictions(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", os.Getenv("HOME"))
	vault := filepath.Join(home, "Notes")
	if err := os.Mkdir(vault, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := DefaultConfig()
	cfg.SingleFile.AllowLSP = false
	c := New("test", cfg, Options{Mode: ModeFile, File: soloFile(t)})
	defer c.close()
	if c.lsp != nil {
		t.Fatal("the launch should start without a manager")
	}
	if err := c.AddVault("notes", vault); err != nil {
		t.Fatal(err)
	}
	if err := c.SwitchVault("notes"); err != nil {
		t.Fatal(err)
	}
	if c.lsp == nil {
		t.Error("promoting into a vault should create the manager project_mode allows")
	}

	// And with project_mode denying it too, the promotion creates nothing.
	both := cfg
	both.Project.AllowLSP = false
	c2 := New("test", both, Options{Mode: ModeFile, File: soloFile(t)})
	defer c2.close()
	if err := c2.AddVault("notes2", filepath.Join(home, "Other")); err != nil {
		t.Fatal(err)
	}
	if err := c2.SwitchVault("notes2"); err != nil {
		t.Fatal(err)
	}
	if c2.lsp != nil {
		t.Error("project_mode.default_allow_lsp: false should survive the promotion")
	}
}

// TestNoActionsPicker: the Actions picker is retired (the menu bar holds its rows), so
// neither of its keys opens anything — and in the minimal launch, where the editor has the
// keys, bare "a" is still text and ctrl+alt+a is not typed.
func TestNoActionsPicker(t *testing.T) {
	s, sh := newHomeCfg(t, DefaultConfig(), Options{Mode: ModeFile, File: soloFile(t)})
	before := s.editor.Text()
	if _, act := s.Update(sh, keyMsg("a")); act.Msg != nil {
		t.Errorf("bare a must stay text while the editor is capturing, got %T", act.Msg)
	}
	if s.editor.Text() == before {
		t.Error("bare a should have been typed into the buffer")
	}
	if _, act := s.Update(sh, keyMsg("ctrl+alt+a")); act.Msg != nil {
		t.Fatalf("ctrl+alt+a should open nothing now, got %T", act.Msg)
	}
	if typed := s.editor.Text(); strings.Contains(typed, "aa") {
		t.Error("ctrl+alt+a must not reach the buffer as text")
	}

	home, hsh := newHome(t)
	if _, act := home.Update(hsh, keyMsg("a")); act.Msg != nil {
		t.Errorf("a should not open a picker from the home screen, got %T", act.Msg)
	}
	if text := home.helpText(); strings.Contains(text, "ctrl+alt+a") || strings.Contains(text, "Actions") {
		t.Errorf("the ? overlay still mentions the Actions picker:\n%s", text)
	}
}
