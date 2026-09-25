package app

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/brohd11/bubblestack/components/editor"
	"github.com/brohd11/bubblestack/core"
)

func TestDisplayDefaultsConfig(t *testing.T) {
	for _, wrap := range []bool{false, true} {
		for _, nums := range []bool{false, true} {
			cfg := writeConfig(t, fmt.Sprintf("project_mode:\n  default_wrap: %t\n  default_line_numbers: %t\nsingle_file_mode:\n  default_wrap: %t\n  default_line_numbers: %t\n", wrap, nums, !wrap, !nums))
			if cfg.Project.Wrap != wrap || cfg.Project.LineNumbers != nums || cfg.SingleFile.Wrap != !wrap || cfg.SingleFile.LineNumbers != !nums {
				t.Fatalf("display defaults were not loaded: %+v / %+v", cfg.Project, cfg.SingleFile)
			}
			if !cfg.Project.GitGutter || !cfg.SingleFile.AllowLSP {
				t.Fatal("partial sections lost other defaults")
			}
			if err := SaveConfig(cfg); err != nil {
				t.Fatal(err)
			}
			loaded, err := LoadConfig()
			if err != nil {
				t.Fatal(err)
			}
			if loaded.Project != cfg.Project || loaded.SingleFile != cfg.SingleFile {
				t.Fatal("round trip changed defaults")
			}
		}
	}
	cfg := writeConfig(t, "project_mode:\n  default_git_gutter: false\n")
	for _, mode := range []Mode{ModeHome, ModeScan, ModeVault, ModeFile} {
		if d := cfg.modeDefaults(mode); d.Wrap || d.LineNumbers {
			t.Fatalf("omitted defaults should be off for %v", mode)
		}
	}
	if _, err := SyncConfig(); err != nil {
		t.Fatal(err)
	}
}

func TestEditorDisplayDefaultsLifecycle(t *testing.T) {
	for _, mode := range []Mode{ModeHome, ModeFile} {
		for _, wrap := range []bool{false, true} {
			for _, nums := range []bool{false, true} {
				t.Run(fmt.Sprintf("%v/wrap=%v/numbers=%v", mode, wrap, nums), func(t *testing.T) {
					cfg := testConfig()
					cfg.Project.Wrap, cfg.Project.LineNumbers = wrap, nums
					cfg.SingleFile.Wrap, cfg.SingleFile.LineNumbers = !wrap, !nums
					wantWrap, wantNums := wrap, nums
					if mode == ModeFile {
						wantWrap, wantNums = !wrap, !nums
					}
					path := filepath.Join(t.TempDir(), "first.txt")
					s, sh := newHomeCfg(t, cfg, Options{Mode: mode, File: path})
					check := func(ed *editor.Screen, w, n bool) {
						t.Helper()
						if ed.WrapMode() != w || ed.LineNumMode() != n {
							t.Fatalf("got wrap=%v numbers=%v, want %v/%v", ed.WrapMode(), ed.LineNumMode(), w, n)
						}
					}
					check(s.editor, wantWrap, wantNums) // scratch or single-file startup
					if mode == ModeHome {
						s.openDoc(sh, path)
					}
					s.editor.SetText(strings.Repeat("x", 300))
					check(s.editor, wantWrap, wantNums)
					first := s.editor
					s.Update(sh, keyMsg("alt+z"))
					s.Update(sh, keyMsg("alt+l"))
					check(first, !wantWrap, !wantNums)
					s.Update(sh, keyMsg("ctrl+l"))
					check(first, !wantWrap, !wantNums)
					if s.View(sh) == "" {
						t.Fatal("empty editor view after toggles")
					}
					if mode == ModeFile {
						return
					}
					s.openDoc(sh, filepath.Join(t.TempDir(), "second.txt"))
					check(s.editor, wantWrap, wantNums)
					s.openDoc(sh, path)
					if s.editor != first {
						t.Fatal("switching docs replaced the existing editor")
					}
					check(s.editor, !wantWrap, !wantNums)
					s.newUnsavedBuffer(sh)
					check(s.editor, wantWrap, wantNums)
					if Of(sh).Config.Project.Wrap != wrap || Of(sh).Config.Project.LineNumbers != nums {
						t.Fatal("runtime toggles changed config")
					}
				})
			}
		}
	}
}

func TestRestoredEditorsUseDisplayDefaults(t *testing.T) {
	root := t.TempDir()
	first := writeDoc(t, root, "first.txt", 20)
	second := writeDoc(t, root, "second.txt", 20)
	cfg := testConfig()
	cfg.Project.Wrap, cfg.Project.LineNumbers = true, true
	opts := Options{Mode: ModeScan, Dir: root}
	s, sh := newHomeCfg(t, cfg, opts)
	s.openDoc(sh, first)
	s.editor.ToggleWrap()
	s.editor.ToggleLineNums()
	s.openDoc(sh, second)
	if err := Of(sh).saveSession(); err != nil {
		t.Fatal(err)
	}
	// A fresh launch must apply config even to inactive, lazily restored documents.
	restoredShared := core.NewShared(New("test", cfg, opts))
	restored := NewHomeScreen(restoredShared).(*homeScreen)
	restored.Init(restoredShared)
	for _, path := range []string{first, second} {
		ed, ok := Of(restoredShared).Doc(path)
		if !ok || !ed.WrapMode() || !ed.LineNumMode() {
			t.Fatalf("restored %q did not use configured defaults", path)
		}
	}
}
