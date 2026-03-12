English | [中文](README.zh_CN.md)

# OpenClaw-like Implementation (Telegram + HTTP Gateway)

This directory is a small runnable binary that implements an
OpenClaw-like shape on top of `trpc-agent-go`:

- A long-running **gateway** process (HTTP endpoints).
- A real IM **channel**: Telegram (long polling).
- A stable **session_id** derived from DM (direct message) vs group chat.
- Skills support via the built-in skills tooling in `llmagent`.

It is intended as a starting point for adding more channels
(Enterprise WeChat, Slack, etc.) and hardening operational controls.

Detailed guide:
[OpenClaw Runtime guide (English)](../docs/mkdocs/en/openclaw-runtime.md)
| [OpenClaw Runtime 指南（中文）](../docs/mkdocs/zh/openclaw-runtime.md)

## Install a prebuilt release

If you want the shortest path from "nothing installed" to "OpenClaw is
running", use the public install script:

```bash
curl -fsSL \
  https://github.com/trpc-group/trpc-agent-go/releases/latest/download/openclaw-install.sh \
  | bash
```

The installer uses the `stdin` profile by default, and that profile uses
the built-in `mock` model. That means the first run works without model
credentials or Telegram credentials.

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

You should see the local terminal channel start immediately. Type a short
message such as `hello`, then use `/quit` or `/exit` to stop.

More details:
[INSTALL.md](./INSTALL.md)
| [INSTALL.zh_CN.md](./INSTALL.zh_CN.md)
| [RELEASE.md](./RELEASE.md)
| [RELEASE.zh_CN.md](./RELEASE.zh_CN.md)

## Run from source

Run with a mock model (no external model credentials needed):

```bash
cd openclaw
go run ./cmd/openclaw -config ./openclaw.stdin.yaml
```

Note: by default, OpenClaw uses `-mode openai` and `-model gpt-5`.
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

OpenClaw supports a YAML config file to avoid a long list of CLI flags.

- Pass `-config /path/to/openclaw.yaml`, or
- set `OPENCLAW_CONFIG=/path/to/openclaw.yaml`.
- If neither is set, OpenClaw also tries
  `~/.trpc-agent-go-github/openclaw/openclaw.yaml`
  (only if the file exists).

CLI flags always override config file values.

The config file supports environment variable placeholders in the form `${NAME}`.
Missing environment variables cause OpenClaw to fail fast with a config error.

### Debug recorder (optional)

When debugging multi-step flows (especially Telegram "Processing..." messages),
it helps to have a single place that captures what happened end-to-end.

OpenClaw includes an opt-in, file-based debug recorder that writes a per-request
trace directory with:

- gateway requests/responses
- runner events
- Telegram message + attachment metadata
- (mode `full`) attachment bytes (to reproduce multimodal issues)

Enable with CLI flags:

```bash
cd openclaw
go run ./cmd/openclaw -debug-recorder
```

Or via YAML:

```yaml
debug_recorder:
  enabled: true
  mode: "full" # "full" (default) or "safe" (no attachment bytes)
  # dir: "<state_dir>/debug" # default
```

Trace output location:

- default: `<state_dir>/debug`
- canonical layout: `<YYYYMMDD>/<HHMMSS>_<channel>_<request_id>/`
- session index:
  `<by-session>/<session-or-user>/<YYYYMMDD>/<HHMMSS>_<message_id>/trace.json`
- files:
  - `meta.json`: trace start metadata
  - `events.jsonl`: event stream (one JSON object per line)
  - `result.json`: trace end status + duration
  - `attachments/<sha256>`: stored bytes (mode `full` only)
  - `by-session/.../trace.json`: pointer to the canonical trace dir

This repo ships two sample configs:

- [`./openclaw.yaml`](./openclaw.yaml) for Telegram.
- [`./openclaw.stdin.yaml`](./openclaw.stdin.yaml) for local terminal chat.

Example config:

