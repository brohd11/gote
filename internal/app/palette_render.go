package app

import (
	"fmt"
	"io"
	"os"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"
)

// The `gote colors` report: the slots, a highlighted sample and the 256-color space, so a
// palette can be judged in context. It renders the colors already decided; it decides
// nothing.

// RenderOptions: a file to sample instead of the built-in samples, and whether to show the
// basic ANSI palette.
type RenderOptions struct {
	Path  string
	Basic bool
	// Profile is the output capability detected by the CLI. Unknown preserves
	// configured colors for callers without a terminal.
	Profile colorprofile.Profile
}

// RenderPalette writes the report using cfg's palette. Pass w through a
// colorprofile.Writer, since nothing else downsamples outside the TUI.
func RenderPalette(w io.Writer, cfg Config, opts RenderOptions) error {
	sc := syntaxColorsForProfile(cfg.SyntaxColors, opts.Profile)
	if opts.Basic {
		sc = cfg.SyntaxColors
		sc.BasicColors = true
	}
	applySyntaxPalette(sc)
	// Restore the configured palette on the way out: the process may be short-lived, but a
	// function that leaves a global holding --basic is one bug away from a mystery.
	defer applySyntaxPalette(cfg.SyntaxColors)

	resolved := sc
	if resolved.BasicColors {
		bracketsEnabled := resolved.Brackets == nil || len(resolved.Brackets) > 0
		resolved = basicSyntaxColors()
		if !bracketsEnabled {
			resolved.Brackets = []string{}
		}
	} else {
		normalizeSyntaxColors(&resolved)
	}

	if opts.Profile != colorprofile.Unknown {
		if _, err := fmt.Fprintf(w, "Terminal color profile: %s\n\n", opts.Profile); err != nil {
			return err
		}
	}
	if err := renderSlots(w, resolved, sc.BasicColors); err != nil {
		return err
	}
	if err := renderSample(w, opts.Path); err != nil {
		return err
	}
	return renderSpace(w)
}

func renderSlots(w io.Writer, sc SyntaxColors, basic bool) error {
	title := "syntax_colors — the palette in effect"
	if basic {
		title = "syntax_colors — basic_colors: true (your terminal's own eight)"
	}
	if _, err := fmt.Fprintf(w, "%s\n\n", title); err != nil {
		return err
	}
	values := syntaxColorSlots(&sc)
	for i, slot := range paletteSlots() {
		value := *values[i]
		// The slot name wearing its own style: a legend that is also the swatch.
		_, err := fmt.Fprintf(w, "  %-12s %-6s %-9s %s\n",
			slot.key, value, describeColor(value), slot.style.Render(slot.key))
		if err != nil {
			return err
		}
	}
	if len(sc.Brackets) == 0 && sc.Brackets != nil {
		if _, err := fmt.Fprintln(w, "  brackets    []     disabled"); err != nil {
			return err
		}
	} else {
		for i, value := range sc.Brackets {
			style := chBracketStyles[i%len(chBracketStyles)]
			if _, err := fmt.Fprintf(w, "  bracket_%-3d %-6s %-9s %s\n",
				i+1, value, describeColor(value), style.Render("bracket")); err != nil {
				return err
			}
		}
	}
	_, err := fmt.Fprintln(w)
	return err
}

// describeColor explains what an index is; 0-15 have no fixed value (the terminal's scheme
// decides), which is why basic_colors exists.
func describeColor(value string) string {
	c, ok := parseSyntaxColor(value)
	if !ok {
		return "?"
	}
	if _, isBasic := c.(ansi.BasicColor); isBasic {
		return "terminal"
	}
	r, g, b, _ := c.RGBA()
	return fmt.Sprintf("#%02x%02x%02x", r>>8, g>>8, b>>8)
}

