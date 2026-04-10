---
name: remote-http-mcp
description: Connect to a remote streamable HTTP MCP service that exposes announcements and FAQ tools.
---

Overview

This skill points to a remote MCP service that is not preconfigured as a
named broker server. Load this skill when you need to discover or call
that remote service dynamically.

The remote MCP service is already running for this example. This skill
does not start it. This skill only documents the endpoint and the
recommended broker workflow.

Remote MCP Endpoint

- Base URL: `__REMOTE_MCP_URL__`
- Transport: streamable HTTP

Workflow

After this skill is loaded:

1. If you need more connection detail, call `skill_list_docs` or
   `skill_select_docs`.
2. Use `mcp_list_tools` with selector `__REMOTE_MCP_URL__`.
3. When you know the candidate tool names, use `mcp_inspect_tools`
   to inspect only those tools.
4. When you know the tool, call `mcp_call` with the selector returned
   by `mcp_list_tools` or `mcp_inspect_tools`.
5. Always pass remote MCP arguments inside `mcp_call.arguments`.

Rules

- Do not invent a different endpoint. Use the exact base URL in this skill.
- Do not try to start this MCP service through `skill_run` or any other
  command. It is already running.
- Prefer `mcp_list_tools` first, then inspect only the specific tool or
  tools you plan to call.
- Prefer tool selectors returned by `mcp_list_tools` or
  `mcp_inspect_tools`. If you must construct an ad-hoc URL selector and
  dot-based parsing would be ambiguous, use the `#tool=` form, for example
  `__REMOTE_MCP_URL__#tool=announcement_publish`.
- If you need announcement publishing, look for `announcement_publish`.
- If you need FAQ tools, look for `faq_search` or `faq_read`.
