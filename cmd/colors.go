package cmd

import (
	"os"

	"github.com/brohd11/gote/internal/app"

	"github.com/charmbracelet/colorprofile"
	"github.com/spf13/cobra"
)

var colorsBasic bool

// colorsCmd helps tune syntax_colors: it prints the slots, a highlighted sample and the
// whole 256-color space in the active palette, since an ANSI index says nothing about
// how it looks.
var colorsCmd = &cobra.Command{
	Use:   "colors [file]",
	Short: "Show the syntax palette, a highlighted sample, and the 256-color space",
	Long: "Show the syntax palette in effect, a highlighted sample, and the 256-color\n" +
		"space to pick from. Pass a file to highlight it instead of the built-in samples.\n" +
		"Colors are set under syntax_colors in `gote config`.",
	Args:         cobra.MaximumNArgs(1),
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := app.LoadConfig()
		if err != nil {
			return err
		}
		var path string
		if len(args) == 1 {
			path = args[0]
		}
		// Through a colorprofile.Writer, which downsamples to the terminal's profile as
		// bubbletea would; otherwise the swatches could show colors the editor cannot.
		out := colorprofile.NewWriter(cmd.OutOrStdout(), os.Environ())
		return app.RenderPalette(out, cfg, app.RenderOptions{Path: path, Basic: colorsBasic, Profile: out.Profile})
	},
}

func init() {
	colorsCmd.Flags().BoolVar(&colorsBasic, "basic", false,
		"render with the basic_colors palette instead of the configured one")
	rootCmd.AddCommand(colorsCmd)
}
