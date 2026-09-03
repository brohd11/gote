# gote - text editor

simple TUI text editor built with Go and Bubbletea.

## Features
 - simple text editing
 - minimal markdown previewer
 - syntax highlighting for select extensions
 - diagnostics and completion through language servers for nine languages
 - brace-aware indent on Enter for the C family, Go, Rust, JS/TS and friends
 - ctrl+/ toggles comments, over a selection or a single line
 - mouse support for scrolling, selection, right click
 - vaults store a collection of files for a focused view

**Note:** `ctrl+/` is bound as `ctrl+_`, because that is the key code the chord actually
produces — terminals put byte `0x1f` on the wire for it. Should your terminal swallow it
entirely, `alt+/` does the same thing.

**Note:** on MacOS, option is treated as alt, but the key does not reach the terminal input by default.
`Terminal -> Settings -> Profiles -> Keyboard -> Use Option as Meta Key`

## gote works in 2 modes:

### Multi Document
Default mode, shows a sidebar with docs in a location folder, as well as a list of open docs.
`gote` opens the editor in the default location configured in `~/.gote/config.yml` and scans the folder recursively for docs.
The `default:` key takes either a directory path (`~/notes`) or the name of a configured vault; Non valid setting falls back to default: `~/.gote/docs`.

The sidebar lists the scan flat by default; `alt+t` swaps it for a folder-by-folder view of
the same tree (`alt+r` there switches row density). Set `folder_view: true` in the config to
open on the folder view instead — it only picks the starting view, the scan runs either way.

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

Run `gote config` to edit `~/.gote/config.yml`.
Set `indent_guides: true` there to draw faint leading-indent guides in the editor; the
default is `false`.

#### Language servers

Language-server support starts lazily when a supported file is opened. Every server is
optional: gote spawns one only when you open a file for it, and a server that isn't installed
costs one status line, not a broken editor.

| Files | Server | Install |
| --- | --- | --- |
| `.c .h .cc .cpp .hpp .hh` | [`clangd`](https://clangd.llvm.org) | ships with Xcode CLT, or `brew install llvm` |
| `.go` | [`gopls`](https://pkg.go.dev/golang.org/x/tools/gopls) | `go install golang.org/x/tools/gopls@latest` |
| `.py` | [`pylsp`](https://github.com/python-lsp/python-lsp-server) | `pip install python-lsp-server` |
| `.sh .bash` + `#!` shells | [`bash-language-server`](https://github.com/bash-lsp/bash-language-server) | `npm i -g bash-language-server` |
| `.rs` | [`rust-analyzer`](https://rust-analyzer.github.io) | `rustup component add rust-analyzer` |
| `.js .jsx .ts .tsx` | [`typescript-language-server`](https://github.com/typescript-language-server/typescript-language-server) | `npm i -g typescript-language-server typescript` |
| `.cs` | [`csharp-ls`](https://github.com/razzmatazz/csharp-language-server) | `dotnet tool install -g csharp-ls` |
| `.lua` | [`lua-language-server`](https://luals.github.io) | `brew install lua-language-server` |
| `.gd` | the Godot editor's own server | run the Godot project |

GDScript is the one that is not a subprocess: it connects to `127.0.0.1:6005`, so the matching
Godot project has to already be open in Godot. Anything installed with `go install` or
`dotnet tool install` lands in a directory (`$(go env GOPATH)/bin`, `~/.dotnet/tools`) that has
to be on your PATH for the bare command to start — otherwise put the full path in `command`.

C# is the weak spot, and deliberately so. The good server — Microsoft's Roslyn language server
— ships as a payload inside the VS Code C# extension and loads nothing from a standard LSP
handshake, waiting instead on a non-standard notification naming a solution, so a general
client cannot drive it. `csharp-ls` is the standalone one that speaks plain LSP; it is less
capable, but it works. Its root is found by pattern (`*.sln`, then `*.csproj`), since those
files are named after the project rather than by convention.

A Go file inside a `go.work` workspace starts one server for the whole workspace rather than
one per module, so cross-module definitions resolve and a monorepo costs a single gopls. A
module with no workspace above it roots at its own `go.mod`.

`.zsh` and `.fish` files get the shell editing behavior — quote and bracket pairing, and
indent on Enter — but no language server: `bash-language-server` reports zsh-only and fish
syntax as errors. It also analyzes the whole workspace in the background, which for a file
with no project root above it means the directory it sits in; narrow that with a
`globPattern` under its `initialization_options` if you open shell files from a large tree.

Diagnostics appear in their own gutter column and in Actions → Diagnostics. The Actions
and editor context menus independently toggle the diagnostics and git gutters and can
restart failed server connections. Set `auto-lsp: false`, disable an individual entry,
or override its `address`/`command` in `~/.gote/config.yml` to change those defaults.
Server-specific `initialization_options` can also be overridden; Python's built-in
options enable pylsp's parameter snippets for callable completions.

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

**macOS note:** a binary downloaded **in a browser** gets quarantined by Gatekeeper. Clear it
with `xattr -dr com.apple.quarantine path/to/binary`. This doesn't apply to the installer
above; the attribute is set by browsers, not by `curl`.
