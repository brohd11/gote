package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

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

// hereArg is the keyword that means "scan the current directory". It wins over a
// directory of the same name — `gote ./here` is the way to reach that one.
const hereArg = "here"

var rootCmd = &cobra.Command{
	Use:   "gote [here|dir|file] [depth]",
	Short: "A simple text editor (TUI)",
	Long: `gote is a simple TUI text editor. With no arguments it opens the default vault
named in ~/.gote/config.yml, or the ~/.gote document store when no valid default is
configured. Given a directory it lists every matching file found by a recursive scan,
to the depth given as a second argument. Given a file it opens that file alone, with
the sidebar and surrounding chrome hidden — the shape to use as your $EDITOR.

  gote                  # configured default vault, otherwise ~/.gote docs
  gote here             # scan the current directory, config depth
  gote here 3           # scan the current directory, depth 3
  gote ~/notes 4        # scan ~/notes, depth 4
  gote notes.md         # edit one file, nothing else on screen

"here" is a keyword, not a path: use ./here to scan a directory of that name. A file
argument that does not exist yet opens an empty buffer, written on ctrl+s.`,
	Version:       version,
	Args:          cobra.MaximumNArgs(2),
	SilenceUsage:  true,
	SilenceErrors: false,
	RunE:          runRoot,
}

func init() {
	rootCmd.SetVersionTemplate("gote {{.Version}}\n")
	rootCmd.Flags().BoolVarP(&scan, "scan", "s", false, "treat the argument as a directory to scan (implied when it is one)")
	rootCmd.Flags().IntVarP(&depth, "depth", "d", 0, "scan depth in directory levels (default: config's scan_depth)")
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

// runRoot resolves the launch options and starts the TUI.
func runRoot(cmd *cobra.Command, args []string) error {
	opts, err := resolveOptions(args, scan, depth, cmd.Flags().Changed("depth"))
	if err != nil {
		return err
	}
	return app.Run(version, opts)
}

// resolveOptions turns the CLI surface into the app's launch options. It is the whole
// of gote's argument grammar, kept apart from the cobra wiring so it can be tested
// without starting a program:
//
//   - no argument: config chooses the default vault later, or (with --scan) scan cwd
//   - "here": a scan of the cwd
//   - a directory (or --scan, or a trailing separator): a scan of it
//   - anything else, existing or not: that file, in the minimal editor
//
// The second argument is the scan depth, overriding --depth; it is meaningless for a
// file and rejected there rather than ignored, since a rejected typo beats a silently
// dropped one. Paths are made absolute — the scan root shows in the breadcrumb and the
// editor saves against the path it was given, neither of which should depend on the
// cwd once the program is running.
func resolveOptions(args []string, scan bool, depth int, depthSet bool) (app.Options, error) {
	opts := app.Options{Depth: depth, DepthSet: depthSet}

	if len(args) > 1 {
		n, err := strconv.Atoi(args[1])
		if err != nil {
			return opts, fmt.Errorf("depth %q is not a number", args[1])
		}
		if n < 0 {
			return opts, fmt.Errorf("depth %d is negative", n)
		}
		opts.Depth, opts.DepthSet = n, true
	}
	if opts.Depth < 0 {
		return opts, fmt.Errorf("depth %d is negative", opts.Depth)
	}

	if len(args) == 0 {
		if scan {
			opts.Mode = app.ModeScan
			dir, err := os.Getwd()
			if err != nil {
				return opts, err
			}
			opts.Dir = dir
		}
		return opts, nil
	}

	arg := args[0]
	if arg == hereArg {
		dir, err := os.Getwd()
		if err != nil {
			return opts, err
		}
		opts.Mode, opts.Dir = app.ModeScan, dir
		return opts, nil
	}

	abs, err := filepath.Abs(arg)
	if err != nil {
		return opts, err
	}
	if isDirArg(arg, scan) {
		opts.Mode, opts.Dir = app.ModeScan, abs
		return opts, nil
	}
	if len(args) > 1 {
		return opts, fmt.Errorf("a depth applies only to a directory scan, and %q is a file", arg)
	}
	opts.Mode, opts.File = app.ModeFile, abs
	return opts, nil
}

// isDirArg reports whether arg names a directory to scan: one that is a directory on
// disk, one written with a trailing separator, or any argument at all under --scan
// (which is how a directory that does not exist yet can still be named).
func isDirArg(arg string, scan bool) bool {
	if scan || strings.HasSuffix(arg, string(filepath.Separator)) {
		return true
	}
	info, err := os.Stat(arg)
	return err == nil && info.IsDir()
}
