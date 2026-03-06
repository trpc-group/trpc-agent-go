# OpenClaw Runtime Redesign

## Goal

Make OpenClaw feel natural for broad local-agent tasks without pushing
generic execution concerns into `skill_run` or breaking
`trpc-agent-go` framework compatibility.

The target UX is:

- normal local work uses `exec_command`
- interactive local work uses `exec_command` + `write_stdin`
- sending back to the current chat uses `message`
- future or recurring work uses `cron`
- skills stay as constrained workspace wrappers, not generic shell entry

## Non-goals

- Do not change `trpc-agent-go` core tool semantics in a breaking way.
- Do not force all framework users to adopt OpenClaw chat or scheduler
  concepts.
- Do not keep legacy OpenClaw `exec` / `bash` / `process` names if the
  cleaner surface is better.

## Layer split

### `trpc-agent-go`

Keep the framework focused on reusable substrate:

- runner
- session/memory/artifact services
- skill/workspace execution
- additive interactive execution support already in `codeexecutor`

This layer should not learn:

- Telegram chat ids
- current chat delivery
- gateway cron jobs
- OpenClaw-specific product prompts

### OpenClaw

OpenClaw owns host and channel semantics:

- host shell execution
- current-session-aware outbound messaging
- gateway-managed scheduler
- tool guidance for choosing the right tool

## Canonical tool surface

OpenClaw should expose these tools by default:

- `exec_command`
- `write_stdin`
- `kill_session`
- `message`
- `cron`

OpenClaw should stop exposing `exec`, `bash`, and `process` as the main
tool surface.

## Detailed design

### 1. Host execution

#### Tool names

- `exec_command`
- `write_stdin`
- `kill_session`

#### Behavior

`exec_command` runs a host shell command in the OpenClaw process
environment.

Input:

- `command` required
- `workdir` optional
- `env` optional
- `yield_time_ms` optional
- `timeout_sec` optional
- `tty` optional
- `background` optional

Output:

- `status`: `running` or `exited`
- `output`: recent stdout/stderr text
- `exit_code` when exited
- `session_id` when background or interactive

`write_stdin` targets an existing process session.

Input:

- `session_id` required
- `chars` optional
- `yield_time_ms` optional
- `close_stdin` optional

Behavior:

- write chars when provided
- when `chars` is empty, it behaves like a poll
- returns latest status/output/exit_code

`kill_session` terminates a session started by `exec_command`.

#### Implementation

Reuse `openclaw/internal/octool/Manager` as the execution engine.

Planned changes:

- keep manager/session internals
- replace old tool wrappers with canonical tool wrappers
- keep PTY support in manager
- make output shape closer to Codex-style exec tools

### 2. Current-session outbound messaging

#### Problem

The agent can reply through the normal gateway turn, but it cannot say
"send this to the current Telegram chat later" because no tool owns that
concept.

#### Tool name

- `message`

#### Behavior

Default behavior:

- if no explicit target is provided, send to the current session target

Explicit behavior:

- `channel` optional
- `target` optional
- `text` required

Target resolution order:

1. explicit tool args
2. runtime-state delivery target
3. infer from current session id

This lets:

- regular chat runs send to the active chat
- cron runs send to the job delivery target
- explicit cross-target sends still work when needed

#### Channel abstraction

Add an optional outbound sender interface in `openclaw/channel`.

OpenClaw-only channels may implement:

- `SendText(ctx, target, text) error`

The target string remains channel-specific.

For Telegram:

- DM target: `<chatID>`
- topic target: `<chatID>:topic:<topicID>`

Telegram already has enough information in session ids to infer this.

#### Implementation

Add:

- channel-level outbound sender interface
- outbound router/registry in OpenClaw
- Telegram implementation for `SendText`
- session-id parsing helpers for default target inference

### 3. Gateway scheduler

#### Tool name

- `cron`

#### Why a gateway scheduler

