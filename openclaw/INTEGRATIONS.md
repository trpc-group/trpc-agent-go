# OpenClaw Demo Integrations (Backends and Tools)

This document is a practical cookbook for:

- switching **session** and **memory** backends via YAML config, and
- enabling `trpc-agent-go`'s **tool ecosystem** in OpenClaw (including
  MCP ToolSets).

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

## Configuration basics (YAML)

OpenClaw supports a YAML file:

- pass `-config ./openclaw.yaml`, or
- set `OPENCLAW_CONFIG=./openclaw.yaml`.

CLI flags always override YAML values.

Duration values use Go-style strings like `30s`, `10m`, `1h`.

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

