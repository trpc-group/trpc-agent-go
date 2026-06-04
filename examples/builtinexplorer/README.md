# Built-in Explorer Example

This example shows the minimal way to expose `builtin.NewExplorer()` as an
AgentTool so the model can call a ready-to-use, read-only exploration agent.

The root agent has two document tools and one explorer tool:

- `search_docs`
- `read_doc`
- `explorer`

The Explorer is mounted as an AgentTool, and the root agent is instructed to
use this tool for document lookup instead of calling `search_docs` / `read_doc`
directly:

```go
explorer := builtin.NewExplorer()
llmagent.WithTools(append(
    []tool.Tool{searchDocsTool(), readDocTool()},
    agenttool.NewTool(explorer),
))
```

The Explorer inherits the parent agent's available capabilities at run time and
adds a built-in read-only system prompt. The read-only behavior is an advisory
prompt constraint, not a permission boundary.

## Run

```bash
cd examples/builtinexplorer
export OPENAI_API_KEY="your-api-key"
go run .
```

## Suggested Prompts

```text
Investigate the release rollback policy and summarize when rollback is required.
```

```text
Search the incident docs and explain what should be in the first customer update.
```
