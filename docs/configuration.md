# Configuration

[Documentation index](README.md)

Settings live in `~/.gote/config.yml`. Run `gote config` to add missing defaults and
open the file using `$EDITOR`, then `$VISUAL`. On Windows, Notepad is the fallback;
on macOS/Linux, set one of those variables. `gote config --dir` opens the containing
directory in the system file manager.

Per-language settings and language servers live separately in `~/.gote/lang.yml`;
see [Languages](#languages-langyml).

Inside gote, **File ▸ Config ▸ General** and **Language** open the same two files as
documents. Saving either one reloads the config in the running session, and so does
**File ▸ Refresh** (for edits made outside gote). A file that fails to parse changes
nothing; the status bar names the error. After a reload:

- the doc list follows `extensions` and `scan_depth` at once (unless `--ext`/`--depth`
  were given at launch), and vaults, `default`, `default_doc_mode`, `format_on_save` and
  the click gestures apply straight away;
- docs opened from then on follow the wrap, line-number and per-language indent settings,
  `kitty_keyboard` and the new `syntax_colors`;
- startup states you toggle at runtime (`file_view`, `indent_guides`, the gutters, the
  menu bar) keep their current state;
- language-server changes while a server is running, and new colors in docs already open,
  need a restart; the status bar says when.

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
  menu_bar: false
```

Project defaults apply to bare, directory, and vault launches; single-file defaults
apply to `gote <file>`. Omitted keys retain their defaults.

Wrap and line-number defaults apply to newly opened/restored documents and scratch
buffers. Alt+Z and Alt+L toggle them per document for the current session.
`indent_guides: true` (the default) draws faint leading-indent guides; set it to false to hide them.

`kitty_keyboard: false` (the default) suits most terminals: stock GNOME Terminal, xterm,
Konsole, Windows Terminal and macOS Terminal.app. They send Ctrl+Backspace as Ctrl+H, so
the editor reads Ctrl+H as Ctrl+Backspace (delete to line start). Set it to `true` if
the terminal speaks the Kitty keyboard protocol (kitty, Ghostty, WezTerm, foot, iTerm2
with CSI u), where every key arrives under its own name and Ctrl+H is plain backspace.
Also set it to `true` if your terminal's Backspace key itself sends Ctrl+H; otherwise
every backspace would clear the line. Alt+Shift+Backspace and Ctrl+U delete to line
start whatever this is set to.

`auto-lsp: false` (the default) leaves every language server off unless
[lang.yml](#languages-langyml) sets `enabled: true` on it. A server also needs the mode's
`default_allow_lsp`. Single-file
`allow_panel_toggle` controls the outline, bottom panel, and Find in Files, and
`menu_bar: true` shows the menu bar from launch; see
[single-file mode](editing.md#single-file-mode).

`default_doc_mode: live` (the default) selects live preview for new Markdown documents;
`off` selects syntax highlighting. See [Markdown](markdown.md).

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

## Languages (lang.yml)

`~/.gote/lang.yml` overrides language behavior and configures
[language servers](language-servers.md#configuration). `gote config lang` opens it.
gote writes it on first launch (or `gote config`) with the built-in servers and a
commented `languages` example. Everything is optional: a missing file, language,
or key keeps the built-in behavior.

```yaml
languages:
  go:
    indent: spaces     # tab | spaces
    indent_size: 2     # on its own, implies spaces; ignored with tab
servers:
  go:
    enabled: true      # overrides auto-lsp for this server
    command: [~/go/bin/gopls]
  python:
    enabled: false
```

`indent` and `indent_size` set the unit that Indent/Dedent (Alt+. and Alt+,) and the
indent guides use. Alt+I still switches it per buffer. Tab without a selection always inserts a
tab, and a tab displays 4 columns wide whatever these say.

A server's `enabled: true` runs it even with `auto-lsp: false`; `enabled: false` keeps
it off under `auto-lsp: true`; without the key it follows `auto-lsp`. A launch mode's
`default_allow_lsp: false` still wins. See
[Language servers](language-servers.md#configuration) for the server keys.

Languages are keyed by id; unknown ids are ignored. The ids and their built-in indents:

| Id | Files | Built-in indent |
| --- | --- | --- |
| `go` | `.go` | tab |
| `c`, `cpp` | `.c`; `.h .cc .cpp .hpp .hh` | tab |
| `python` | `.py` | 4 spaces |
| `rust`, `csharp`, `java`, `kotlin`, `swift`, `php`, `fish` | `.rs .cs .java .kt .swift .php .fish` | 4 spaces |
| `javascript`, `javascriptreact`, `typescript`, `typescriptreact` | `.js .jsx .ts .tsx` | 2 spaces |
| `json`, `yaml`, `css`, `scss`, `dart`, `protobuf`, `terraform`, `groovy`, `glsl` | `.json .yaml .yml .css .scss .dart .proto .tf .gradle .glsl` | 2 spaces |
| `shellscript`, `zsh` | `.sh .bash`; `.zsh` | 2 spaces |
| `markdown` | `.md .markdown` | 2 spaces |
| `gdscript`, `lua` | `.gd`; `.lua` | tab |
| `rb`, `perl`, `splus`, `mysql`, `html`, `xml`, `toml`, `ini`, `vim`, `make` | `.rb .pl .r .sql .html .xml .toml .ini .vim .mk` | tab |

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
