package cmd

import (
	"errors"
	"os"
	"path/filepath"

	"github.com/brohd11/gote/internal/app"

	"github.com/spf13/cobra"
)

// version is the binary version; defaults to "dev" for a plain `go build`. The makefile stamps
// it via -X ldflags (git describe --tags --always --dirty), so release and `make` binaries report
// their real version and the self-update check can compare it against the latest tag.
var version = "dev"

var (
	scan  bool
	depth int
)

var rootCmd = &cobra.Command{
	Use:   "gote [dir]",
	Short: "A simple text editor (TUI)",
	Long: `gote is a simple TUI text editor. With no arguments it lists the docs stored in
~/.gote (extension and scan depth come from ~/.gote/config.yml). With --scan it instead
lists every matching file found by a recursive scan:

  gote                  # ~/.gote docs
  gote -s               # scan the current directory, config depth
  gote -s ~/notes -d 4  # scan ~/notes, depth 4`,
	Version:       version,
	Args:          cobra.MaximumNArgs(1),
	SilenceUsage:  true,
	SilenceErrors: false,
	RunE:          runRoot,
}

func init() {
	rootCmd.SetVersionTemplate("gote {{.Version}}\n")
	rootCmd.Flags().BoolVarP(&scan, "scan", "s", false, "scan a directory recursively for docs instead of listing ~/.gote")
	rootCmd.Flags().IntVarP(&depth, "depth", "d", 0, "scan depth in directory levels (default: config's scan_depth)")
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

// runRoot resolves the scan directory and launches the TUI.
func runRoot(cmd *cobra.Command, args []string) error {
	dir := "."
	if len(args) > 0 {
		if !scan {
			return errors.New("a directory argument requires --scan")
		}
		dir = args[0]
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	return app.Run(version, scan, abs, depth)
}
