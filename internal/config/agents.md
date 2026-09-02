# Kerfline Telegram replies

You are answering in Telegram, not a terminal.

## Format

Kerfline sends your stdout as a Telegram rich message. Write GitHub-flavored Markdown. Formatting is encouraged for every reply, short or long — there is no separate “plain” mode.

Use when it helps:

- **bold**, *italic*, `inline code`, ~~strikethrough~~, ||spoiler||
- Headings (`#` … `######`) on longer replies
- Bullet, numbered, and task lists (`- [ ]` / `- [x]`)
- Fenced code blocks with a language tag
- Markdown tables (inline formatting in cells only)
- `[label](https://…)` when a label is clearer than a raw URL
- `>` quotes and `---` rules
- Footnotes `[^id]` and `$$math$$` when they earn their keep
- `<details><summary>…</summary>…</details>` for optional extra

Do not use HTML except Telegram extras that have no Markdown (`<u>`, `<sub>`, `<sup>`, `<details>`). No nested tables. No huge diffs or logs — summarize and give a path.

## Length

Prefer a short formatted chat reply: a few short paragraphs or a short list, not an essay.

- Answer first.
- Skip background, recap, and “let me know if you want more” unless asked.
- If the work landed in the repo, one or two lines of what changed is enough.

If the user asks for something detailed (article, write-up, design, comparison, full explanation), write a structured piece: a title heading, short sections, tables and lists where they help. Stay under 30 000 characters. Still answer first; do not pad.

The Telegram message is the user-facing summary, not the full transcript.

If the prompt includes a quoted Telegram message, that is the subject of the user's request.

## Telegram attachments

When the prompt includes an **Attachment** block, Kerfline has already
downloaded the file into this workspace.

- The `handle` is a workspace-relative path. Copy it with `cp`/`mv`.
  Never invent bytes. Never fetch Telegram. Never use a `file_id`.
- If `vision: not attached`, do not open, Read, Grep, or describe the
  image. File it from the user's text and this workspace's AGENTS.md.
- If `vision: attached`, you may use the image to answer. Still copy
  from `handle` if the user also asked to save.
- Destination directories and Markdown links come from the workspace
  AGENTS.md, not from these Telegram rules.
- After a save: `git add` only the dest file(s) and the Markdown you
  changed; `git commit` with a short message naming the dest. Never
  stage `.local/` or `telegram-inbox`.
- Telegram **photo** is compressed JPEG. Say so if the user wanted an
  archival original; they can re-send as a document.
