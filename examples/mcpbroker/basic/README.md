# MCP Broker Example

This example demonstrates the `tool/mcpbroker` flow for `LLMAgent`,
including a skill-discovered remote MCP endpoint.

Unlike the legacy `tool/mcp` example, the model does not see every
remote MCP tool up front. The MCP surface stays brokered through:

- `mcp_list_servers`
- `mcp_list_tools`
- `mcp_inspect_tools`
- `mcp_call`

This demo also enables skills in `knowledge_only` mode, so the model can
progressively load a skill that reveals an ad-hoc streamable HTTP MCP URL:

- `skill_load`
- `skill_list_docs`
- `skill_select_docs`

The agent uses those tools to progressively discover, inspect, and call
MCP tools on demand. `mcp_list_tools` returns lightweight summaries, and
`mcp_inspect_tools` expands schema only for selected tools instead of
dumping an entire catalog worth of raw schema.

This is not a `skill_run` scenario. The remote MCP service is already
running before the first model turn. The skill only documents the remote
endpoint and what that endpoint can do.

## What This Example Covers

- A local `stdio` MCP server with a wider tool catalog:
  - utility: `echo`, `add`
  - issue workflows: `issue_create`, `issue_list`, `issue_comment`
  - docs workflows: `doc_search`, `doc_read`
  - calendar workflows: `calendar_create`, `calendar_list`
  - meeting workflows: `meeting_schedule`, `meeting_cancel`
- A local streamable HTTP MCP server started by the example process
  itself before the conversation begins, but only exposed to the model
  through the skill
  `remote-http-mcp`
- `LLMAgent + Runner` interactive chat
- Broker-enabled MCP discovery and invocation
- Code-configured named server
- Skill-driven discovery of an ad-hoc HTTP MCP endpoint
- No `skill_run`; only `skill_load`, `skill_list_docs`, and
  `skill_select_docs`

## Prerequisites

- Go 1.24 or later
- Valid OpenAI-compatible credentials

## Environment Variables

| Variable | Description | Default |
| --- | --- | --- |
| `OPENAI_API_KEY` | API key for the model service | required |
| `OPENAI_BASE_URL` | Base URL for the model API endpoint | `https://api.openai.com/v1` |

## Usage

Run from the repository root:

```bash
cd examples/mcpbroker/basic
export OPENAI_API_KEY="<your-api-key>"
go run .
```

With an OpenAI-compatible proxy:

```bash
cd examples/mcpbroker/basic
export OPENAI_BASE_URL="https://your-openai-compatible-endpoint/v1"
export OPENAI_API_KEY="<your-api-key>"
go run . -model gpt-5 -streaming=false
```

Keep real API keys in your shell environment. Do not write them into README
files, committed scripts, or logs.

Run a single-turn prompt:

```bash
go run . -prompt "Create a calendar event titled \"Broker demo review\" starting at 2026-04-07T15:00:00+08:00 and invite alice@example.com."
```

Run a one-shot local stdio demo:

```bash
go run . -model gpt-5 -streaming=false \
  -prompt "Create a calendar event titled \"Broker demo review\" starting at 2026-04-07T15:00:00+08:00 and invite alice@example.com."
```

Run a one-shot skill-discovered remote HTTP demo:

```bash
go run . -model gpt-5 -streaming=false \
  -prompt "Load the skill remote-http-mcp, find its remote MCP endpoint, inspect the announcement tools there, and publish an announcement titled \"Broker via skill\" to engineers saying \"Remote MCP connected successfully.\""
```

Real one-shot run excerpt:

```bash
go run . -model gpt-5 -streaming=false \
  -prompt "Create a calendar event titled \"Broker demo review\" starting at 2026-04-07T15:00:00+08:00 and invite alice@example.com."
```

Output:

