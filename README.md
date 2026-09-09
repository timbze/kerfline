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

Built-in Grok rules (`internal/config/agents.md`) apply to every chat: replies are Telegram rich Markdown (short by default; a longer article if asked). If `~/.config/kerfline/AGENTS.md` exists and is non-empty, it replaces the built-in text. Restart the bot after editing it.

Create the bot with BotFather. Start it, DM `/chatid`, put that id in `chats/notes.toml`. For a group, add the bot, `/chatid`, and choose `require_mention` (see below). In a forum group, `/ask` replies stay in the topic they were written in; `/chatid` prints `topic_id` when you run it inside a topic.

```bash
make install
systemctl --user enable --now kerfline
```

### Triggers and the reply gate

- **Groups that only want `/ask` or `@bot`:** keep `require_mention = true`. Only those host triggers start a turn (unchanged).
- **Groups that want “hey kerf / hey grok / clearly workspace data”:** set `require_mention = false` so every non-empty line reaches Kerfline. A cheap gate decides whether to run a full Grok turn. No typing indicator and no Telegram reply when the gate says skip.
- **DMs:** keep `require_mention = false`. The same gate applies unless `[grok].gate = false`, which restores always-on full turns.
- **Explicit `/ask` and `@botusername`:** skip the gate (typing + full turn immediately).
- **Informational drops:** if Grok files workspace data and has nothing to say, it prints only `👍`. Kerfline sets a thumbs-up reaction on that message instead of a chat reply. A trailing `👍` after leftover chatter is still that ack. Empty stdout is still `(empty reply)`.

Optional knobs in `contrib/config.example.toml` under `[grok]`: `gate`, `gate_model`, `gate_timeout`, `gate_reasoning` (`none` | `low` | `medium` | `high` | `xhigh` | `omit`), `gate_speech_max` (default `2m`). Defaults: model `grok-4.3`, reasoning `none`. Use `omit` to leave `reasoning_effort` off the request for models that do not accept `none`.

## Photos, documents, and voice

Kerfline can file Telegram photos and documents into the chat’s git workspace **without** sending pixels to Grok.

- Caption a photo with `/ask save this …`, or send the photo first and **reply** to it with `/ask …` (or unadorned text in a DM).
- The host downloads the file into gitignored `.local/telegram-inbox/` and Grok `cp`s that handle to a dest from the **workspace** `AGENTS.md`. Grok is told not to open the image; inbox `Read`/`Grep` is denied.
- Bare photos (no caption, no later reply) are ignored — no Grok turn and no ack.
- Telegram **photos** are compressed JPEGs. For an archival scan, send it as a **document**.
- Looking at a picture (vision) is not enabled yet. Until it is, “what does this show?” still stages the file and asks Grok to file from the text, without attaching pixels.

Voice notes and audio: caption `/ask …`, or **reply** to the clip with `/ask …` (or unadorned text in a DM). In chats with `require_mention = false`, a clip at or under `gate_speech_max` (default two minutes) is transcribed and sent through the reply gate even with no caption; if the gate says reply, that same transcript is reused for the Grok turn (no second STT). Longer bare clips are still ignored. Kerfline downloads the OGG, transcribes it with Grok STT (SuperGrok login inside the JailBee container, or `XAI_API_KEY`), and puts the transcript in the Grok prompt. Grok does not listen to the file.

Grok can send a workspace file back in the reply with a Markdown image whose path is in that chat’s repo:

```markdown
![lab photo](scans/2026-08-24.jpg)
```

JPEG/PNG/WebP are uploaded as photos; other types as documents. Paths outside the workspace, or under `.local/` / `.git/`, are refused. Remote `https://` images are left as Markdown and not fetched.

`.local/` must stay gitignored (notes already has that). Per-chat `vision = "never"` skips look-only turns. Do not install a binary that handles captions but cannot download files.

## Adding a chat

New workspace repo with `.jailbee/`, `jailbee new`, then a new `chats/foo.toml`. Same binary.

To bind only one forum topic, set `telegram_topic_id` to the id from `/chatid` in that topic. Other topics in the group are ignored unless another chat file covers them. You can have a catch-all file (no `telegram_topic_id`) plus topic-specific files for the same group: the matching topic file wins, including its allowlist and workspace. Do not run two files that share `telegram_chat_id` against an older Kerfline binary.

## Later: Gitea / tea

Do **not** mount `~/.config/tea`. Create a Gitea user that can only see that one repo, `tea login` inside the container, add `gitea.example.com:443` to JailBee strict egress, fill `gitea_remote` / `tea_login` on the chat file.

## License

Kerfline is MIT. See [`LICENSE`](LICENSE).

[JailBee](https://github.com/VRTFinland/jailbee) is a separate GPL-3.0-or-later runtime; this repo does not include it.
