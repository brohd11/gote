package cmd

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"

	"github.com/brohd11/gote/internal/app"
	"github.com/brohd11/goutil/envopt"

	"github.com/spf13/cobra"
)

// version is stamped by the makefile via -X ldflags; "dev" for a plain go build.
var version = "dev"

var (
	scan    bool
	depth   int
	exts    []string
	vault   bool
	preview bool
)

// flags is the parsed flag state the argument grammar reads, kept as a struct so
// resolveOptions' signature does not grow a positional parameter per flag added.
type flags struct {
	scan     bool
	depth    int
	depthSet bool
	exts     []string
	extsSet  bool
	vault    bool
	preview  bool
}

// hereArg is the keyword that means "scan the current directory". It wins over a
// directory of the same name — `gote ./here` is the way to reach that one.
const hereArg = "here"

// depthEnv supplies a default scan depth.
const depthEnv = "GOTE_DEPTH"

// resolveDepth picks the scan depth: a typed flag, else $GOTE_DEPTH, else the default.
// set reports whether either source spoke, to tell an explicit 0 from silence.
func resolveDepth(flagDepth int, flagChanged bool) (depth int, set bool, err error) {
	return envopt.Int(depthEnv, flagDepth, flagChanged)
}

var rootCmd = &cobra.Command{
	Use:   "gote [here|dir|file|vault] [depth]",
	Short: "A simple text editor (TUI)",
	Long: `gote is a simple TUI text editor. With no arguments it opens what the "default"
key in ~/.gote/config.yml names — a directory path, or a configured vault's name — and
the ~/.gote/docs document store when no valid default is configured. Given a directory
it lists every matching file found by a recursive
scan, to the depth given as a second argument. Given the name of a configured vault it
opens that vault. Given a file it opens that file alone, with the sidebar and
surrounding chrome hidden — the shape to use as your $EDITOR.

Any text file is a document. Set extensions in the config to narrow that permanently,
or --ext to narrow one run; --ext with no value widens a narrowed config back again.

  gote                  # the config's default dir or vault, otherwise ~/.gote/docs
  gote here             # scan the current directory, config depth
  gote here 3           # scan the current directory, depth 3
  gote ~/notes 4        # scan ~/notes, depth 4
  gote main-vault       # open the configured vault named main-vault
  gote notes.md         # edit one file, nothing else on screen
  gote --ext=md here    # scan the current directory, markdown only
  gote --ext=           # every text file, whatever the config says
  gote --vault mv       # the vault named mv, even when ./mv exists
  gote --vault          # list the configured vaults
  gote -P notes.md      # read one markdown file, full screen

"here" is a keyword, not a path: use ./here to scan a directory of that name. A vault
name is only consulted for an argument that names nothing on disk, so ./main-vault
still reaches a file of that name; --vault is the way back, reading its argument as a
vault name and nothing else. A file argument that does not exist yet opens an empty
buffer, written on ctrl+s.

Set GOTE_DEPTH to the depth you always want and every scan starts there, without the
config's scan_depth and without typing a number. A depth given as an argument or with
--depth still wins; GOTE_DEPTH= (blank) drops it for one run.`,
	Version:       version,
	Args:          cobra.MaximumNArgs(2),
	SilenceUsage:  true,
	SilenceErrors: false,
	RunE:          runRoot,
}

