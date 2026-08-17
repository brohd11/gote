package app

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// TestLoadConfigDefaults: a missing config.yml means the defaults, no error.
func TestLoadConfigDefaults(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cfg, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(cfg, DefaultConfig()) {
		t.Fatalf("cfg = %+v, want %+v", cfg, DefaultConfig())
	}
}

// TestLoadConfigFile: set keys load; unset keys keep their defaults.
func TestLoadConfigFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(home, ".gote")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.yml"), []byte("extensions: [MD, .txt]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(cfg.Extensions, []string{"md", "txt"}) {
		t.Fatalf("extensions = %v, want them lowercased and undotted", cfg.Extensions)
	}
	if want := DefaultConfig().ScanDepth; cfg.ScanDepth != want {
		t.Fatalf("scan depth = %d, want the default %d", cfg.ScanDepth, want)
	}
}

// TestLoadConfigNoExtensions: the unconfigured default is no filter at all — every
// text file is a document.
func TestLoadConfigNoExtensions(t *testing.T) {
	cfg := writeConfig(t, "scan_depth: 2\n")
	if len(cfg.Extensions) != 0 || len(cfg.Filter().Exts) != 0 {
		t.Fatalf("extensions = %v, want none — the default filter takes any text file", cfg.Extensions)
	}
}

// TestLoadConfigDroppedExtensionKey: the extension scalar this schema used to carry is
// gone. A config still holding it must load clean and simply not act on it — an
// unknown key that errored would lock a user out of their vaults over a dead setting.
func TestLoadConfigDroppedExtensionKey(t *testing.T) {
	cfg := writeConfig(t, "extension: txt\nscan_depth: 3\n")
	if len(cfg.Extensions) != 0 {
		t.Fatalf("extensions = %v, want the dead key to have no effect", cfg.Extensions)
	}
	if cfg.ScanDepth != 3 {
		t.Fatalf("scan depth = %d, want the rest of the file to still load", cfg.ScanDepth)
	}
}

// writeConfig puts raw at ~/.gote/config.yml under a temp HOME and loads it.
func writeConfig(t *testing.T, raw string) Config {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(home, ".gote")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.yml"), []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

// TestEnsureConfig: a missing config is materialized with the defaults, so `gote config`
// always opens a real schema; an existing one is returned untouched.
func TestEnsureConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	want := filepath.Join(home, ".gote", "config.yml")

	path, err := EnsureConfig()
	if err != nil {
		t.Fatal(err)
	}
	if path != want {
		t.Fatalf("path = %q, want %q", path, want)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("EnsureConfig should have written the file: %v", err)
	}
	// The whole point of materializing it: the file is where the schema is documented, so
	// every key has to be in it — an omitted one is a setting the user cannot discover.
	for _, key := range []string{"extensions:", "scan_depth:", "default:", "vaults:"} {
		if !strings.Contains(string(raw), key) {
			t.Fatalf("a materialized config should show every key, %q is missing:\n%s", key, raw)
		}
	}

	if err := os.WriteFile(path, []byte("scan_depth: 9\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := EnsureConfig(); err != nil {
		t.Fatal(err)
	}
	raw, err = os.ReadFile(path)
	if err != nil || string(raw) != "scan_depth: 9\n" {
		t.Fatalf("an existing config must not be rewritten, got %q (%v)", raw, err)
	}
}

// TestDefaultConfigRoundTrip: the materialized file is exactly marshal(DefaultConfig()),
// so writing it and reading it back has to land on DefaultConfig() again. The trap is
// Extensions: normalizeExts collapses an empty list to nil, so seeding it as an empty
// slice would make a fresh config differ from the defaults it was written from.
func TestDefaultConfigRoundTrip(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if _, err := EnsureConfig(); err != nil {
		t.Fatal(err)
	}
	got, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, DefaultConfig()) {
		t.Fatalf("round trip = %#v, want %#v", got, DefaultConfig())
	}
}

