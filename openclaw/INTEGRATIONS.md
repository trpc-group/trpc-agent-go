# OpenClaw Demo Integrations (Backends and Tools)

This document is a practical cookbook for:

- switching **session** and **memory** backends via YAML config, and
- enabling `trpc-agent-go`'s **tool ecosystem** in OpenClaw (including
  MCP ToolSets).
- using file-based **skills** (`SKILL.md` folders) to extend the agent
  without writing Go code.

It is written for absolute beginners and includes copy-paste examples.

## Mental model (from first principles)

OpenClaw (in this repo) is a small runnable demo built on top of
`trpc-agent-go`.

When you send a message (Telegram or HTTP), OpenClaw:

1) derives a stable `session_id` (DM vs group/topic),
2) loads the session history from the **session service**,
3) runs the agent,
4) optionally writes updated history/memory back to storage,
5) replies to the channel.

Two storage concepts matter:

- **Session**: conversation history for one `session_id`.
- **Memory**: long-lived user facts/preferences, usually keyed by
  `app_name + user_id`.

OpenClaw lets you choose implementations for both using config:

- `session.backend` + optional `session.config`
- `memory.backend` + optional `memory.config`

In addition, OpenClaw can load **skills** from the filesystem:

- Skills are reusable "playbooks" stored as folders with `SKILL.md`.
- They are a low-friction way to ship prompts, scripts, and docs.
- They are loaded from multiple roots (workspace + managed + extra dirs).

## Configuration basics (YAML)

OpenClaw supports a YAML file:

- pass `-config ./openclaw.yaml`, or
- set `OPENCLAW_CONFIG=./openclaw.yaml`.

CLI flags always override YAML values.

Duration values use Go-style strings like `30s`, `10m`, `1h`.

## Agent selection (LLM vs Claude Code)

An **agent** is the component that decides how to answer a message.

OpenClaw supports two agent types:

- `llm` (default): uses `llmagent` + your `model` config and supports
  skills and tools.
- `claude-code`: invokes a locally installed Claude Code CLI (`claude`)
  via the existing `agent/claudecode` implementation in this repo.

### Use the Claude Code agent

YAML:

```yaml
agent:
  type: "claude-code"

  # Optional. Default is "claude" (looked up in $PATH).
  claude_bin: "claude"

  # Optional. Supported: "json" or "stream-json".
  claude_output_format: "stream-json"

  # Optional. Extra CLI args added before the session flags + prompt.
  claude_extra_args:
    - "--permission-mode"
    - "bypassPermissions"

  # Optional. Extra env vars passed to the CLI process.
  claude_env:
    - "KEY=VALUE"

  # Optional. Working directory for the CLI process.
  claude_work_dir: "."
```

CLI flags:

- `-agent-type claude-code`
- `-claude-bin <PATH>`
- `-claude-output-format json|stream-json`
- `-claude-extra-args <A,B,C>` (comma-separated)
- `-claude-env <K=V,K=V>` (comma-separated)
- `-claude-workdir <DIR>`

Notes and limitations:

- In `claude-code` mode, OpenClaw's `tools:` section is not supported
  and these flags must be off:
  - `-enable-local-exec`
  - `-enable-openclaw-tools`
  - `-refresh-toolsets-on-run`
- `agent.add_session_summary`, `agent.max_history_runs`, and
  `agent.preload_memory` are LLM-only knobs.
- You can omit `model:` in `claude-code` mode unless you enable
  model-backed features like `session.summary.enabled` or
  `memory.auto.enabled`.

## Skills (SKILL.md skill packs)

### What is a skill?

A **skill** in `trpc-agent-go` is a folder that contains:

- a `SKILL.md` file (Markdown + YAML front matter), and
- optional `.md` / `.txt` documents, scripts, and other files.

Think of it as a small, versioned "how-to" package that the agent can
load and execute.

OpenClaw exposes built-in skills tooling to the agent:

- `skill_load`: load the content of a skill.
- `skill_list_docs` / `skill_select_docs`: browse extra docs in the skill.
- `skill_run`: run a command inside a staged skill workspace.

Rule of thumb: keep logic in scripts and keep `SKILL.md` as a clear,
human-readable contract.

### How OpenClaw finds skills (roots and precedence)

OpenClaw searches multiple skill roots (highest precedence first):

1) Workspace skills: `skills.root` / `-skills-root` (default: `./skills`)
2) Project AgentSkills: `./.agents/skills`
3) Personal AgentSkills: `$HOME/.agents/skills`
4) Managed skills: `<state-dir>/skills`
5) Repo bundled skills: `./openclaw/skills` (when running from repo root)
6) Extra dirs: `skills.extra_dirs` / `-skills-extra-dirs`

