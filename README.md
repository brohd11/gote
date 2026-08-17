# gote - text editor

simple TUI text editor built with Go and Bubbletea.

## Install

```bash
curl -fsSL https://raw.githubusercontent.com/brohd11/gote/main/install.sh | sh
```

## Features
 - simple text editing
 - minimal markdown previewer
 - syntax highlighting for select extensions
 - mouse support for scrolling, selection, right click
 - vaults store a collection of files for a focused view

## gote works in 2 modes:

### Multi Document
Default mode, shows a sidebar with docs in a location folder, as well as a list of open docs. 
`gote` opens the editor in the default location configured in `~/.gote/config.yml` and scans the folder recursively for docs. The `default:` key takes either a directory path (`~/notes`) or the name of a configured vault; unset, or pointing at something that isn't there, it falls back to the `~/.gote/docs` store.

`gote here [depth:int]` Opens gote in the current directory and scans `depth` folders deep for docs.

Create a vault, and you can open by name `gote <my-vault>`. This scans recursively for docs as well.

**Note:** If the passed argument is a valid relative path and clashes with a vault, the relative path will be selected. Pass `--vault` to read the argument as a vault name.

`gote --vault` lists the configured vaults, as does a vault that doesn't exist.


### Single Document

`gote <my/file.md>`

Open the editor with a single document. Useful if you have your terminal default editor set to gote.

`gote -P <my/file.md>`

Open a markdown file straight into the full-screen reader, with the rest of the interface out of the way — `esc` drops into the editor behind it. Preview only works for `md` files, otherwise just launches gote.

