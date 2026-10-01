# Language servers

[Documentation index](README.md)

Language servers start lazily when a supported file opens. Each is optional:
a missing server produces status feedback while the editor remains usable.
Available features depend on the server's advertised capabilities.

## Supported servers and setup

Install the server for the languages you use:

| Files | Server | Install |
| --- | --- | --- |
| `.c .h .cc .cpp .hpp .hh` | [clangd](https://clangd.llvm.org) | Xcode CLT, or `brew install llvm` |
| `.go` | [gopls](https://pkg.go.dev/golang.org/x/tools/gopls) | `go install golang.org/x/tools/gopls@latest` |
| `.py` | [pylsp](https://github.com/python-lsp/python-lsp-server) | `pip install python-lsp-server` |
| `.sh .bash` and supported shell shebangs | [bash-language-server](https://github.com/bash-lsp/bash-language-server) | `npm i -g bash-language-server` |
| `.rs` | [rust-analyzer](https://rust-analyzer.github.io) | `rustup component add rust-analyzer` |
| `.js .jsx .ts .tsx` | [typescript-language-server](https://github.com/typescript-language-server/typescript-language-server) | `npm i -g typescript-language-server typescript` |
| `.cs` | [csharp-ls](https://github.com/razzmatazz/csharp-language-server) | `dotnet tool install -g csharp-ls` |
| `.lua` | [lua-language-server](https://luals.github.io) | `brew install lua-language-server` |
| `.gd` | Godot editor's server | Open the matching project in Godot |

Server executables must be on PATH, or their full paths must be set in `command`.
For Go-installed tools this usually means `$(go env GOPATH)/bin`; for .NET tools,
`~/.dotnet/tools`. Windows `.cmd` wrappers are supported.

GDScript connects to `127.0.0.1:6005` instead of starting a subprocess. It requires
`project.godot` in the file's project ancestry and the matching project open in Godot.

A Go module inside a `go.work` workspace shares one gopls for that workspace; a
standalone module roots at its `go.mod`. C# uses `csharp-ls`, with project discovery
through `*.sln` and `*.csproj` files.

Zsh and Fish receive shell editing behavior but no language server. For Bash files
without a project marker, the server root falls back to the file's directory. If
that tree is large, use the server's `globPattern` initialization option to narrow
its background analysis.

## Configuration

Both `auto-lsp` and the launch mode's `default_allow_lsp` must be true. Disable a
single server or override its transport under `language_servers`:

```yaml
auto-lsp: true
language_servers:
  go:
    command: [gopls]
  gdscript:
    address: 127.0.0.1:6005
  python:
    disabled: true
```

The server keys are `clangd`, `go`, `python`, `bash`, `rust`, `typescript`, `csharp`,
`lua`, and `gdscript`. `command` is an argument list; `address` connects to an
externally managed server. Use `gote config` to see the built-in commands and options.

Each server can supply an `initialization_options` mapping. Built-in Python options
enable parameter snippets for callable completions. Go completes the function name
without parentheses, so typing `(` triggers signature help. Overriding initialization
options replaces that server's built-in options; retain any defaults you want.

Use **Options → LSP → Restart language servers** to restart the session's servers.
For mode defaults, see [Configuration](configuration.md#startup-display-and-language-tools).

## Language tools

These shortcuts work from the editor:

| Key | Action |
| --- | --- |
| Ctrl+Space | Completion |
| Alt+Shift+G | Go to definition; list choices when there is more than one |
| Alt+Shift+B | Jump back through navigation history |
| Alt+Shift+H | Hover information at the cursor |
| Alt+Shift+O | Show/hide the document outline |
| Alt+Shift+R | Find references |
| Alt+Shift+M | Organize imports and format the document |

Signature help appears when you open an argument list and follows the active
parameter. Unsupported features are reported as unavailable.

Alt+click goes to definition by default. Right-click or Ctrl+click opens clipboard
and symbol actions at the clicked location: Hover info, Go to definition, and Find
references. Single-file mode omits the latter two context-menu actions. The click
modifiers are [configurable](configuration.md#mouse-gestures); hover information is
requested through a shortcut/menu, not by moving the pointer.

## Diagnostics and outline

Diagnostics share the [bottom panel](editing.md#search-and-the-bottom-panel) with
Find in Files. **Options → LSP → Show diagnostics** reveals the Diag tab.
Alt+pipe toggles the panel without taking focus from the editor. The active file's
diagnostics appear first. **Options → LSP → Diagnostics gutter** toggles severity
markers beside the editor.

Diagnostic scope depends on the server. Gote accepts pushed diagnostics and requests
workspace diagnostics from servers that support them; it does not open every file
to force analysis. Diagnostics can therefore include unopened files. Workspace-level
diagnostics survive closing a tab; diagnostics tied to an open document are retired
with it when the server does not provide project-wide coverage.

The outline starts hidden below Docs. Alt+Shift+O or **View → Outline** toggles it.
It is a filterable tree: Enter or a click jumps to a symbol, Left/Right and Space
fold branches, and selection follows the enclosing symbol while editing. It refreshes
after document switches and settled edits. Servers without document-symbol support
show an unavailable message.

Single-file mode locks the outline, bottom panel, and Find in Files by default;
see [single-file mode](editing.md#single-file-mode).

## Formatting

Alt+Shift+M or **Options → LSP → Format document** organizes imports and formats the
buffer where supported. Save afterwards to write the changes. If you edit while a
format request is pending, gote skips the stale result.

`format_on_save: true` requests formatting after Ctrl+S writes the file. If formatting
changes the buffer, it is dirty again and needs another save. The default is false.
