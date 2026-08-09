package app

import (
	"context"

	"github.com/brohd11/goutil/selfupdate"

	"github.com/brohd11/bubblestack/components"
)

// selfUpdateRepo is gote's own GitHub repo slug, passed to the shared self-update library.
const selfUpdateRepo = "brohd11/gote"

// selfUpdateHooks builds the shared self-update flow's (bubblestack/components) hook
// set for gote: the app name, the running version, and goutil's self-update library
// aimed at gote's own repo and the running binary's directory. The conversion between
// goutil's selfupdate.Info and the flow's app-agnostic SelfUpdateInfo is a direct one
// — the structs are field-identical by design.
func selfUpdateHooks(version string) components.SelfUpdateHooks {
	return components.SelfUpdateHooks{
		AppName: "gote",
		Check: func(ctx context.Context) (components.SelfUpdateInfo, error) {
			info, err := selfupdate.Check(ctx, selfUpdateRepo, version)
			return components.SelfUpdateInfo(info), err
		},
		Apply: func(ctx context.Context, info components.SelfUpdateInfo, report func(string, ...any)) error {
			binDir, err := selfupdate.BinDir()
			if err != nil {
				return err
			}
			return selfupdate.Apply(ctx, selfUpdateRepo, selfupdate.Info(info), binDir, report)
		},
	}
}