Using `launchd` or raw `cron` should be an advanced fallback, not the
default product path.

The scheduler should:

- survive restarts
- know which user/session created the job
- run future agent turns
- optionally announce back to the originating chat

#### Supported job model

First implementation should support the most useful isolated pattern:

- `schedule.kind = "at"`
- `schedule.kind = "every"`
- `schedule.kind = "cron"`
- payload is always an isolated agent turn

Job fields:

- `id`
- `name`
- `enabled`
- `schedule`
- `message`
- `user_id`
- `delivery.channel`
- `delivery.target`
- `timeout_sec`
- `created_at`
- `updated_at`
- `last_run_at`
- `next_run_at`

The scheduler creates a fresh isolated session per run:

- `cron:<jobID>:<runID>`

This avoids unbounded cross-run context accumulation.

#### Delivery

If `delivery` is omitted in a normal interactive chat session:

- default to the current chat

If a cron run uses `message`, runtime state still carries the same
delivery target so the agent can address the same chat naturally.

#### Persistence

Persist under OpenClaw state dir:

- `cron/jobs.json`

Use one service that:

- loads jobs on startup
- recalculates `next_run_at`
- ticks in the background
- runs due jobs
- updates persisted state after mutations and runs

#### Implementation

Add a new internal package, for example:

- `openclaw/internal/cron`

Core pieces:

- typed job model
- JSON store
- schedule calculator
- background service
- tool wrapper

### 4. Tool guidance

OpenClaw should explicitly guide the model:

- use `exec_command` for host shell work
- use `write_stdin` for interactive follow-up input
- use `message` to send to the current chat or explicit target
- use `cron` for future or recurring work
- use `skill_run` only for skill workspace workflows

This guidance belongs in OpenClaw agent setup, not core framework code.

## File-level implementation plan

### OpenClaw

#### Replace old host tool surface

- `openclaw/internal/octool/tools.go`
  - remove old `exec` / `bash` / `process` declarations
  - add canonical wrappers

#### Add outbound sender interface

- `openclaw/channel/channel.go`
  - add optional outbound sender interface

#### Add Telegram sender support

- `openclaw/internal/channel/telegram/channel.go`
- `openclaw/internal/channel/telegram/utils.go`
- maybe new helper file if needed

#### Add outbound router

- new package under `openclaw/internal/`
  - maintain channel sender registry
  - resolve and dispatch outbound text

#### Add scheduler

- new package under `openclaw/internal/cron`
  - types
  - service
  - store
  - schedule helpers
  - tool

#### Wire runtime/app

- `openclaw/app/app.go`
  - create shared exec manager
  - create outbound router
  - create cron service
  - register canonical tools
  - register channels into outbound router
  - start/stop cron service with runtime lifecycle

- `openclaw/app/run_options.go`
  - add config toggles if needed
  - preferably keep tool enablement simple

#### Update docs

- `openclaw/README.md`
- `openclaw/INTEGRATIONS.md`
- maybe sample config snippets

### `trpc-agent-go`

Avoid broad surface changes.

Only consider framework edits if strictly additive and necessary.

Current plan is to avoid core changes beyond already-landed additive
interactive skill execution support.

## Compatibility strategy

### Framework compatibility

- no renaming or semantic changes to existing framework APIs
- only additive use of existing `runner.RunOptions`
- no changes to skill core behavior

### OpenClaw compatibility

OpenClaw is allowed to clean up historical tool surface.

This redesign prefers a clean default tool surface over preserving the old
`exec` / `bash` / `process` names.

## Why this is the best tradeoff

This plan keeps the clean architecture:

- core framework stays reusable
- OpenClaw becomes code-agent-first
- generic shell work no longer abuses `skill_run`
- periodic tasks become a composition of generic primitives

It also directly addresses the observed Telegram pain points:

- current-chat delivery becomes first-class
- periodic tasks no longer require `BOT_TOKEN` or manual `chat_id`
- interactive CLI work uses real host execution tools
