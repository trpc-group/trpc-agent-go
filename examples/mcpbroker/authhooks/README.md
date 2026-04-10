# MCP Broker Auth Hook Example

This example demonstrates the new runtime auth hooks for `tool/mcpbroker`:

- `WithHTTPHeaderInjector(...)`
- `WithErrorInterceptor(...)`

Unlike the `basic` example, this one does not involve a model. It calls the
broker tools directly to show the execution path clearly:

1. list named servers
2. try to inspect a protected HTTP MCP without a user token
3. observe the business-wrapped authorization error
4. add a user token to `context.Context`
5. inspect tools again and call the protected MCP tool successfully

## What It Covers

- Named protected HTTP MCP server
- Optional ad-hoc protected HTTP MCP path
- Per-run `Authorization` header injection from `context.Context`
- Unauthorized error wrapping through `ErrorInterceptor`

## Usage

```bash
cd examples/mcpbroker/authhooks
go run .
```

Try ad-hoc mode:

```bash
go run . -mode adhoc
```

Flags:

- `-mode`: `named` or `adhoc`, default `named`
- `-token`: token stored into `context.Context` for the successful path, default `demo-user-token`

## Expected Output

You should see:

- named server discovery
- one failed `mcp_list_tools` call without a token
- one successful `mcp_list_tools` call with a token
- one successful `mcp_inspect_tools` call for `whoami`
- one successful `mcp_call` for `whoami`

The protected MCP server only accepts:

```text
Authorization: Bearer demo-user-token
```

The broker hook resolves that header from the request context, not from
model-visible arguments.

Example output:

```text
== MCP Broker Auth Hook Example ==
Mode: named
Protected URL: http://127.0.0.1:<port>/mcp

mcp_list_servers:
{
  "servers": [
    {
      "name": "secure_http",
      "transport": "streamable"
    }
  ]
}

== Without token ==
mcp_list_tools error: authorization required for http://127.0.0.1:<port>/mcp (list_tools)

== With token ==
mcp_list_tools:
{
  "tools": [
    {
      "name": "whoami",
      "selector": "secure_http.whoami",
      "signature": "whoami()",
      "description": "Return the current caller identity derived from the bearer token.",
      "has_output_schema": false
    }
  ]
}

mcp_inspect_tools:
{
  "tools": [
    {
      "name": "whoami",
      "selector": "secure_http.whoami",
      "description": "Return the current caller identity derived from the bearer token.",
      "has_output_schema": false,
      "input_schema": {
        "type": "object"
      }
    }
  ]
}

mcp_call:
{
  "content": [
    {
      "type": "text",
      "text": "current caller: demo-user"
    }
  ]
}
```
