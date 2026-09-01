# Kerfline

A Go Telegram bot that maps **each chat to one git workspace**. Grok runs inside a [JailBee](https://github.com/VRTFinland/jailbee) container for that workspace, not on the host. SuperGrok login (no API key).

The name is *kerf* (the slit a blade leaves) plus *line* (the wire). One narrow opening per chat.

v1 is local git. Gitea `tea` is configured in chat files but not used yet — when it is, it must be a **dedicated Gitea user invited to that one repo**, never the host `tea` login.

## Layout

| Path | Role |
|---|---|
| this repo | Go bot (host process), binary `kerfline` |
| `~/kerfline-workspaces/notes` | first Grok workspace (JailBee container `main`) |
| `~/.config/kerfline/` | token, per-chat config, optional `AGENTS.md` override (not in git) |

## Telegram

Webhook by default. The bot listens on localhost; put HTTPS in front (Caddy, nginx, Cloudflare tunnel, Tailscale).

On every start in webhook mode it:

1. `deleteWebhook`
2. `getUpdates` until the queue is empty (process those messages)
3. `setWebhook` and serve

If `telegram.public_url` is empty, it long-polls instead so you can run before a tunnel exists. `mode = "poll"` forces that.

## Setup

### 1. Host (Incus)

Needs sudo, once:

```bash
sudo -E ./scripts/host-setup-incus.sh
# log out/in, then:
incus admin init    # defaults are fine
jailbee setup --yes
jailbee doctor
```

### 2. Notes workspace

```bash
cd ~/kerfline-workspaces/notes
jailbee init
jailbee base build          # ~10–15 min, once
jailbee new main
jailbee net loose main      # only for login
jailbee exec main -- grok login --device-auth
jailbee net strict main
jailbee exec main -- grok -p "reply pong" --always-approve
```

### 3. Bot config

```bash
mkdir -p ~/.config/kerfline/chats
cp contrib/config.example.toml ~/.config/kerfline/config.toml
cp contrib/chats/notes.example.toml ~/.config/kerfline/chats/notes.toml
install -m 600 /dev/null ~/.config/kerfline/env
echo 'BOT_TOKEN=…' >> ~/.config/kerfline/env
```

Built-in Grok rules (`internal/config/agents.md`) apply to every chat: Telegram gets plain text, keep replies short. If `~/.config/kerfline/AGENTS.md` exists and is non-empty, it replaces the built-in text. Restart the bot after editing it.

Create the bot with BotFather. Start it, DM `/chatid`, put that id in `chats/notes.toml`. For a group, add the bot, `/chatid`, set `require_mention = true`. In a forum group, `/ask` replies stay in the topic they were written in; `/chatid` prints `topic_id` when you run it inside a topic.

```bash
make install
systemctl --user enable --now kerfline
```

Trigger: `/ask …` or `@bot …`. In a DM with `require_mention = false`, any text is a Grok turn.

## Adding a chat

New workspace repo with `.jailbee/`, `jailbee new`, then a new `chats/foo.toml`. Same binary.

To bind only one forum topic, set `telegram_topic_id` to the id from `/chatid` in that topic. Other topics in the group are ignored unless another chat file covers them. You can have a catch-all file (no `telegram_topic_id`) plus topic-specific files for the same group: the matching topic file wins, including its allowlist and workspace. Do not run two files that share `telegram_chat_id` against an older Kerfline binary.

## Later: Gitea / tea

Do **not** mount `~/.config/tea`. Create a Gitea user that can only see that one repo, `tea login` inside the container, add `gitea.example.com:443` to JailBee strict egress, fill `gitea_remote` / `tea_login` on the chat file.

## License

Kerfline is MIT. See [`LICENSE`](LICENSE).

[JailBee](https://github.com/VRTFinland/jailbee) is a separate GPL-3.0-or-later runtime; this repo does not include it.
