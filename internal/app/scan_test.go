package app

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
)

func TestScanDocsFileLimit(t *testing.T) {
	root := writeTree(t)
	for _, tc := range []struct {
		limit   int
		names   []string
		partial bool
	}{
		{2, []string{"F.MD", "a.md"}, true},
		{4, []string{"F.MD", "a.md", "c.md", "d.md"}, true},
		{5, []string{"F.MD", "a.md", "c.md", "d.md"}, false},
	} {
		t.Run(fmt.Sprint(tc.limit), func(t *testing.T) {
			result := ScanDocs(root, 5, mdOnly, ScanLimits{MaxFiles: tc.limit})
			if !reflect.DeepEqual(docNames(result.Files), tc.names) || result.Partial != tc.partial {
				t.Fatalf("scan = %v, partial %v; want %v, partial %v", docNames(result.Files), result.Partial, tc.names, tc.partial)
			}
		})
	}

	// b.txt is rejected before the nested documents and must not consume the cap.
	result := ScanDocs(root, 5, mdOnly, ScanLimits{MaxFiles: 3})
	if !equalNames(docNames(result.Files), "F.MD", "a.md", "c.md") || !result.Partial {
		t.Fatalf("filtered scan = %+v", result)
	}
	if home := HomeDocs(root, 5, mdOnly, ScanLimits{MaxFiles: 3}); !reflect.DeepEqual(home, result) {
		t.Fatalf("home scan = %+v, want %+v", home, result)
	}
}

func TestScanDocsTimeout(t *testing.T) {
	for _, kind := range []string{"accepted file", "rejected file", "empty directories"} {
		t.Run(kind, func(t *testing.T) {
			root := t.TempDir()
			var want []string
			switch kind {
			case "accepted file":
				write(t, root, "a.md", []byte("text"))
				want = []string{"a.md"}
			case "rejected file":
				write(t, root, "a.bin", []byte{0})
			case "empty directories":
				if err := os.MkdirAll(filepath.Join(root, "a", "b"), 0o755); err != nil {
					t.Fatal(err)
				}
			}
			write(t, root, "z.md", []byte("never reached"))
			clock := time.Unix(0, 0)
			now := func() time.Time {
				at := clock
				clock = clock.Add(time.Second)
				return at
			}
			result := scanDocs(root, 5, anyText, ScanLimits{Timeout: 3 * time.Second}, now)
			if !result.Partial || !equalNames(docNames(result.Files), want...) {
				t.Fatalf("timed scan = %v, partial %v; want %v, partial true", docNames(result.Files), result.Partial, want)
			}
		})
	}
}

func TestSeedReplacesPartialState(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", os.Getenv("HOME"))
	root := writeTree(t)
	store, err := DocsDir()
	if err != nil {
		t.Fatal(err)
	}
	write(t, store, "a.md", []byte("a"))
	write(t, store, "b.md", []byte("b"))
	for _, mode := range []Mode{ModeHome, ModeScan, ModeVault} {
		t.Run(fmt.Sprint(mode), func(t *testing.T) {
			cfg := DefaultConfig()
			cfg.ScanMaxFiles = 1
			c := New("test", cfg, Options{Mode: mode, Dir: root, Vault: "notes"})
			if !c.ScanPartial || len(c.Files) != 1 {
				t.Fatalf("initial seed = %v, partial %v", docNames(c.Files), c.ScanPartial)
			}
			c.Config.ScanMaxFiles = 10
			c.Seed()
			if c.ScanPartial || len(c.Files) <= 1 {
				t.Fatalf("complete reseed = %v, partial %v", docNames(c.Files), c.ScanPartial)
			}
			c.Config.ScanMaxFiles = 1
			c.Seed()
			c.Mode = ModeFile
			c.Seed()
			if c.ScanPartial || len(c.Files) != 0 {
				t.Fatal("single-file mode retained scan state")
			}
		})
	}
}

func TestPartialScanHeaderAndNavigation(t *testing.T) {
	root := scanTree(t)
	cfg := DefaultConfig()
	cfg.ScanMaxFiles = 1
	s, sh := newHomeCfg(t, cfg, Options{Mode: ModeScan, Dir: root})
	c := Of(sh)
	const status = "(Partial Scan: 1 Files)"
	header := func() string { return strings.Split(ansi.Strip(s.header.View(false)), "\n")[0] }
	if !strings.Contains(header(), status) {
		t.Fatalf("partial header = %q", header())
	}
	filterList(t, s.docsPanel.List(), "no matches")
	if !strings.Contains(header(), status) {
		t.Fatal("filtering changed the scan count")
	}
	// Preserve the label even when the root path has to be shortened.
	width := menusWidth(s.header.menuSpans) + 1 + ansi.StringWidth(status) + 5
	s.header.SetSize(width, headerRows)
	if !strings.Contains(header(), status) {
		t.Fatalf("narrow header = %q", header())
	}
	for width := 0; width < 100; width++ {
		s.header.SetSize(width, headerRows)
		if got := ansi.StringWidth(header()); got > width {
			t.Fatalf("header width %d exceeds %d", got, width)
		}
	}

	// Folder browsing still reaches a file omitted from the flat and grouped lists.
	if len(c.Files) != 1 || c.Files[0].Name != "notes.md" || hasRow(rowTitles(s.groupedPanel.List()), "deep.md") {
		t.Fatal("the bounded seed should omit the nested document")
	}
	s.setFileView(fileViewFolder)
	s.filePanel.SetDir(sh, filepath.Join(root, "sub"))
	selectRow(t, s.filePanel.List(), "deep.md")
	s.filePanel.UpdatePanel(sh, keyMsg("enter"))
	if s.currentPath != filepath.Join(root, "sub", "deep.md") {
		t.Fatal("folder view could not open the omitted document")
	}

	c.Config.ScanMaxFiles = 10
	s.reseed(sh)
	if c.ScanPartial || strings.Contains(header(), "Partial Scan") {
		t.Fatal("a complete refresh retained the partial label")
	}
	// A vault switch replaces the old root's result and label in both directions.
	c.Config.ScanMaxFiles = 1
	c.Config.Vaults["empty"] = VaultConfig{Path: t.TempDir()}
	c.Config.Vaults["large"] = VaultConfig{Path: root}
	s.activateVault(sh, "large")
	if !c.ScanPartial || !strings.Contains(header(), status) {
		t.Fatal("vault switch did not apply the scan cap")
	}
	s.activateVault(sh, "empty")
	if c.ScanPartial || len(c.Files) != 0 || strings.Contains(header(), "Partial Scan") {
		t.Fatal("vault switch retained the previous scan state")
	}
}
