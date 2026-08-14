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
	exts  []string
)

// flags is the parsed flag state the argument grammar reads, kept as a struct so
// resolveOptions' signature does not grow a positional parameter per flag added.
type flags struct {
	scan     bool
	depth    int
	depthSet bool
	exts     []string
	extsSet  bool
}

// hereArg is the keyword that means "scan the current directory". It wins over a
// directory of the same name — `gote ./here` is the way to reach that one.
const hereArg = "here"

var rootCmd = &cobra.Command{
	Use:   "gote [here|dir|file|vault] [depth]",
	Short: "A simple text editor (TUI)",
	Long: `gote is a simple TUI text editor. With no arguments it opens the default vault
named in ~/.gote/config.yml, or the ~/.gote/docs document store when no valid default
is configured. Given a directory it lists every matching file found by a recursive
scan, to the depth given as a second argument. Given the name of a configured vault it
opens that vault. Given a file it opens that file alone, with the sidebar and
surrounding chrome hidden — the shape to use as your $EDITOR.

Any text file is a document. Set extensions in the config to narrow that permanently,
or --ext to narrow one run; --ext with no value widens a narrowed config back again.

  gote                  # configured default vault, otherwise ~/.gote/docs
  gote here             # scan the current directory, config depth
  gote here 3           # scan the current directory, depth 3
  gote ~/notes 4        # scan ~/notes, depth 4
  gote main-vault       # open the configured vault named main-vault
  gote notes.md         # edit one file, nothing else on screen
  gote --ext=md here    # scan the current directory, markdown only
  gote --ext=           # every text file, whatever the config says

"here" is a keyword, not a path: use ./here to scan a directory of that name. A vault
name is only consulted for an argument that names nothing on disk, so ./main-vault
still reaches a file of that name. A file argument that does not exist yet opens an
empty buffer, written on ctrl+s.`,
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
	// Flags, not PersistentFlags: --ext means nothing to `config` or `update` and has no
	// business in their help. On the root it already works in either position — `here` is
	// a positional argument rather than a subcommand, and pflag parses flags interspersed
	// with positionals, so `gote --ext=md here` and `gote here --ext=md` are the same.
	rootCmd.Flags().StringSliceVar(&exts, "ext", nil,
		"limit discovery to these extensions, overriding the config (repeatable, or comma-separated; empty means any text file)")
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

// runRoot resolves the launch options and starts the TUI. The config is loaded here
// rather than inside app.Run because the argument grammar consults it: a bare argument
// naming nothing on disk may still name a configured vault.
func runRoot(cmd *cobra.Command, args []string) error {
	cfg, err := app.LoadConfig()
	if err != nil {
		return err
	}
	opts, err := resolveOptions(args, flags{
		scan:     scan,
		depth:    depth,
		depthSet: cmd.Flags().Changed("depth"),
		exts:     exts,
		extsSet:  cmd.Flags().Changed("ext"),
	}, func(name string) (string, bool, error) { return app.LookupVault(cfg, name) })
	if err != nil {
		return err
	}
	return app.Run(version, cfg, opts)
}

// resolveOptions turns the CLI surface into the app's launch options. It is the whole
// of gote's argument grammar, kept apart from the cobra wiring so it can be tested
// without starting a program:
//
//   - no argument: config chooses the default vault later, or (with --scan) scan cwd
//   - "here": a scan of the cwd
//   - a directory (or --scan, or a trailing separator): a scan of it
//   - a name that is nothing on disk but IS a configured vault: that vault
//   - anything else, existing or not: that file, in the minimal editor
//
// The vault rung sits below the filesystem on purpose: everything that already names
// something real keeps meaning what it meant, so the reading can only change for an
// argument that used to open an empty buffer. ./main-vault is the way to reach a local
// file that shares a vault's name, the same escape hatch ./here has. lookupVault
// reports (path, configured, err); a configured vault whose path has gone bad is a
// launch error rather than a silent fall-through to a file of that name.
//
// The second argument is the scan depth, overriding --depth; it applies to a vault as
// it does to any other recursive root, and is meaningless for a file — rejected there
// rather than ignored, since a rejected typo beats a silently dropped one. Paths are
// made absolute — the scan root shows in the breadcrumb and the editor saves against
// the path it was given, neither of which should depend on the cwd once the program is
// running.
//
// --ext passes straight through to every mode: it filters the lists, and the app
// normalizes it (Ctx.New via NewDocFilter), so there is nothing to validate here.
func resolveOptions(args []string, f flags, lookupVault func(string) (string, bool, error)) (app.Options, error) {
	scan := f.scan
	opts := app.Options{Depth: f.depth, DepthSet: f.depthSet, Exts: f.exts, ExtsSet: f.extsSet}

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
	if lookupVault != nil && !exists(arg) {
		path, configured, err := lookupVault(arg)
		if err != nil {
			return opts, err
		}
		if configured {
			opts.Mode, opts.Vault, opts.Dir = app.ModeVault, arg, path
			return opts, nil
		}
	}
	if len(args) > 1 {
		return opts, fmt.Errorf("a depth applies only to a directory scan, and %q is a file", arg)
	}
	opts.Mode, opts.File = app.ModeFile, abs
	return opts, nil
}

// exists reports whether arg names anything at all on disk, which is what keeps the
// vault rung from stealing an argument that already means a file.
func exists(arg string) bool {
	_, err := os.Lstat(arg)
	return err == nil
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
