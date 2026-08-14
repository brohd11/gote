package cmd

import (
	"github.com/brohd11/gote/internal/app"

	"github.com/spf13/cobra"
)

var configCmd = &cobra.Command{
	Use:   "config",
	Short: "Edit ~/.gote/config.yml",
	Long: `config opens ~/.gote/config.yml in the minimal editor — the same chrome-less
shape as "gote somefile.md". A missing config is written with the defaults first, so
there is always a real schema on screen to edit rather than an empty buffer.

Edits take effect on the NEXT launch: gote reads its config once at startup and has
no watcher, so saving here changes nothing about the running session.

"config" names this command. Use "gote ./config" to open a file of that name.`,
	Args:          cobra.NoArgs,
	SilenceUsage:  true,
	SilenceErrors: false,
	RunE:          runConfig,
}

func init() {
	rootCmd.AddCommand(configCmd)
}

func runConfig(_ *cobra.Command, _ []string) error {
	path, err := app.EnsureConfig()
	if err != nil {
		return err
	}
	cfg, err := app.LoadConfig()
	if err != nil {
		return err
	}
	return app.Run(version, cfg, app.Options{Mode: app.ModeFile, File: path})
}