```yaml
app_name: "openclaw"

http:
  addr: ":8080"

admin:
  enabled: true
  addr: "127.0.0.1:19789"
  auto_port: true

agent:
  # Short instruction text (optional).
  instruction: "You are a helpful assistant. Reply in a friendly tone."
  # Optional: load and merge multiple markdown files into the system prompt.
  # Files are read in alphabetical order.
  # system_prompt_dir: "./prompts/system"
  # Optional: enable an outer verification loop. Unsafe because it can
  # execute host commands.
  # ralph_loop:
  #   enabled: true
  #   max_iterations: 5
  #   verify:
  #     command: "go test ./..."
  #     timeout: "2m"

model:
  mode: "openai"
  name: "gpt-5"
  openai_variant: "auto"

tools:
  # Optional; default is serial execution.
  # When enabled and the model returns multiple tool calls in one step,
  # OpenClaw executes them concurrently.
  enable_parallel_tools: true

channels:
  - type: "telegram"
    config:
      token: "${TELEGRAM_BOT_TOKEN}"
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
- For secrets (model keys, Telegram tokens), keep them out of version control.
  Prefer environment variables when available.
- The sample config in `./openclaw.yaml` is ready to use with
  `go run ./cmd/openclaw -config ./openclaw.yaml`.
- The sample config in `./openclaw.stdin.yaml` is ready to use with
  `go run ./cmd/openclaw -config ./openclaw.stdin.yaml`.
- The sample config in `./openclaw.stdin.sqlite.yaml` is ready to use
  with `go run ./cmd/openclaw -config ./openclaw.stdin.sqlite.yaml`.
- Plugin sections:
  - `channels` configures channel plugins. The default binary ships with the
    `telegram` and `stdin` channel plugins; other channel types require a
    custom binary that imports them. See `openclaw/EXTENDING.md` and
    `openclaw/examples/stdin_chat/`.
  - `tools.enable_parallel_tools` toggles parallel tool execution for one
    model step (optional).
  - `tools.providers` and `tools.toolsets` work out of the box for the
    built-in types shipped in this repo. Custom types still require a
    custom binary. See `openclaw/INTEGRATIONS.md` and
    `openclaw/EXTENDING.md`.

## Customize prompts

OpenClaw supports customizing the main agent's prompt with either:

- Inline config fields (`agent.instruction`, `agent.system_prompt`), or
- File-based prompts (`agent.*_files`, `agent.*_dir`) to keep long prompts
  out of YAML.

CLI equivalents:

```bash
cd openclaw
go run ./cmd/openclaw \
  -mode mock \
  -agent-instruction "You are a helpful assistant." \
  -agent-system-prompt-dir ./examples/stdin_chat/prompts/system
```

## Ralph Loop (optional)

Ralph Loop is an outer loop that reruns the agent until a verifiable
completion condition is met (or until the maximum number of iterations is
reached).

OpenClaw supports it only for `agent.type: llm`, because the `claude-code`
agent does not consume session history (so loop feedback would be ignored).

Ralph Loop is considered unsafe because it can execute a host command
after each iteration.

YAML example:

```yaml
agent:
  ralph_loop:
    enabled: true
    max_iterations: 5
    verify:
      command: "go test ./..."
      timeout: "2m"
      env: ["CGO_ENABLED=1"]
```

CLI example:

```bash
cd openclaw
go run ./cmd/openclaw \
  -mode mock \
  -agent-ralph-loop \
  -agent-ralph-verify-command 'go test ./...' \
  -agent-ralph-verify-timeout 2m
```

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

Stream one message via HTTP SSE:

```bash
curl -N 'http://127.0.0.1:8080/v1/gateway/messages:stream' \
  -H 'Content-Type: application/json' \
  -d '{"from":"alice","text":"Hello"}'
