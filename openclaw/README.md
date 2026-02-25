# OpenClaw-like Demo (Telegram + HTTP Gateway)

This directory is a small runnable binary that demonstrates an
OpenClaw-like shape on top of `trpc-agent-go`:

- A long-running **gateway** process (HTTP endpoints).
- A real IM **channel**: Telegram (long polling).
- A stable **session_id** derived from DM vs group chat.
- Skills support via the built-in skills tooling in `llmagent`.

It is intended as a starting point for adding more channels
(Enterprise WeChat, Slack, etc.) and hardening operational controls.

## Quick start

Run with a mock model (no external model credentials needed):

```bash
cd openclaw
go run ./cmd/openclaw \
  -mode mock \
  -http-addr :8080
```

Note: by default, this demo uses `-mode openai` and `-model gpt-5`.
If you do not have model credentials, keep using `-mode mock`.

Health check:

```bash
curl -sS 'http://127.0.0.1:8080/healthz'
```

Send one message via HTTP (webhook-style):

```bash
curl -sS 'http://127.0.0.1:8080/v1/gateway/messages' \
  -H 'Content-Type: application/json' \
  -d '{"from":"alice","text":"Hello"}'
```

## Run with a real model (OpenAI)

This demo uses the `model/openai` implementation with provider variants.

For OpenAI:

```bash
export OPENAI_API_KEY="your-api-key"

cd openclaw
go run ./cmd/openclaw \
  -http-addr :8080
```

By default, `-model` uses `$OPENAI_MODEL` if set, otherwise it falls
back to `gpt-5`.

### DeepSeek (OpenAI-compatible)

If you use DeepSeek, set `DEEPSEEK_API_KEY`:

```bash
export DEEPSEEK_API_KEY="your-api-key"

cd openclaw
go run ./cmd/openclaw \
  -mode openai \
  -model deepseek-chat \
  -http-addr :8080
```

If you already use the OpenAI-compatible environment variables, this also works:

```bash
export OPENAI_API_KEY="your-api-key"
export OPENAI_BASE_URL="https://api.deepseek.com/v1"

cd openclaw
go run ./cmd/openclaw \
  -mode openai \
  -model deepseek-chat \
  -http-addr :8080
```

By default, `-openai-variant` is `auto` and is inferred from `-model`.
You can override it explicitly:

```bash
export OPENAI_API_KEY="your-api-key"

cd openclaw
go run ./cmd/openclaw \
  -mode openai \
  -openai-variant openai \
  -model gpt-4o-mini \
  -http-addr :8080
```

## Enable Telegram

This demo uses **Telegram long polling** (`getUpdates`), so it does not
require a public HTTPS endpoint.

### 1) Create a bot token

1) Talk to `@BotFather`.

2) Run `/newbot`.

3) Pick a bot name and a username (Telegram requires the username to end with
`bot`).

4) Copy the bot token.

### 2) Ensure long polling is enabled (no webhook)

A Telegram bot cannot use `getUpdates` while a webhook is set.

If you ever configured a webhook for this bot, delete it:

```bash
curl -sS "https://api.telegram.org/bot$TELEGRAM_BOT_TOKEN/getWebhookInfo"
curl -sS "https://api.telegram.org/bot$TELEGRAM_BOT_TOKEN/deleteWebhook"
```

### 3) Group chat notes (privacy mode)

If you add the bot into a group, Telegram privacy mode affects which messages
the bot receives.

- With privacy **enabled** (default), the bot typically only receives messages
  that mention it (for example `@mybot`), commands (for example `/start`),
  and replies to the bot.
- With privacy **disabled**, the bot can receive all group messages.

This demo recommends mention gating (`-require-mention`) for groups, so keeping
privacy enabled is usually fine. If you want to disable privacy, use
`@BotFather` and run `/setprivacy`.

### 4) Run the binary

Run the binary with `-telegram-token`:

```bash
cd openclaw
go run ./cmd/openclaw \
  -mode mock \
  -http-addr :8080 \
  -telegram-token "$TELEGRAM_BOT_TOKEN"
```

### Telegram networking (proxy / timeout / retries)

If your environment requires an HTTP proxy, set `-telegram-proxy`:

