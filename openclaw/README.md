# OpenClaw-like Demo (Telegram + HTTP Gateway)

This directory is a small runnable binary that demonstrates an
OpenClaw-like shape on top of `trpc-agent-go`:

- A long-running **gateway** process (HTTP endpoints).
- A real IM **channel**: Telegram (long polling).
- A stable **session_id** derived from DM (direct message) vs group chat.
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

## Agent types

By default, OpenClaw runs the `llm` agent (the built-in `llmagent`),
which uses your `model` config and supports skills/tools.

If you have Claude Code installed locally and want OpenClaw to drive
messages through the Claude Code CLI, use `claude-code`:

```bash
cd openclaw
go run ./cmd/openclaw \
  -agent-type claude-code \
  -http-addr :8080
```

YAML equivalent:

```yaml
agent:
  type: "claude-code"
  claude_output_format: "stream-json"
```

Notes:

- In `claude-code` mode, OpenClaw's `tools:` section is not supported.
- `model:` is optional unless you enable model-backed features like
  `session.summary.enabled` or `memory.auto.enabled`.

## Configuration (YAML)

This demo supports a YAML config file to avoid a long list of CLI flags.

- Pass `-config /path/to/openclaw.yaml`, or
- set `OPENCLAW_CONFIG=/path/to/openclaw.yaml`.

CLI flags always override config file values.

Example config:

```yaml
app_name: "openclaw"

http:
  addr: ":8080"

model:
  mode: "openai"
  name: "gpt-5"
  openai_variant: "auto"

gateway:
  allow_users: ["123456789"]
  require_mention: false
  mention_patterns: ["@mybot"]

telegram:
  token: "<YOUR_TELEGRAM_BOT_TOKEN>"
  streaming: "progress"
  http_timeout: "60s"

session:
  backend: "inmemory"
  summary:
    enabled: false

memory:
  backend: "inmemory"
  auto:
    enabled: false
```

Run:

```bash
cd openclaw
go run ./cmd/openclaw -config ./openclaw.yaml
```

Notes:

- Duration fields use Go-style strings like `60s`, `10m`, `1h`.
- For secrets (model keys, Telegram tokens), prefer environment variables
  or CLI flags instead of committing config files.
- Plugin sections:
  - `channels` enables additional channel plugins and requires a custom
    binary that imports those plugins. See `openclaw/EXTENDING.md` and
    `openclaw/examples/stdin_chat/`.
  - `tools.providers` and `tools.toolsets` work out of the box for the
    built-in types shipped in this repo. Custom types still require a
    custom binary. See `openclaw/INTEGRATIONS.md` and
    `openclaw/EXTENDING.md`.

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

You can override the OpenAI-compatible base URL with:

- `OPENAI_BASE_URL` (environment), or
- `-openai-base-url` (CLI flag), or
- `model.base_url` (YAML config).

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

### Telegram commands

This demo supports a few basic commands:

- `/help`: show a short help message.
- `/cancel`: cancel the current run for the same DM/thread session.

### Telegram reply streaming (preview)

This demo can optionally use `editMessageText` to show a processing preview,
then replace it with the final answer.

`-telegram-streaming` modes:

- `off`: send the final answer as messages.
- `block`: send one "Processing..." message, then edit once to final.
- `progress` (default): keep editing the message while the model is running.

To disable streaming:

```bash
cd openclaw
go run ./cmd/openclaw \
  -mode mock \
  -telegram-token "$TELEGRAM_BOT_TOKEN" \
  -telegram-streaming off
```

### Telegram threads and topics

This demo derives `session_id` based on whether the inbound message is a
DM (direct message, i.e. a private chat) or a group message:

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

## Extending this demo (custom channels / internal skills)

This demo is intentionally small and "composition-first": it wires
existing `trpc-agent-go` building blocks together, instead of hiding
them behind a large framework.

It ships:

- A runnable binary: `go run ./cmd/openclaw`
- An importable library: `trpc.group/trpc-go/trpc-agent-go/openclaw/app`

For enterprise/internal customization, the recommended pattern is to
build your own "distribution binary" in another repo:

1) Import `openclaw/app`.
2) Enable internal-only plugins via anonymous imports (`import _ "..."`).
3) Use a YAML config file to turn plugins on.

### Why a custom binary? (Go idioms)

Go is a compiled language: a running binary cannot magically discover
new Go packages at runtime.

