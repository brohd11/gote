package app

import (
	"strings"
	"sync"

	"github.com/brohd11/bubblestack/components/editor"

	"charm.land/lipgloss/v2"
)

// The semantic overlay: chroma paints, then the language server corrects it.
//
// chroma is a lexer, so `Config` and `buf` reach it as the same token and it colors
// neither. A server type-checks, so textDocument/semanticTokens says which is a type. This
// file is the join: the chroma highlighter still runs and still owns every run, and a
// semantic token only re-styles the range it names.
//
// Nothing here talks to the server. Fetching is the manager's job (lsp_semantic.go) and
// the answers land in the cache below; Parse only ever reads it. That is deliberate and
// load-bearing — the editor calls the same factory for a synchronous bounded preview on
// the UI goroutine (see the editor's highlightPreview), so a Parse that waited on an RPC
// would stall typing.

// semanticCache holds the newest token set per path, addressed BY LINE CONTENT rather
// than by row. That is the whole design: a token set describes text, and the editor
// re-derives highlighting from two different places — the asynchronous exact parse over
// the whole document, and a synchronous fragment covering the caret row down to the
// bottom of the viewport, re-run on every keystroke. A fragment's row 0 is some row
// partway down the document, so anything keyed by absolute row misses it entirely and the
// server's colors vanish from half the screen between keystrokes.
//
// Keyed by content, none of that matters: a line keeps its colors for exactly as long as
// its text is unchanged, wherever it has drifted to and whichever parse is asking. The
// editor already reasons this way — it validates preview rows with
// spansText(spans) == line before it will use them.
//
// Written from the UI goroutine as results arrive and read from the background parse
// goroutine, hence the mutex. Keyed by path rather than held on the highlighter because a
// highlighter instance is rebuilt on every refresh and the tokens must outlive it.
var semanticCache struct {
	sync.RWMutex
	byPath map[string]semanticEntry
}

type semanticEntry struct {
	editSeq int
	byLine  map[uint64][]lineToken
}

// lineToken is a semanticToken with the row dropped: the offsets were always relative to
// the start of the line, so the row was the only positional thing in it. Comparable, so
// two rows' token lists can be checked for agreement.
type lineToken struct {
	startUTF16 int
	lenUTF16   int
	slot       string
}

// setSemanticTokens publishes a decoded set for path, folding it into the by-content map.
// An empty set is still published: "the server answered and found nothing to paint" has to
// overwrite a previous answer, or a stale overlay outlives the code it described.
//
// lineHashes fingerprints the document the tokens were decoded from, one per row, so a
// token's row can be turned into the key for the text that row held.
func setSemanticTokens(path string, editSeq int, tokens []semanticToken, lineHashes []uint64) {
	semanticCache.Lock()
	defer semanticCache.Unlock()
	if semanticCache.byPath == nil {
		semanticCache.byPath = map[string]semanticEntry{}
	}
	semanticCache.byPath[path] = semanticEntry{
		editSeq: editSeq,
		byLine:  tokensByLine(tokens, lineHashes),
	}
}

// tokensByLine groups tokens under the hash of the line they were found on.
//
// Two identical lines can in principle carry different tokens — unlikely once types are
// collapsed to palette slots, but not impossible. Rather than guess which one a future
// occurrence means, a hash whose rows disagree is dropped: that line then renders with
// chroma's colors, which is the behavior this whole overlay is an improvement on. It fails
// toward the old look rather than toward the wrong color.
//
// Empty lines are skipped. They carry no tokens and would otherwise all collide.
func tokensByLine(tokens []semanticToken, lineHashes []uint64) map[uint64][]lineToken {
	byRow := make(map[int][]lineToken, len(tokens))
	for _, token := range tokens {
		if token.line < 0 || token.line >= len(lineHashes) {
			continue
		}
		byRow[token.line] = append(byRow[token.line], lineToken{
			startUTF16: token.startUTF16, lenUTF16: token.lenUTF16, slot: token.slot,
		})
	}

	emptyLine := hashLine("")
	out := make(map[uint64][]lineToken, len(byRow))
	ambiguous := map[uint64]bool{}
	for row, rowTokens := range byRow {
		key := lineHashes[row]
		if key == emptyLine || ambiguous[key] {
			continue
		}
		if existing, seen := out[key]; seen {
			if !sameLineTokens(existing, rowTokens) {
				delete(out, key)
				ambiguous[key] = true
			}
			continue
		}
		out[key] = rowTokens
	}
	return out
}

func sameLineTokens(a, b []lineToken) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// forgetSemanticTokens drops a path's overlay, so a closed buffer does not hold its tokens
// (or hand them to a different file that later takes the same path).
func forgetSemanticTokens(path string) {
	semanticCache.Lock()
	defer semanticCache.Unlock()
	delete(semanticCache.byPath, path)
}

func semanticTokensFor(path string) map[uint64][]lineToken {
	semanticCache.RLock()
	defer semanticCache.RUnlock()
	return semanticCache.byPath[path].byLine
}

// semanticHighlighter wraps a chroma highlighter with the overlay. It satisfies
// editor.Highlighter by delegating, which keeps the lexical half exactly as it was:
// with no tokens cached — no server, a file it will not answer for, or the moment before
// the first answer — this is the chroma highlighter and nothing else.
type semanticHighlighter struct {
	base  editor.Highlighter
	path  string
	lines [][]editor.Span
}

var _ editor.Highlighter = (*semanticHighlighter)(nil)

