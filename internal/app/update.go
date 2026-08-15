package app

import (
	"github.com/brohd11/bubblestack/components"
	bsupdate "github.com/brohd11/bubblestack/selfupdate"
)

// selfUpdateRepo is gote's own GitHub repo slug, passed to the shared self-update library.
const selfUpdateRepo = "brohd11/gote"

// selfUpdateHooks builds the shared self-update flow's (bubblestack/components) hook
// set for gote: the app name, the running version, and goutil's self-update library
// aimed at gote's own repo and the running binary's directory. The wiring lives in
// bubblestack/selfupdate, which owns the (field-identical by design) conversion
// between goutil's selfupdate.Info and the flow's app-agnostic SelfUpdateInfo.
func selfUpdateHooks(version string) components.SelfUpdateHooks {
	return bsupdate.Hooks("gote", selfUpdateRepo, version)
}