Duplicate names:

- If two skills have the same `name`, the higher-precedence one wins.
- This demo is **fail-closed**: if the winning skill is gated off (see
  below), OpenClaw does not fall back to a lower-precedence copy.

### Configure skills in YAML

```yaml
skills:
  root: "./skills"               # optional; overrides workspace root
  extra_dirs:
    - "/path/to/team/skills"      # optional; lowest precedence
  debug: false                   # log gating decisions when true
```

CLI equivalents:

- `-skills-root <DIR>`
- `-skills-extra-dirs <A,B,C>` (comma-separated)
- `-skills-debug`

### Minimal `SKILL.md` template

Create a new folder under your workspace skills root:

```
skills/
  hello/
    SKILL.md
    scripts/
      hello.sh
```

Example `skills/hello/SKILL.md`:

```md
---
name: hello
description: Write a hello file to the workspace output directory.
---

Overview

This skill writes a small file under `out/`.

Command

bash scripts/hello.sh out/hello.txt

Output Files

- out/hello.txt
```

Then run OpenClaw (skills are discovered automatically):

```bash
cd openclaw
go run ./cmd/openclaw -config ./examples/stdin_chat/openclaw.yaml
```

This starts a local terminal chat (STDIN channel).

Now ask the assistant to list and run skills. For example:

```
List available skills, then run the hello skill.
```

### How `skill_run` runs your command (practical tips)

When the agent runs a skill, it typically calls `skill_run`.

`skill_run` does a few important things behind the scenes:

- It stages the entire skill folder into a per-session workspace.
- It makes the staged skill files **read-only** (so skills are treated as
  immutable inputs).
- It creates convenient writable directories under the skill root:
  `out/`, `work/`, and `inputs/`.

Practical guidance when writing skills:

- Use relative paths (for example `bash scripts/run.sh ...`).
- Write outputs under `out/` so the tool can collect and return them.
- Keep outputs small and text-friendly when possible.

### OpenClaw metadata gating (optional)

If `SKILL.md` front matter contains `metadata.openclaw`, OpenClaw can
filter the skill at load time based on the local environment.

Supported fields:

- `metadata.openclaw.always`
- `metadata.openclaw.os` (`darwin`, `linux`, `win32`)
- `metadata.openclaw.requires.bins`
- `metadata.openclaw.requires.anyBins`
- `metadata.openclaw.requires.env`
- `metadata.openclaw.requires.config`

#### `requires.config` (config-based gating)

Some skill packs want to be visible only when a certain integration is
enabled in your OpenClaw config. For example, a Discord skill should not
show up unless the Discord channel is configured.

To support that, this demo builds a set of **config keys** at startup
based on your YAML config and enabled plugins. A skill can then require
one or more keys:

```yaml
metadata:
  openclaw:
    requires:
      config:
        - "channels.discord.token"
        - "tools.providers.mcp"
```

How keys are derived (practical rules):

- Channels
  - If a channel plugin exists in `channels:`, this key is present:
    - `channels.<type>`
  - If the channel's `config:` contains a truthy value, this key is
    present:
    - `channels.<type>.<fieldPath>`
- Tool providers
  - `tools.providers.<type>`
  - `tools.providers.<type>.<fieldPath>` (truthy values only)
- ToolSets
  - `tools.toolsets.<type>`
  - `tools.toolsets.<type>.<fieldPath>` (truthy values only)

Truthy values:

- strings: non-empty (after trimming spaces)
- bool: `true`
- numbers: non-zero
- lists/maps: non-empty

Compatibility aliases (for upstream OpenClaw skill packs):

- For every configured plugin `type`, this demo also adds:
  - `plugins.entries.<type>.enabled`
  - `plugins.entries.<type>.config.<fieldPath>`

Built-in integrations:

- If Telegram is enabled (`telegram.token` / `-telegram-token`), this
  demo adds:
  - `channels.telegram`
  - `channels.telegram.token`

Tool surface keys (optional):

- If `tools.enable_openclaw_tools: true`, this demo adds:
  - `tools.exec`, `tools.bash`, `tools.process`
- If `tools.enable_local_exec: true`, this demo adds:
  - `tools.local_exec`

To inspect the derived config keys for your current settings, run:

```bash
cd openclaw
go run ./cmd/openclaw inspect config-keys -config ./openclaw.yaml
```

