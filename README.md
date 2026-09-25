# Kerfline

Kerfline is a Telegram bot that maps **each chat to one git workspace**. A
message becomes a Grok turn inside a [JailBee](https://github.com/VRTFinland/jailbee)
container for that workspace — not on the host. Grok files notes, answers
questions, and can send workspace files back as photos or documents.

The name is *kerf* (the slit a blade leaves) plus *line* (the wire): one
narrow opening per chat.

## Status

Working personal bot, used daily, MIT-licensed. It is not a one-command
installer. First run is still several independent layers (Incus, JailBee’s
golden image, a workspace repo, a Grok login, a bot token). `kerfline doctor`
and `kerfline init` are planned and **not implemented yet** — until they
exist, this file is the setup path.

v1 is local git. Gitea `tea` keys exist on chat files but are unused.

## How it works

```
Telegram chat
    → Kerfline (this Go process, on the host)
        → jailbee ls in that workspace → Incus full name
        → jailbee exec <full name> -- grok …
            → Grok inside an Incus container
                ↳ one git workspace per chat
```

This repo is only the host bot. Workspace repos (notes, a project, …) are
**separate git trees** with their own `.jailbee/` and `AGENTS.md`.

What that split buys you:

- Isolation: Grok’s tools run in the container, not on the host
- Persistence: the workspace is ordinary git you already know how to backup
- Scope: one chat (or one forum topic) owns one repo
- Media: photos and documents can be filed without sending pixels to Grok;
  look captions attach JPEG/PNG so Grok can describe them; voice notes are
  transcribed; Grok can send a workspace file back as a Telegram photo

## Requirements

- **Linux** host
- **[Incus](https://linuxcontainers.org/incus/)**
- **[JailBee](https://github.com/VRTFinland/jailbee)**
- **Grok** inside the workspace container: SuperGrok device login, or `XAI_API_KEY`
- **Go 1.24+** to build
- A Telegram bot token from [@BotFather](https://t.me/BotFather)

JailBee is a separate GPL-3.0-or-later runtime. Kerfline talks to it only as
a child process (`jailbee exec … -- grok …`). This tree does not include it.

## First run

Six jobs. If something fails, name the layer — an Incus or login problem
will not look like a Telegram bug.

### 1. Incus and JailBee

Install the CLI, then follow JailBee’s host guide (Incus, UID mapping,
firewall):

```bash
uv tool install jailbee      # or: pipx install jailbee
```

**[JailBee installation](https://github.com/VRTFinland/jailbee/blob/main/docs/installation.md)**
end to end, then `jailbee setup` and `jailbee doctor`. Per-repo image and
container steps come after, in the workspace below — not in this repo.

On Arch, `scripts/host-setup-incus.sh` is an optional sudo helper for Incus
packages and subuid lines. It is not the install path for other distros.

### 2. Build Kerfline

```bash
make build          # go build -o kerfline ./cmd/kerfline
```

### 3. A workspace repo

Not this tree. Pick a directory, make it a git repo, and give it JailBee +
Grok:

```bash
mkdir -p ~/kerfline-workspaces/notes
cd ~/kerfline-workspaces/notes
git init
echo .local/ >> .gitignore    # Telegram inbox; must stay untracked
jailbee config init
```

Enable Grok in `.jailbee/config.yaml`. Kerfline runs `grok` itself; you do
not need `autostart`:

```yaml
agents:
  grok:
    enabled: true
```

Then:

```bash
jailbee init
jailbee base build          # ~10–15 min, once per host
jailbee new main
```

See JailBee’s **[Getting started](https://github.com/VRTFinland/jailbee/blob/main/docs/getting-started.md)**
if `doctor` or `new` complains.

### 4. Log Grok in

Device auth needs open egress for a few minutes:

```bash
jailbee net loose main --for 15m
jailbee exec main -- grok login --device-auth
jailbee exec main -- grok -p "reply pong" --always-approve
```

Or set `XAI_API_KEY` in the container instead of SuperGrok login. STT uses
the same login (or that env var).

### 5. Bot config (long-poll)

Leave `telegram.public_url` empty. The example file’s `mode` is `webhook`,
but with no public URL Kerfline long-polls — that is the intended first run.
Do not set up HTTPS or systemd yet.

```bash
mkdir -p ~/.config/kerfline/chats
cp contrib/config.example.toml ~/.config/kerfline/config.toml
cp contrib/chats/notes.example.toml ~/.config/kerfline/chats/notes.toml
install -m 600 /dev/null ~/.config/kerfline/env
echo 'BOT_TOKEN=…' >> ~/.config/kerfline/env
```

Edit `chats/notes.toml`:

- `workspace` — **absolute** path of the repo from step 3
- `jailbee_container = "main"` — the **short** name from `jailbee ls` in
  that workspace. Kerfline looks up the Incus full name from that listing
  (`notes-main`, not a foreign container that happens to be called `main`)
  and refuses to start if the name is not in this workspace.
- `require_mention = false` for a private DM (`true` in groups unless you
  want every line gated; see below)
- `telegram_chat_id = 0` until the next step

Built-in Grok rules (`internal/config/agents.md`) apply to every chat:
Telegram Markdown, short by default. A non-empty
`~/.config/kerfline/AGENTS.md` replaces that text. Restart after editing it.
The workspace’s own `AGENTS.md` is project notes (paths, what to file
where) — a different file, in the other repo.

### 6. Bind the chat and talk

```bash
./kerfline
```

Start the bot in Telegram, DM `/chatid`, paste that id into
`telegram_chat_id`, restart. Then `/ask ping` — or just talk, in a DM.

In a group, add the bot, `/chatid`, and keep `require_mention = true` unless
you want the reply gate on every line. In a forum group, `/ask` replies stay
in the topic they were written in; `/chatid` prints `topic_id` when you run
it inside a topic.

## Using it

### Who gets a Grok turn

- **Groups that only want `/ask` or `@bot`:** keep `require_mention = true`.
- **Groups that want “hey kerf / hey grok / clearly workspace data”:** set
  `require_mention = false` so every non-empty line reaches Kerfline. A cheap
  gate decides whether to run a full Grok turn. No typing indicator and no
  Telegram reply when the gate says skip.
- **DMs:** `require_mention = false`. The same gate applies unless
  `[grok].gate = false`, which restores always-on full turns.
- **Explicit `/ask` and `@botusername`:** skip the gate (typing + full turn).
- **Informational drops:** if Grok files workspace data and has nothing to
  say, it prints only `👍`. Kerfline sets a thumbs-up reaction on that
  message instead of a chat reply. A trailing `👍` after leftover chatter is
  still that ack. Empty stdout is still `(empty reply)`.

Optional knobs in `contrib/config.example.toml` under `[grok]`: `gate`,
`gate_model`, `gate_timeout`, `gate_reasoning` (`none` | `low` | `medium` |
`high` | `xhigh` | `omit`), `gate_speech_max` (default `2m`). Defaults: model
`grok-4.3`, reasoning `none`. Use `omit` to leave `reasoning_effort` off the
request for models that do not accept `none`.

### Reply reasoning level

`[grok].reasoning_levels` lists the `--reasoning-effort` values the full Grok
turn may run at: `low` | `medium` | `high` | `xhigh`, the values grok
accepts. With two or more, the gate also picks a level per message: the
lowest one it thinks will do the job. `/ask`, `@bot`, and “hey kerf” messages skip the reply decision
but still get a short effort-only gate call, made after the typing indicator
starts. With one level, that level is always used and no call is made. Leave
it unset to never pass the flag.

`reasoning_default` (default: the lowest listed level) is used when the gate
errors. A chat file can set its own `reasoning_levels` and
`reasoning_default`. Don't also put `--reasoning-effort` in `extra_args`.

### Photos, documents, and voice

Kerfline files Telegram photos and documents into the chat’s git workspace.
It classifies the caption or reply **from the text first**, then decides
whether Grok should see the pixels.

- Caption a photo with `/ask save this …`, or send the photo first and
  **reply** to it with `/ask …` (or unadorned text in a DM).
- The host downloads the file into gitignored `.local/telegram-inbox/` and
  Grok `cp`s that handle to a dest from the **workspace** `AGENTS.md`. Inbox
  `Read`/`Grep` is denied either way.
- **Save-only** (“save this”, “file this”, a date, no look phrases): Grok
  gets the handle and is told not to open the image. Pixels stay off the
  model.
- **Look** (“what’s this”, “describe”, “can you see…”) or **mixed** (look
  and save): JPEG/PNG pixels are attached so Grok can answer. It still
  copies from the handle if you also asked to save. PDF, WebP, and HEIC
  stay file-only in v1.
- Bare photos (no caption, no later reply) are ignored — no Grok turn and no
  ack.
- Telegram **photos** are compressed JPEGs. For an archival scan, send it as
  a **document**.

Voice notes and audio: caption `/ask …`, or **reply** to the clip with
`/ask …` (or unadorned text in a DM). In chats with `require_mention = false`,
a clip at or under `gate_speech_max` (default two minutes) is transcribed and
sent through the reply gate even with no caption; if the gate says reply,
that same transcript is reused for the Grok turn (no second STT). Longer
bare clips are still ignored. Kerfline downloads the OGG, transcribes it with
Grok STT (SuperGrok login inside the container, or `XAI_API_KEY`), and puts
the transcript in the Grok prompt. Grok does not listen to the file.

Grok can send a workspace file back in the reply with a Markdown image whose
path is in that chat’s repo:

```markdown
![lab photo](scans/2026-08-24.jpg)
```

JPEG/PNG/WebP are uploaded as photos; other types as documents. Paths
outside the workspace, or under `.local/` / `.git/`, are refused. Remote
`https://` images are left as Markdown and not fetched.

`.local/` must stay gitignored. Default `vision = "auto"` is the save-vs-look
split above. Per-chat `vision = "never"` skips look-only turns;
`vision = "always"` attaches JPEG/PNG even on save.

## Adding a chat

New workspace repo with `.jailbee/`, `jailbee new`, then a new
`chats/foo.toml`. Same binary.

To bind only one forum topic, set `telegram_topic_id` to the id from
`/chatid` in that topic. Other topics in the group are ignored unless another
chat file covers them. You can have a catch-all file (no `telegram_topic_id`)
plus topic-specific files for the same group: the matching topic file wins,
including its allowlist and workspace.

## Production extras

Once poll works, these are optional.

### Webhook

Set `telegram.public_url` to an HTTPS origin (Caddy, nginx, Cloudflare
tunnel, Tailscale). Kerfline listens on localhost. On every start in webhook
mode it:

1. `deleteWebhook`
2. `getUpdates` until the queue is empty (process those messages)
3. `setWebhook` and serve

`mode = "poll"` forces long poll even if a URL is set.

### systemd (user unit)

```bash
make install
systemctl --user enable --now kerfline
```

## Later: Gitea / tea

Do **not** mount `~/.config/tea`. Create a Gitea user that can only see that
one repo, `tea login` inside the container, add `gitea.example.com:443` to
JailBee strict egress, fill `gitea_remote` / `tea_login` on the chat file.

## License

Kerfline is MIT. See [`LICENSE`](LICENSE).

[JailBee](https://github.com/VRTFinland/jailbee) is a separate GPL-3.0-or-later
runtime; this repo does not include it.
