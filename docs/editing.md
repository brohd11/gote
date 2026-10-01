# Editing

[Documentation index](README.md)

## Menus and focus

Project mode provides **File**, **Edit**, **View**, and **Options** menus. Click a
label or use Alt+F, Alt+E, Alt+V, or Alt+O. While editing, Alt+F keeps its word-forward
behavior; use Alt+E, then Left to reach File. In an open menu, Left/Right switches
menus and an underlined letter selects its row.

Click a pane or use Shift+Tab to cycle focus. Esc switches between the editor and
the last focused pane. If that pane is gone, it falls back to Docs, revealing the
sidebar when necessary. Active filters and popups handle Esc first.

Alt+? opens shortcut help while editing; ? also works outside text entry.

## Essential shortcuts

| Key | Action |
| --- | --- |
| Ctrl+S | Save; ask for a filename for a pathless buffer |
| Ctrl+N | New unsaved tab (project mode) |
| Alt+W | Close the current buffer; exit in single-file mode |
| Ctrl+Q | Quit, confirming unsaved changes |
| Ctrl+C / Ctrl+X / Ctrl+V | Copy / cut / paste |
| Ctrl+/ or Alt+/ | Toggle comments for a selection or the current line |
| Ctrl+F | Search the current buffer |
| Ctrl+Alt+F | Find in Files |
| Alt+Z | Toggle soft wrapping |
| Alt+L | Toggle line numbers |
| Alt+9 / Alt+0 | Previous / next document in the active group |
| Alt+T / Ctrl+T | Move tab right (or split) / left |
| Alt+Backslash | Toggle the sidebar |
| Alt+Shift+O | Toggle the language-server outline |
| Alt+Shift+B | Return from a navigation jump |

Alt+pipe (`alt+|`) toggles the bottom panel while preserving editor focus. The
sidebar chord is `alt+\`. Markdown and language-tool shortcuts are covered in
[Markdown](markdown.md) and [Language servers](language-servers.md).

Supported languages provide syntax highlighting, bracket/quote pairing, comment
toggling, and language-aware indentation. Soft wrapping changes only the display:
Up/Down follows visible rows, Shift+Up/Down extends selection, and text/whitespace
are preserved. Wrap prefers whitespace boundaries and splits overlong words or URLs
when necessary.

## Files and vaults

The Docs sidebar lists text files in the current root. Select **View → File view**
for Flat, Folder, or Grouped view. Set `file_view` in the
[config](configuration.md#documents-and-vaults) to choose the starting view.

- **Flat:** a searchable list of scan results.
- **Folder:** browse directories with Enter or `d`; `x` goes up, bounded by the scan
  or vault root. Enter opens a file; `d` does nothing on a file. `.` toggles hidden
  files/directories for the session, retaining file-type and dependency-folder
  filters. Alt+R changes row density. These controls apply outside filter entry.
- **Grouped:** the same scan results grouped by complete root-relative folder path.
  Each folder contains only its direct files; `.` holds root files. Left/Right
  collapses/expands, Space toggles, and Enter or a click toggles a folder or opens a
  file. `/` searches even collapsed files.

Views retain their cursor and filter. Group folds survive view switches and
refreshes. Ctrl+R renames a selected file and Ctrl+D deletes it; the right-click
menu also offers Open, Rename, and Delete. Grouped folder headings have no file
actions. **File → Refresh** reloads the listing and checks open files for disk changes.
See [Git](git.md) for sidebar colors.

A vault names a document folder. **File → Vaults** opens a configured vault;
**More → + New vault** adds one. The menu's **gote** entry returns to the configured
default location. Vaults can also be declared in the config and opened by name from
the [CLI](cli.md#launch-modes).

## Tabs and editor groups

Each editor group has a tab bar in opening order. Alt+9/Alt+0 switches documents;
`[`/`]` also works outside text entry. Click a tab to select it; overflow arrows
scroll the bar without changing documents. Tabs remain visible with the sidebar hidden.

| Marker | Meaning |
| --- | --- |
| `(*)` | Unsaved changes |
| `(!)` | Changed on disk |
| `(!*)` | Both |
| `[P]` | Reader mode |

Change markers also appear in the single-file editor title.

Alt+T moves a tab right. At the rightmost group, it creates a group if the source
has at least two tabs. Ctrl+T moves left when a group exists there. There can be up
to four groups. **View → Tab Groups** provides these actions and **Close editor
group**, which moves remaining tabs to a neighbor. Empty groups close automatically.

Moving a tab preserves edits, undo history, cursor, and scroll position. Each document
belongs to one group; opening it again focuses that group. New documents open in the
last active group, and language tools follow its selected document. Unfocused editors
keep their syntax colors and hide the caret.

Project/vault sessions restore group layout and open files, but **not unsaved text or
pathless buffers**. See [persistence](configuration.md#what-persists).

## New buffers, saving, and external changes

Ctrl+N creates a pathless buffer named with the first available `unsaved_N` number.
Saving or closing it releases that number. Typing or pasting into the empty startup
buffer also adds its tab. Ctrl+S asks for a filename, then manages the saved file like
other documents.

When the terminal regains focus, or on **File → Refresh**, gote checks loaded open
files for disk changes. Clean buffers reload, preserving cursor/scroll where possible
and clearing undo history. Dirty buffers retain your edits.

Saving checks again and asks before overwriting an externally changed file or
recreating a deleted file. Cancelling keeps the buffer and its change marker.
Closing or quitting confirms unsaved changes. If formatting on save is enabled,
[formatting changes need another save](language-servers.md#formatting).

## Search and the bottom panel

Ctrl+F searches the current buffer. Ctrl+Alt+F or **Edit → Find in Files** opens a
form with Search and optional Path fields. Tab/Shift+Tab moves between fields,
Enter starts the search, and Esc cancels.

A blank path uses the current document root (the project/vault root, or the file's
directory in single-file mode). Relative paths start there; absolute directories
are accepted. Search is literal and smart-case: lowercase queries ignore case,
while a query containing uppercase is case-sensitive.

Search runs asynchronously over text files, using unsaved content from open buffers
that have paths. It skips hidden, dependency, and build directories. Results appear
in the bottom panel's **Search** tab.

The full-width bottom panel contains **Diag** and **Search** tabs. Click a tab or
use Left/Right while the panel is focused. Drag its top divider to resize it; the
size lasts for the session.

In either view, Up/Down selects an entry and Enter or a click jumps to its location.
Alt+Shift+B returns. Messages wrap; Page Up/Down and the mouse wheel scroll. Esc
returns focus to the editor while leaving the panel open. For diagnostic scope,
see [Diagnostics](language-servers.md#diagnostics-and-outline).

## Single-file mode

`gote path/to/file` starts with just the editor: no menu bar, sidebar, tab bar, or
help bar. Alt+? still opens shortcut help, and Alt+W closes the file and exits.
Ctrl+N and editor groups are unavailable.
Launch a directory or vault from the shell to use project mode.

By default, `single_file_mode.allow_panel_toggle: false` disables Alt+pipe,
Alt+Shift+O, and Ctrl+Alt+F. The help page lists locked shortcuts with the setting
responsible. Set it to true to allow the bottom panel, outline, and Find in Files;
this does not restore the menu or help bar.

Language tools are enabled by default. Set `single_file_mode.default_allow_lsp:
false` to disable them for file launches, or `auto-lsp: false` for every mode.
Single-file right-click menus include clipboard actions and Hover info; they omit
definition/reference actions that could leave the file.