```text
🚀 MCP Broker Example
Model: gpt-5
Variant: auto
Streaming: false
Agent-visible tools: mcp_call, mcp_inspect_tools, mcp_list_servers, mcp_list_tools, skill_list_docs, skill_load, skill_select_docs
Demo named servers: local_stdio_code
Demo skills: remote-http-mcp
========================================================================

🤖 Assistant:
🔧 Tool calls:
   • mcp_list_servers
     Args: {}
🔄 Executing tools...

✅ Tool results:
   • {"servers":[{"name":"local_stdio_code","transport":"stdio"}]}

🔧 Tool calls:
   • mcp_list_tools
     Args: {"selector":"local_stdio_code"}
🔄 Executing tools...

✅ Tool results:
   • {"tools":[{"name":"add","selector":"local_stdio_code.add","signature":"add(a: number, b: number)","description":"Add two numbers and return the numeric result.","has_output_schema":false},{"name":"calendar_create","selector":"local_stdio_code.calendar_create","signature":"calendar_create(title: string, start_time: string, attendee?: string)","description":"Create a calendar event with title and start time.","has_output_schema":false},{"name":"calendar_list","selector":"local_stdio_code.calendar_list","signature":"calendar_list(day?: string)","description":"List upcoming calendar events for an optional day.","has_output_schema":false},{"name":"doc_read","selector":"local_stdio_code.doc_read","signature":"doc_read(doc_id: string)","description":"Read a documentation article by document identifier.","has_output_schema":false},{"name":"doc_search","selector":"local_stdio_code.doc_search","signature":"doc_search(query: string, product?: string)","description":"Search documentation articles by query and optional product area.","has_output_schema":false},{"name":"echo","selector":"local_stdio_code.echo","signature":"echo(text: string, prefix?: string)","description":"Echo text back to the caller with an optional prefix.","has_output_schema":false},{"name":"issue_comment","selector":"local_stdio_code.issue_comment","signature":"issue_comment(issue_id: string, body: string)","description":"Add a comment to an existing issue.","has_output_schema":false},{"name":"issue_create","selector":"local_stdio_code.issue_create","signature":"issue_create(title: string, description?: string, priority?: string)","description":"Create a project issue with title, optional description, and priority.","has_output_schema":false},{"name":"issue_list","selector":"local_stdio_code.issue_list","signature":"issue_list(assignee?: string, status?: string)","description":"List project issues filtered by optional status or assignee.","has_output_schema":false},{"name":"meeting_cancel","selector":"local_stdio_code.meeting_cancel","signature":"meeting_cancel(meeting_id: string)","description":"Cancel an online meeting by meeting identifier.","has_output_schema":false},{"name":"meeting_schedule","selector":"local_stdio_code.meeting_schedule","signature":"meeting_schedule(topic: string, participants?: number)","description":"Schedule an online meeting with topic and participant count.","has_output_schema":false}]}

🔧 Tool calls:
   • mcp_inspect_tools
     Args: {"selector":"local_stdio_code","tools":["calendar_create"]}
🔄 Executing tools...

✅ Tool results:
   • {"tools":[{"name":"calendar_create","selector":"local_stdio_code.calendar_create","description":"Create a calendar event with title and start time.","has_output_schema":false,"input_schema":{"properties":{"attendee":{"description":"Optional attendee email.","type":"string"},"start_time":{"description":"Event start time in ISO-8601 format.","type":"string"},"title":{"description":"Event title.","type":"string"}},"required":["title","start_time"],"type":"object"}}]}

🔧 Tool calls:
   • mcp_call
     Args: {"arguments":{"attendee":"alice@example.com","start_time":"2026-04-07T15:00:00+08:00","title":"Broker demo review"},"selector":"local_stdio_code.calendar_create"}
🔄 Executing tools...

✅ Tool results:
   • {"content":[{"type":"text","text":"Created calendar event \"Broker demo review\" at 2026-04-07T15:00:00+08:00 attendee=\"alice@example.com\""}]}

🤖 Assistant: Event created: “Broker demo review” on 2026-04-07 at 15:00 (+08:00), with alice@example.com invited.
```

The broker first lists lightweight tool summaries, then inspects only the
selected tool schema, and finally calls the concrete MCP tool.

Flags:

- `-model`: model name, default `gpt-4o-mini`
- `-variant`: optional OpenAI provider variant. If empty, infer from `OPENAI_BASE_URL`
- `-streaming`: enable streaming output, default `true`
- `-prompt`: optional one-shot prompt

## Sample Prompts

- `What MCP servers are available to you?`
- `Create a calendar event titled "Broker demo review" starting at 2026-04-07T15:00:00+08:00 and invite alice@example.com.`
- `Create an issue titled "Broker demo" with high priority.`
- `Search the documentation for "MCP broker".`
- `What is 12 plus 30?`
- `Echo the text "hello broker".`
- `Load the skill remote-http-mcp, find its remote MCP endpoint, inspect the announcement tools there, and publish an announcement titled "Broker via skill".`
- `Use the remote-http-mcp skill to search the remote FAQ for "broker routing".`

## How It Works

The example starts an `LLMAgent` with broker tools plus built-in skill
knowledge tools:

```go
broker := mcpbroker.New(
    mcpbroker.WithServers(map[string]mcp.ConnectionConfig{
        "local_stdio_code": {
            Transport: "stdio",
            Command:   "go",
            Args:      []string{"run", serverPath},
            Timeout:   10 * time.Second,
        },
    }),
    mcpbroker.WithAllowAdHocHTTP(true),
)

agent := llmagent.New(
    "mcp-broker-assistant",
    llmagent.WithModel(modelInstance),
    llmagent.WithTools(broker.Tools()),
    llmagent.WithSkills(repo),
    llmagent.WithSkillToolProfile(llmagent.SkillToolProfileKnowledgeOnly),
)
```

The named local MCP server is still a plain `stdio` server:

```go
server := mcp.NewStdioServer("mcpbroker-demo-server", "1.0.0")
server.RegisterTool(echoTool, ...)
server.RegisterTool(addTool, ...)
server.RegisterTool(issueCreateTool, ...)
server.RegisterTool(docSearchTool, ...)
server.RegisterTool(calendarCreateTool, ...)
```

The remote MCP server is a streamable HTTP server started in-process
before the conversation begins:

```go
remote := startRemoteHTTPDemoServer()
skillsRoot, _ := prepareRenderedSkillsRoot(exampleDir, remote.url)
repo, _ := skill.NewFSRepository(skillsRoot)
```

The generated skill `remote-http-mcp` contains the exact ad-hoc MCP URL
and tells the model to use selectors like:

```text
http://127.0.0.1:<port>/mcp
http://127.0.0.1:<port>/mcp.announcement_publish
```

That skill does not start or manage the server. It only tells the model
that this already-running endpoint can be connected dynamically through
`mcp_list_tools`, `mcp_inspect_tools`, and `mcp_call`.

## Expected Effect

The visible tool surface to the model stays relatively small and stable:

- For named MCPs, the model first discovers named servers with `mcp_list_servers`
- For skill-provided remote MCPs, the model first loads the skill to get
  the endpoint, then uses `mcp_list_tools` against that ad-hoc URL
- It then uses `mcp_inspect_tools` to expand schema only for the selected tools
- Only then does it call a concrete MCP tool with `mcp_call`

With the wider local demo catalog, prompts like "find the issue creation
tool" or "find the doc search tool" should naturally push the model
toward targeted inspection instead of reading full schemas for the entire
catalog every time.
The skill-backed remote server adds a second path: dynamic endpoint
discovery without pre-registering that remote MCP as a named broker
server.

That is the main difference from the old `tool/mcp` path, where remote
MCP tools are expanded into the model's tool list before the turn
starts.
