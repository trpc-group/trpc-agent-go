# Built-in Explorer Example

This example shows how to use `builtin.NewExplorer()` as a focused,
read-only exploration agent.

The root agent has three tools:

- `search_docs`
- `read_doc`
- `save_note`

The Explorer is mounted with:

```go
builtin.NewExplorer(
    builtin.WithToolFilter(tool.NewIncludeToolNamesFilter(
        "search_docs",
        "read_doc",
    )),
)
```

That filter demonstrates the recommended production pattern: Explorer's
read-only behavior is a prompt constraint, not a permission boundary. If the
root agent has mutating tools such as `save_note`, narrow the Explorer surface
with `WithToolFilter` or `WithTools`.

## Mounting Modes

Run as an AgentTool:

```bash
cd examples/builtinexplorer
export OPENAI_API_KEY="your-api-key"
go run . -mode=agenttool
```

In this mode the model sees an `explorer` tool. The root agent calls Explorer
synchronously, receives the findings, and then continues the turn.

Run as a SubAgent:

```bash
go run . -mode=transfer
```

In this mode the model sees `transfer_to_agent` with `explorer` as a target.
The root agent hands off the investigation to Explorer.

## Suggested Prompts

```text
Investigate the release rollback policy and summarize when rollback is required.
```

```text
Search the incident docs and save a note with the customer update guidance.
```

The second prompt lets you compare responsibilities: Explorer should gather
the read-only facts, while the root agent owns the state-changing `save_note`
tool.
