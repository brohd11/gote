package app

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/brohd11/bubblestack/components/editor"
)

// writeLangConfig loads a home whose lang.yml is raw (and whose config.yml is absent).
func writeLangConfig(t *testing.T, raw string) Config {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", os.Getenv("HOME"))
	dir := filepath.Join(home, ".gote")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "lang.yml"), []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func TestLanguageServerConfig(t *testing.T) {
	cfg := writeLangConfig(t, `servers:
  gdscript:
    enabled: false
  go:
    enabled: true
  python:
    command: [pylsp, --verbose]
`)
	if gd := cfg.Lang.Servers["gdscript"]; gd.Enabled == nil || *gd.Enabled || gd.Address != "127.0.0.1:6005" {
		t.Fatalf("enabled alone should keep the built-in transport: %#v", gd)
	}
	if got := cfg.Lang.Servers["go"]; !reflect.DeepEqual(got.Command, []string{"gopls"}) || got.InitializationOptions == nil {
		t.Fatalf("enabled alone should keep the built-in command and options: %#v", got)
	}
	if got := cfg.Lang.Servers["python"].Command; !reflect.DeepEqual(got, []string{"pylsp", "--verbose"}) {
		t.Fatalf("python command = %v", got)
	}
	if got := cfg.Lang.Servers["python"].InitializationOptions; got == nil {
		t.Fatal("custom Python transport should inherit built-in initialization options")
	}

	cfg = writeLangConfig(t, "servers: {}\n")
	if !reflect.DeepEqual(cfg.Lang.Servers, defaultLanguageServers()) {
		t.Fatalf("missing entries should inherit built-ins: %#v", cfg.Lang.Servers)
	}

	cfg = writeLangConfig(t, `servers:
  python:
    command: [pylsp]
    initialization_options: {}
`)
	if opts := cfg.Lang.Servers["python"].InitializationOptions; opts == nil || len(opts) != 0 {
		t.Fatalf("explicit empty initialization options should disable built-ins: %#v", cfg.Lang.Servers["python"])
	}
}

// TestLangConfigTemplate: the generated lang.yml spells out the built-in servers, loads as
// the defaults, and never overwrites an existing file.
func TestLangConfigTemplate(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", os.Getenv("HOME"))
	if err := EnsureLangConfig(); err != nil {
		t.Fatal(err)
	}
	path, _ := LangPath()
	raw, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(raw), "languages:") || !strings.Contains(string(raw), "gopls") {
		t.Fatalf("template not written: %q (%v)", raw, err)
	}
	cfg, err := LoadConfig()
	if err != nil || !reflect.DeepEqual(cfg, DefaultConfig()) {
		t.Fatalf("the template should load as the defaults: %+v (%v)", cfg.Lang, err)
	}

	if err := os.WriteFile(path, []byte("languages: {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := EnsureLangConfig(); err != nil {
		t.Fatal(err)
	}
	if raw, _ := os.ReadFile(path); string(raw) != "languages: {}\n" {
		t.Fatalf("an existing lang.yml must not be rewritten, got %q", raw)
	}
}

func TestLangConfigMalformed(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	dir := filepath.Join(home, ".gote")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "lang.yml"), []byte("languages: [\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig()
	if err == nil || !strings.Contains(err.Error(), "lang.yml") {
		t.Fatalf("a malformed lang.yml should be reported by name, got %v", err)
	}
	if !reflect.DeepEqual(cfg.Lang.Servers, defaultLanguageServers()) {
		t.Fatal("a malformed lang.yml should fall back to the built-in servers")
	}
}

func TestLangIndentSpaces(t *testing.T) {
	lang := LangConfig{Languages: map[string]LanguageSettings{
		"go":         {Indent: "spaces", IndentSize: 2},
		"python":     {Indent: "tab", IndentSize: 8},
		"javascript": {IndentSize: 4},
		"rust":       {Indent: "spaces"},
		"c":          {Indent: "spaces"},
		"lua":        {Indent: "bogus"},
	}}
	for _, tc := range []struct {
		id            string
		builtin, want int
	}{
		{"go", 0, 2},         // explicit spaces and size
		{"python", 4, 0},     // tab ignores the size
		{"javascript", 2, 4}, // a size alone means spaces
		{"rust", 4, 4},       // spaces with no size keeps a spaces built-in
		{"c", 0, defaultIndentSpaces},
		{"lua", 3, 3},  // an unknown value changes nothing
		{"yaml", 2, 2}, // unlisted
	} {
		if got := lang.indentSpaces(tc.id, tc.builtin); got != tc.want {
			t.Errorf("%s: indentSpaces(%d) = %d, want %d", tc.id, tc.builtin, got, tc.want)
		}
	}
}

// TestLangIndentReachesTheEditor: the override is what block indent uses, and it leaves
// the shared built-in profile alone.
func TestLangIndentReachesTheEditor(t *testing.T) {
	cfg := writeLangConfig(t, `languages:
  Go:
    indent: spaces
    indent_size: 2
`)
	indented := func(resolve editor.LanguageResolver) string {
		ed := editor.New(editor.Opts{Path: "main.go", ResolveLanguage: resolve})
		ed.SetText("x")
		pressEditor(ed, "alt+.")
		return ed.Text()
	}
	if got := indented(languageResolver(cfg.Lang)); got != "  x" {
		t.Fatalf("go with indent_size 2 indented to %q, want two spaces", got)
	}
	if got := indented(editorLanguageForPath); got != "\tx" {
		t.Fatalf("the built-in go profile was changed: indented to %q", got)
	}
}

func TestServerEnabledOverridesAutoLSP(t *testing.T) {
	on, off := true, false
	cfg := DefaultConfig()
	if cfg.anyLSP() || cfg.lspFor("go") {
		t.Fatal("the defaults should leave every server off")
	}
	setEnabled := func(id string, v *bool) {
		server := cfg.Lang.Servers[id]
		server.Enabled = v
		cfg.Lang.Servers[id] = server
	}
	setEnabled("go", &on)
	setEnabled("python", &off)
	if !cfg.anyLSP() || !cfg.lspFor("go") || cfg.lspFor("python") || cfg.lspFor("rust") {
		t.Fatal("enabled: true should run go alone under auto-lsp: false")
	}
	cfg.AutoLSP = true
	if !cfg.lspFor("rust") || cfg.lspFor("python") {
		t.Fatal("under auto-lsp: true, only python's enabled: false should hold it back")
	}
}

// TestLangLSPManager: one server's enabled: true is enough for a manager, and Reconcile
// takes only that server's documents.
func TestLangLSPManager(t *testing.T) {
	on := true
	cfg := DefaultConfig()
	// A refused address, so no real server is spawned.
	cfg.Lang.Servers["python"] = LanguageServerConfig{Enabled: &on, Address: "127.0.0.1:1"}
	s, sh := newHomeCfg(t, cfg, Options{})
	c := Of(sh)
	defer c.close()
	if c.lsp == nil {
		t.Fatal("enabled: true for python should create a manager under auto-lsp: false")
	}
	py, _ := seedDoc(t, s, sh, "main.py", "x = 1\n")
	rs, _ := seedDoc(t, s, sh, "main.rs", "fn main() {}\n")
	c.lsp.Reconcile(c)
	c.lsp.mu.Lock()
	_, hasPy := c.lsp.desired[filepath.Clean(py)]
	_, hasRs := c.lsp.desired[filepath.Clean(rs)]
	c.lsp.mu.Unlock()
	if !hasPy || hasRs {
		t.Fatalf("reconcile took python=%v rust=%v, want only python", hasPy, hasRs)
	}
}
