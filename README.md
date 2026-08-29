# telegram-jailbee

A Go Telegram bot that maps **each chat to one git workspace**. Grok runs inside a [JailBee](https://github.com/VRTFinland/jailbee) container for that workspace, not on the host. SuperGrok login (no API key).

v1 is local git. Gitea `tea` is configured in chat files but not used yet — when it is, it must be a **dedicated Gitea user invited to that one repo**, never the host `tea` login.

## Layout

| Path | Role |
|---|---|
| this repo | Go bot (host process) |
| `~/kerfline-workspaces/notes` | first Grok workspace (JailBee container `main`) |
| `~/.config/telegram-jailbee/` | token + per-chat config (not in git) |

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
mkdir -p ~/.config/telegram-jailbee/chats
cp contrib/config.example.toml ~/.config/telegram-jailbee/config.toml
cp contrib/chats/notes.example.toml ~/.config/telegram-jailbee/chats/notes.toml
install -m 600 /dev/null ~/.config/telegram-jailbee/env
echo 'BOT_TOKEN=…' >> ~/.config/telegram-jailbee/env
```

Create the bot with BotFather. Start it, DM `/chatid`, put that id in `chats/notes.toml`. For a group, add the bot, `/chatid`, set `require_mention = true`.

```bash
make install
systemctl --user enable --now telegram-jailbee
```

Trigger: `/ask …` or `@bot …`. In a DM with `require_mention = false`, any text is a Grok turn.

## Adding a chat

New workspace repo with `.jailbee/`, `jailbee new`, then a new `chats/foo.toml`. Same binary.

## Later: Gitea / tea

Do **not** mount `~/.config/tea`. Create a Gitea user that can only see that one repo, `tea login` inside the container, add `gitea.example.com:443` to JailBee strict egress, fill `gitea_remote` / `tea_login` on the chat file.
