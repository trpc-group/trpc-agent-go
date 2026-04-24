# Agent Skills Chat Demo

> The folder name `skillrun` is a historical artifact. This example
> now uses the recommended Agent Skills execution path
> (`skill_load` + `workspace_exec`, plus `workspace_save_artifact` for
> persisting outputs). For the full reference of how Agent Skills are
> wired, see [docs/mkdocs/en/skill.md](../../docs/mkdocs/en/skill.md).

An interactive chat built on `runner.Runner` + `llmagent.LLMAgent`
that:

- Lists / loads skills from a configurable skills root
- Streams assistant tokens and renders tool calls / tool responses
- Executes commands inside a per-session, isolated skill workspace
- Stages user-uploaded files into `work/inputs/` for skill scripts to
  read
- Stores output files via the in-memory artifact service when the
  model calls `workspace_save_artifact`

## Features

- Interactive chat with streaming or non-streaming modes
- Agent Skills repository injection and overview
- On-demand loading of `SKILL.md` / doc content with `skill_load`
- Script execution in an isolated workspace via `workspace_exec`
  (writable skill working copy materialized under `skills/<name>/`)
- Persisting workspace files as artifacts via `workspace_save_artifact`
- Automatic staging of user-uploaded file inputs into `work/inputs/`
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

| Argument             | Description                                                                | Default            |
| -------------------- | -------------------------------------------------------------------------- | ------------------ |
| `-model`             | Name of the model to use                                                   | `deepseek-chat`    |
| `-stream`            | Stream responses                                                           | `true`             |
| `-skills-root`       | Skills repository root directory                                           | `env or ./skills`  |
| `-skills-guidance`   | Include built-in skills tooling/workspace guidance in the system message   | `true`             |
| `-send-file-inputs`  | Forward user file content parts to the model provider                      | `false`            |
| `-executor`          | Workspace executor: `local` or `container`                                 | `local`            |
| `-trusted-local`     | Local executor: reuse a fixed workspace root (unsafe, opt-in)              | `false`            |
| `-trusted-root`      | Trusted-local workspace root                                               | `./skill_workspace` |
| `-inputs-host`       | Host dir exposed as `inputs/` inside skill workspaces                      | ``                 |

## Usage

```bash
cd examples/skillrun
export OPENAI_API_KEY="your-api-key"
# Optional: export SKILLS_ROOT to point at your skills repo
go run .
```

To reduce system prompt size (for example, when you only need docs and
won't run commands), disable the built-in guidance:

```bash
go run . -skills-guidance=false
```

Workspace paths and env vars available inside scripts:

- `$SKILLS_DIR/<name>`: writable skill working copy (session-scoped)
- `$WORK_DIR`: writable shared workspace (use `$WORK_DIR/inputs` for inputs)
- `$RUN_DIR`: per-run working directory
- `$OUTPUT_DIR`: unified outputs directory

Container zero-copy hint:

- Bind a host folder as the inputs base so files under that folder
  become symlinks inside the container (no copy):
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

# 2) Point the demo at that repo
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
- If you want a stable reference to an output file (for hand-off to
  other tools or to surface back to the user), ask the assistant to
  call `workspace_save_artifact` on the workspace path.
- This example wires an in-memory artifact service by default, so
  `workspace_save_artifact` works out of the box.

### Use with OpenClaw skills

This repo vendors the upstream OpenClaw skill pack under
`openclaw/skills/`. You can point this example at it:

```bash
cd examples/skillrun
export SKILLS_ROOT="../../openclaw/skills"
go run .
```

## Session Commands

- `/artifacts` lists all artifact keys saved in this session.
- `/pull <name> [version]` downloads an artifact to `downloads/`.
- `/upload <path>` attaches a local file as inline bytes.
- `/upload_id <path>` uploads a file and attaches it by `file_id`.
- `/upload_artifact <path>` uploads a file to the artifact service and
  attaches it by `artifact://...` `file_id`.
- By default, file content parts are omitted from requests sent to the
  model provider (for compatibility). Use `-send-file-inputs` to pass
  them through if your provider supports file inputs.

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
you upload into the conversation, using the `user-file-ops` skill.

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

3. Upload the file into the conversation:

   ```text
   👤 You: /upload_artifact /tmp/skillrun-notes.txt
   ```

   If you want to attach inline bytes, use:

   ```text
   👤 You: /upload /tmp/skillrun-notes.txt
   ```

   If your model provider supports file uploads and you want to attach
   by provider `file_id`, use:

   ```text
   👤 You: /upload_id /tmp/skillrun-notes.txt
   ```

4. Ask the assistant to summarize it:

   ```text
   👤 You: Please use the user-file-ops skill to summarize
   work/inputs/skillrun-notes.txt and write the summary to
   out/user-notes-summary.txt.
   ```

   The assistant will typically:

   - load the `user-file-ops` skill with `skill_load`
   - run a command like:

     ```bash
     bash scripts/summarize_file.sh \
       work/inputs/skillrun-notes.txt \
       out/user-notes-summary.txt
     ```

     by calling `workspace_exec` with `cwd: skills/user-file-ops`.

   The skill script computes simple statistics (lines, words, bytes)
   and includes the first few non-empty lines of the file in the
   summary.

5. To pull the summary out of the workspace, ask the assistant to
   call `workspace_save_artifact` on `out/user-notes-summary.txt`.
   Then `/artifacts` will list it and `/pull out/user-notes-summary.txt`
   will download it into the local `downloads/` directory.

## Tips

- You can ask the assistant to list available skills (optional).
- No need to type "load"; the assistant loads skills when needed.
- Ask to run a command exactly as shown in the skill docs.

## What You'll See

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
   • workspace_exec (ID: call_def456)
     Args: {"command":"bash scripts/build.sh","cwd":"skills/demo-skill"}

🔄 Executing tools...
✅ CallableTool response (ID: call_def456): {"status":"ok","output":"...","exit_code":0}

🤖 Assistant: Build completed. Output: ...
```
