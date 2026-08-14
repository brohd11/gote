package cmd

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/brohd11/gote/internal/app"
)

// TestResolveOptions covers gote's whole argument grammar: which of the four modes
// each invocation lands in, where the scan roots, and which typos are refused rather
// than silently reinterpreted. The mode dispatch is the part worth pinning — an
// argument becomes a vault by NOT being on disk and a file by NOT being a directory,
// so a rule that stops firing turns a scan into an editor session on a directory path,
// or a vault into an empty buffer.
func TestResolveOptions(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "note.md")
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(dir, "not-yet.md")

	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}

	// The bare name a vault is reached by, and a same-named file next to the test
	// binary that must go on winning over it.
	const vaultName = "main-vault"
	shadowed := filepath.Join(cwd, "shadow-vault")
	if err := os.WriteFile(shadowed, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Remove(shadowed) })

	// vaults maps a configured name to its resolved path; the empty string stands for
	// a vault that IS configured but whose path has gone bad (a launch error, not a
	// fall-through to a file of that name).
	vaults := map[string]string{
		vaultName:      dir,
		"shadow-vault": dir,
		"broken-vault": "",
	}
	lookupVault := func(name string) (string, bool, error) {
		path, configured := vaults[name]
		if !configured {
			return "", false, nil
		}
		if path == "" {
			return "", true, os.ErrNotExist
		}
		return path, true, nil
	}

	tests := []struct {
		name     string
		args     []string
		scan     bool
		depth    int
		depthSet bool
		exts     []string
		extsSet  bool

		wantMode  app.Mode
		wantDir   string
		wantFile  string
		wantVault string
		wantDepth int
		wantSet   bool
		wantExts  []string
		wantErr   bool
	}{
		{name: "bare", wantMode: app.ModeHome},
		{name: "scan flag alone", scan: true, wantMode: app.ModeScan, wantDir: cwd},
		{name: "here", args: []string{"here"}, wantMode: app.ModeScan, wantDir: cwd},
		{
			name: "here with depth", args: []string{"here", "3"},
			wantMode: app.ModeScan, wantDir: cwd, wantDepth: 3, wantSet: true,
		},
		{
			// 0 is a real depth (this directory only), which is why DepthSet exists.
			name: "here depth zero", args: []string{"here", "0"},
			wantMode: app.ModeScan, wantDir: cwd, wantDepth: 0, wantSet: true,
		},
		{name: "directory", args: []string{dir}, wantMode: app.ModeScan, wantDir: dir},
		{
			name: "directory with depth", args: []string{dir, "2"},
			wantMode: app.ModeScan, wantDir: dir, wantDepth: 2, wantSet: true,
		},
		{
			// The positional depth wins over -d: it is the more specific of the two.
			name: "positional depth beats flag", args: []string{"here", "4"}, depth: 9, depthSet: true,
			wantMode: app.ModeScan, wantDir: cwd, wantDepth: 4, wantSet: true,
		},
		{
			name: "depth flag alone", args: []string{dir}, depth: 7, depthSet: true,
			wantMode: app.ModeScan, wantDir: dir, wantDepth: 7, wantSet: true,
		},
		{name: "file", args: []string{file}, wantMode: app.ModeFile, wantFile: file},
		{
			// nano's contract: a path that does not exist is a new document.
			name: "missing path is a new file", args: []string{missing},
			wantMode: app.ModeFile, wantFile: missing,
		},
		{
			// --scan is how a directory that does not exist yet can still be named.
			name: "scan flag forces a directory", args: []string{missing}, scan: true,
			wantMode: app.ModeScan, wantDir: missing,
		},
		{name: "depth on a file", args: []string{file, "2"}, wantErr: true},
		{name: "non-numeric depth", args: []string{"here", "x"}, wantErr: true},
		{name: "negative depth", args: []string{"here", "-1"}, wantErr: true},
		{name: "negative depth flag", args: []string{"here"}, depth: -1, depthSet: true, wantErr: true},
		{
			// --ext rides along with every mode; the app normalizes it, so the grammar
			// passes it through untouched.
			name: "ext with a scan", args: []string{"here"}, exts: []string{"md"}, extsSet: true,
			wantMode: app.ModeScan, wantDir: cwd, wantExts: []string{"md"},
		},
		{
			name: "ext on a bare launch", exts: []string{"md", "txt"}, extsSet: true,
			wantMode: app.ModeHome, wantExts: []string{"md", "txt"},
		},
		{
			// `gote --ext=` — an empty set is set, and means "any text file", which is
			// how a config that restricts gets widened for one run.
			name: "empty ext is still set", args: []string{"here"}, exts: []string{""}, extsSet: true,
			wantMode: app.ModeScan, wantDir: cwd, wantExts: []string{""},
		},
		{
			// The new rung: nothing on disk by that name, but the config knows it.
			name: "vault name", args: []string{vaultName},
			wantMode: app.ModeVault, wantVault: vaultName, wantDir: dir,
		},
		{
			// A vault is a recursive root like any other, so the depth applies.
			name: "vault with depth", args: []string{vaultName, "3"},
			wantMode: app.ModeVault, wantVault: vaultName, wantDir: dir, wantDepth: 3, wantSet: true,
		},
		{
			// The filesystem wins: a real file of that name still opens as a file.
			name: "file shadows a vault name", args: []string{"shadow-vault"},
			wantMode: app.ModeFile, wantFile: shadowed,
		},
		{
			// ...and ./name is the escape hatch, exactly as it is for "here".
			name: "dot-slash never names a vault", args: []string{"./" + vaultName},
			wantMode: app.ModeFile, wantFile: filepath.Join(cwd, vaultName),
		},
		{
			// Configured but broken: a launch error, not a silent empty buffer.
			name: "broken vault", args: []string{"broken-vault"}, wantErr: true,
		},
		{
			// An unconfigured name keeps nano's contract.
			name: "unknown name is still a new file", args: []string{"no-such-vault"},
			wantMode: app.ModeFile, wantFile: filepath.Join(cwd, "no-such-vault"),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			opts, err := resolveOptions(tc.args, flags{
				scan: tc.scan, depth: tc.depth, depthSet: tc.depthSet,
				exts: tc.exts, extsSet: tc.extsSet,
			}, lookupVault)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("resolveOptions(%v) should have failed, got %+v", tc.args, opts)
				}
				return
			}
			if err != nil {
				t.Fatalf("resolveOptions(%v): %v", tc.args, err)
			}
			if opts.Mode != tc.wantMode {
				t.Errorf("mode = %v, want %v", opts.Mode, tc.wantMode)
			}
			if opts.Dir != tc.wantDir {
				t.Errorf("dir = %q, want %q", opts.Dir, tc.wantDir)
			}
			if opts.File != tc.wantFile {
				t.Errorf("file = %q, want %q", opts.File, tc.wantFile)
			}
			if opts.Vault != tc.wantVault {
				t.Errorf("vault = %q, want %q", opts.Vault, tc.wantVault)
			}
			if opts.Depth != tc.wantDepth || opts.DepthSet != tc.wantSet {
				t.Errorf("depth = %d (set %v), want %d (set %v)",
					opts.Depth, opts.DepthSet, tc.wantDepth, tc.wantSet)
			}
			if !reflect.DeepEqual(opts.Exts, tc.wantExts) || opts.ExtsSet != tc.extsSet {
				t.Errorf("exts = %v (set %v), want %v (set %v)",
					opts.Exts, opts.ExtsSet, tc.wantExts, tc.extsSet)
			}
		})
	}
}

// TestResolveOptionsAbs: paths are absolute by the time they reach the app, so the
// breadcrumb and the editor's save target don't depend on the cwd afterwards.
func TestResolveOptionsAbs(t *testing.T) {
	opts, err := resolveOptions([]string{"root_test.go"}, flags{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if opts.Mode != app.ModeFile || !filepath.IsAbs(opts.File) {
		t.Fatalf("a relative file should resolve to an absolute path, got %+v", opts)
	}
}
