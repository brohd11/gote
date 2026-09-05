package cmd

import (
	"github.com/brohd11/gote/internal/app"
	"github.com/brohd11/goutil/configcmd"
)

var configCmd = configcmd.NewCommand(configcmd.Options{
	Path: app.ConfigPath,
	Dir:  app.Dir,
	// SyncConfig rather than EnsureConfig: opening the config to edit it is the moment
	// to top up any key added since the file was written.
	Ensure: func() error {
		_, err := app.SyncConfig()
		return err
	},
})

func init() {
	rootCmd.AddCommand(configCmd)
}
