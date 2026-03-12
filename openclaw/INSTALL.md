# OpenClaw prebuilt install

If you want a runnable `openclaw` binary without cloning the repo or
running `go build`, use the public install script:

```bash
curl -fsSL \
  https://raw.githubusercontent.com/trpc-group/trpc-agent-go/main/openclaw/install.sh \
  | bash
```

The default profile is `stdin`, so the first launch works without model
credentials:

```bash
openclaw
```

## Install profiles

Supported profiles:

- `stdin`: local terminal chat with the mock model.
- `stdin-sqlite`: local terminal chat with SQLite-backed session and
  memory storage.
- `telegram`: Telegram channel example from this repo.

Examples:

```bash
curl -fsSL \
  https://raw.githubusercontent.com/trpc-group/trpc-agent-go/main/openclaw/install.sh \
  | bash -s -- --profile stdin-sqlite
```

```bash
curl -fsSL \
  https://raw.githubusercontent.com/trpc-group/trpc-agent-go/main/openclaw/install.sh \
  | bash -s -- --profile telegram
```

Install a pinned version:

```bash
curl -fsSL \
  https://raw.githubusercontent.com/trpc-group/trpc-agent-go/main/openclaw/install.sh \
  | bash -s -- --version v0.0.1
```

## Install locations

By default the script writes:

- Binary: `~/.local/bin/openclaw`
- Active config: `~/.trpc-agent-go/openclaw/openclaw.yaml`
- Profile templates: `~/.trpc-agent-go/openclaw/profiles/`
- State dir: `~/.trpc-agent-go/openclaw`
- Managed skills: `~/.trpc-agent-go/openclaw/skills`
- Bundled release skills:
  `~/.trpc-agent-go/openclaw/bundled-skills`

`openclaw.yaml` is only replaced when it does not exist yet, or when you
pass `--force-config`. The bundled skills directory is refreshed on every
install or upgrade so it stays aligned with the selected release.

Custom install paths:

```bash
curl -fsSL \
  https://raw.githubusercontent.com/trpc-group/trpc-agent-go/main/openclaw/install.sh \
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
- Release archives include the OpenClaw bundled skills pack from this
  repo, so prebuilt installs have the same built-in skills as source
  checkouts.
- If `~/.local/bin` is not on your `PATH`, the installer prints the exact
  export line to add.
