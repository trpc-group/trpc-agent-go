# Structured Output + Skills (Typed JSON)

> This demo is built on top of an older skill execution surface. For
> the recommended way to wire Agent Skills in new code, see
> [docs/mkdocs/en/skill.md](../../docs/mkdocs/en/skill.md). The
> structured-output pattern shown here is orthogonal and also works on
> top of the recommended setup.

This example demonstrates:

- Using Agent Skills to load knowledge and run scripts
- Disabling automatic execution of fenced code blocks in assistant text
  (`llmagent.WithEnableCodeExecutionResponseProcessor(false)`)
- Returning a **typed** final answer via `WithStructuredOutputJSON`

The key idea is:

- The agent may call tools first (Skills are tools).
- Only the **final** answer must be a single JSON object that matches the
  schema.

## Run

From this directory:

```bash
go run . -model deepseek-chat
```

Then ask anything (for example: "run the hello skill").

You should see:

- tool calls (`skill_load`, then `skill_run`)
- a final JSON response
- a typed `event.StructuredOutput` payload printed by the example

## What the skill does

The demo skill lives under `skills/hello/`. It runs:

```bash
bash scripts/hello.sh
```

and prints a short, deterministic message.
