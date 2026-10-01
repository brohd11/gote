# Installation

[Documentation index](README.md)

## macOS and Linux

```sh
curl -fsSL https://raw.githubusercontent.com/brohd11/gote/main/install.sh | sh
```

The installer places `gote` in `~/.local/bin` by default. Follow its PATH instructions
if the command is not found. Published builds cover macOS and Linux on amd64 and arm64.

## Windows

Run in PowerShell:

```powershell
irm https://raw.githubusercontent.com/brohd11/gote/main/install.ps1 | iex
```

The default location is `%LOCALAPPDATA%\bin`. Open a new terminal after adding it
to PATH. The published Windows build is amd64.

Editors and language servers installed as `.cmd` wrappers are supported. Existing
CRLF files keep their line endings when saved; new and mixed-line-ending files use LF.

## Updates and installer options

```sh
gote update --check   # Check without installing
gote update           # Install a newer release
```

In project mode, **File → Update gote** also checks for updates. For custom install
locations, version pinning, PATH options, and download troubleshooting, see the
[shared install reference](https://github.com/brohd11/goutil/blob/main/docs/install.md).

On macOS, a binary downloaded through a browser may be quarantined. For a trusted
manual download, clear the attribute with:

```sh
xattr -dr com.apple.quarantine path/to/gote
```

The curl installer above does not add the browser quarantine attribute.

## Terminal setup

In macOS Terminal, enable **Terminal → Settings → Profiles → Keyboard → Use Option
as Meta Key**. Option then sends gote's Alt shortcuts.

Gote uses desktop-style clipboard keys: Ctrl+C copies, Ctrl+X cuts, and Ctrl+V
pastes. Use Ctrl+Q to quit; it confirms unsaved changes.

Ctrl+/ toggles comments. Terminals commonly encode it as `ctrl+_`; Alt+/ is an
alternative if your terminal does not pass Ctrl+/ through.

Terminals may intercept modified mouse clicks. The default gestures are Alt+click
for go to definition and Ctrl+click for the editor context menu. Right-click also
opens the menu. Configure `click_definition` and `click_context` if needed; see
[Configuration](configuration.md#mouse-gestures). Hover information is available
from the context menu or Alt+Shift+H.

For color detection and the basic terminal palette, see
[Syntax colors](configuration.md#syntax-colors).
