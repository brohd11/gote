# Configuration

[Documentation index](README.md)

Settings live in `~/.gote/config.yml`. Run `gote config` to add missing defaults and
open the file using `$EDITOR`, then `$VISUAL`. On Windows, Notepad is the fallback;
on macOS/Linux, set one of those variables. `gote config --dir` opens the containing
directory in the system file manager.

## Documents and vaults

```yaml
default: ~/.gote/docs
scan_depth: 5
extensions: []
file_view: flat
vaults:
  notes:
    path: ~/notes
```

`default` accepts a directory path or a configured vault name. An invalid default
falls back to `~/.gote/docs`. Vaults are named document folders; use `gote notes` or
`gote --vault notes` for the example above.

An empty `extensions` list discovers any text file. `file_view` accepts `flat`,
`folder`, or `grouped`. When it is empty, omitted, or unrecognized, the legacy
`folder_view: true` starts in Folder view; otherwise the default is Flat.

See [CLI reference](cli.md) for per-run overrides and
[Editing](editing.md#files-and-vaults) for file-view controls.

## Startup display and language tools

These are the per-mode defaults:

```yaml
project_mode:
  default_wrap: false
  default_line_numbers: false
  default_git_gutter: true
  default_diagnostics_gutter: true
  default_allow_lsp: true
single_file_mode:
  default_wrap: false
  default_line_numbers: false
  default_git_gutter: true
  default_diagnostics_gutter: false
  default_allow_lsp: true
  allow_panel_toggle: false
```

Project defaults apply to bare, directory, and vault launches; single-file defaults
apply to `gote <file>`. Omitted keys retain their defaults.

Wrap and line-number defaults apply to newly opened/restored documents and scratch
buffers. Alt+Z and Alt+L toggle them per document for the current session.
`indent_guides: true` enables faint leading-indent guides; the default is false.

`auto-lsp: true` is the default master switch for language servers. Both it and the
mode's `default_allow_lsp` must be true to allow a server. Single-file
`allow_panel_toggle` controls the outline, bottom panel, and Find in Files; see
[single-file mode](editing.md#single-file-mode).

`default_doc_mode: off` selects syntax highlighting for new Markdown documents;
`live` selects live preview. See [Markdown](markdown.md).

`format_on_save: false` is the default. When enabled, formatting happens **after**
the write: formatting changes leave the buffer dirty until the next save. See
[Formatting](language-servers.md#formatting).

Server commands, addresses, and initialization options are documented under
[Language-server configuration](language-servers.md#configuration).

## Syntax colors

`syntax_colors` accepts ANSI-256 indices as strings (such as `"133"`) or hex colors
(such as `"#af5faf"`). Run `gote config` to see the available slots and defaults.
Invalid colors fall back to their defaults. Set `syntax_colors.brackets: []` to
disable rainbow bracket colors.

Gote detects terminal color capability at startup. A 16-color terminal uses the
basic syntax palette; 256-color and true-color terminals use the configured palette.
This detection does not rewrite your config. To force the basic palette:

```yaml
syntax_colors:
  basic_colors: true
```

Use `gote colors` to inspect the detected profile and effective palette, or
`gote colors path/to/file.go` to preview a file. `gote colors --basic` previews the
basic palette without changing the config.

## Mouse gestures

```yaml
click_definition: alt
click_context: ctrl
```

Each setting accepts `alt`, `ctrl`, `shift`, or `none` (disabled). Terminals may
intercept a gesture before gote receives it; use the editor's right-click menu or
keyboard shortcuts when that happens. See [terminal setup](installation.md#terminal-setup).

## What persists

- Config edits set startup preferences. Switching file views or toggling wrap/line
  numbers during a session does not write those preferences.
- **View → Preview → Default** writes `default_doc_mode` and applies to documents
  opened afterwards. Each open Markdown buffer retains its own mode until closed.
- Project/vault sessions restore open files, cursor and scroll positions, tab groups,
  selected tabs, group widths, and the active group. State lives under `~/.gote/state`.
  Unsaved text and pathless buffers are not restored.
- Folder visibility toggles, group folds in the file list, and bottom-panel sizing
  are session controls, not startup preferences.
