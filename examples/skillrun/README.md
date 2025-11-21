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
- Example `user-file-ops` skill to summarize user-provided text files

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
| `-inputs-host`  | Host dir exposed as `inputs/` inside skill workspaces (local or container) | `` |
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
- When `-inputs-host` is set (local or container), the host folder is
  also available inside each skill workspace under `work/inputs`
  (and `inputs/` from the skill root).

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
- You can ask to "list skills" and pick one (optional).
- Use natural language to run a command from the skill docs.
- Example: "Use demo-skill to run the sample build command."
- If you expect files, mention patterns to collect (for example,
  `output_files: ["out/**"]` or `output_files: ["$OUTPUT_DIR/**"]`).
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

### User File Processing Example (`user-file-ops`)

This example shows how to let the assistant summarize a text file that
you already have on your machine, using the `user-file-ops` skill.

1. Create a small sample file on your host:

   ```bash
   echo "hello from skillrun" > /tmp/skillrun-notes.txt
   echo "this is another line" >> /tmp/skillrun-notes.txt
   ```

2. Start the interactive chat:

   ```bash
   cd examples/skillrun
   go run .
   ```

3. In the chat, tell the assistant where your file is and what you want:

   ```text
   👤 You: I have a text file at /tmp/skillrun-notes.txt.
   Please use the user-file-ops skill to summarize it, mapping it to
   work/inputs/user-notes.txt and writing the summary to
   out/user-notes-summary.txt.
   ```

   The assistant will typically:

   - load the `user-file-ops` skill with `skill_load`
   - run a command like:

     ```bash
     bash scripts/summarize_file.sh \
       work/inputs/user-notes.txt \
       out/user-notes-summary.txt
     ```

   The skill script computes simple statistics (lines, words, bytes)
   and includes the first few non-empty lines of the file in the
   summary.

4. If artifacts are enabled (for example with `-artifacts`), the
   summary file can be saved as an artifact and then:

   - `/artifacts` will list files such as `out/user-notes-summary.txt`
   - `/pull out/user-notes-summary.txt` will download it into the
     local `downloads/` directory.

## Tips

- You can ask the assistant to list available skills (optional).
- No need to type "load"; the assistant loads skills when needed.
- Ask to run a command exactly as shown in the skill docs.

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
 - You can ask to list skills (optional).
 - No need to type 'load'; the assistant loads skills when needed.
 - Ask to run a command from the skill docs.

👤 You: list skills
🔧 CallableTool calls initiated:
   • skill_load (ID: call_abc123)

🔄 Executing tools...
✅ CallableTool response (ID: call_abc123): {"status":"loaded: ..."}

🤖 Assistant: Here are the available skills: ...

👤 You: run the demo-skill build example
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
