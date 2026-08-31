# First things — easy setup without tightening JailBee

Goal: anyone on a Linux host can get Kerfline talking to one Telegram chat without guessing which of six layers is broken. Kerfline stays MIT. JailBee stays a separate GPL-3.0-or-later runtime.

This is the setup plan, not a rewrite of the bot.

## License (do not recouple)

Kerfline already talks to JailBee only as a child process (`jailbee exec … -- grok …`). Setup code that *calls* JailBee is the same kind of use.

**Fine (MIT stays):**

- Check `jailbee` on `PATH`
- `uv tool install jailbee` / tell the user to install it from PyPI
- Run `jailbee config init`, `jailbee doctor`, and (with confirmation) `jailbee init` / `base build` / `new`
- Write Kerfline’s own `~/.config/kerfline/` files
- Write a *small original* `.jailbee/config.yaml`, or better: let JailBee generate it

**Not fine (would mix GPL into this tree or a combined distribution):**

- Copy JailBee’s Python, `install.d` snippets, or large chunks of its docs/templates
- Vendor JailBee source
- Ship the JailBee program inside a Kerfline tarball/image

README one-liner when we add `LICENSE`: Kerfline is MIT; JailBee is a separate GPL-3.0-or-later runtime; this repo does not include it.

## What is actually hard

`go build` is easy. First run is six independent jobs, and the current README assumes this machine (`~/kerfline-workspaces/notes`, webhook, systemd, Arch Incus script):

1. Linux + Incus
2. JailBee CLI + golden image (`jailbee base build` is 10–15 min, once per host)
3. A **workspace git repo** (not this repo) with `.jailbee/` and a container (`jailbee new main`)
4. `grok login` inside that container
5. Bot token + `~/.config/kerfline/`
6. Optional HTTPS for webhooks

A single “do everything” bash script will fight distros, sudo, and that image build. Stage it. Name the next missing piece.

## Product shape

Two Kerfline commands plus a thinner README. Distro-spanning Incus install is **not** Kerfline’s job.

### 1. `kerfline doctor` (do this first)

Read-only. Print pass/fail for:

- Kerfline binary / `PATH`
- `BOT_TOKEN` (env or `~/.config/kerfline/env`)
- `config.toml` parses
- At least one `chats/*.toml` with a real `telegram_chat_id` (0 is still a placeholder)
- `jailbee` on `PATH`
- Workspace path exists
- `$workspace/.jailbee/config.yaml` exists
- Named container is up (`jailbee ls`)
- `grok` answers inside it (`jailbee exec … -- grok -p ping` or equivalent)

Do not mutate. Exit non-zero if anything required is missing. This is how people stop debugging the wrong layer.

### 2. `kerfline init`

Scaffold Kerfline files only. Never overwrite existing config without `--force`.

Write:

- `~/.config/kerfline/config.toml` — **poll mode**, no webhook secret required
- `~/.config/kerfline/env` — mode 600; prompt for `BOT_TOKEN` if unset
- `~/.config/kerfline/chats/<name>.toml` — `workspace` is a real path, `telegram_chat_id = 0` until `/chatid`

Workspace default: `~/kerfline-workspaces/default` (or `--workspace`). Do not hard-code `/home/you/kerfline-workspaces/notes`.

If `jailbee` is installed and the workspace has no `.jailbee/config.yaml`:

```bash
cd "$workspace"   # git init if needed
jailbee config init
```

Do **not** auto-run `jailbee init`, `jailbee base build`, or `jailbee new` without asking. Those change host Incus state; the image build is long.

Print the exact next commands, including device login:

```bash
jailbee init && jailbee base build && jailbee new main
jailbee net loose main --for 15m
jailbee exec main -- grok login --device-auth
```

Then: start the bot, DM `/chatid`, paste the id into the chat toml, `kerfline doctor`.

### 3. Happy path vs production extras

**First run:** long-poll, foreground `kerfline`, one workspace, one DM (`require_mention = false`).

**Later:** `make install` (user systemd unit), webhook + reverse proxy. Do not make those step 1. The bot already falls back to poll when `public_url` is empty — document that as the default, not webhook.

### 4. Host / JailBee install

Keep `scripts/host-setup-incus.sh` as an Arch helper if we still want it. For “anyone”: detect missing Incus/JailBee and point at JailBee’s install doc (`uv tool install jailbee`, `jailbee setup`, `jailbee doctor`). Do not reimplement JailBee’s host installer in this repo.

### 5. Workspace vs this repo

The bot tree and the Grok workspace stay two git repos. That split is correct. What newcomers need is a default workspace path and a recipe, not a copy of `~/kerfline-workspaces/notes`.

## Documented first run (target README)

```text
1. Linux host with Incus. Install JailBee (uv tool install jailbee).
2. make build   # or go build -o kerfline ./cmd/kerfline
3. kerfline init --workspace ~/kerfline-workspaces/default
4. In that workspace: jailbee init && jailbee base build && jailbee new main
5. grok login inside the container
6. Start the bot, DM /chatid, paste the id into the chat toml
7. kerfline doctor && kerfline
```

JailBee and Telegram cannot be skipped. What we remove is guesswork: hardcoded paths, webhook as step 1, and silent failures between layers.

## Implementation order

1. `kerfline doctor`
2. `kerfline init` (poll-first config + chat file; optional `jailbee config init`)
3. README rewritten to the happy path above; example chat toml stops using `/home/you/kerfline-workspaces/notes`
4. `LICENSE` (MIT) + the JailBee runtime note
5. systemd / webhook left as extras in the README

Do not start with a distro-spanning Incus installer.
