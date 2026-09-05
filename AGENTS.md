# Kerfline

Go Telegram bot. Each chat maps to one git **workspace**. Grok runs inside a [JailBee](https://github.com/VRTFinland/jailbee) container for that workspace, not on the host.

This repo is the host bot. Workspace repos (notes, etc.) are separate git trees with their own `.jailbee/` and `AGENTS.md`. Do not add JailBee config here unless asked.

## Commands

```bash
make build          # go build -o kerfline ./cmd/kerfline
go test ./...
make install        # binary + user systemd unit
```

Runtime config is `~/.config/kerfline/` (not in git): `config.toml`, `chats/*.toml`, `env` (`BOT_TOKEN`). Examples in `contrib/`.

## Layout

| Path | Role |
|---|---|
| `cmd/kerfline` | Host process |
| `internal/bot` | Telegram handlers, `/ask` / mention triggers |
| `internal/config` | TOML load; embeds default Grok rules |
| `internal/runner` | `jailbee exec … -- grok` |
| `internal/media` | Stage inbound Telegram files; upload outbound Markdown images |
| `internal/session` | Per-chat Grok session ids; resume within 4h idle |
| `contrib/` | Example config and systemd unit |
| `plan/` | Setup notes, not product spec |

## Three AGENTS.md files

Do not mix these up.

| File | Audience |
|---|---|
| **This file** | Coding agents working on the bot |
| `internal/config/agents.md` | Built-in Grok Telegram reply rules (`grok --rules`). Embedded in the binary |
| `~/.config/kerfline/AGENTS.md` | Optional override of those Telegram rules (non-empty file replaces the embed) |
| `<workspace>/AGENTS.md` | Per-workspace Grok project file (paths, domain notes). Lives in the **other** repo |

Telegram formatting, “don’t narrate”, and attachment rules belong in `internal/config/agents.md`, not here.

## Constraints

- MIT. JailBee is a separate GPL-3.0-or-later runtime. Do not copy JailBee source, `install.d` snippets, or large chunks of its docs into this tree.
- Talk to JailBee only as a child process (`jailbee exec … -- grok …`).
- Gitea `tea` keys exist on chat files but are unused. When wired up, use a dedicated Gitea user invited to that one repo — never the host `tea` login.
- Never put personal names in captions, file paths, examples, tests, documentation, or commit messages. Use generic placeholders.
- Do not hard-code host paths like `/home/you/…` in new examples; keep `contrib/` generic.