// TestDirLayout: the doc store sits one level below the config home. That gap is the
// whole point — discovery reads DocsDir and so cannot reach config.yml.
func TestDirLayout(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	dir, err := Dir()
	if err != nil {
		t.Fatal(err)
	}
	docs, err := DocsDir()
	if err != nil {
		t.Fatal(err)
	}
	cfgPath, err := ConfigPath()
	if err != nil {
		t.Fatal(err)
	}
	if docs != filepath.Join(dir, "docs") || cfgPath != filepath.Join(dir, "config.yml") {
		t.Fatalf("layout = docs %q, config %q, under %q", docs, cfgPath, dir)
	}

	// The seed creates the store, and config.yml — a text file the default filter would
	// otherwise happily take — is not in it.
	if err := SaveConfig(DefaultConfig()); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "docs.md"), []byte("not a doc either"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := docNames(HomeDocs(docs, DocFilter{})); len(got) != 0 {
		t.Fatalf("the store = %v, want it empty — nothing in the config home is a document", got)
	}
	if st, err := os.Stat(docs); err != nil || !st.IsDir() {
		t.Fatal("seeding should create the doc store")
	}
}

// TestLoadConfigMalformed: a malformed file reports an error and falls back to the
// defaults.
func TestLoadConfigMalformed(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(home, ".gote")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.yml"), []byte("extension: [unclosed"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig()
	if err == nil {
		t.Fatal("a malformed config should report an error")
	}
	if !reflect.DeepEqual(cfg, DefaultConfig()) {
		t.Fatalf("cfg = %+v, want the defaults on a malformed file", cfg)
	}
}

func TestConfigVaultRoundTrip(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	vault := filepath.Join(home, "Notes")
	if err := os.Mkdir(vault, 0o755); err != nil {
		t.Fatal(err)
	}
	want := DefaultConfig()
	want.Default = "notes"
	want.Vaults["notes"] = VaultConfig{Path: vault, Open: []string{}}
	if err := SaveConfig(want); err != nil {
		t.Fatal(err)
	}

	raw, err := os.ReadFile(filepath.Join(home, ".gote", "config.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "open: []") {
		t.Fatalf("new vault schema must preserve the reserved empty open list:\n%s", raw)
	}
	got, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("round trip = %#v, want %#v", got, want)
	}
}

func TestNormalizeDirPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	notes := filepath.Join(home, "Notes")
	if err := os.Mkdir(notes, 0o755); err != nil {
		t.Fatal(err)
	}
	if got, err := normalizeDirPath("~/Notes"); err != nil || got != notes {
		t.Fatalf("normalize ~/Notes = %q, %v; want %q", got, err, notes)
	}
	file := filepath.Join(home, "note.md")
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := normalizeDirPath(file); err == nil {
		t.Fatal("a vault path naming a file should fail")
	}
	// The reader's must-exist check is what keeps a vanished vault reportable, so it has
	// to survive the split that lets New Vault create its own directory.
	if _, err := normalizeDirPath("~/Gone"); err == nil {
		t.Fatal("a vault path naming a missing directory should fail")
	}
}

// TestEnsureVaultDir covers the New Vault side of the same path: make what is missing,
// take what is there, and refuse what is not a directory at all.
func TestEnsureVaultDir(t *testing.T) {
	home := t.TempDir()

	nested := filepath.Join(home, "a", "b", "c")
	if err := ensureVaultDir(nested); err != nil {
		t.Fatalf("ensure missing nested dir: %v", err)
	}
	info, err := os.Stat(nested)
	if err != nil || !info.IsDir() {
		t.Fatalf("nested vault dir = %v, %v; want a directory", info, err)
	}

	existing := filepath.Join(home, "Notes")
	doc := filepath.Join(existing, "note.md")
	if err := os.Mkdir(existing, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(doc, []byte("kept"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := ensureVaultDir(existing); err != nil {
		t.Fatalf("adopt existing dir: %v", err)
	}
	if got, err := os.ReadFile(doc); err != nil || string(got) != "kept" {
		t.Fatalf("adopting a folder disturbed its contents: %q, %v", got, err)
	}

	file := filepath.Join(home, "note.md")
	if err := os.WriteFile(file, []byte("kept"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := ensureVaultDir(file); err == nil {
		t.Fatal("a vault path naming a file should fail")
	}
	if got, err := os.ReadFile(file); err != nil || string(got) != "kept" {
		t.Fatalf("rejected file = %q, %v; want it untouched", got, err)
	}
}
