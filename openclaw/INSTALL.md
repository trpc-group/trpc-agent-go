# OpenClaw prebuilt install

If you want a runnable `openclaw` binary without cloning the repo or
running `go build`, use the public install script:

```bash
curl -fsSL \
  https://github.com/trpc-group/trpc-agent-go/releases/latest/download/openclaw-install.sh \
  | bash
```

## Fastest first run

The default profile is `stdin`, and that profile uses the built-in `mock`
model. In other words, the first run does not need an API key or a chat
channel credential.

The installer keeps the GitHub build's config and state under
`~/.trpc-agent-go-github/openclaw` by default.

If `openclaw` is not found after install, run the PATH commands printed
by the installer. For bash, the persistent form is:

```bash
grep -qxF 'export PATH="$HOME/.local/bin:$PATH"' "$HOME/.bashrc" || \
  printf '\nexport PATH="$HOME/.local/bin:$PATH"\n' >> "$HOME/.bashrc"
. "$HOME/.bashrc"
```

Then start OpenClaw:

```bash
openclaw
```

You should immediately enter the local terminal chat mode. Try a message
such as `hello`. Use `/quit` or `/exit` to stop.

## Install profiles

Supported profiles:

- `stdin`: local terminal chat with the mock model.
- `stdin-sqlite`: local terminal chat with SQLite-backed session and
  memory storage.
- `telegram`: Telegram channel example from this repo.

Examples:

```bash
curl -fsSL \
  https://github.com/trpc-group/trpc-agent-go/releases/latest/download/openclaw-install.sh \
  | bash -s -- --profile stdin-sqlite
```

```bash
curl -fsSL \
  https://github.com/trpc-group/trpc-agent-go/releases/latest/download/openclaw-install.sh \
  | bash -s -- --profile telegram
```

Install a pinned version:

```bash
curl -fsSL \
  https://github.com/trpc-group/trpc-agent-go/releases/latest/download/openclaw-install.sh \
  | bash -s -- --version v0.0.1
```

## Install locations

By default the script writes:

- Binary: `~/.local/bin/openclaw`
- Active config: `~/.trpc-agent-go-github/openclaw/openclaw.yaml`
- Profile templates: `~/.trpc-agent-go-github/openclaw/profiles/`
- State dir: `~/.trpc-agent-go-github/openclaw`
- Managed skills: `~/.trpc-agent-go-github/openclaw/skills`
- Bundled release skills:
  `~/.trpc-agent-go-github/openclaw/bundled-skills`

`openclaw.yaml` is only replaced when it does not exist yet, or when you
pass `--force-config`. The bundled skills directory is refreshed on every
install or upgrade so it stays aligned with the selected release.

Custom install paths:

```bash
curl -fsSL \
  https://github.com/trpc-group/trpc-agent-go/releases/latest/download/openclaw-install.sh \
  | bash -s -- \
      --bin-dir "$HOME/bin" \
      --config-dir "$HOME/.config/openclaw" \
      --state-dir "$HOME/.local/share/openclaw"
```

## Upgrade

Installed binaries support an in-place upgrade command:

```bash
openclaw upgrade
```

The command downloads the latest published OpenClaw release, updates the
binary, refreshes profile templates and bundled skills, and preserves the
current `openclaw.yaml` unless you reinstall with `--force-config`.

## Telegram profile

If you choose `--profile telegram`, load the required credentials before
starting:

```bash
export TELEGRAM_BOT_TOKEN='replace-with-your-token'
export OPENAI_API_KEY='replace-with-your-api-key'
# optional:
# export OPENAI_BASE_URL='https://your-endpoint/v1'
```

Then launch:

```bash
openclaw
```

## Notes

- The install script resolves published releases from
  `trpc-group/trpc-agent-go` and downloads the matching
  `openclaw-v<version>-<os>-<arch>.tar.gz` archive for the current
  machine.
- The installer prints the exact commands to update the current shell and
  your shell rc file when `~/.local/bin` is not already on your `PATH`.
- Release archives include the OpenClaw bundled skills pack from this
  repo, so prebuilt installs have the same built-in skills as source
  checkouts.