```

The stream emits newline-delimited SSE events. Each `data:` payload is a
JSON `StreamEvent` with a stable `type` field:

- `run.started`
- `run.ignored`
- `run.progress`
- `message.delta`
- `message.completed`
- `run.completed`
- `run.error`

Typical successful flow:

1. `run.started`
2. zero or more `run.progress`
3. zero or more `message.delta`
4. `message.completed`
5. `run.completed`

`run.progress` is a low-frequency, system-generated status update. It is
meant for channels that want a short "still working" summary without
guessing from partial text. The first release uses stable stages such as:

- `preparing`
- `reading_document`
- `reading_spreadsheet`
- `running_tool`
- `summarizing`

For in-process integrations, channel plugins can prefer
`StreamMessage(...)` when `deps.Gateway` also implements
`registry.StreamingGatewayClient`.

Send a multimodal message via HTTP:

- Use `text` for the main text message.
- Use `content_parts` for additional inputs (images, audio, files, links, etc.).

Security note: for URL-based parts (`audio.url`, `file.url`, `video.url`),
the gateway downloads the content. By default, it blocks URLs that resolve
to loopback/private addresses to reduce SSRF risk. If you embed the gateway
server in your own program, you can adjust this via gateway options (for
example, `gateway.WithAllowPrivateContentPartURLs(true)` or
`gateway.WithAllowedContentPartDomains(...)`).

Example (text + image URL):

```bash
curl -sS 'http://127.0.0.1:8080/v1/gateway/messages' \
  -H 'Content-Type: application/json' \
  -d '{
    "from": "alice",
    "text": "What is in this image?",
    "content_parts": [
      {
        "type": "image",
        "image": {
          "url": "https://example.com/image.png",
          "detail": "auto"
        }
      }
    ]
  }'
```

Example (audio by URL):

```bash
curl -sS 'http://127.0.0.1:8080/v1/gateway/messages' \
  -H 'Content-Type: application/json' \
  -d '{
    "from": "alice",
    "content_parts": [
      {
        "type": "audio",
        "audio": {
          "url": "https://example.com/voice.wav"
        }
      }
    ]
  }'
```

Example (file by URL):

```bash
curl -sS 'http://127.0.0.1:8080/v1/gateway/messages' \
  -H 'Content-Type: application/json' \
  -d '{
    "from": "alice",
    "text": "Summarize this document.",
    "content_parts": [
      {
        "type": "file",
        "file": {
          "url": "https://example.com/report.pdf"
        }
      }
    ]
  }'
```

If you send non-text inputs (`image`, `audio`, `file`, `video`), make sure
the configured model supports those input types.

Note: OpenAI Chat Completions does not support raw file inputs in the same
way as images/audio. OpenClaw persists inbound `file` and `video` parts to
stable host paths under the state directory, keeps those refs in session
history, and exposes them back to tools. In practice this means later turns
can still operate on the same upload with `read_document`,
`read_spreadsheet`, or `exec_command`
(`$OPENCLAW_LAST_UPLOAD_PATH`, `$OPENCLAW_LAST_UPLOAD_NAME`,
`$OPENCLAW_LAST_UPLOAD_MIME`, `$OPENCLAW_LAST_PDF_PATH`,
`$OPENCLAW_LAST_AUDIO_PATH`, `$OPENCLAW_LAST_VIDEO_PATH`,
`$OPENCLAW_LAST_IMAGE_PATH`, `$OPENCLAW_SESSION_UPLOADS_DIR`) or
`skill_run` (`host://...` inputs staged into `$WORK_DIR/inputs`).

For common file-reading tasks, prefer the first-party tools:

- `read_document`: stable reads for PDF, DOCX, and text-like uploads.
- `read_spreadsheet`: stable reads for XLSX and CSV uploads.

Use `exec_command` as the fallback for conversions, custom scripts, and
other host-side work that those file tools cannot satisfy.

## Run with a real model (OpenAI)

OpenClaw uses the `model/openai` implementation with provider variants.

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

OpenClaw uses **Telegram long polling** (`getUpdates`), so it does not
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

OpenClaw recommends mention gating (`-require-mention`) for groups, so keeping
privacy enabled is usually fine. If you want to disable privacy, use
`@BotFather` and run `/setprivacy`.

### 4) Run the binary

Add a Telegram channel to your config file:

```yaml
channels:
  - type: "telegram"
    config:
      token: "<YOUR_TELEGRAM_BOT_TOKEN>"
      ## Optional:
      # streaming: "progress"
      # proxy: "http://127.0.0.1:7890"
      # http_timeout: "60s"
      # max_retries: 3
      # max_download_bytes: 20971520
      # session_reset_idle: "24h"
      # session_reset_daily: true
      # on_block: "reset"
```

Run:

```bash
cd openclaw
go run ./cmd/openclaw \
  -mode mock \
  -http-addr :8080 \
  -config ./openclaw.yaml
```