const goSample = `package main

// Load reads the config and returns it.
func Load(path string) (Config, error) {
	buf := append(make([]byte, 0, 16), 'x')
	if path == "" {
		return Config{}, ErrNoPath
	}
	return NewConfig(string(buf), 42), nil
}
`

const mdSample = `# Heading

A paragraph with *emphasis*, **strong** text and ` + "`inline code`" + `.

> A quoted line.

- a list item
- and [a link](https://example.com)
`

// renderSample runs the real highlighter over real text: swatches alone hide colors that
// clash in code.
func renderSample(w io.Writer, path string) error {
	samples := []struct{ name, text string }{
		{"sample.go", goSample},
		{"sample.md", mdSample},
	}
	if path != "" {
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		samples = []struct{ name, text string }{{path, string(data)}}
	}

	for _, s := range samples {
		if _, err := fmt.Fprintf(w, "%s\n\n", s.name); err != nil {
			return err
		}
		if err := writeHighlighted(w, s.name, s.text); err != nil {
			return err
		}
		if _, err := fmt.Fprintln(w); err != nil {
			return err
		}
	}
	return nil
}

func writeHighlighted(w io.Writer, name, text string) error {
	lines := strings.Split(strings.TrimRight(text, "\n"), "\n")
	profile := languageForPath(name)
	if profile == nil || profile.editor.NewHighlighter == nil {
		// No profile is not an error: the file still deserves to be shown, plain, which
		// is exactly what the editor would do with it.
		for _, line := range lines {
			if _, err := fmt.Fprintf(w, "  %s\n", line); err != nil {
				return err
			}
		}
		return nil
	}
	h := profile.editor.NewHighlighter()
	h.Parse(strings.Join(lines, "\n"))
	for row, line := range lines {
		out := line
		// The editor's own rule: spans that do not reconstruct the line are not trusted,
		// so the plain text is shown instead of a corrupted render.
		if spans := h.HighlightLine(row); spans != nil {
			var b strings.Builder
			for _, sp := range spans {
				if sp.Style == nil {
					b.WriteString(sp.Text)
				} else {
					b.WriteString(sp.Style.Render(sp.Text))
				}
			}
			if plain := spansPlainText(spans); plain == line {
				out = b.String()
			}
		}
		if _, err := fmt.Fprintf(w, "  %s\n", out); err != nil {
			return err
		}
	}
	return nil
}

// renderSpace prints the 256-color space in its structure: 16-231 is a 6x6x6 cube (16 +
// 36r + 6g + b, channels {0,95,135,175,215,255}) at six per row, 232-255 a grey ramp (8 +
// 10*(i-232)).
func renderSpace(w io.Writer) error {
	if _, err := fmt.Fprintln(w, "0-15 — your terminal's own scheme, not fixed values"); err != nil {
		return err
	}
	if err := swatchRow(w, 0, 16, 8); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, "\n16-231 — the 6x6x6 cube: 16 + 36r + 6g + b, channels {0,95,135,175,215,255}"); err != nil {
		return err
	}
	if err := swatchRow(w, 16, 232, 6); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, "\n232-255 — the grey ramp: 8 + 10*(i-232)"); err != nil {
		return err
	}
	return swatchRow(w, 232, 256, 6)
}

// swatchRow prints each index as foreground text (a syntax color is text), in 8-column
// cells so the widest row fits 80 columns.
func swatchRow(w io.Writer, from, to, perRow int) error {
	for i := from; i < to; i++ {
		style := lipgloss.NewStyle().Foreground(lipgloss.Color(fmt.Sprint(i)))
		if _, err := fmt.Fprintf(w, " %s", style.Render(fmt.Sprintf("%3d ████", i))); err != nil {
			return err
		}
		if (i-from+1)%perRow == 0 {
			if _, err := fmt.Fprintln(w); err != nil {
				return err
			}
		}
	}
	if (to-from)%perRow != 0 {
		if _, err := fmt.Fprintln(w); err != nil {
			return err
		}
	}
	return nil
}
