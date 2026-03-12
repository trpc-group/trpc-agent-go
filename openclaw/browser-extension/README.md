# OpenClaw Browser Relay Extension

This Chrome extension attaches the current browser tab to the local
OpenClaw browser server.

## Load locally

1. Open `chrome://extensions`
2. Enable Developer Mode
3. Click `Load unpacked`
4. Select `openclaw/browser-extension`

## Usage

1. Start the local browser server
2. Open the extension popup
3. Save the local browser server URL
4. Click `Attach Current Tab`

The attached tab will be exposed as the `chrome` browser profile on the
browser server.

If the browser server requires `OPENCLAW_BROWSER_SERVER_TOKEN`, include
it in the saved URL:

```text
http://127.0.0.1:19790?token=secret-token
```

## Automation note

For smoke tests and CI-style relay verification, the background service
worker exposes internal test hooks that the browser-server smoke script
can call after loading the extension. Real users still attach tabs
through the popup.