This prints one key per line, suitable for copy/pasting into
`metadata.openclaw.requires.config`.

Example (require `curl`):

```md
---
name: http_get
description: Fetch a URL with curl and write it to out/.
metadata:
  openclaw:
    requires:
      bins: ["curl"]
---

...
```

To understand why a skill is missing, enable debug logs:

- YAML: `skills.debug: true`
- CLI: `-skills-debug`

### `{baseDir}` placeholder

Some OpenClaw skill packs use `{baseDir}` in commands and docs to mean
"the directory that contains this skill".

This demo replaces `{baseDir}` in loaded skill bodies/docs with the
actual local path to keep those skill packs usable.

### Skills and tool compatibility (OpenClaw-style `exec` / `process`)

Some skill packs assume an OpenClaw-like tool surface, especially:

- `exec` (or `bash`): execute a shell command
- `process`: manage long-running sessions

This demo can enable OpenClaw-compatible tools:

```yaml
tools:
  enable_openclaw_tools: true
```

To further reduce risk, you can restrict what `skill_run` is allowed to
execute.

Set either env var on the OpenClaw process:

- `TRPC_AGENT_SKILL_RUN_ALLOWED_COMMANDS`: allowlist (comma/space separated)
- `TRPC_AGENT_SKILL_RUN_DENIED_COMMANDS`: denylist (comma/space separated)

When either list is set, `skill_run` rejects shell syntax (pipes,
redirects, `&&`, `||`) and runs exactly one executable + args.

Security note: these tools can execute local commands. Only enable them
for trusted users/channels.

### Advanced: remote skill packs via URL

`trpc-agent-go/skill` supports `http://` / `https://` skill roots that
point to an archive (`.zip`, `.tar`, `.tar.gz`, `.tgz`) containing a
skills directory tree.

This is useful when you want to ship a versioned skill pack as an
artifact without rebuilding the OpenClaw binary:

```yaml
skills:
  root: "https://example.com/skills.tgz"
```

## Session backends

Supported `session.backend` values:

- `inmemory` (default)
- `redis`
- `mysql`
- `postgres`
- `clickhouse`

### Session: inmemory (default)

Good for local demos. Data is lost when the process exits.

```yaml
session:
  backend: "inmemory"
```

### Session: redis

Good for centralized storage (multi-instance deployments).

```yaml
session:
  backend: "redis"
  redis:
    url: "redis://127.0.0.1:6379/0"
    key_prefix: "openclaw"
```

Notes:

- `url` and `instance` are two ways to specify where Redis is.
  Use `url` unless you have an internal service discovery system.
- `key_prefix` is optional. `app_name` is still used for isolation.

### Session: mysql / postgres / clickhouse

These backends are configured via `session.config`.

Pick one of:

- `dsn`: a DSN string, or
- `instance`: an instance name (useful in environments with DB
  discovery/config systems).

Common config fields:

- `skip_db_init`: set true if your DB schema is pre-created
- `table_prefix`: optional prefix for table names

MySQL example:

```yaml
session:
  backend: "mysql"
  config:
    dsn: "user:pass@tcp(127.0.0.1:3306)/openclaw"
    skip_db_init: false
    table_prefix: "oc_"
```

Postgres example:

```yaml
session:
  backend: "postgres"
  config:
    dsn: "postgres://user:pass@127.0.0.1:5432/openclaw?sslmode=disable"
    skip_db_init: false
    table_prefix: "oc_"
```

ClickHouse example:

```yaml
session:
  backend: "clickhouse"
  config:
    dsn: "clickhouse://127.0.0.1:9000/default"
    skip_db_init: false
    table_prefix: "oc_"
```

## Memory backends

Supported `memory.backend` values:

- `inmemory` (default)
- `redis`
- `mysql`
- `postgres`
- `pgvector`

### Memory: inmemory (default)

Good for local demos. Data is lost when the process exits.

```yaml
memory:
  backend: "inmemory"
```

### Memory: redis

Centralized storage.

```yaml
memory:
  backend: "redis"
  redis:
    url: "redis://127.0.0.1:6379/0"
    key_prefix: "openclaw"
  limit: 200
```

### Memory: mysql / postgres

Configured via `memory.config`.

Pick one of:

- `dsn`, or
- `instance`

Common config fields:

- `schema` (Postgres only, optional)
- `table_name` (optional)
- `skip_db_init`
- `soft_delete` (optional)

MySQL example:

