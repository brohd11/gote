# Git

[Documentation index](README.md)

Git integration provides file-status colors and changes against HEAD. Git is
optional; unavailable status leaves normal file/directory colors.

## Sidebar status colors

All three Docs views use these colors:

| State | Color |
| --- | --- |
| Staged | Green |
| Untracked | Bright green |
| Modified | Yellow |
| Conflicted or deleted | Red |
| Ignored | Gray |
| Clean | Normal foreground |

Files with both staged and unstaged changes show the unstaged state. Folders keep
their directory color unless changes beneath them take priority. Deleted files
contribute to parent-folder colors without adding deleted-file rows. Nested repos,
submodules, and worktrees are supported. Selected rows retain their Git colors.
The shortcut-help page also contains a color legend.

Status refreshes in the background while the sidebar is visible. Idle polling
backs off from 2 to 10 to 30 to 60 seconds; saves, file operations, folder changes,
sidebar re-show, and terminal focus trigger refreshes. **File → Refresh** updates
the listing and status when you need an explicit refresh.

## Gutter and diff inspection

**View → Git gutter** toggles line-change markers. The initial value is
`default_git_gutter` in each launch mode's [configuration](configuration.md#startup-display-and-language-tools).

Click a gutter marker or press **Alt+Shift+D** in the editor to inspect a unified
diff against HEAD, including unsaved edits. The shortcut opens the changed section
at the cursor or within its three context lines, and works with the gutter hidden.
Deletion ticks reveal removed text. Removed lines are red and additions are green.

The popup wraps long lines. Up/Down, Page Up/Down, or the mouse wheel over the popup
scrolls it. Esc or Alt+Shift+D closes it. Typing, clicking outside, scrolling outside,
and changing panes/documents/layout dismiss the popup and continue the original action.
