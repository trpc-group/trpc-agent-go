# Team Example: Swarm Input Debug

This example prints the `model.Request` that each Swarm member sends to the
model. It is meant for inspecting what context the child agent receives after
`transfer_to_agent`.

## Usage

```bash
cd examples/team/swarm_input_debug
export OPENAI_BASE_URL="http://v2.open.venus.oa.com/llmproxy/"
export OPENAI_API_KEY="your-api-key"
go run .
```

For a deterministic local run that does not call a remote model:

```bash
go run . -mock
```

To verify the target shape, enable independent Swarm members and rewrite the
handoff input from the original user input:

```bash
go run . -mock -child-isolated -rewrite-child-input
```

Useful flags:

| Flag | Default | Description |
| --- | --- | --- |
| `-model` | `deepseek-chat` | Model name. |
| `-input` | Built-in sample | One user message sent to the Swarm team. |
| `-mock` | `false` | Use a scripted local model instead of a remote model. |
| `-streaming` | `false` | Enable model streaming. |
| `-child-isolated` | `false` | Configure Swarm handoff with independent member sessions. |
| `-rewrite-child-input` | `false` | Use Swarm handoff input builder to render the child input. |
| `-child-template` | Built-in sample | Text/template used when rewriting the child input. |
| `-content-limit` | `4000` | Maximum printed characters per message. |
| `-print-provider-json` | `false` | Also print the OpenAI request JSON after provider conversion. |

The output contains:

- `BeforeModel agent=parent`: the entry agent's final model input.
- `BeforeTool agent=parent tool=transfer_to_agent`: the handoff tool arguments.
- `BeforeModel agent=child`: the child agent's final model input.

This makes it easy to compare the original user input, the transfer message,
and any parent-agent history included in the child's model request.

Without `-child-isolated`, the child request shows the default Swarm behavior:
parent/root session context is still visible. With `-child-isolated`, the child
has a different session ID and the first child model request contains only the
child system instruction and the rendered handoff input.
