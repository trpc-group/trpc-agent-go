---
name: mcporter
description: Use the mcporter CLI to list, configure, auth, and call MCP servers/tools directly (HTTP or stdio), including ad-hoc servers, config edits, CLI/type generation, and MCP-backed skills that need a durable command path.
homepage: http://mcporter.dev
metadata:
  {
    "openclaw":
      {
        "emoji": "📦",
        "requires": { "bins": ["mcporter"] },
        "install":
          [
            {
              "id": "node",
              "kind": "node",
              "package": "mcporter",
              "bins": ["mcporter"],
              "label": "Install mcporter (node)",
            },
          ],
      },
  }
---

# mcporter

Use `mcporter` to work with MCP servers directly.

In OpenClaw, treat `mcporter` as the execution substrate for MCP-backed
skills. When a user wants to keep an MCP capability for future work, prefer
creating or updating a skill that describes when to use the capability and
stores the repeatable mcporter command path. Do not create a separate runtime
registry when a skill can carry the durable behavior.

Skill-first MCP shape

- `SKILL.md`: triggers, workflow, constraints, examples, and recovery steps.
- `mcp.json`: mcporter-native server config when the skill needs a dedicated
  server definition.
- `references/`: schemas, API notes, or longer operation guides.
- `scripts/`: wrappers for fragile multi-step calls or post-processing.

Credential handling

- Keep shared, bundled, published, or repo-tracked skills free of raw secrets.
  Reference environment variables, token helper commands, OAuth login state,
  runtime config, or platform-managed credentials instead.
- If the user explicitly provides a complete private MCP config or a
  credential-bearing endpoint and asks to keep or use it, save it in a local
  private config file such as skill-local `mcp.json` in a writable
  user-managed skill root, or a dedicated user-managed private mcporter config
  file. Keep it non-shared and excluded from source control and packaging. Set
  file permissions to `0600` when possible. Do not ask the user to re-enter the
  same value as an environment variable, and do not edit shell startup or
  trusted env files just to persist it. Do not echo the secret value back in
  CLI output, logs, or errors; redact or omit token and secret fields from
  displayed tool results.
- For bot-global OpenClaw MCP capabilities, prefer a local skill plus
  skill-local `mcp.json`. Use `mcporter --config path/to/skill/mcp.json ...`
  as the durable command path. Treat `~/.mcporter/mcporter.json` as an
  interoperability copy when useful, not as the capability boundary. The
  credential-bearing source of truth for a durable skill run is the explicit
  `--config` file you pass.

For a durable MCP skill, first inspect the server schema:

```bash
mcporter --config path/to/skill/mcp.json list <server> --schema --output json
```

Then call the selected tool with explicit arguments:

```bash
mcporter --config path/to/skill/mcp.json call <server.tool> key=value
```

Quick start

- `mcporter list`
- `mcporter list <server> --schema`
- `mcporter call <server.tool> key=value`

Call tools

- Selector: `mcporter call linear.list_issues team=ENG limit:5`
- Function syntax: `mcporter call "linear.create_issue(title: \"Bug\")"`
- Full URL: `mcporter call https://api.example.com/mcp.fetch url:https://example.com`
- Stdio: `mcporter call --stdio "bun run ./server.ts" scrape url=https://example.com`
- JSON payload: `mcporter call <server.tool> --args '{"limit":5}'`

Auth + config

- OAuth: `mcporter auth <server | url> [--reset]`
- Config: `mcporter config list|get|add|remove|import|login|logout`

Daemon

- `mcporter daemon start|status|stop|restart`

Codegen

- CLI: `mcporter generate-cli --server <name>` or `--command <url>`
- Inspect: `mcporter inspect-cli <path> [--json]`
- TS: `mcporter emit-ts <server> --mode client|types`

Notes

- Prefer explicit `--config` for skills. mcporter can also use its default
  project or user config paths for local ad-hoc CLI work, but shared or
  packaged skills and automation should always pass an explicit `--config`.
- Prefer `--output json` for machine-readable results.