```bash
cd openclaw
go run ./cmd/openclaw \
  -mode mock \
  -telegram-token "$TELEGRAM_BOT_TOKEN" \
  -telegram-proxy "http://127.0.0.1:7890"
```

If you set `-telegram-http-timeout`, make sure it is larger than the
long-polling timeout (25s by default), for example:

```bash
cd openclaw
go run ./cmd/openclaw \
  -mode mock \
  -telegram-token "$TELEGRAM_BOT_TOKEN" \
  -telegram-http-timeout 60s
```

You can also tune retries with `-telegram-max-retries` (default: 3).

### Telegram doctor command

To quickly validate your Telegram setup (token, webhook, pairing store):

```bash
cd openclaw
go run ./cmd/openclaw doctor \
  -telegram-token "$TELEGRAM_BOT_TOKEN"
```

### 5) Send a message

Open a chat with your bot (or add it into a group) and send a text message.

By default, DMs are **fail-closed** and require pairing.

On the first DM, the bot replies with a 6-digit pairing code and will
not process your message yet.

To approve a user, run:

```bash
cd openclaw
go run ./cmd/openclaw pairing approve <CODE> \
  -telegram-token "$TELEGRAM_BOT_TOKEN"
```

You can also list pending pairing requests:

```bash
cd openclaw
go run ./cmd/openclaw pairing list \
  -telegram-token "$TELEGRAM_BOT_TOKEN"
```

After approval, the bot forwards inbound text to the gateway and sends
the final reply back to Telegram.

To disable pairing (less safe), run the gateway with:

```bash
cd openclaw
go run ./cmd/openclaw \
  -mode mock \
  -http-addr :8080 \
  -telegram-token "$TELEGRAM_BOT_TOKEN" \
  -telegram-dm-policy open
```

### Telegram threads and topics

This demo derives `session_id` based on whether the inbound message is a DM
or a group message:

- DMs: `thread` is empty, so the session is per-user.
- Groups: `thread` is the chat ID, so the session is per-group.
- Group topics: if Telegram provides `message_thread_id`, `thread` becomes
  `<chat_id>:topic:<message_thread_id>`, so each topic gets its own session.

### Telegram polling offset

This demo stores the Telegram `getUpdates` offset on disk so restarts
can resume from the last processed update.

- Default state dir: `$HOME/.trpc-agent-go/openclaw`
- Override with: `-state-dir`

On the first run (when no offset file exists), the poller drains pending
updates by default to avoid replying to very old messages. You can
disable this behavior with `-telegram-start-from-latest=false`.

## Safety knobs

### Allowlist

To only allow specific user IDs:

```bash
go run ./cmd/openclaw \
  -mode mock \
  -telegram-token "$TELEGRAM_BOT_TOKEN" \
  -allow-users "123456789,987654321"
```

The allowlist is matched against:

- Telegram: the numeric `from.id` (as a string)
- HTTP: `user_id` if set, otherwise `from`

To find your Telegram `from.id`:

1) Do not run this demo yet (or stop it), so your local process does not
consume updates.

2) Send any message to your bot in Telegram.

3) Call `getUpdates` and look for `message.from.id`:

```bash
curl -sS "https://api.telegram.org/bot$TELEGRAM_BOT_TOKEN/getUpdates"
```

### Mention gating (groups)

To ignore group messages unless a mention pattern is present:

```bash
go run ./cmd/openclaw \
  -mode mock \
  -telegram-token "$TELEGRAM_BOT_TOKEN" \
  -require-mention
```

When `-require-mention` is set and `-mention` is empty, this demo uses
`@<bot_username>` as the default mention pattern.

If Telegram is disabled (HTTP-only), you must provide `-mention` explicitly.

To override patterns:

```bash
go run ./cmd/openclaw \
  -mode mock \
  -telegram-token "$TELEGRAM_BOT_TOKEN" \
  -require-mention \
  -mention "@mybot,/agent"
```

### Telegram group policy and allowlist

By default, this demo ignores all group messages (`-telegram-group-policy` is
`disabled`).

To enable groups (less safe), use:

```bash
cd openclaw
go run ./cmd/openclaw \
  -mode mock \
  -telegram-token "$TELEGRAM_BOT_TOKEN" \
  -telegram-group-policy open \
  -require-mention
```

