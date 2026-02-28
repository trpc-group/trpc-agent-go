# STDIN Chat (Custom OpenClaw Distribution)

This example shows the *intended extension workflow* for OpenClaw in Go:

- The GitHub repo provides the reusable runtime: `openclaw/app`.
- A downstream repo (for example, an internal distribution) builds its own
  binary and enables plugins via **anonymous imports** (`import _ "..."`).

In this repo, the custom binary enables two small reference plugins:

- `openclaw/plugins/stdin`: a channel that reads one line per message from
  STDIN and prints replies to STDOUT.
- `openclaw/plugins/echotool`: a tool provider that registers a simple `echo`
  tool.

## Run

From the repo root:

```bash
cd openclaw

go run ./examples/stdin_chat \
  -config ./examples/stdin_chat/openclaw.yaml
```

What you should see:

- The HTTP gateway starts (listening on `:8080` by default).
- The STDIN channel prints a short help message.

Now type a line and press Enter. With `model.mode: mock`, the assistant will
reply with an `Echo: ...` message.

To stop, type `/quit` or `/exit`.

## How it works (high level)

1) `examples/stdin_chat/main.go` imports `openclaw/app`.

2) It also imports plugins anonymously:

- Anonymous imports run each plugin package's `init()` function.
- Each plugin calls `openclaw/registry.Register...(...)` to register itself.

3) The YAML config (`openclaw.yaml`) enables plugins:

- `channels`: starts the `stdin` channel.
- `tools.providers`: loads the `echotool` tool provider.
- `agent.system_prompt_dir`: loads multiple `.md` files into the agent's
  system prompt (alphabetical order).

## Notes

- The `echotool` tool provider is enabled by config, but the mock model does
  not call tools. To actually exercise tools, switch `model.mode` to `openai`
  and configure your model credentials.
