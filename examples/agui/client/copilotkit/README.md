# CopilotKit Front-End for the AG-UI Server

This example shows how to pair the Go-based AG-UI server with a React front-end built on [CopilotKit](https://docs.copilotkit.ai/). The UI streams Server-Sent Events from the AG-UI endpoint using the `@ag-ui/client` HTTP agent and renders an assistant sidebar provided by CopilotKit.

## Start the CopilotKit client

```bash
pnpm install   # or npm install
pnpm dev       # or npm run dev
```

Available environment variables before `pnpm dev`:

- `AG_UI_ENDPOINT`: override the AG-UI endpoint URL (defaults to  `http://127.0.0.1:8080/agui`).

Open `http://localhost:3000` and start chatting with the full-screen assistant UI. The input shows the placeholder `Calculate 2*(10+11)`, first explain the idea, then calculate, and finally give the conclusion.`—press Enter to run that scenario or type your own request. Tool calls and their results appear inline inside the chat transcript.

![agui-copilotkit](../../../../.resource/images/examples/agui-copilotkit.png)