### Telegram networking (proxy / timeout / retries)

Configure Telegram networking under the Telegram channel `config:`:

- `proxy`: HTTP proxy URL (optional)
- `http_timeout`: HTTP client timeout (optional; should be > 25s long polling)
- `max_retries`: retry count for transient failures (optional; default: 3)
- `max_download_bytes`: per-file download limit for inbound attachments
  (optional; default: 20971520 / 20 MiB)

To override the Telegram API base URL (for testing), set
`OPENCLAW_TELEGRAM_BASE_URL`.

### Telegram doctor command

To quickly validate your Telegram setup (token, webhook, pairing store):

```bash
cd openclaw
go run ./cmd/openclaw doctor -config ./openclaw.yaml
```

### Dependency inspection and bootstrap

OpenClaw can inspect common host-side file tooling requirements and can
prepare a managed Python environment under the state directory for Python
packages used by host tooling.

Inspect the default file-tool profiles:

```bash
cd openclaw
go run ./cmd/openclaw inspect deps
```

Inspect specific profiles or skill metadata:

```bash
cd openclaw
go run ./cmd/openclaw inspect deps \
  -profile pdf,office \
  -skill nano-pdf
```

Preview the install plan:

```bash
cd openclaw
go run ./cmd/openclaw bootstrap deps \
  -profile common-file-tools
```

Apply the plan:

```bash
cd openclaw
go run ./cmd/openclaw bootstrap deps \
  -profile common-file-tools \
  -apply
```

The bootstrap command never runs automatically on startup. Startup logs may
print a suggested `bootstrap deps` command when optional file tools are
missing, but installation is always explicit. The managed Python environment
is created with access to the current system site-packages, so existing
packages such as `pandas` remain visible after bootstrap.

### 5) Send a message

Open a chat with your bot (or add it into a group) and send:

- a text message, or
- a photo, document, audio, voice note, video, animation, or video note.

Inbound attachments are downloaded from Telegram and forwarded to the gateway
as multimodal `content_parts`. The uploaded files are also persisted under the
OpenClaw state directory so later turns in the same chat can keep working on
the same PDF, image, audio, or video without asking the user to upload again.
When a Telegram voice note can be transcribed locally, OpenClaw also injects
the transcript as the user instruction while keeping the original audio upload
available to tools and follow-up turns.

For host-side tools, OpenClaw injects stable attachment metadata such as
`OPENCLAW_LAST_UPLOAD_PATH`, `OPENCLAW_LAST_UPLOAD_NAME`,
`OPENCLAW_LAST_UPLOAD_MIME`, `OPENCLAW_SESSION_UPLOADS_DIR`, and
`OPENCLAW_RECENT_UPLOADS_JSON`, plus kind-aware shortcuts like
`OPENCLAW_LAST_PDF_PATH`, `OPENCLAW_LAST_AUDIO_PATH`,
`OPENCLAW_LAST_VIDEO_PATH`, and `OPENCLAW_LAST_IMAGE_PATH`, so the agent can
keep operating on the recent chat uploads without guessing local paths.

When the agent generates local output files under the current working
directory or the OpenClaw state directory, Telegram can send them back as
documents, images, audio, or video via the `message` tool. Replies that mention
generated files by local path are also sanitized to user-facing filenames, and
OpenClaw will try to send those generated files back to the current chat
automatically when it can resolve them safely.

By default, DMs are **fail-closed** and require pairing.

On the first DM, the bot replies with a 6-digit pairing code and will
not process your message yet.

To approve a user, run:

```bash
cd openclaw
go run ./cmd/openclaw pairing approve <CODE> -config ./openclaw.yaml
```

You can also list pending pairing requests:

```bash
cd openclaw
go run ./cmd/openclaw pairing list -config ./openclaw.yaml
```

After approval, the bot forwards inbound text to the gateway and sends
the final reply back to Telegram.

To disable pairing (less safe), set the Telegram channel `dm_policy` to `open`:

```yaml
channels:
  - type: "telegram"
    config:
      dm_policy: "open"
```

### Telegram commands

OpenClaw supports a few basic commands:

