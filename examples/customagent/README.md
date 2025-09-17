# Custom Agent Example

This example shows how to implement a business-driven agent by directly implementing the `agent.Agent` interface (no Graph required). The demo performs a lightweight intent classification using the LLM, then branches logic:

- Intent = `chitchat`: reply conversationally
- Intent = `task`: output a short actionable plan (3–5 steps)

It’s a good starting point when you want to “embed” LLM into existing code and keep full control over branching, validation, and fallbacks, without introducing workflow/graph orchestration yet.

## What it demonstrates

- Minimal custom agent implementing `agent.Agent`
- Using `model.Model.GenerateContent` directly to call the LLM
- Streaming LLM outputs as `event.Event` to the caller
- Clean separation between intent classification and action responses
- Ready to evolve: you can later add tools, callbacks, or move to Chain/Parallel/Graph agents as complexity grows

## Files

- `main.go`: wires the custom agent into `runner.Runner` and handles CLI flags/streaming output
- `custom_agent.go`: custom agent implementation with intent branching

## Prerequisites

- GGo 1.21 or later
- Valid API key for an OpenAI-compatible model provider

### Environment setup

```bash
# For DeepSeek
export OPENAI_API_KEY="your-deepseek-api-key"
export OPENAI_BASE_URL="https://api.deepseek.com/v1"

# For OpenAI
export OPENAI_API_KEY="your-openai-api-key"
# OPENAI_BASE_URL not needed for OpenAI
```

## Build and Run (Interactive)

```bash
cd examples/customagent

# Build
go build -o customagent .

# Start interactive chat (default model: deepseek-chat)
./customagent

# Use a specific model
./customagent -model gpt-4o-mini
```

Interactive commands:

- `/history` — Ask the agent to show conversation history
- `/new` — Start a new session
- `/exit` — Quit

## How it works

1) `Run(...)` starts by performing a small classification prompt (not shown to the user) to decide between `chitchat` and `task`.
2) Based on the intent, it issues a second LLM request and streams the response as events using `event.NewResponseEvent(...)`.
3) We integrate via `runner.Runner` so you get session handling and event persistence out of the box.

## Extending this example

- Add tools: return `[]tool.Tool` (e.g., `function.NewFunctionTool(...)`) to call DB/HTTP/services
- Add callbacks: use Agent/Model/Tool callbacks to hook custom behaviors
- Grow into multi-agent: when branching gets complex, evolve to `ChainAgent`/`ParallelAgent` or adopt `GraphAgent` for orchestrated workflows

---

中文简介：该示例演示如何“直接实现 Agent 接口”把 LLM 嵌入到业务代码中，通过先意图识别、再分支处理（闲聊/任务）来实现最小闭环。适合不想一开始就上 Graph/Workflow，又希望灵活编码业务流程的场景。
