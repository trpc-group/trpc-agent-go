# Native Browser Use Example

This example uses OpenClaw's native `browser` tool provider.

What you get:

- One first-class `browser` tool instead of raw `browser_*` MCP tools.
- Official-style actions such as `tabs`, `open`, `snapshot`,
  `screenshot`, `navigate`, and `act`.
- Playwright MCP still runs underneath, so screenshots continue to flow
  back to the model as real image messages.
- If you want host/sandbox/node routing or a current-tab relay, use the
  browser-server example in `openclaw/examples/browser_server_use/`.
  That path now also covers download/wait-download, advanced snapshots,
  cookies/storage, and host-side browser state controls.

## Requirements

- Go
- Node.js with `npx`
- An OpenAI-compatible API key

## Run

```bash
cd openclaw
go run ./cmd/openclaw \
  -config ./examples/browser_use/openclaw.yaml
```

If `:8080` is busy:

```bash
cd openclaw
go run ./cmd/openclaw \
  -config ./examples/browser_use/openclaw.yaml \
  -http-addr :18080
```

## Try it

```bash
curl -sS 'http://127.0.0.1:8080/v1/gateway/messages' \
  -H 'Content-Type: application/json' \
  -d '{"from":"alice","text":"Open https://example.com with the browser tool, take a snapshot, then tell me the page title."}'
```

The model will see one native `browser` tool and can call actions such
as:

- `{"action":"tabs"}`
- `{"action":"open","url":"https://example.com"}`
- `{"action":"snapshot"}`
- `{"action":"act","request":{"kind":"click","ref":"..."} }`
- `{"action":"act","request":{"kind":"wait","text":"Example Domain"}}`

## Security defaults

The native browser provider blocks some risky navigation targets by
default:

- loopback hosts such as `localhost` and `127.0.0.1`
- private network IPs such as `10.x.x.x` and `192.168.x.x`
- `file://` URLs

If you need them, enable them explicitly in config:

```yaml
tools:
  providers:
    - type: "browser"
      config:
        allow_loopback: true
        allow_private_networks: true
        allow_file_urls: true
```

You can also restrict browsing to a domain set:

```yaml
tools:
  providers:
    - type: "browser"
      config:
        allowed_domains: ["example.com"]
        blocked_domains: ["admin.example.com"]
```

## Optional Chrome profile

You can add a second profile for Playwright MCP's Chrome extension mode:

```yaml
tools:
  providers:
    - type: "browser"
      config:
        default_profile: "openclaw"
        profiles:
          - name: "openclaw"
            transport: "stdio"
            command: "npx"
            args: ["--yes", "@playwright/mcp@latest", "--headless"]
          - name: "chrome"
            transport: "stdio"
            command: "npx"
            args: ["--yes", "@playwright/mcp@latest", "--extension"]
```

When the user explicitly talks about the current browser tab, relay, or
extension attach flow, the model can switch to `profile:"chrome"`.

## Browser server variant

If you want OpenClaw to route `browser` through a dedicated browser
runtime service, including:

- host browser-server routing
- sandbox and node targets
- Chrome extension attach for the current tab

use:

- `openclaw/examples/browser_server_use/`
- `openclaw/browser-server/`
- `openclaw/browser-extension/`

That browser-server path needs one extra setup step before smoke tests:
run `npx playwright install chromium` inside `openclaw/browser-server`.
