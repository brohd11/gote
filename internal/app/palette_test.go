package app

import (
	"bytes"
	"strings"
	"testing"

	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"
)

// Capability selection happens before any highlighter caches styles. Rich
// colors do not have to quantize to the basic palette: ANSI output uses it directly.
func TestSyntaxPaletteForColorProfile(t *testing.T) {
	t.Cleanup(func() { applySyntaxPalette(defaultSyntaxColors()) })
	sample := func() map[string]string {
		out := make(map[string]string)
		for _, slot := range paletteSlots() {
			out[slot.key] = slot.style.Render("x")
		}
		return out
	}
	for _, profile := range []colorprofile.Profile{colorprofile.ANSI, colorprofile.ANSI256, colorprofile.TrueColor, colorprofile.ASCII, colorprofile.NoTTY, colorprofile.Unknown} {
		for _, forced := range []bool{false, true} {
			mode := "/automatic"
			if forced {
				mode = "/forced-basic"
			}
			t.Run(profile.String()+mode, func(t *testing.T) {
				cfg := DefaultConfig()
				cfg.AutoLSP = false
				cfg.SyntaxColors.String = "#123456"
				cfg.SyntaxColors.BasicColors = forced
				configured := cfg.SyntaxColors
				expected := configured
				if profile == colorprofile.ANSI {
					expected.BasicColors = true
				}
				applySyntaxPalette(expected)
				want := sample()
				c := newWithColorProfile("test", cfg, Options{}, profile)
				defer c.close()
				if c.Config.SyntaxColors != configured {
					t.Fatal("runtime detection changed saved color settings")
				}
				for slot, got := range sample() {
					if got != want[slot] {
						t.Errorf("%s: palette does not match detected capability", slot)
					}
					if profile == colorprofile.ANSI {
						var buf bytes.Buffer
						w := colorprofile.Writer{Forward: &buf, Profile: profile}
						if _, err := w.Write([]byte(got)); err != nil {
							t.Fatal(err)
						}
						if buf.String() != want[slot] {
							t.Errorf("%s changed color during ANSI rendering", slot)
						}
					}
				}
			})
		}
	}
}

func TestPaletteReportUsesDetectedProfile(t *testing.T) {
	t.Cleanup(func() { applySyntaxPalette(defaultSyntaxColors()) })
	cfg := DefaultConfig()
	configured := cfg.SyntaxColors
	for _, profile := range []colorprofile.Profile{colorprofile.ANSI, colorprofile.TrueColor, colorprofile.NoTTY} {
		var buf bytes.Buffer
		out := &colorprofile.Writer{Forward: &buf, Profile: profile}
		if err := RenderPalette(out, cfg, RenderOptions{Profile: profile}); err != nil {
			t.Fatal(err)
		}
		report := buf.String()
		if !strings.Contains(report, "Terminal color profile: "+profile.String()) {
			t.Fatal("report omits detected capability")
		}
		if strings.Contains(report, "basic_colors: true") != (profile == colorprofile.ANSI) {
			t.Fatalf("report names the wrong effective palette for %s", profile)
		}
		if profile == colorprofile.NoTTY && ansi.Strip(report) != report {
			t.Fatal("colorless output regained terminal colors")
		}
	}
	if cfg.SyntaxColors != configured {
		t.Fatal("palette report modified config")
	}
}

// TestSyntaxColorTolerance covers what a hand-edited config can do to the palette. A bad
// value must not reach lipgloss.Color, which answers one with a no-op color: a typo would
// erase every comment in the buffer rather than mis-color it.
func TestSyntaxColorTolerance(t *testing.T) {
	def := defaultSyntaxColors()
	for _, slot := range syntaxColorSlots(&def) {
		if _, ok := parseSyntaxColor(*slot); !ok {
			t.Fatalf("default color %q does not parse", *slot)
		}
	}
	basic := basicSyntaxColors()
	for _, slot := range syntaxColorSlots(&basic) {
		if _, ok := parseSyntaxColor(*slot); !ok {
			t.Fatalf("basic color %q does not parse", *slot)
		}
	}

	for _, bad := range []string{"", "  ", "nonsense", "256", "-1", "#zzzzzz", "#abcd", "0x10"} {
		if _, ok := parseSyntaxColor(bad); ok {
			t.Errorf("parseSyntaxColor(%q) accepted", bad)
		}
	}
	for _, good := range []string{"0", "255", " 133 ", "#af5faf", "#a5f", "#AF5FAF"} {
		if _, ok := parseSyntaxColor(good); !ok {
			t.Errorf("parseSyntaxColor(%q) rejected", good)
		}
	}

	// Unparseable slots fall back to their default; valid ones and the flag are left as
	// the user wrote them, so toggling basic_colors off restores the file's own palette.
	sc := SyntaxColors{BasicColors: true, Keyword: "nonsense", Type: "", Func: "#a5f", String: "300"}
	normalizeSyntaxColors(&sc)
	if sc.Keyword != def.Keyword || sc.Type != def.Type || sc.String != def.String {
		t.Errorf("bad slots not repaired: %+v", sc)
	}
	if sc.Func != "#a5f" {
		t.Errorf("valid hex rewritten to %q", sc.Func)
	}
	if !sc.BasicColors {
		t.Error("normalizeSyntaxColors cleared BasicColors")
	}
}
