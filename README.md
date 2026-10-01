# gote

A terminal text editor built with Go and Bubble Tea. Edit a single file with a
minimal interface, or work across a folder or named vault with tabs and editor groups.

- Syntax highlighting, language-aware indentation, and mouse support
- Markdown live preview, Reader mode, and side-by-side preview
- Find in files, Git status colors, and inline diff inspection
- Optional language servers for completion, diagnostics, navigation, and formatting

## Install

macOS and Linux:

```sh
curl -fsSL https://raw.githubusercontent.com/brohd11/gote/main/install.sh | sh
```

Windows (PowerShell):

```powershell
irm https://raw.githubusercontent.com/brohd11/gote/main/install.ps1 | iex
```

Update with `gote update`. See [Installation](docs/installation.md) for install
locations, terminal setup, and platform notes.

## Quick start

```sh
gote here             # Browse text files in the current directory
gote notes.md         # Edit one file; create it on save if it does not exist
gote ~/notes          # Browse a folder
gote -P notes.md      # Open Markdown in Reader mode
gote                  # Open the configured default folder or vault
gote config           # Edit ~/.gote/config.yml using $EDITOR or $VISUAL
```

Save with **Ctrl+S**, close the current buffer with **Alt+W**, and quit with
**Ctrl+Q** (confirms unsaved changes). **Ctrl+C / Ctrl+X / Ctrl+V** copy, cut, and
paste. **Alt+?** opens shortcut help while editing; **?** also works outside text entry.

Project mode has File, Edit, View, and Options menus. Click their labels or use
**Alt+F / Alt+E / Alt+V / Alt+O**. While editing, Alt+F moves forward a word;
open Edit with Alt+E and press Left to reach File.

On macOS Terminal, enable **Use Option as Meta Key** to send Alt shortcuts.
See [terminal setup](docs/installation.md#terminal-setup).

## Documentation

- [Documentation index](docs/README.md)
- [Installation](docs/installation.md) — install, update, and terminal setup
- [CLI reference](docs/cli.md) — launch modes, vault selection, flags, and scan depth
- [Configuration](docs/configuration.md) — defaults, colors, and saved preferences
- [Editing](docs/editing.md) — files, vaults, tabs, groups, shortcuts, and search
- [Markdown](docs/markdown.md) — live preview, Reader mode, and side preview
- [Git](docs/git.md) — status colors, gutters, and diff inspection
- [Language servers](docs/language-servers.md) — setup, language tools, and diagnostics
