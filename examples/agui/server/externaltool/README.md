# External Tool AG-UI Servers

This directory groups AG-UI examples where tool execution is split between the server-side agent flow and the caller.

- [`llmagent/`](llmagent/) uses `LLMAgent` with dynamically declared
  `WithExternalTools` caller-executed tools.
- [`agentnode/`](agentnode/) uses `GraphAgent` with an `AgentNode` whose
  child `LLMAgent` receives node-scoped external tools and resumes through an
  AG-UI `role=tool` message.
- [`graphagent/`](graphagent/) uses `GraphAgent` interrupt and resume with two graph-executed internal tools and two caller-executed external tools.