- `/help`: show a short help message.
- `/cancel`: cancel the current run for the same DM/thread session.
- `/reset` and `/new`: start a new DM session (old data is kept).
- `/forget`: permanently delete your stored sessions, memories, and debug
  traces (DM only).
- `/jobs`: list scheduled jobs scoped to the current chat.
- `/jobs_clear`: remove scheduled jobs scoped to the current chat.
  Future executions stop immediately. If a matching job is currently
  running, OpenClaw cancels that in-flight run and suppresses any pending
  delivery for this chat.
- `/persona`: show the active persona preset for this chat and list the
  available presets.
- `/persona <id>`: switch the active persona preset for this chat.
- `/personas`: list the available persona presets.

On startup, OpenClaw also registers these commands with Telegram via
`setMyCommands`, so supported clients can show them in the slash-command
menu. If registration fails, the commands still work when typed manually.

Built-in persona presets:

- `default`: keep the normal assistant behavior.
- `girlfriend`: warm, playful, and affectionate companion tone.
- `concise`: direct, brief, and action-first replies.
- `coach`: structured, pragmatic, and goal-oriented.
- `creative`: more imaginative, vivid, and idea-rich replies.

Examples:

```text
/persona
/persona girlfriend
/persona default
```

You can also configure automatic DM session resets:

- `session_reset_idle`: rotate the active DM session after it has been idle
  for the configured duration.
- `session_reset_daily`: rotate the active DM session when the date changes
  (local time).

To react to privacy/lifecycle events, configure:

- `on_block`: what to do when a user blocks the bot (`my_chat_member` updates).
  Supported values: `reset` (default), `forget`, `none`.

### Telegram reply streaming (preview)

OpenClaw can optionally use `editMessageText` to show a processing preview,
then replace it with the final answer.

Telegram `streaming` modes (Telegram channel config):

- `off`: send the final answer as messages.
- `block`: send one "Processing..." message, then edit once to final.
- `progress` (default): keep editing the message while the model is running.

Outbound Telegram text now uses `parse_mode: "HTML"` by default:

- Markdown-ish model output is rendered into Telegram-safe HTML.
- Raw HTML from the model is escaped before sending.
- If Telegram rejects the formatted HTML, OpenClaw automatically retries
  with plain text.

To disable streaming:

```yaml
channels:
  - type: "telegram"
    config:
      streaming: "off"
```

### Telegram threads and topics

OpenClaw derives `session_id` based on whether the inbound message is a
DM (direct message, i.e. a private chat) or a group message:

- DMs: `thread` is empty, so the session is per-user. The active DM session
  can be rotated via `/reset` (or automatically via `session_reset_*`) and is
  persisted under `<state_dir>/telegram/`.
- Groups: `thread` is the chat ID, so the session is per-group.
- Group topics: if Telegram provides `message_thread_id`, `thread` becomes
  `<chat_id>:topic:<message_thread_id>`, so each topic gets its own session.

### Telegram polling offset

OpenClaw stores the Telegram `getUpdates` offset on disk so restarts
can resume from the last processed update.

- Default state dir: `$HOME/.trpc-agent-go-github/openclaw`
- Override with: `-state-dir`

On the first run (when no offset file exists), the poller drains pending
updates by default to avoid replying to very old messages. You can
disable this behavior with `start_from_latest: false` in the Telegram channel
config.

## Safety knobs

### Allowlist

To only allow specific user IDs:

```bash
go run ./cmd/openclaw \
  -mode mock \
  -config ./openclaw.yaml \
  -allow-users "123456789,987654321"
```

The allowlist is matched against:

- Telegram: the numeric `from.id` (as a string)
- HTTP: `user_id` if set, otherwise `from`

To find your Telegram `from.id`:

1) Do not run OpenClaw yet (or stop it), so your local process does not
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
  -config ./openclaw.yaml \
  -require-mention \
  -mention "@mybot"
```

Mention patterns can also be set in config via `gateway.mention_patterns`.

If `-require-mention` (or `gateway.require_mention`) is enabled and mention
patterns are empty, the gateway refuses to start.

To override patterns:

```bash
go run ./cmd/openclaw \
  -mode mock \
  -config ./openclaw.yaml \
  -require-mention \
  -mention "@mybot,/agent"
