# Tool Safety Guard example

See `../../tool/safety/README.md` for design notes.

```bash
go run .
```

Uses `agent.WithToolPermissionPolicy` in real agents; this demo calls
`Guard.CheckToolPermission` directly so it runs without an API key.
