package app

import (
	"github.com/brohd11/bubblestack/components"
	bsupdate "github.com/brohd11/bubblestack/selfupdate"
)

// selfUpdateHooks points the shared self-update flow at gote's release repo (also named
// in cmd/update.go).
func selfUpdateHooks(version string) components.SelfUpdateHooks {
	return bsupdate.Hooks("gote", "brohd11/gote", version)
}