To allowlist specific groups/topics, use:

```bash
cd openclaw
go run ./cmd/openclaw \
  -mode mock \
  -telegram-token "$TELEGRAM_BOT_TOKEN" \
  -telegram-group-policy allowlist \
  -telegram-allow-threads "<chat_id>,<chat_id>:topic:<message_thread_id>"
```

You can discover `chat_id` and `message_thread_id` from `getUpdates`.

### Local code execution (unsafe)

This demo can optionally enable the local code execution tool for the
agent. This is **unsafe** when exposed to external inputs (Telegram,
webhook traffic).

It is disabled by default. To enable:

```bash
go run ./cmd/openclaw \
  -mode openai \
  -telegram-token "$TELEGRAM_BOT_TOKEN" \
  -enable-local-exec
```

## Skills

This demo supports AgentSkills-style `SKILL.md` skill folders, and
borrows a few design ideas from OpenClaw:

- Multiple skill roots (workspace, managed, extra dirs) with precedence.
- Optional load-time gating via `metadata.openclaw.requires.*`.
- `{baseDir}` placeholder substitution for better OpenClaw skill
  compatibility.

### Bundled skills

This demo includes a few simple bundled skills under `openclaw/skills/`:

- `hello`: write a small file to `out/`.
- `envdump`: dump environment info to `out/env.txt`.
- `http_get`: fetch a URL with `curl` into `out/`.

### Locations and precedence

Skills are loaded from these locations (highest precedence first):

1) Workspace skills: `-skills-root` (default: `./skills`)
2) Project AgentSkills: `./.agents/skills`
3) Personal AgentSkills: `$HOME/.agents/skills`
4) Managed skills: `<state-dir>/skills`
5) Repo bundled skills (when running from repo root): `./openclaw/skills`
6) Extra dirs: `-skills-extra-dirs` (comma-separated, lowest precedence)

If two skills have the same `name`, the one from the higher-precedence
location wins.

### OpenClaw metadata gating (optional)

If a skill's `SKILL.md` front matter contains `metadata.openclaw`, this
demo can filter the skill at load time based on the local environment:

- `metadata.openclaw.os` (darwin/linux/win32)
- `metadata.openclaw.requires.bins`
- `metadata.openclaw.requires.anyBins`
- `metadata.openclaw.requires.env`

Enable `-skills-debug` to log which skills are skipped and why.

### `{baseDir}` placeholder

Many OpenClaw skills use `{baseDir}` in commands (for example to run
scripts under `scripts/`). This demo replaces `{baseDir}` in loaded
skill bodies/docs with the local skill folder path.

### Using OpenClaw skill packs

If you already have an OpenClaw skills directory, you can reuse it:

```bash
cd openclaw
go run ./cmd/openclaw \
  -mode openai \
  -model deepseek-chat \
  -skills-extra-dirs "/path/to/openclaw/skills"
```

Note: OpenClaw skills often assume the OpenClaw tool surface. This demo
can optionally enable OpenClaw-compatible `exec` / `process` tools (see
below), but it is not a full OpenClaw replacement.

In a chat, you can ask the assistant to list and run skills. For
example:

```
List available skills, then run the hello skill.
```

## Session and memory

This demo uses `trpc-agent-go` sessions to store conversation history
per `session_id` (derived from DM vs group/topic).

The session service is in-memory by default, so session history is
cleared when the process exits.

It also enables an in-memory memory service and memory tools
(`memory_add`, `memory_load`, etc.) for the agent. Stored memories are
kept in process memory and are cleared when the process exits.

## OpenClaw exec/process tools (unsafe)

OpenClaw skills commonly rely on two tools:

- `exec` (or older skills: `bash`) to run shell commands
- `process` to manage background sessions

This demo can provide OpenClaw-compatible tools, but they are **unsafe**
when exposed to untrusted inputs.

Enable with:

```bash
go run ./cmd/openclaw \
  -mode openai \
  -model deepseek-chat \
  -telegram-token "$TELEGRAM_BOT_TOKEN" \
  -enable-openclaw-tools
```

Once enabled, you can ask the assistant to run a command. For example:

```
Use the exec tool to run: echo hello
If it runs in background, use the process tool to poll until it exits.
```
