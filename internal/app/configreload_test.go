package app

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/brohd11/bubblestack/components"
)

// writeConfigFile writes ~/.gote/<name> under the test's HOME.
func writeConfigFile(t *testing.T, name, body string) string {
	t.Helper()
	dir, err := Dir()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestReloadConfig: a reload applies the file's filter and depth, but not over the launch
// flags that beat them.
func TestReloadConfig(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", os.Getenv("HOME"))
	c := New("test", DefaultConfig(), Options{})
	writeConfigFile(t, "config.yml", "extensions: [txt]\nscan_depth: 2\nformat_on_save: true\n")
	if _, err := c.ReloadConfig(); err != nil {
		t.Fatal(err)
	}
	want := NewDocFilter([]string{"txt"})
	if !reflect.DeepEqual(c.Filter, want) || c.Depth != 2 || !c.Config.FormatOnSave || c.NewExt != defaultExt(want.Exts) {
		t.Fatalf("reload: filter %+v depth %d format %v newExt %q", c.Filter, c.Depth, c.Config.FormatOnSave, c.NewExt)
	}

	flagged := New("test", DefaultConfig(), Options{Depth: 7, DepthSet: true, Exts: []string{"md"}, ExtsSet: true})
	if _, err := flagged.ReloadConfig(); err != nil {
		t.Fatal(err)
	}
	if flagged.Depth != 7 || !reflect.DeepEqual(flagged.Filter, NewDocFilter([]string{"md"})) {
		t.Fatalf("--depth/--ext must survive a reload: depth %d filter %+v", flagged.Depth, flagged.Filter)
	}
	if flagged.Config.ScanDepth != 2 {
		t.Fatalf("the config itself still reloads: scan_depth %d", flagged.Config.ScanDepth)
	}
}

// TestReloadConfigMalformed: a file that fails to parse changes nothing, rather than
// falling back to the defaults LoadConfig would return.
func TestReloadConfigMalformed(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", os.Getenv("HOME"))
	cfg := DefaultConfig()
	cfg.FormatOnSave = true
	c := New("test", cfg, Options{})
	for _, name := range []string{"config.yml", "lang.yml"} {
		path := writeConfigFile(t, name, "extensions: [txt\n")
		if _, err := c.ReloadConfig(); err == nil {
			t.Fatalf("a malformed %s must fail the reload", name)
		}
		if !c.Config.FormatOnSave {
			t.Fatalf("a malformed %s reset the running config", name)
		}
		_ = os.Remove(path)
	}
}

// TestReloadConfigRestartNotes: what a running session cannot pick up is reported.
func TestReloadConfigRestartNotes(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", os.Getenv("HOME"))
	cfg := DefaultConfig()
	cfg.AutoLSP = true
	c := New("test", cfg, Options{})
	defer c.close()
	if c.lsp == nil {
		t.Fatal("auto-lsp should give the launch a manager")
	}
	writeConfigFile(t, "config.yml", "auto-lsp: false\n")
	restart, err := c.ReloadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(restart, []string{"language servers"}) {
		t.Fatalf("restart = %v", restart)
	}
	if again, _ := c.ReloadConfig(); len(again) != 0 {
		t.Fatalf("an unchanged reload needs no restart: %v", again)
	}
}

// TestFileConfigMenu: File ▸ Config opens config.yml and lang.yml as docs, creating them
// first, and saving one reloads the config.
func TestFileConfigMenu(t *testing.T) {
	_, s, sh := newHomeRouter(t, Options{})
	c := Of(sh)
	defer c.close()
	var items []components.MenuItem
	for _, it := range s.fileMenuItems() {
		if it.Label == "Config" {
			items = it.Submenu()
		}
	}
	if got := menuLabels(items); !reflect.DeepEqual(got, []string{"General", "Language"}) {
		t.Fatalf("File ▸ Config = %v", got)
	}
	items[1].Pick(sh)
	langPath, _ := LangPath()
	if s.currentPath != langPath {
		t.Fatalf("Language opened %q, want %q", s.currentPath, langPath)
	}
	items[0].Pick(sh)
	configPath, _ := ConfigPath()
	if s.currentPath != configPath {
		t.Fatalf("General opened %q, want %q", s.currentPath, configPath)
	}
	if _, err := os.Stat(configPath); err != nil {
		t.Fatalf("General should create config.yml: %v", err)
	}

	writeConfigFile(t, "config.yml", "format_on_save: true\n")
	s.editorSaved(sh, configPath)
	if !c.Config.FormatOnSave {
		t.Fatal("saving config.yml should reload the config")
	}
}

// TestFileConfigMenuMinimal: the single-file launch's File menu has no Config row.
func TestFileConfigMenuMinimal(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "note.md")
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	_, s, sh := newHomeRouter(t, Options{Mode: ModeFile, File: file})
	defer Of(sh).close()
	for _, it := range s.fileMenuItems() {
		if it.Label == "Config" {
			t.Fatal("minimal mode should not list File ▸ Config")
		}
	}
}
