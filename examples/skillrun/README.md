# Agent Skills: Interactive skill_run Example

This example demonstrates an end-to-end interactive chat using `Runner`
and `LLMAgent` with Agent Skills. The assistant streams content, shows
tool calls and tool responses, and executes skill scripts via the
`skill_run` tool without inlining script bodies.

## Features

- Interactive chat with streaming or non-streaming modes
- Agent Skills repository injection and overview
- `skill_load` to load SKILL.md/doc content on demand
- `skill_run` to execute commands safely in a workspace, returning
  stdout/stderr and file artifacts
- Clear visualization of tool calls and tool responses

## Prerequisites

- Go 1.21 or later
- Valid API key and base URL for your model provider (OpenAI-compatible)

## Environment Variables

| Variable          | Description                              | Default                  |
| ----------------- | ---------------------------------------- | ------------------------ |
| `OPENAI_API_KEY`  | API key for the model service (required) | ``                       |
| `OPENAI_BASE_URL` | Base URL for the model API endpoint      | `https://api.openai.com` |
| `SKILLS_ROOT`     | Skills repository root directory         | `./skills`               |

## Command Line Arguments

| Argument        | Description                        | Default          |
| --------------- | ---------------------------------- | ---------------- |
| `-model`        | Name of the model to use           | `deepseek-chat`  |
| `-stream`       | Stream responses                   | `true`           |
| `-skills-root`  | Skills repository root directory   | `env or ./skills` |
| `-executor`     | Workspace executor: local|container | `local`          |

## Usage

```bash
cd examples/skillrun
export OPENAI_API_KEY="your-api-key"
# Optional: export SKILLS_ROOT to point at your skills repo
go run .
```

### Examples

```bash
# Non-streaming mode
go run . -stream=false

# Custom model
go run . -model gpt-4o-mini

# Custom skills root
go run . -skills-root /path/to/skills

# Run with container workspace executor (requires Docker)
go run . -executor container
```

## Tips

- Ask the assistant to list available skills and choose one.
- Use natural language to request loading docs or running a command.
- Example: "Load <skill-name> and run the example build command."

## What You’ll See

```
🚀 Skill Run Chat
Model: deepseek-chat
Stream: true
Skills root: ./skills
Executor: local
Session: chat-1703123456
==================================================
Tips:
 - Ask to list skills and pick one.
 - Ask the assistant to run a command from SKILL.md.
 - Example: 'Load <skill> and run its example build'.

👤 You: list skills
🔧 CallableTool calls initiated:
   • skill_load (ID: call_abc123)

🔄 Executing tools...
✅ CallableTool response (ID: call_abc123): {"status":"loaded: ..."}

🤖 Assistant: Here are the available skills: ...

👤 You: load demo-skill and run its build example
🔧 CallableTool calls initiated:
   • skill_run (ID: call_def456)
     Args: {"skill":"demo-skill","command":"bash scripts/build.sh"}

🔄 Executing tools...
✅ CallableTool response (ID: call_def456): {"stdout":"...","artifacts":[...]}

🤖 Assistant: Build completed. Output: ...
```
