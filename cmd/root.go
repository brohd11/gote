package cmd

import (
	"fmt"
	"io"
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
buffer, written on ctrl+s.`,
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
	// No shorthand: cobra hands -v to --version because Version is set, and it does that
	// only when nothing else has claimed the letter. Taking it here would silently strip
	// -v from --version rather than collide loudly.
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
// rather than inside app.Run because the argument grammar consults it: a bare argument
// naming nothing on disk may still name a configured vault.
func runRoot(cmd *cobra.Command, args []string) error {
	// Best-effort, and deliberately before the load: the file is where the schema is
	// documented, so a user who never runs `gote config` should still end up with one to
	// read. A failed write (a read-only home, say) is not a reason to refuse an editor,
	// and LoadConfig treats the still-missing file as the defaults anyway.
	_, _ = app.EnsureConfig()
	cfg, err := app.LoadConfig()
	if err != nil {
		return err
	}
	opts, list, err := resolveOptions(args, flags{
		scan:     scan,
		depth:    depth,
		depthSet: cmd.Flags().Changed("depth"),
		exts:     exts,
		extsSet:  cmd.Flags().Changed("ext"),
		vault:    vault,
		preview:  preview,
	}, func(name string) (string, bool, error) { return app.LookupVault(cfg, name) })
	// Printed before the error is returned: an unnamed vault lists and succeeds, a
	// misnamed one lists and fails, and both want the list. Cobra writes its Error: line
	// to stderr after RunE returns, so on a terminal the list reads first.
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

// printVaults writes the configured vaults as name and path columns, marking the config's
// default. Paths print as config.yml writes them, ~ and all: this listing is also what a
// misspelled name gets, so it has to render a vault whose directory has gone missing.
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
// --vault replaces that whole ladder with its one rung: the argument is a vault name,
// the filesystem is never consulted, and "here" is not a keyword. It is the way to reach
// a vault the cwd shadows, and it turns the one reading that cannot fail — an unknown
// name opening an empty buffer — into a listing, reported by the returned listVaults so
// the caller does the printing. A name that IS configured but broken still errors
// without a listing: it matched, and the path is the problem.
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
