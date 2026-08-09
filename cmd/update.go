package cmd

import (
	"github.com/brohd11/goutil/selfupdate"
)

func init() {
	rootCmd.AddCommand(selfupdate.NewUpdateCommand("brohd11/gote", "gote", version))
}
