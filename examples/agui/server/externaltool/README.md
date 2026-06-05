# External Tool AG-UI Servers

This directory groups AG-UI examples where tool execution is split between the server-side agent flow and the caller.

- [`llmagent/`](llmagent/) uses `LLMAgent` with dynamically declared `WithExternalTools` caller-executed tools.
- [`graphagent/`](graphagent/) uses `GraphAgent` interrupt and resume with two graph-executed internal tools and two caller-executed external tools.
- [`agentnode_llmagent/`](agentnode_llmagent/) demonstrates AgentNode child `LLMAgent` external tools. The child receives node-scoped external tools, while a parent graph node performs the checkpoint interrupt and resumes through an AG-UI `role=tool` message.
- [`agentnode_graphagent/`](agentnode_graphagent/) uses a parent `GraphAgent` with two `AgentNode` children; the first child `GraphAgent` interrupts internally, and the second child `LLMAgent` runs after resume.
- [`agenttool_graphagent_graphagent/`](agenttool_graphagent_graphagent/) uses a parent `GraphAgent`
  `ToolsNode` with an `AgentTool` whose child `GraphAgent` interrupts and
  resumes through AG-UI.
