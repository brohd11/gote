package app

import (
	"testing"
	"time"

	"go.lsp.dev/protocol"
)

func waitSemanticResult(t *testing.T, manager *lspManager) *lspSemanticResult {
	t.Helper()
	deadline := time.After(3 * time.Second)
	for {
		select {
		case event := <-manager.events:
			if event.semantic != nil {
				return event.semantic
			}
		case <-deadline:
			t.Fatal("timed out waiting for a semantic token result")
		}
	}
}

func waitSemanticLegend(t *testing.T, c *Ctx, path string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		c.lsp.mu.Lock()
		doc, ok := c.lsp.desired[path]
		ready := ok && c.lsp.supportsSemanticLocked(doc)
		c.lsp.mu.Unlock()
		if ready {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for the server's semantic legend to be cached")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// TestLSPDeclaresSemanticTokensCapability is the answer to "do we declare it on this
// side": the server has to see the client capability or it is entitled to stay silent.
// gopls additionally gates the feature behind its own initialization option, which is
// covered by TestGoplsDefaultAsksForSemanticTokens.
func TestLSPDeclaresSemanticTokensCapability(t *testing.T) {
	server := newRecordingLSPServer()
	server.hoverResult = &protocol.Hover{Contents: protocol.String("x")}
	c, _, _ := newRequestLaneCtx(t, server, "value = 1\n")
	_ = c

	server.mu.Lock()
	params := server.initializeParams
	server.mu.Unlock()
	if params == nil || params.Capabilities.TextDocument == nil {
		t.Fatal("no text document capabilities were sent")
	}
	tokens := params.Capabilities.TextDocument.SemanticTokens
	if full, ok := tokens.Requests.Full.(protocol.Boolean); !ok || !bool(full) {
		t.Errorf("requests.full = %#v, want Boolean(true)", tokens.Requests.Full)
	}
	if tokens.AugmentsSyntaxTokens == nil || !*tokens.AugmentsSyntaxTokens {
		t.Error("augmentsSyntaxTokens must be true: chroma paints first and these correct it")
	}
	if len(tokens.Formats) != 1 || tokens.Formats[0] != protocol.TokenFormatRelative {
		t.Errorf("formats = %v, want the relative encoding", tokens.Formats)
	}
	// The declaration says what the client can decode, so it carries the whole spec
	// vocabulary rather than only the types the palette happens to paint.
	if len(tokens.TokenTypes) != len(semanticTokenTypeNames) {
		t.Errorf("declared %d token types, want the spec's %d",
			len(tokens.TokenTypes), len(semanticTokenTypeNames))
	}
	if len(tokens.TokenModifiers) != len(semanticTokenModifierNames) {
		t.Errorf("declared %d modifiers, want the spec's %d",
			len(tokens.TokenModifiers), len(semanticTokenModifierNames))
	}
}

// TestGoplsDefaultAsksForSemanticTokens: gopls returns no provider at all unless its own
// semanticTokens option is set, which is separate from — and just as necessary as — the
// LSP capability above.
func TestGoplsDefaultAsksForSemanticTokens(t *testing.T) {
	options := DefaultConfig().LanguageServers["go"].InitializationOptions
	if enabled, ok := options["semanticTokens"].(bool); !ok || !enabled {
		t.Errorf("gopls initialization_options semanticTokens = %#v, want true",
			options["semanticTokens"])
	}
}

// TestLSPSemanticLaneResolvesThroughTheServerLegend: a token's type arrives as an index
// into the SERVER's legend, and the order differs between servers — so the legend below
// deliberately does not match gote's own declared order. Resolving by index rather than by
// name would silently paint the wrong colors.
func TestLSPSemanticLaneResolvesThroughTheServerLegend(t *testing.T) {
	server := newRecordingLSPServer()
	server.hoverResult = &protocol.Hover{Contents: protocol.String("x")}
	server.semanticLegend = []string{"comment", "type", "variable"}
	server.semanticData = []uint32{
		0, 0, 3, 1, 0, // legend[1] = "type"
		0, 4, 5, 2, 0, // legend[2] = "variable": unmapped, dropped
	}
	c, path, ed := newRequestLaneCtx(t, server, "Foo = value\n")
	waitSemanticLegend(t, c, path)

	if id := c.lsp.RequestSemanticTokens(path, ed.EditSeq()); id == 0 {
		t.Fatal("a server advertising a legend refused the request")
	}
	waitLSPCall(t, server.calls, "semanticTokens")
	result := waitSemanticResult(t, c.lsp)
	if result.err != nil {
		t.Fatalf("semantic tokens failed: %v", result.err)
	}
	if len(result.tokens) != 1 {
		t.Fatalf("got %d tokens, want 1 (the variable is unmapped): %+v",
			len(result.tokens), result.tokens)
	}
	if got := result.tokens[0]; got.slot != "type" || got.startUTF16 != 0 || got.lenUTF16 != 3 {
		t.Errorf("token = %+v, want the type at 0..3", got)
	}
	if result.path != path {
		t.Errorf("result path = %q, want %q", result.path, path)
	}
}

// TestLSPSemanticLaneUnadvertisedIsNeverRequested: a server with no provider must not be
// asked. Unlike the on-demand lane this refusal is silent — nobody pressed anything — so
// the only way to notice a regression is that the call goes out anyway.
func TestLSPSemanticLaneUnadvertisedIsNeverRequested(t *testing.T) {
	server := newRecordingLSPServer()
	server.hoverResult = &protocol.Hover{Contents: protocol.String("x")}
	c, path, ed := newRequestLaneCtx(t, server, "value = 1\n")

	if id := c.lsp.RequestSemanticTokens(path, ed.EditSeq()); id != 0 {
		t.Fatalf("request returned id %d, want a refusal", id)
	}
	assertNoLSPCall(t, server.calls, "semanticTokens", 100*time.Millisecond)
}