```

### Telegram group policy and allowlist

By default, OpenClaw ignores all group messages (`group_policy` defaults to
`disabled`).

To enable groups (less safe), use:

```yaml
channels:
  - type: "telegram"
    config:
      group_policy: "open"
```

To allowlist specific groups/topics, use:

```yaml
channels:
  - type: "telegram"
    config:
      group_policy: "allowlist"
      allow_threads:
        - "<chat_id>"
        - "<chat_id>:topic:<message_thread_id>"
```

You can discover `chat_id` and `message_thread_id` from `getUpdates`.

### Local code execution (unsafe)

OpenClaw can optionally enable the local code execution tool for the
agent. This is **unsafe** when exposed to external inputs (Telegram,
webhook traffic).

It is disabled by default. To enable:

```bash
go run ./cmd/openclaw \
  -mode openai \
  -config ./openclaw.yaml \
  -enable-local-exec
```

## Skills

OpenClaw supports AgentSkills-style `SKILL.md` skill folders, and
borrows a few design ideas from OpenClaw:

- Multiple skill roots (workspace, managed, extra dirs) with precedence.
- Optional load-time gating via `metadata.openclaw.requires.*`.
- `{baseDir}` placeholder substitution for better OpenClaw skill
  compatibility.

### Bundled skills

OpenClaw vendors the upstream OpenClaw skill pack under `openclaw/skills/`
(see `openclaw/skills/README.md` for attribution and license).

It also includes a few simple example skills:

- `hello`: write a small file to `out/`.
- `envdump`: dump environment info to `out/env.txt`.
- `http_get`: fetch a URL with `curl` into `out/`.

### Locations and precedence

Skills are loaded from these locations (highest precedence first):

1) Workspace skills: `-skills-root` (default: `./skills`)
2) Project AgentSkills: `./.agents/skills`
3) Personal AgentSkills: `$HOME/.agents/skills`
4) Managed skills: `<state-dir>/skills`
5) Installed bundled skills: `<state-dir>/bundled-skills`
6) Repo bundled skills (when running from repo root): `./openclaw/skills`
7) Extra dirs: `-skills-extra-dirs` (comma-separated, lowest precedence)

If two skills have the same `name`, the one from the higher-precedence
location wins.

Prebuilt release installs refresh `<state-dir>/bundled-skills` on each
install or upgrade, while leaving `<state-dir>/skills` available for your
own managed skills.

### OpenClaw metadata gating (optional)

If a skill's `SKILL.md` front matter contains `metadata.openclaw`, this
OpenClaw can filter the skill at load time based on the local environment:

- `metadata.openclaw.os` (darwin/linux/win32)
- `metadata.openclaw.requires.bins`
- `metadata.openclaw.requires.anyBins`
- `metadata.openclaw.requires.env`
- `metadata.openclaw.requires.config`

Enable `-skills-debug` to log which skills are skipped and why.

### OpenClaw-style skill config (`skills.entries`)

Upstream OpenClaw supports providing per-skill environment variables and
API keys via config. OpenClaw supports the same idea in YAML:

```yaml
skills:
  # Optional: restrict which bundled skills are enabled by default.
  # Applies only to bundled skills under ./openclaw/skills.
  allowBundled: ["gh-issues", "notion"]
  load_mode: "turn" # once|turn|session
  loaded_content_in_tool_results: true
  max_loaded_skills: 0
  skip_fallback_on_session_summary: true
  # Optional: override default skills guidance text. Set to "" to disable it.
  tooling_guidance: ""

  # Optional: per-skill config (by skillKey or skill name).
  entries:
    gh-issues:
      # Injected into metadata.openclaw.primaryEnv when present.
      apiKey: "..."
      # Injected into skill_run env (never overrides host env).
      env:
        GH_TOKEN: "..."
