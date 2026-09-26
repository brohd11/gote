package app

import (
	"fmt"

	"github.com/brohd11/bubblestack/core"
)

// Find references (alt+shift+r), shown through locationPicker like multiple definitions.

func (s *homeScreen) applyReferences(sh *core.Shared, result *lspRequestResult) core.Action {
	switch len(result.locations) {
	case 0:
		return core.SetStatus("no references found")
	case 1:
		// The declaration is included, so one result is the symbol itself; jump to it.
		return s.jumpToLocation(sh, result.locations[0])
	}
	return core.Push(s.locationPicker(sh,
		fmt.Sprintf("References (%d)", len(result.locations)), "references", result.locations))
}
