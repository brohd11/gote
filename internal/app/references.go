package app

import (
	"fmt"

	"github.com/brohd11/bubblestack/core"
)

// Find references (alt+n). The result is a list of jump targets, which is exactly what
// several definitions already are — so this shares locationPicker with them rather than
// growing a second list screen that would differ only in its title.

func (s *homeScreen) applyReferences(sh *core.Shared, result *lspRequestResult) core.Action {
	switch len(result.locations) {
	case 0:
		return core.SetStatus("no references found")
	case 1:
		// The declaration itself is included in the request, so a lone result is the
		// symbol standing alone. Jumping there anyway is right when it is elsewhere,
		// and harmless when it is the caret's own line.
		return s.jumpToLocation(sh, result.locations[0])
	}
	return core.Push(s.locationPicker(sh,
		fmt.Sprintf("References (%d)", len(result.locations)), "references", result.locations))
}
