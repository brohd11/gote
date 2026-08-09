package cmd

import (
	"os"

	"github.com/brohd11/gote/internal/app"

	"github.com/spf13/cobra"
)

// version is the binary version; defaults to "dev" for a plain `go build`. The makefile stamps
// it via -X ldflags (git describe --tags --always --dirty), so release and `make` binaries report
// their real version and the self-update check can compare it against the latest tag.
var version = "dev"

var rootCmd = &cobra.Command{
	Use:           "gote",
	Short:         "A simple text editor (TUI)",
	Version:       version,
	Args:          cobra.NoArgs,
	SilenceUsage:  true,
	SilenceErrors: false,
	RunE:          runRoot,
}

func init() {
	rootCmd.SetVersionTemplate("gote {{.Version}}\n")
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

// runRoot launches the editor TUI.
func runRoot(cmd *cobra.Command, args []string) error {
	return app.Run(version)
}