```yaml
memory:
  backend: "mysql"
  limit: 500
  config:
    dsn: "user:pass@tcp(127.0.0.1:3306)/openclaw"
    skip_db_init: false
    table_name: "memories"
    soft_delete: true
```

Postgres example:

```yaml
memory:
  backend: "postgres"
  limit: 500
  config:
    dsn: "postgres://user:pass@127.0.0.1:5432/openclaw?sslmode=disable"
    schema: "public"
    skip_db_init: false
    table_name: "memories"
    soft_delete: true
```

### Memory: pgvector (vector search on Postgres)

`pgvector` uses Postgres + the `pgvector` extension, and an **embedder**
to convert text into vectors.

The OpenClaw demo currently supports an OpenAI-compatible embedder:

- by default it uses environment variables (recommended), or
- you can configure `memory.config.embedder` explicitly.

Minimal example (embedder reads `OPENAI_API_KEY`):

```yaml
memory:
  backend: "pgvector"
  limit: 500
  config:
    dsn: "postgres://user:pass@127.0.0.1:5432/openclaw?sslmode=disable"
    schema: "public"
    table_name: "memories"
    index_dimension: 1536
    max_results: 8
```

Explicit embedder example:

```yaml
memory:
  backend: "pgvector"
  config:
    dsn: "postgres://user:pass@127.0.0.1:5432/openclaw?sslmode=disable"
    embedder:
      type: "openai"
      model: "text-embedding-3-small"
      dimensions: 1536
      api_key: "<YOUR_API_KEY>"
      base_url: "https://api.openai.com/v1"
```

Security note: treat `api_key` as a secret. Prefer environment variables
over committing config files.

## Conversation compression (summary) and context shaping

This section covers two "enterprise-ish" features that many OpenClaw
users care about:

- **Session summary**: compress long chat history into a short summary.
- **Memory**: persist long-lived facts about the user (preferences, IDs,
  frequently used paths, etc).

They sound similar but solve different problems:

- A **session summary** is about *this conversation thread* (one
  `session_id`). It helps keep context short as the thread grows.
- **memory** is about *the user* across many sessions. It helps the
  assistant remember stable facts.

### Session summarization (writes the summary)

When enabled, the runner enqueues background jobs after assistant
replies to generate a summary and store it in the session backend.

Enable via YAML:

```yaml
session:
  summary:
    enabled: true
    policy: "any"            # any|all; see below
    event_threshold: 20      # summarize after N new events
    token_threshold: 0       # optional; 0 means "ignore"
    idle_threshold: "0s"     # optional; 0 means "ignore"
    max_words: 200           # optional; best-effort cap
```

Policy:

- `any`: summarize when **any** threshold triggers (recommended).
- `all`: summarize only when **all** thresholds trigger.

Practical tip: if you're testing locally, set `event_threshold` small
(for example `5`) so you can see summaries appear quickly.

### Session summary injection (uses the summary)

Generating a summary is only half of the story. You also need to tell
the agent to **use** that summary as part of the model context:

```yaml
agent:
  add_session_summary: true
```

What this does:

1) OpenClaw prepends the latest session summary as a **system** message.
2) It then only includes the **incremental** messages after the summary,
   instead of sending the entire history every time.

This is the main way to reduce prompt token usage in long-running
threads.

### Preload memories into the prompt (optional)

If you use a memory backend, you can preload recent memories into the
system prompt:

```yaml
agent:
  preload_memory: 10   # 0=off, -1=all, N>0=most recent N
```

Recommendation: keep this small (like 10–50) so it stays readable and
doesn't dominate the prompt.

### Cap raw history when you do not use summary (optional)

If you do **not** enable `agent.add_session_summary`, you can still cap
how much raw history is sent to the model:

```yaml
agent:
  max_history_runs: 50   # 0=unlimited
```

Note: `max_history_runs` is only applied when `add_session_summary` is
false.

## Tools: providers vs ToolSets

OpenClaw can expose tools to the agent in two ways:

- `tools.providers`: add one or more `tool.Tool` instances (static list)
- `tools.toolsets`: add one or more `tool.ToolSet` instances (dynamic or
  grouped tools)

Tool naming:

- Tools from `tools.providers` keep their own tool name.
- Tools from `tools.toolsets` are automatically namespaced to avoid name
  conflicts:
  `"<toolset_name>_<tool_name>"`.

If you set `tools.refresh_toolsets_on_run: true`, ToolSet tools are
reloaded on each agent run (useful for MCP where tools can change).

## Built-in tool providers

These providers are built in to the OpenClaw demo binary.

