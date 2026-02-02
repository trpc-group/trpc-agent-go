# Structured Output + Skills (Typed JSON)

This example demonstrates:

- Using Agent Skills (`skill_load` / `skill_run`)
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
