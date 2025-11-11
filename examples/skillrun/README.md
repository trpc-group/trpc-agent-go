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
  stdout/stderr and output files
  (and optionally saving files as artifacts)
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
| `-inputs-host`  | Host dir bind-mounted to `/opt/trpc-agent/inputs` (container) | `` |
| `-artifacts`    | Save files via artifact service     | `false`          |
| `-omit-inline`  | Omit inline file contents           | `false`          |
| `-artifact-prefix` | Artifact filename prefix (e.g., `user:`) | ``     |

## Usage

```bash
cd examples/skillrun
export OPENAI_API_KEY="your-api-key"
# Optional: export SKILLS_ROOT to point at your skills repo
go run .
```

Workspace paths and env vars:
- `$SKILLS_DIR/<name>`: read-only staged skill
- `$WORK_DIR`: writable shared workspace (use `$WORK_DIR/inputs` for inputs)
- `$RUN_DIR`: per-run working directory
- `$OUTPUT_DIR`: unified outputs (collector/artifact saves read from here)

Optional inputs/outputs spec with `skill_run`:
- Inputs example (map external files into workspace):
  `{ "inputs": [ {"from": "artifact://datasets/raw.csv@3",
     "to": "work/inputs/raw.csv"} ] }`
- Outputs example (collect and save artifacts):
  `{ "outputs": {"globs": ["out/**/*.csv"], "save": true,
     "name_template": "user:", "inline": false } }`

Container zero-copy hint:
- Bind a host folder as the inputs base so `host://` inputs under that
  folder become symlinks inside the container (no copy):
  `-executor container -inputs-host /path/to/datasets`

### Use with anthropics/skills

You can test against the public Anthropics skills repository.

```bash
# 1) Clone the repo anywhere you like
git clone https://github.com/anthropics/skills \
  "$HOME/src/anthropics-skills"

# 2) Point skillrun at that repo
export SKILLS_ROOT="$HOME/src/anthropics-skills"

# 3) Run the example (local workspace executor)
go run .

# Optional: Use container executor for extra isolation (needs Docker)
go run . -executor container
```

In chat:
- Ask to "list skills" and pick one you see.
- Say "load <name>" when you want to use it.
- Ask to run a command exactly as shown in the skill docs.
- If you expect files, mention patterns to collect (for example,
  `output_files: ["out/**"]`).
- For production, prefer `save_as_artifacts: true` and
  `omit_inline_content: true` to store files via the artifact
  service and reduce payload size.
- This example wires an in-memory artifact service by default,
  so `save_as_artifacts` works out of the box.
- You can also use CLI flags to enable artifact saving without
  hand-crafting JSON arguments in chat:
  `-artifacts -omit-inline -artifact-prefix user:`

List and download saved artifacts:

- `/artifacts` lists all artifact keys saved in this session.
- `/pull <artifact_files.name> [version]` downloads a file to the
  `downloads/` directory.

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
- Example: "Load <name> and run the sample build command."

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
 - Ask the assistant to run a command from the skill docs.
 - Example: 'Load <name> and run the sample build'.

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
✅ CallableTool response (ID: call_def456): {"stdout":"...","output_files":[...]}

# If artifacts were saved, you'll also see:
   Saved artifacts:
   - out/report.pptx (v0)

🤖 Assistant: Build completed. Output: ...
```