The idiomatic "plugin" pattern in Go is:

1) A shared registry package (`openclaw/registry`).
2) Plugin packages call `registry.Register...(...)` in `init()`.
3) Your binary links plugins in by importing them (often anonymously).

### Extension points (overview)

OpenClaw demo supports these extension points:

- **Channels**: implement `openclaw/channel.Channel` and register with
  `registry.RegisterChannel(type, factory)`.
  Enable via YAML `channels: [...]`.
- **Tool providers**: register with
  `registry.RegisterToolProvider(type, factory)`.
  Enable via YAML `tools.providers: [...]`.
- **ToolSet providers**: register with
  `registry.RegisterToolSetProvider(type, factory)`.
  Enable via YAML `tools.toolsets: [...]`.
- **Model types**: register with `registry.RegisterModel(type, factory)`.
  Select via `model.mode` (`-mode`) and optional `model.config`.
- **Session backends**: register with
  `registry.RegisterSessionBackend(type, factory)`.
  Select via `session.backend` (`-session-backend`) and optional
  `session.config`.
- **Memory backends**: register with
  `registry.RegisterMemoryBackend(type, factory)`.
  Select via `memory.backend` (`-memory-backend`) and optional
  `memory.config`.
- **Skills**: no Go code needed; point `skills.extra_dirs` at a folder.

For a step-by-step plugin authoring guide (with copy-paste templates),
see `openclaw/EXTENDING.md`.

### Working example: custom binary + plugins

See `openclaw/examples/stdin_chat/` for a runnable reference
distribution binary:

- `main.go` imports `openclaw/app`
- It enables two plugins via anonymous imports:
  - `openclaw/plugins/stdin` (channel)
  - `openclaw/plugins/echotool` (tool provider)
- `openclaw.yaml` turns the plugins on

This is intentionally close to what an internal repo would do.

### Add internal skills (no code changes)

For skills, the simplest workflow is to keep a separate skills folder
and point this demo at it:

- Use `-skills-extra-dirs "/path/to/skills"` (comma-separated), or
- put skills under the managed directory: `<state-dir>/skills`.

This allows an internal team to iterate on skill packs independently,
without forking the gateway/channel code.

## Session and memory

This demo uses `trpc-agent-go` sessions to store conversation history
per `session_id` (derived from DM vs group/topic).

The session service is in-memory by default, so session history is
cleared when the process exits.

It also enables an in-memory memory service and memory tools
(`memory_add`, `memory_load`, etc.) for the agent. Stored memories are
kept in process memory and are cleared when the process exits.

### Centralized storage (Redis)

If you want a centralized store (for multi-instance deployments), you
can switch session and memory backends to Redis:

```bash
cd openclaw
go run ./cmd/openclaw \
  -mode mock \
  -session-backend redis \
  -session-redis-url "redis://127.0.0.1:6379/0" \
  -memory-backend redis \
  -memory-redis-url "redis://127.0.0.1:6379/0"
```

The Redis key-space is still isolated by `app_name` and `user_id`. You
can override `app_name` with `-app-name` (or `app_name` in YAML) to
match your business identifier.

### SQL backends (MySQL/Postgres/ClickHouse/PGVector)

This demo also supports SQL backends already implemented in
`trpc-agent-go`:

- Session: `mysql`, `postgres`, `clickhouse`
- Memory: `mysql`, `postgres`, `pgvector` (vector search via Postgres)

They are configured via `session.config` / `memory.config`. See
`openclaw/INTEGRATIONS.md` for copy-paste config examples.

### Session summarization (optional)

The runner can enqueue background session summarization jobs after
assistant replies.

Two related knobs:

- `session.summary`: generate and store summaries in the session backend.
- `agent.add_session_summary`: prepend the latest summary to the model
  context (and only send incremental history after the summary).

To enable both:

```bash
cd openclaw
go run ./cmd/openclaw \
  -mode openai \
  -session-summary \
  -session-summary-policy any \
  -session-summary-events 20 \
  -add-session-summary
```

### Auto memory extraction (optional)

The runner can also enqueue background auto memory extraction jobs after
assistant replies. When enabled, the memory service uses an LLM-based
extractor to maintain user memories automatically.

Enable with:

```bash
cd openclaw
go run ./cmd/openclaw \
  -mode openai \
  -memory-auto \
  -memory-auto-policy all \
  -memory-auto-messages 20
```

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