### Provider: duckduckgo

Adds one tool: `duckduckgo_search`.

```yaml
tools:
  providers:
    - type: "duckduckgo"
      config:
        timeout: "30s"
```

Optional config fields:

- `base_url` (default uses the official API endpoint)
- `user_agent`
- `timeout`

### Provider: webfetch_http (safe by default)

Adds one tool: `web_fetch`.

This provider is **fail-closed** by default: you must provide either an
allowlist (`allowed_domains`) or explicitly set `allow_all_domains`.

Allowlist example:

```yaml
tools:
  providers:
    - type: "webfetch_http"
      config:
        allowed_domains:
          - "example.com"
          - "example.com/docs"
        timeout: "30s"
```

Open (not recommended):

```yaml
tools:
  providers:
    - type: "webfetch_http"
      config:
        allow_all_domains: true
```

Optional config fields:

- `blocked_domains`
- `max_content_length`
- `max_total_content_length`

## Built-in ToolSets

### ToolSet: mcp

Connects to an MCP server and exposes its tools.

Recommended settings:

- set `tools.refresh_toolsets_on_run: true`
- set a `name` for the toolset (it becomes the namespace prefix)

Example using the local stdio MCP server from
`openclaw/examples/mcp_stdio_server/`:

```yaml
tools:
  refresh_toolsets_on_run: true
  toolsets:
    - type: "mcp"
      name: "demo_mcp"
      config:
        transport: "stdio"
        command: "go"
        args: ["run", "./examples/mcp_stdio_server"]
        timeout: "60s"
        tool_filter:
          mode: "include"
          names: ["echo", "add"]
        reconnect:
          enabled: true
          max_attempts: 3
```

Supported transports:

- `stdio`
- `sse`
- `streamable` / `streamable_http`

For `sse` / `streamable`, use:

```yaml
config:
  transport: "sse"
  server_url: "http://127.0.0.1:8081/sse"
  headers:
    Authorization: "Bearer token"
```

### ToolSet: file (read-only by default)

File tools are powerful. This provider defaults to **read-only**:

- `save_file` is disabled
- `replace_content` is disabled

Read-only example:

```yaml
tools:
  toolsets:
    - type: "file"
      name: "fs"
      config:
        base_dir: "."
        read_only: true
```

Enable write operations (use with caution):

```yaml
tools:
  toolsets:
    - type: "file"
      name: "fs"
      config:
        base_dir: "."
        enable_save: true
        enable_replace: true
```

### ToolSet: openapi

Turns an OpenAPI spec into a toolset (one tool per operation).

The spec source must be exactly one of:

- `spec.file`
- `spec.url`
- `spec.inline`

From file:

```yaml
tools:
  toolsets:
    - type: "openapi"
      name: "petstore"
      config:
        spec:
          file: "./petstore.yaml"
        timeout: "30s"
        allow_external_refs: false
```

Inline:

```yaml
tools:
  toolsets:
    - type: "openapi"
      name: "demo"
      config:
        spec:
          inline: |
            openapi: 3.0.0
            info: {title: Demo, version: "1.0"}
            paths: {}
```

### ToolSet: google

Google Search toolset (requires credentials).

Config fields:

- `api_key` (or environment `GOOGLE_API_KEY`)
- `engine_id` (or environment `GOOGLE_SEARCH_ENGINE_ID`)
- optional: `lang`, `size`, `offset`, `base_url`, `timeout`

```yaml
tools:
  toolsets:
    - type: "google"
      name: "google"
      config:
        lang: "en"
        size: 5
```

### ToolSet: wikipedia

Wikipedia search toolset.

```yaml
tools:
  toolsets:
    - type: "wikipedia"
      name: "wiki"
      config:
        language: "en"
        max_results: 5
```

### ToolSet: arxivsearch

ArXiv paper search toolset.

```yaml
tools:
  toolsets:
    - type: "arxivsearch"
      name: "arxiv"
      config:
        page_size: 5
        delay_seconds: "1s"
        num_retries: 3
```

### ToolSet: email

Email sending toolset.

Important: the tool requires credentials in tool call arguments. Do not
enable it for untrusted users/channels.

```yaml
tools:
  toolsets:
    - type: "email"
      name: "mail"
```

## Custom plugins (internal distributions)

If you need extra channels/tools/backends not shipped in this repo, the
Go-idiomatic way is:

1) create a small binary in another repo that imports `openclaw/app`, and
2) register your own factories via `openclaw/registry`.

See `openclaw/EXTENDING.md` for the full guide.
