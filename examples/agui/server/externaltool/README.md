# External Tool AG-UI Servers

This directory groups AG-UI examples where tool execution is split between the server-side agent flow and the caller.

- [`llmagent/`](llmagent/) uses `LLMAgent` with `WithToolExecutionFilter` for caller-executed tools.
- [`graphagent/`](graphagent/) uses `GraphAgent` interrupt and resume with two graph-executed internal tools and two caller-executed external tools.
