# CLI reference

[Documentation index](README.md)

```text
gote [here|dir|file|vault] [depth]
```

## Launch modes

| Command | Behavior |
| --- | --- |
| `gote` | Open the configured `default` directory or vault; otherwise use `~/.gote/docs` |
| `gote here` | Scan the current directory |
| `gote ~/notes 3` | Scan a directory to the given depth |
| `gote notes.md` | Open one file in the minimal editor; a missing file is created on save |
| `gote main-vault` | Open a configured vault when the argument names nothing on disk |
| `gote --vault main-vault` | Treat the argument as a vault name even if a local path matches |
| `gote --vault` | List configured vaults |
| `gote -P notes.md` | Open Markdown directly in Reader mode; Esc returns to editing |

`here` is a keyword; use `./here` for a directory with that name. Existing paths take
precedence over vault names. An unknown explicit vault name prints the vault list
and an error. A configured vault whose directory is missing is a launch error.

Directory and vault launches provide the multi-document interface. A file launch
starts with only the editor, without the menu bar, sidebar, tabs, or help bar.
See [single-file mode](editing.md#single-file-mode) for its panel restrictions.

## Flags and file discovery

| Flag | Meaning |
| --- | --- |
| `--scan`, `-s` | Treat the argument as a directory to scan; implied for an existing directory |
| `--depth`, `-d` | Set scan depth in directory levels |
| `--ext` | Restrict discovery to extensions, repeatable or comma-separated |
| `--vault` | Select or list configured vaults |
| `--preview`, `-P` | Start a Markdown file in Reader mode; ignored for other file types |
| `--version`, `-v` | Print the version |
| `--help`, `-h` | Show command help |

By default, discovery includes any text file. The config's `extensions` list can
narrow it; `--ext` overrides that list for one run:

```sh
gote --ext=md,txt here
gote --ext= here       # Any text file, even with a narrowed config
gote --scan           # Scan the current directory
```

`--scan` and `--vault` cannot be combined. The positional depth is valid for
folder/vault scans, not a single file.

## Scan depth

Depth is chosen in this order:

1. Positional depth, such as `gote here 3`
2. `--depth`
3. `GOTE_DEPTH`
4. The config's `scan_depth` (default 5)

```sh
export GOTE_DEPTH=2
gote here             # Uses depth 2
gote here 4           # Uses depth 4
GOTE_DEPTH= gote here  # Ignore the environment default for this run
```

Malformed or negative depths are rejected. A blank `GOTE_DEPTH` is ignored.

## Utility commands

| Command | Purpose |
| --- | --- |
| `gote config` | Edit the config with `$EDITOR`, then `$VISUAL`; Windows falls back to Notepad |
| `gote config --dir` | Open the config directory in the system file manager |
| `gote colors [file]` | Show the detected color profile, effective palette, and a highlighted sample |
| `gote colors --basic` | Preview the basic terminal palette |
| `gote update [--check]` | Install an update, or check without installing |

See [Configuration](configuration.md) and [Installation](installation.md) for details.
