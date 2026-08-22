# gote - text editor

simple TUI text editor built with Go and Bubbletea.

## Features
 - simple text editing
 - minimal markdown previewer
 - syntax highlighting for select extensions
 - mouse support for scrolling, selection, right click
 - vaults store a collection of files for a focused view

**Note:** on MacOS, option is treated as alt, but the key does not reach the terminal input by default.
`Terminal -> Settings -> Profiles -> Keyboard -> Use Option as Meta Key`

## gote works in 2 modes:

### Multi Document
Default mode, shows a sidebar with docs in a location folder, as well as a list of open docs.
`gote` opens the editor in the default location configured in `~/.gote/config.yml` and scans the folder recursively for docs.
The `default:` key takes either a directory path (`~/notes`) or the name of a configured vault; Non valid setting falls back to default: `~/.gote/docs`.

`gote here [depth:int]` Opens gote in the current directory and scans `depth` folders deep for docs.
Without a depth it uses `scan_depth` from the config (5 by default).

Set `GOTE_DEPTH` to scan a different depth without typing one every run — `export GOTE_DEPTH=2`
and every scan starts two folders deep, config included. Anything typed still wins: the depth
argument beats `--depth`, which beats the variable. A malformed or negative value is refused
rather than quietly ignored, and a blank one (`GOTE_DEPTH= gote here`) drops it for a single run.

#### Vaults

Create a vault, and you can open by name `gote <my-vault>`. This scans recursively for docs as well.

**Note:** If the passed argument is a valid relative path and clashes with a vault, the relative path will be selected.
Pass `--vault` to read the argument as a vault name.

`gote --vault` lists the configured vaults, as does a vault that doesn't exist.


### Single Document

`gote <my/file.md>`

Open the editor with a single document. Useful if you have your terminal default editor set to gote.

`gote -P <my/file.md>`

Open a markdown file straight into the full-screen reader, with the rest of the interface out of the way.
`esc` drops into the editor. Preview only works for `md` files, otherwise just launches gote.


## Install

Unix:
```bash
curl -fsSL https://raw.githubusercontent.com/brohd11/gote/main/install.sh | sh
```

Windows:
```powershell
irm https://raw.githubusercontent.com/brohd11/gote/main/install.ps1 | iex
```

To update:
```
gote update
```
More install details (location, flags, etc): [shared install reference](https://github.com/brohd11/goutil/blob/main/docs/install.md).

<sub>macOS note: a binary downloaded **in a browser** gets quarantined by Gatekeeper — clear it
with `xattr -dr com.apple.quarantine path/to/binary`. This doesn't apply to the installer
above; the attribute is set by browsers, not by `curl`.</sub>