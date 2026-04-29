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

Keep raw secrets out of the skill. Prefer environment variables, token helper
commands, OAuth login state, or platform-managed credentials. If `mcp.json`
uses a token, reference it by environment variable instead of copying the
token value into the file.

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

- Config default: `./config/mcporter.json` (override with `--config`).
- Prefer `--output json` for machine-readable results.
