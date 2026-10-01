# Markdown

[Documentation index](README.md)

Markdown documents (`.md` and `.markdown`) have three display modes. Pathless
scratch buffers can also be previewed.

## Document modes

| Mode | Display |
| --- | --- |
| Off | Editable Markdown source with syntax highlighting |
| Live | Editable text with inline Markdown rendering |
| Reader | Rendered Markdown replacing that document's editor pane |

Ctrl+P cycles Off → Live → Reader → Off. **View → Preview → Doc** selects a mode
in project mode; it is disabled for other file types. Each buffer keeps its mode
until it closes. Esc leaves Reader and returns to the previous editing mode.

Open a file directly in Reader with:

```sh
gote -P notes.md
```

This starts in the minimal single-file interface. Esc returns to editing. The
preview flag is ignored for non-Markdown files. A Markdown document opened from a
link in rendered Markdown opens in Reader; relative links resolve from the source
document's directory.

## Live preview

Live preview hides heading markers and inline emphasis/code/link markup, draws
bullets and task boxes as glyphs, and aligns tables. Lines do not reflow.

The caret's line, selected lines, and lines with search hits show source so edits
apply to the raw text. A list bullet remains rendered unless the caret or selection
touches its marker. Rendered lines use the Reader theme; source lines use the
`md_*` syntax colors. Fenced blocks with a language name receive syntax highlighting
in Live and Reader modes; ordinary source editing uses one code color.

## Side preview

Alt+P or **View → Preview → Side by side** toggles a preview column beside the
editors. It renders the active document independently of that document's mode,
including Reader. For an unsupported file it shows “Could not preview” and keeps
the layout in place.

## Default mode

Set `default_doc_mode: off` or `live` in the
[config](configuration.md). **View → Preview → Default** also writes this setting.
It affects documents opened afterwards, leaving existing buffers' modes unchanged.
Reader is selected per document or with `-P`, rather than as a startup default.
