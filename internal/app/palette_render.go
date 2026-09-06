package app

import (
	"fmt"
	"io"
	"os"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// The `gote colors` report. Picking a syntax color means answering two questions the
// config file cannot: what does index 137 look like, and what does it look like against
// code? A palette is only judgeable in context, so this prints the slots, a highlighted
// sample and the whole 256-color space together.
//
// Nothing here decides colors. It renders what defaultSyntaxColors and the config already
// decided, which is what makes it a tuning tool rather than a second source of truth.

// RenderOptions is what `gote colors` was asked for: a file to highlight instead of the
// built-in samples, and whether to show the basic ANSI palette rather than the configured
// one.
type RenderOptions struct {
	Path  string
	Basic bool
}

// RenderPalette writes the report to w. cfg supplies the palette in effect, so what the
// report shows is what the editor would draw — the caller is expected to hand w through a
// colorprofile.Writer for the same reason, since outside the TUI nothing else downsamples.
func RenderPalette(w io.Writer, cfg Config, opts RenderOptions) error {
	sc := cfg.SyntaxColors
	if opts.Basic {
		sc = SyntaxColors{BasicColors: true}
	}
	applySyntaxPalette(sc)
	// Restore the configured palette on the way out: the process may be short-lived, but a
	// function that leaves a global holding --basic is one bug away from a mystery.
	defer applySyntaxPalette(cfg.SyntaxColors)

	resolved := sc
	if resolved.BasicColors {
		resolved = basicSyntaxColors()
	} else {
		normalizeSyntaxColors(&resolved)
	}

	if err := renderSlots(w, resolved, opts.Basic); err != nil {
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
	_, err := fmt.Fprintln(w)
	return err
}

// describeColor spells out what an index actually is, which is the thing the config file
// cannot say. 0-15 have no fixed value to print — they are whatever the terminal's scheme
// says — and that distinction is the whole reason basic_colors exists.
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

// renderSample runs the real highlighter over real text. A palette judged on swatches
// alone is how the first 256-color defaults shipped with a type and a func color that
// were indistinguishable in actual code.
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
				b.WriteString(sp.Style.Render(sp.Text))
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

// renderSpace prints the 256-color space in its own shape rather than as a flat run.
// 16-231 is a 6x6x6 RGB cube — index 16 + 36r + 6g + b, each channel from
// {0,95,135,175,215,255} — so six per row is one blue ramp, and six rows is one red
// plane. 232-255 is a 24-step grey ramp, 8 + 10*(i-232). Knowing the shape is what turns
// picking a color from a hunt into arithmetic.
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

// swatchRow prints each index as a FOREGROUND block, because a syntax color is text: a
// background swatch flatters colors that are unreadable as glyphs. Readability against
// real text is the sample section's job, which is why this one is just the color.
//
// Cells are eight columns so the widest row here — the sixteen terminal colors, eight per
// row — lands inside eighty, and the cube's six-wide rows are comfortable at any width.
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
