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

## Enable Telegram

1) Create a bot via `@BotFather` and get a token.

2) Run the binary with `-telegram-token`:

```bash
cd openclaw
go run ./cmd/openclaw \
  -mode mock \
  -http-addr :8080 \
  -telegram-token "$TELEGRAM_BOT_TOKEN"
```

3) Open a chat with your bot (or add it into a group) and send a text
message.

The bot will forward inbound text to the gateway and send the final
reply back to Telegram.

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

To override patterns:

```bash
go run ./cmd/openclaw \
  -mode mock \
  -telegram-token "$TELEGRAM_BOT_TOKEN" \
  -require-mention \
  -mention "@mybot,/agent"
```

## Skills

By default, the binary loads skills from `./skills`. A minimal example
skill is available at `skills/hello`.

In a chat, you can ask the assistant to list and run skills. For
example:

```
List available skills, then run the hello skill.
```

