# Report Agent Server

This server reuses the standard `llmagent` pipeline but orchestrates a “document mode” reporting experience. In practice it is meant to pair with the CopilotKit client under `examples/agui/client/copilotkit`, which listens for the document open/close signals and renders the report in a dedicated panel.

## Run The Server

Make sure the required API key for your chosen model provider is exported (e.g. `OPENAI_API_KEY`). 

```bash
cd examples/agui
go run ./server/reportagent --model deepseek-chat
```

The server will display startup logs indicating the bound address:

```log
2025-11-13T12:01:19+08:00       INFO    report/main.go:85       AG-UI: serving agent "report-agent" on http://127.0.0.1:8080/agui
```

Then start any AG-UI client such as `client/copilotkit` and issue a prompt like `Draft a quarterly progress report`. You should observe:

- The client spawns a document box as soon as `open_report_document` is streamed.
- The assistant's textual report arrives inside that document box.
- Once `close_report_document` runs, the box disappears and subsequent turns return to the normal chat pane.

![report](../../../../.resource/images/examples/agui-report.gif)
