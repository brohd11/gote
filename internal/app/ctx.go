package app

import (
	"github.com/brohd11/bubblestack/core"
)

// Ctx is gote's app context, stored on core.Shared.App and recovered with Of.
type Ctx struct {
	Version string
}

// New builds the context. version is the binary's version string, used by the
// self-update check.
func New(version string) *Ctx {
	return &Ctx{Version: version}
}

// Of recovers the gote context from a Shared. Screens call c := app.Of(sh).
func Of(sh *core.Shared) *Ctx { return core.App[Ctx](sh) }
