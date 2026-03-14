# Host Exec Tool Example

This example demonstrates how to give an `LLMAgent` a direct host
command tool for project-local shell work.

Unlike the fenced-code execution path, `exec_command` runs shell
commands directly. That makes it a better fit for personal agents that
need to inspect a repository, run builds, execute tests, or manage a
long-running command in place.

## What the Tool Set Provides

- `exec_command`: runs a shell command in the configured base
  directory
- `write_stdin`: continues a running session or polls it when `chars`
  is empty
- `kill_session`: stops a running session

## When to Use It

Use this tool set when you want the model to do tasks like:

- list files in the current project
- run `go test ./...` and summarize failures
- start a long command and keep polling output
- send input to an interactive command

## Run the Example

Set your model credentials in the environment first, for example:

```bash
export OPENAI_API_KEY="your-api-key"
export OPENAI_BASE_URL="your-base-url"
```

Then run:

```bash
go run ./examples/tool/hostexec -model deepseek-chat -base-dir .
```

## Example Session

```text
You: List the first few files in this repository.

Assistant:
Tool: exec_command
Args: {"command":"ls | head","yield_time_ms":0}
Assistant: I listed the repository root and found ...
```

For a longer task:

```text
You: Run go test ./... and tell me whether anything failed.

Assistant:
Tool: exec_command
Args: {"command":"go test ./...","yield_time_ms":500}
Tool: write_stdin
Args: {"session_id":"...","chars":"","yield_time_ms":500}
Assistant: The test run completed and ...
```

## Notes

- Commands run on the current machine.
- Relative `workdir` values resolve from the configured `-base-dir`.
- This tool is intended for trusted, local workflows.
