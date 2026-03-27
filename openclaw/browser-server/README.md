# OpenClaw Browser Server

This is the browser control plane for OpenClaw browser use.

It provides:

- a Playwright-backed `openclaw` browser profile
- a Chrome extension relay-backed `chrome` browser profile
- loopback/private-network/file URL navigation guards
- HTTP routes for browser actions
- a WebSocket bridge for the browser extension

Action coverage:

- `openclaw` profile: tabs, snapshot, screenshot, navigate, click, type,
  press, hover, scrollIntoView, drag, select, fill, wait, evaluate,
  cookies, storage, pdf, download, waitDownload, upload, dialog,
  offline, headers, credentials, geolocation, media, timezone, locale,
  and device
- `chrome` profile: tabs, snapshot, screenshot, navigate, click, type,
  press, hover, scrollIntoView, drag, select, fill, wait, evaluate,
  resize, cookies, and storage
- `chrome` relay still does not expose console capture, pdf export, file
  upload, dialog hooks, or host-side emulation/network overrides

Notable behavior details:

- host screenshots accept `ref` and CSS `element`; element shots reject
  `fullPage`
- host snapshots accept `mode`, `compact`, `depth`, `selector`, `frame`,
  and optional overlay `labels`
- host upload accepts `inputRef`, CSS `element`, or a clickable `ref`
  that arms a file chooser
- host cookies/storage actions work against the full Playwright context
- relay cookies/storage actions work against the current page origin and
  visible page state only
- relay screenshots can return `jpeg` and crop to a visible `ref` inside
  the current viewport
- host console capture accepts `level` filtering such as `error`

## Run

```bash
cd openclaw/browser-server
npm install
npx playwright install chromium
npm start
```

`npx playwright install chromium` is required for the managed host
profile and for the relay smoke tests. The relay smoke defaults to the
Playwright-bundled Chromium because extension sideloading is no longer a
reliable automation path in Chrome/Edge.

When OpenClaw itself runs with a browser provider whose `server_url`
points at `http://127.0.0.1:19790`, `go run ./cmd/openclaw` now probes
that address and auto-starts this browser-server if needed. Auto-start
expects the dependencies above to already be installed and logs the
managed process under `<state_dir>/debug/services/browser-server.log`.
If your checkout lives somewhere custom, point
`OPENCLAW_BROWSER_SERVER_DIR` at `openclaw/browser-server`.

Environment variables:

- `OPENCLAW_BROWSER_SERVER_ADDR`
  - default: `127.0.0.1:19790`
- `OPENCLAW_BROWSER_SERVER_TOKEN`
  - optional bearer token for HTTP APIs
  - extension relay passes it as `?token=...` on the WebSocket URL
- `OPENCLAW_BROWSER_HEADLESS`
  - default: `true`
- `OPENCLAW_BROWSER_EXECUTABLE_PATH`
  - optional system browser path such as `/usr/bin/chromium-browser`
- `OPENCLAW_BROWSER_ALLOWED_DOMAINS`
  - comma-separated
- `OPENCLAW_BROWSER_BLOCKED_DOMAINS`
  - comma-separated
- `OPENCLAW_BROWSER_ALLOW_LOOPBACK`
  - default: `false`
- `OPENCLAW_BROWSER_ALLOW_PRIVATE_NETWORKS`
  - default: `false`
- `OPENCLAW_BROWSER_ALLOW_FILE_URLS`
  - default: `false`

## Smoke tests

After `npm install`, you can run:

```bash
cd openclaw/browser-server
npm run smoke:host
npm run smoke:relay
npm run smoke:host:headed
npm run smoke:relay:headed
```

What they verify:

- `smoke:host`
  - starts the browser server
  - launches a managed browser profile
  - opens `https://example.com`
  - verifies snapshot, labeled snapshot, scrollIntoView, wait,
    wait-by-fn, cookies, storage, offline toggles, timezone override,
    evaluate, download, wait-download, and screenshot
  - injects a file input and verifies upload through both CSS selector
    and clickable ref flows
- `smoke:relay`
  - starts the browser server
  - launches Chromium with the relay extension loaded
  - attaches the current tab through the extension background
  - verifies `profile=chrome` tabs, snapshot, scrollIntoView, wait,
    wait-by-fn, cookies, storage, evaluate, and cropped `jpeg`
    screenshots
- `smoke:host:headed`
  - repeats the managed browser smoke with a visible Chromium window
- `smoke:relay:headed`
  - repeats the relay smoke with a visible Chromium window

If you want to override the default bundled browser for relay smokes, set
`OPENCLAW_BROWSER_EXECUTABLE_PATH` to a Chromium-compatible executable
before running the command.

## Main routes

- `GET /`
- `GET /profiles`
- `GET /extension/status`
- `POST /start`
- `POST /stop`
- `GET /tabs`
- `POST /tabs/open`
- `POST /tabs/focus`
- `DELETE /tabs/:id`
- `POST /snapshot`
- `POST /screenshot`
- `POST /navigate`
- `POST /console`
- `GET /cookies`
- `POST /cookies/set`
- `POST /cookies/clear`
- `GET /storage/local`
- `POST /storage/local/set`
- `POST /storage/local/clear`
- `GET /storage/session`
- `POST /storage/session/set`
- `POST /storage/session/clear`
- `POST /pdf`
- `POST /download`
- `POST /wait/download`
- `POST /upload`
- `POST /dialog`
- `POST /set/offline`
- `POST /set/headers`
- `POST /set/credentials`
- `POST /set/geolocation`
- `POST /set/media`
- `POST /set/timezone`
- `POST /set/locale`
- `POST /set/device`
- `POST /act`

The extension WebSocket endpoint is:

- `GET /extension/ws`

If you enable `OPENCLAW_BROWSER_SERVER_TOKEN`, set the extension server
URL to include the same token, for example:

```text
http://127.0.0.1:19790?token=secret-token
```
