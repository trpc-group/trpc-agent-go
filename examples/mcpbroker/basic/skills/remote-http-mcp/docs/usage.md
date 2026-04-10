Remote HTTP MCP Usage

Use this skill when the user asks for a capability that is not covered by
the named stdio demo servers and you need the dynamic remote MCP path.

The remote MCP service is already running. Do not start it. Just connect
to the URL exposed by this skill.

Suggested broker calls

Announcement publishing path:

1. List tools:

   `mcp_list_tools(selector="__REMOTE_MCP_URL__")`

2. Inspect the announcement tool:

   `mcp_inspect_tools(selector="__REMOTE_MCP_URL__", tools=["announcement_publish"])`

3. Publish an announcement with the selector returned by the broker:

   `mcp_call(selector="__REMOTE_MCP_URL__.announcement_publish", arguments={"title":"Broker via skill","audience":"engineers","body":"Remote MCP connected successfully."})`

FAQ search path:

1. List tools:

   `mcp_list_tools(selector="__REMOTE_MCP_URL__")`

2. Inspect FAQ tools:

   `mcp_inspect_tools(selector="__REMOTE_MCP_URL__", tools=["faq_search"])`

Prefer selectors returned by `mcp_list_tools` or `mcp_inspect_tools`.
If a hand-written ad-hoc URL selector would be ambiguous, use the
`#tool=` form, for example:

`mcp_call(selector="__REMOTE_MCP_URL__#tool=announcement_publish", arguments={"title":"Broker via skill","audience":"engineers","body":"Remote MCP connected successfully."})`