```

OpenClaw defaults to materializing loaded skill bodies/docs into tool
result messages. This keeps the system prompt more stable while still
letting `SkillLoadMode` control how long loaded skill state survives.

The built-in skills guidance is also more runtime-oriented by default:
the agent prefers skill-owned scripts when present and may use minimal
read-only probes such as `--help` or `--version` to verify external CLI
syntax before taking side effects.

### `{baseDir}` placeholder

Many OpenClaw skills use `{baseDir}` in commands (for example to run
scripts under `scripts/`). OpenClaw replaces `{baseDir}` in loaded
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

Note: OpenClaw skills often assume the OpenClaw tool surface. OpenClaw
enables the OpenClaw host tools for the default LLM agent so skills can
use `exec_command`, `message`, and `cron`, but it is not a full
OpenClaw replacement.

In a chat, you can ask the assistant to list and run skills. For
example:

```
List available skills, then run the hello skill.
```

## Extending OpenClaw (custom channels / internal skills)

OpenClaw is intentionally small and "composition-first": it wires
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

OpenClaw supports these extension points:

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
and point OpenClaw at it:

- Use `-skills-extra-dirs "/path/to/skills"` (comma-separated), or
- put skills under the managed directory: `<state-dir>/skills`.

This allows an internal team to iterate on skill packs independently,
without forking the gateway/channel code.

## Session and memory

OpenClaw uses `trpc-agent-go` sessions to store conversation history
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

### Local persistence (SQLite)

If you want local persistence across restarts (without running Redis),
use the SQLite-backed backends.

By default, they store data in:

- Session `sqlite`: `<state_dir>/sessions.sqlite`
- Memory `sqlite`: `<state_dir>/memories.sqlite`
- Memory `sqlitevec`: `<state_dir>/memories_vec.sqlite`

Example using session + memory SQLite:

```bash
cd openclaw
go run ./cmd/openclaw \
  -mode mock \
  -session-backend sqlite \
  -memory-backend sqlite
```

The bundled sample config `./openclaw.stdin.sqlite.yaml` also uses
SQLite for both session and memory.

`memory.backend=sqlitevec` is also supported for vector search. It uses
an OpenAI-compatible embedder and requires embedding credentials at
runtime.

SQLite-backed memory backends require a CGO-enabled build.
`sqlitevec` is additionally compiled behind the
`openclaw_sqlitevec` build tag so the default `go build ./cmd/openclaw`
path does not require the extra `sqlite-vec` header dependency.

### SQL backends (MySQL/Postgres/ClickHouse/PGVector)

OpenClaw also supports SQL backends already implemented in
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

## OpenClaw host tools (unsafe)

OpenClaw exposes a code-agent-first host tool surface for the default
LLM agent, but it is **unsafe** when exposed to untrusted inputs.

The assistant gets:

- `exec_command` for general host shell work
- `write_stdin` and `kill_session` for interactive commands
- `message` for sending text, PDFs, images, audio, or video to the current
  chat or explicit targets
- `cron` for future or recurring jobs

When OpenClaw finds a managed Python environment under
`<state_dir>/toolchain/python`, `exec_command` automatically prepends that
environment to `PATH` so host commands can use the installed Python packages
without changing prompts or shell commands.

To disable these tools explicitly:

```bash
go run ./cmd/openclaw \
  -mode openai \
  -model deepseek-chat \
  -config ./openclaw.yaml \
  -enable-openclaw-tools=false
```

Once enabled, you can ask the assistant to run a command, send to the
current chat, or create a recurring job. For example:

```
Use exec_command to run: echo hello
If it is interactive, continue with write_stdin.
Create a cron job that reports system resources every minute to
this Telegram chat.
```

Cron jobs are persisted under the OpenClaw state directory, so they
continue after gateway restarts. In Telegram, `/jobs` and `/jobs_clear`
provide a direct way to inspect or clean up jobs for the current chat
without going through the model.

When `admin.enabled` is true (default), OpenClaw also starts a local
admin surface on `admin.addr` (default `127.0.0.1:19789`). When
`admin.auto_port` is true (default), OpenClaw automatically moves to a
nearby free port if the preferred admin port is busy, and startup logs
print the actual admin URL.

The admin surface is intentionally broader than cron management. It now
includes:

- runtime and host metadata
- gateway routes and JSON endpoints
- scheduled job inspection plus run/remove/clear actions
- exec session inspection
- persisted upload browsing with direct open/download links
- upload-session and media-kind filtered JSON views for multimodal traces
- session-indexed debug trace browsing with direct links to
  `meta.json`, `events.jsonl`, and `result.json`
