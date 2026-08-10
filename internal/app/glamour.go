package app

import (
	"strings"

	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/glamour/ansi"
	"github.com/charmbracelet/glamour/styles"
	"github.com/charmbracelet/lipgloss"
)

// showLinkURLs prints the raw URL after each link's label — glamour's stock behavior,
// and unconditional in its link renderer, so the only lever is the style config below.
// Off, a link renders as just its styled text. Flip it and rebuild to compare the two.
//
// Off is not quite free: glamour writes the separating space BEFORE it consults the
// style, so a suppressed URL leaves one stray space where it was. That is the whole
// cost, and it is invisible at the end of a line.
const showLinkURLs = false

// glamourMargin is the left indent glamour puts on the document body, and the width it
// gives up on both sides to do it. Stock is 2, which is right for a bare terminal and
// wrong inside a ScrollContainer that already draws a border and pads a column.
const glamourMargin uint = 0

// glamourRender is the alternative markdown renderer behind the second preview pane —
// the same signature as components.RenderMarkdown so the two are interchangeable in
// previewScreen.
//
// It is here to be evaluated against the custom reader, not to replace it: glamour is a
// full CommonMark implementation (tables, blockquotes, syntax-highlighted code blocks)
// but brings its own theme, which does not follow bubblestack's. Its own chrome — the
// document margin and the blank line above and below — is dropped here (glamourStyle),
// so what is left of the mismatch is color, not geometry. The custom reader stays the
// docs-page renderer regardless of how this comparison lands.
//
// A renderer is built per call rather than cached because the width changes with the
// terminal and WithWordWrap is fixed at construction. DocOpts.Render has no error
// channel, so both failure paths surface as the text of the page — a preview that says
// why it is empty beats one that just is.
func glamourRender(src string, width int) string {
	r, err := glamour.NewTermRenderer(
		glamour.WithStyles(glamourStyle()),
		glamour.WithColorProfile(lipgloss.ColorProfile()),
		glamour.WithWordWrap(width),
	)
	if err != nil {
		return "glamour: " + err.Error()
	}
	out, err := r.Render(src)
	if err != nil {
		return "glamour: " + err.Error()
	}
	// The document's own block prefix/suffix is cleared below; this catches the blank
	// lines the block elements themselves leave at either end, which in a pane read as
	// the render having started late.
	return strings.Trim(out, "\n")
}

// glamourStyle is the stock dark/light config with gote's two deviations: no document
// margin, and (unless showLinkURLs) no raw URLs.
//
// Both the light/dark choice and the color depth come from lipgloss rather than from
// glamour's own detection. bubblestack.Run primes that detection at startup, so this is
// the answer the rest of the screen is already drawn with — and glamour's WithAutoStyle
// would otherwise disagree with it whenever there is no TTY to sniff, falling back to a
// style with no colors at all.
//
// The stock configs are package-level values, so this is a copy; every field touched is
// assigned a fresh pointer rather than written through the shared one, which is what
// keeps the originals intact for the next call.
func glamourStyle() ansi.StyleConfig {
	cfg := styles.LightStyleConfig
	if lipgloss.HasDarkBackground() {
		cfg = styles.DarkStyleConfig
	}

	margin := glamourMargin
	cfg.Document.Margin = &margin
	// The stock "\n" pair is the blank line above and below the whole render.
	cfg.Document.BlockPrefix = ""
	cfg.Document.BlockSuffix = ""

	if !showLinkURLs {
		// glamour renders the URL through this template when one is set, and skips
		// writing anything at all for an empty result — so a template that produces
		// nothing is how a URL is dropped. The label is styled by LinkText, untouched.
		cfg.Link.Format = "{{if false}}{{.text}}{{end}}"
	}

	// CodeBlock keeps its own margin and its chroma palette: that inset is what makes a
	// fenced block read as a block, and the palette is the part of glamour worth having.
	return cfg
}
