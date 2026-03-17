# OpenClaw A2A Sub-Agent Example

This example starts an OpenClaw runtime in-process, enables the native
`/a2a` surface, and then connects back to it through
`agent/a2aagent`.

It demonstrates a realistic deployment shape:

- a sandboxed OpenClaw runtime hosts bundled skills and host binaries
- another agent process talks to it as an A2A sub-agent
- follow-up turns reuse the same A2A `session_id`

The example uses the bundled `weather` skill, so `curl` must be
available on the host.

## Run

From the repo root:

```bash
cd openclaw

# Export your usual OpenAI-compatible credentials first if needed.
go run ./examples/a2a_subagent \
  -question "What's the weather in Shanghai today?" \
  -follow-up "What about tomorrow?"
```

If you want to override the default environment-based model settings:

```bash
cd openclaw
go run ./examples/a2a_subagent \
  -model gpt-5 \
  -openai-base-url https://api.openai.com/v1
```

What this example does:

1. starts OpenClaw on a random loopback port
2. enables the native A2A surface at `/a2a`
3. exposes only the bundled `weather` skill in the sandbox
4. sends two turns through an `A2AAgent`
5. shows that the follow-up question reuses the same session

Typical output looks like:

```text
A2A URL: http://127.0.0.1:41237/a2a
Agent: openclaw-sandbox
Published skills: 1

Q1: What's the weather in Shanghai today?
A1: ...

Q2: What about tomorrow?
A2: ...
```

## Notes

- The example intentionally does not use a mock model.
- The remote A2A card is stable by default and does not enumerate every
  tool. Pass `-advertise-tools` if you need that behavior.
