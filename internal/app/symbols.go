package app

import (
	"strings"

	"github.com/brohd11/bubblestack/components"
	"github.com/brohd11/bubblestack/core"

	"charm.land/bubbles/v2/list"
	"go.lsp.dev/protocol"
)

// The document outline (alt+o): every symbol the server can see in the current file,
// as a filterable list that jumps on select.

func (s *homeScreen) applySymbols(sh *core.Shared, result *lspRequestResult) core.Action {
	if len(result.symbols) == 0 {
		return core.SetStatus("no symbols in this document")
	}
	path := result.path
	items := make([]list.Item, 0, len(result.symbols))
	for _, symbol := range result.symbols {
		target := lspLocation{Path: path, Range: symbol.Range}
		items = append(items, components.Item{
			// The indent is part of the NAME rather than the description because the
			// list filters on the name: nesting has to survive a /-query that
			// rearranges the rows out of document order.
			Name:   strings.Repeat("  ", symbol.Depth) + symbolMark(symbol.Kind) + " " + symbol.Name,
			Desc:   symbol.Detail,
			Filter: symbol.Name,
			Pick: func(sh *core.Shared) core.Action {
				return core.Seq(core.Pop(), s.jumpToLocation(sh, target))
			},
		})
	}
	// InitialIndex lands the cursor on the symbol the caret is already inside, so alt+o
	// opens where you are rather than at the top of the file.
	return core.Push(components.NewPicker(items, components.PickerOpts{
		Title: "Outline", Crumb: "outline",
		InitialIndex: symbolNearest(result.symbols, result.position),
	}))
}

// symbolNearest is the index of the last symbol starting at or before position — the
// one the caret is in, for every outline that reads in document order.
func symbolNearest(symbols []lspSymbol, position protocol.Position) int {
	best := 0
	for i, symbol := range symbols {
		start := symbol.Range.Start
		if start.Line > position.Line || start.Line == position.Line && start.Character > position.Character {
			break
		}
		best = i
	}
	return best
}

// symbolMark is one glyph per symbol kind, in the spirit of the diagnostics gutter's
// E/W/I/H: a narrow column that separates a function from a field at a glance without
// spending width on the kind's name. Kinds that share a shape share a glyph.
func symbolMark(kind protocol.SymbolKind) string {
	switch kind {
	case protocol.SymbolKindFile, protocol.SymbolKindModule, protocol.SymbolKindNamespace,
		protocol.SymbolKindPackage:
		return "▣"
	case protocol.SymbolKindClass, protocol.SymbolKindInterface, protocol.SymbolKindStruct:
		return "◆"
	case protocol.SymbolKindMethod, protocol.SymbolKindFunction, protocol.SymbolKindConstructor:
		return "ƒ"
	case protocol.SymbolKindField, protocol.SymbolKindProperty:
		return "·"
	case protocol.SymbolKindEnum, protocol.SymbolKindEnumMember:
		return "≡"
	case protocol.SymbolKindConstant:
		return "◇"
	case protocol.SymbolKindVariable:
		return "○"
	}
	return "•"
}