// semanticHighlighterFactory wraps a language profile's factory so each instance knows the
// path it is highlighting. The editor's LanguageResolver is the only path-derived seam it
// offers, which is why the path is bound here rather than passed to Parse.
func semanticHighlighterFactory(base func() editor.Highlighter, path string) func() editor.Highlighter {
	return func() editor.Highlighter {
		inner := base()
		if inner == nil {
			return nil
		}
		return &semanticHighlighter{base: inner, path: path}
	}
}

// HighlightRestartLine forwards the base highlighter's fast-preview hint when it has one.
// The overlay adds no multi-line lexical state of its own — a semantic token never spans
// rows — so the restart answer is entirely chroma's.
func (h *semanticHighlighter) HighlightRestartLine(row int) int {
	if provider, ok := h.base.(editor.HighlightRestartProvider); ok {
		return provider.HighlightRestartLine(row)
	}
	return row
}

func (h *semanticHighlighter) Parse(text string) {
	h.base.Parse(text)
	h.lines = nil

	byLine := semanticTokensFor(h.path)
	if len(byLine) == 0 {
		return
	}
	// Nothing here knows or cares which rows of the document these are. text may be the
	// whole file (the exact parse) or a fragment starting partway down it (the keystroke
	// preview) — the row is only ever used to ask the base highlighter for its spans, and
	// the lookup is on what the row SAYS.
	//
	// The row's text comes from the base spans rather than from splitting text again:
	// chroma is lossless, so a row's spans always concatenate back to it, and this is the
	// same identity the editor itself checks before it will render a preview row.
	lines := strings.Count(text, "\n") + 1
	h.lines = make([][]editor.Span, lines)
	for row := range lines {
		spans := h.base.HighlightLine(row)
		line := spansPlainText(spans)
		if line == "" {
			continue
		}
		tokens, ok := byLine[hashLine(line)]
		if !ok {
			continue
		}
		if overlaid, ok := overlaySpans(line, spans, tokens); ok {
			h.lines[row] = overlaid
		}
	}
}

func (h *semanticHighlighter) HighlightLine(row int) []editor.Span {
	if row >= 0 && row < len(h.lines) && h.lines[row] != nil {
		return h.lines[row]
	}
	return h.base.HighlightLine(row)
}

// spansPlainText is the text a row's spans describe. It is how the overlay recovers the
// line it is about to paint without re-splitting the document: chroma is lossless, so a
// row's spans always concatenate back to that row, and this is the same identity the
// editor itself checks before it will render a preview row.
func spansPlainText(spans []editor.Span) string {
	var b strings.Builder
	for _, sp := range spans {
		b.WriteString(sp.Text)
	}
	return b.String()
}

// overlaySpans re-cuts one line's spans so each token's range wears its slot's style.
//
// It works over a per-rune style key rather than the spans directly, because a token
// boundary can fall anywhere inside a span and lipgloss.Style carries a func field, so
// styles cannot be compared to re-group runs. The key is the source: a non-negative index
// into the original spans, or a negative one into the styles the tokens brought.
//
// Returns false when the spans do not reconstruct the line, which is the editor's own
// contract — that line then falls back to whatever the base highlighter said.
func overlaySpans(line string, spans []editor.Span, tokens []lineToken) ([]editor.Span, bool) {
	runes := []rune(line)
	keys := make([]int, len(runes))
	styles := make([]lipgloss.Style, 0, len(spans)+len(tokens))

	at := 0
	for _, span := range spans {
		styles = append(styles, span.Style)
		key := len(styles) - 1
		for range []rune(span.Text) {
			if at >= len(keys) {
				return nil, false // spans overrun the line: not our text
			}
			keys[at] = key
			at++
		}
	}
	if at != len(keys) {
		return nil, false // spans do not cover the line
	}

	for _, token := range tokens {
		style, ok := slotStyle(token.slot)
		if !ok {
			continue
		}
		// The offsets are UTF-16 code units, which is what gote negotiates at initialize;
		// on any line with an astral-plane rune they are not rune indices.
		start := utf16OffsetToRune(runes, token.startUTF16)
		end := utf16OffsetToRune(runes, token.startUTF16+token.lenUTF16)
		if start < 0 || end < 0 || start >= end || end > len(runes) {
			continue // a token that does not land cleanly is dropped, never clamped
		}
		styles = append(styles, style)
		key := len(styles) - 1
		for i := start; i < end; i++ {
			keys[i] = key
		}
	}

	out := make([]editor.Span, 0, len(spans)+len(tokens))
	for i := 0; i < len(runes); {
		j := i
		for j < len(runes) && keys[j] == keys[i] {
			j++
		}
		out = append(out, editor.Span{Text: string(runes[i:j]), Style: styles[keys[i]]})
		i = j
	}
	return out, true
}

// applySemanticTokens publishes a fetched token set and asks the editor to re-highlight.
//
// The refresh is the whole reason RefreshHighlight exists: the tokens describe text the
// editor parsed some milliseconds ago, and an edit-driven pipeline would never revisit
// those rows on its own. A result is dropped when the buffer has moved on since it was
// requested — the same staleness rule applyRequestResult follows, and the reason editSeq
// rides along on the request.
func (s *homeScreen) applySemanticTokens(result *lspSemanticResult) {
	if result == nil || result.err != nil || result.path == "" {
		return
	}
	setSemanticTokens(result.path, result.editSeq, result.tokens, result.lineHashes)
	if s.editor == nil || result.path != s.currentPath {
		return
	}
	if s.editor.EditSeq() != result.editSeq {
		return // the buffer changed under the answer; the next fetch covers it
	}
	s.editor.RefreshHighlight()
}
