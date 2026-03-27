# Browser Server Example

This example wires OpenClaw's native `browser` tool to the local
browser server instead of talking to Playwright MCP directly.

What you get:

- host target routed through `openclaw/browser-server`
- optional `chrome` profile for the current Chromium tab attached by the
  extension relay
- optional `sandbox` and `node` targets in the same browser contract

## Requirements

- Go
- Node.js
- Playwright Chromium via `npx playwright install chromium`
- Browser extension tests via `npm test` in `openclaw/browser-extension`
- An OpenAI-compatible API key

## 1. Start the browser server

```bash
cd openclaw/browser-server
npm install
npx playwright install chromium
npm start
```

The default address is `http://127.0.0.1:19790`.

## 2. Optional: load the Chrome relay extension

1. Open `chrome://extensions`
2. Enable Developer Mode
3. Click `Load unpacked`
4. Select `openclaw/browser-extension`
5. Open the extension popup and save
   `http://127.0.0.1:19790`
6. Click `Attach Current Tab`

That attached tab becomes the `chrome` browser profile on the browser
server.

## 3. Start OpenClaw

```bash
cd openclaw
go run ./cmd/openclaw \
  -config ./examples/browser_server_use/openclaw.yaml
```

## 4. Try it

Managed browser example:

```bash
curl -sS 'http://127.0.0.1:8080/v1/gateway/messages' \
  -H 'Content-Type: application/json' \
  -d '{"from":"alice","text":"Open https://example.com with browser, take a snapshot, and tell me what the page says."}'
```

Current browser tab example after extension attach:

```bash
curl -sS 'http://127.0.0.1:8080/v1/gateway/messages' \
  -H 'Content-Type: application/json' \
  -d '{"from":"alice","text":"Use the browser tool on my current Chrome tab, inspect the page, and take a screenshot."}'
```

The model can now use:

- `{"action":"tabs","profile":"openclaw"}`
- `{"action":"snapshot","profile":"chrome"}`
- `{"action":"act","profile":"chrome","request":{"kind":"click","ref":"..."}}`
- `{"action":"act","profile":"chrome","request":{"kind":"scrollIntoView","ref":"..."}}`
- `{"action":"storage","profile":"chrome","operation":"set","store":"local","key":"token","value":"abc"}`
- `{"action":"cookies","profile":"openclaw","operation":"get"}`
- `{"action":"download","profile":"openclaw","ref":"...","path":"report.txt"}`
- `{"action":"timezone","profile":"openclaw","timezoneId":"Asia/Shanghai"}`
- `{"action":"snapshot","target":"sandbox"}`
- `{"action":"snapshot","target":"node","node":"edge"}`

## Smoke the runtime directly

You can validate the browser plane before wiring it into OpenClaw:

```bash
cd openclaw/browser-server
npm install
npm run smoke:host
npm run smoke:relay
npm run smoke:host:headed
npm run smoke:relay:headed
```
The relay smoke uses Playwright's bundled Chromium by default so the
extension can load in both headless and headed runs, and the smoke now
checks snapshot, scrollIntoView, wait, wait-by-fn, cookies, storage,
evaluate, cropped relay screenshots, host upload flows, download flows,
and host advanced snapshot options end to end.

You can also validate the extension logic without launching a browser:

```bash
cd openclaw/browser-extension
npm test
```