func init() {
	rootCmd.SetVersionTemplate("gote {{.Version}}\n")
	rootCmd.Flags().BoolVarP(&scan, "scan", "s", false, "treat the argument as a directory to scan (implied when it is one)")
	rootCmd.Flags().IntVarP(&depth, "depth", "d", 0, "scan depth in directory levels")
	// Show the real default (the resolveDepth ladder) in --help instead of the flag's 0.
	rootCmd.Flags().Lookup("depth").DefValue = "$GOTE_DEPTH, else the config's scan_depth"
	// Root-only flags: --ext means nothing to `config` or `update`. It works before or after
	// `here`, since that is a positional argument.
	rootCmd.Flags().StringSliceVar(&exts, "ext", nil,
		"limit discovery to these extensions, overriding the config (repeatable, or comma-separated; empty means any text file)")
	// No -v shorthand: cobra gives -v to --version only when nothing else claims it.
	rootCmd.Flags().BoolVar(&vault, "vault", false,
		"read the argument as a configured vault name rather than a path; with no name, or one that matches no vault, list the vaults instead")
	rootCmd.Flags().BoolVarP(&preview, "preview", "P", false,
		"open a markdown file straight into the full-screen reader (ignored for anything else)")
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

// runRoot resolves the launch options and starts the TUI. The config is loaded here
// because a bare argument may name a configured vault.
func runRoot(cmd *cobra.Command, args []string) error {
	// Best effort, before the load: the file documents the schema. A failed write (read-only
	// home) is no reason to refuse to start.
	_, _ = app.EnsureConfig()
	cfg, err := app.LoadConfig()
	if err != nil {
		return err
	}
	d, dSet, err := resolveDepth(depth, cmd.Flags().Changed("depth"))
	if err != nil {
		return err
	}
	opts, list, err := resolveOptions(args, flags{
		scan:     scan,
		depth:    d,
		depthSet: dSet,
		exts:     exts,
		extsSet:  cmd.Flags().Changed("ext"),
		vault:    vault,
		preview:  preview,
	}, func(name string) (string, bool, error) { return app.LookupVault(cfg, name) })
	// Print the vault list before returning any error: both a bare --vault and a misnamed one
	// want it, and cobra prints its error afterwards.
	if list {
		printVaults(cmd.OutOrStdout(), app.VaultList(cfg))
	}
	if err != nil {
		return err
	}
	if list {
		return nil
	}
	return app.Run(version, cfg, opts)
}

// printVaults writes the vaults as name and path columns, marking the default. Paths print
// as configured (with ~), so a vault whose directory is missing still lists.
func printVaults(w io.Writer, entries []app.VaultEntry) {
	if len(entries) == 0 {
		fmt.Fprintln(w, "No vaults are configured. Add one from the Vaults menu, or run `gote config`.")
		return
	}
	width := 0
	for _, e := range entries {
		if n := len(e.Name); n > width {
			width = n
		}
	}
	fmt.Fprintln(w, "Configured vaults:")
	for _, e := range entries {
		line := fmt.Sprintf("  %-*s  %s", width, e.Name, e.Path)
		if e.Default {
			line += "  · default"
		}
		fmt.Fprintln(w, line)
	}
}

// resolveOptions is gote's argument grammar, separate from cobra so it can be tested:
//   - no argument: the config's default (or, with --scan, a scan of cwd)
//   - "here": a scan of cwd
//   - a directory, a trailing separator, or --scan: a scan of it
//   - a name that is nothing on disk but is a configured vault: that vault
//   - anything else, existing or not: that file in the minimal editor
//
// Vaults rank below the filesystem, so ./name reaches a local file that shares a vault's
// name. A configured vault with a bad path is a launch error. --vault reads the argument
// only as a vault name; an unknown name then returns listVaults instead of opening a file.
//
// The second argument is the scan depth (overriding --depth); it is rejected for a file.
// Paths are made absolute. --ext passes through for the app to normalize.
func resolveOptions(args []string, f flags, lookupVault func(string) (string, bool, error)) (opts app.Options, listVaults bool, err error) {
	scan := f.scan
	// Preview rides along unjudged: it asks for a reader the launch may have nothing to
	// show in, and the app is where that is known, so no rung below has to consider it.
	opts = app.Options{Depth: f.depth, DepthSet: f.depthSet, Exts: f.exts, ExtsSet: f.extsSet, Preview: f.preview}

	if f.vault && scan {
		return opts, false, fmt.Errorf("--scan cannot be combined with --vault")
	}

	if len(args) > 1 {
		n, err := strconv.Atoi(args[1])
		if err != nil {
			return opts, false, fmt.Errorf("depth %q is not a number", args[1])
		}
		if n < 0 {
			return opts, false, fmt.Errorf("depth %d is negative", n)
		}
		opts.Depth, opts.DepthSet = n, true
	}
	if opts.Depth < 0 {
		return opts, false, fmt.Errorf("depth %d is negative", opts.Depth)
	}

	if f.vault {
		if len(args) == 0 || args[0] == "" {
			return opts, true, nil
		}
		name := args[0]
		var path string
		configured := false
		if lookupVault != nil {
			var err error
			if path, configured, err = lookupVault(name); err != nil {
				return opts, false, err
			}
		}
		if !configured {
			return opts, true, fmt.Errorf("vault %q is not configured", name)
		}
		opts.Mode, opts.Vault, opts.Dir = app.ModeVault, name, path
		return opts, false, nil
	}

	if len(args) == 0 {
		if scan {
			opts.Mode = app.ModeScan
			dir, err := os.Getwd()
			if err != nil {
				return opts, false, err
			}
			opts.Dir = dir
		}
		return opts, false, nil
	}

	arg := args[0]
	if arg == hereArg {
		dir, err := os.Getwd()
		if err != nil {
			return opts, false, err
		}
		opts.Mode, opts.Dir = app.ModeScan, dir
		return opts, false, nil
	}

	abs, err := filepath.Abs(arg)
	if err != nil {
		return opts, false, err
	}
	if isDirArg(arg, scan) {
		opts.Mode, opts.Dir = app.ModeScan, abs
		return opts, false, nil
	}
	if lookupVault != nil && !exists(arg) {
		path, configured, err := lookupVault(arg)
		if err != nil {
			return opts, false, err
		}
		if configured {
			opts.Mode, opts.Vault, opts.Dir = app.ModeVault, arg, path
			return opts, false, nil
		}
	}
	if len(args) > 1 {
		return opts, false, fmt.Errorf("a depth applies only to a directory scan, and %q is a file", arg)
	}
	opts.Mode, opts.File = app.ModeFile, abs
	return opts, false, nil
}

// exists reports whether arg names anything at all on disk, which is what keeps the
// vault rung from stealing an argument that already means a file.
func exists(arg string) bool {
	_, err := os.Lstat(arg)
	return err == nil
}

// isDirArg reports whether arg names a directory to scan: an existing directory, a
// trailing separator, or anything under --scan (so a not-yet-existing one works).
func isDirArg(arg string, scan bool) bool {
	if scan || len(arg) > 0 && os.IsPathSeparator(arg[len(arg)-1]) {
		return true
	}
	info, err := os.Stat(arg)
	return err == nil && info.IsDir()
}
