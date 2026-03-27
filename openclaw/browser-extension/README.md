# OpenClaw Browser Relay Extension

This Chrome extension attaches the current browser tab to the local
OpenClaw browser server.

What it gives you:

- a `chrome` browser profile backed by your current tab
- attach and detach from the popup
- snapshots, screenshots, navigation, cookies, storage, and tab
  focus/close
- relay-side `act` support for `click`, `type`, `press`, `hover`,
  `scrollIntoView`, `select`, `fill`, `drag`, `wait`, `evaluate`, and
  `resize`

## Load locally

1. Open `chrome://extensions`
2. Enable Developer Mode
3. Click `Load unpacked`
4. Select `openclaw/browser-extension`
5. Reload the extension after manifest or background changes

## Usage

1. Start the local browser server
2. Open the extension popup
3. Save the local browser server URL
4. Click `Attach Current Tab`
5. Use the `chrome` browser profile from OpenClaw

The attached tab will be exposed as the `chrome` browser profile on the
browser server.

If the browser server requires `OPENCLAW_BROWSER_SERVER_TOKEN`, include
it in the saved URL:

```text
http://127.0.0.1:19790?token=secret-token
```

Current relay limits:

- `console`, `pdf`, `upload`, and `dialog` remain host-profile features
- cookies and storage are scoped to the current page origin and visible
  page context
- screenshots are still limited to the visible tab viewport, but they
  can now crop to a visible `ref` or CSS `element`
- snapshots support `selector`, `interactive`, `compact`, `depth`, and
  optional overlay labels; `frame` remains unsupported in relay mode
- waits in the relay are best-effort DOM/browser checks, not full CDP
  lifecycle hooks
- relay `evaluate` and `wait --fn` accept a constrained arrow-function
  subset such as `() => document.title` or
  `(element) => element.textContent`

## Tests

The relay has a small Node test suite that runs without any browser
installation:

```bash
cd openclaw/browser-extension
npm test
```

## Automation note

For smoke tests and CI-style relay verification, the background service
worker exposes internal test hooks that the browser-server smoke script
can call after loading the extension. Real users still attach tabs
through the popup.
