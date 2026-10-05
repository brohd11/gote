package cmd

import (
	"github.com/brohd11/gote/internal/app"
	"github.com/brohd11/goutil/configcmd"
)

var configCmd = configcmd.NewCommand(configcmd.Options{
	Path:   app.ConfigPath,
	Dir:    app.Dir,
	Ensure: app.EnsureConfigFiles, // shared with File ▸ Config
})

// langCmd is `gote config lang`: the same editor launch for lang.yml, which starts from a
// commented template rather than every key, since everything in it is an override.
var langCmd = configcmd.NewCommand(configcmd.Options{
	Path:   app.LangPath,
	Dir:    app.Dir,
	Ensure: app.EnsureLangConfig,
})

func init() {
	langCmd.Use = "lang"
	langCmd.Short = "Edit per-language settings and language servers (lang.yml)"
	configCmd.AddCommand(langCmd)
	rootCmd.AddCommand(configCmd)
}
