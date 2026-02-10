# SkillLoadMode (Skills Load/Offload) Example

This example demonstrates how **SkillLoadMode** controls how long a skill's
`SKILL.md` body (and selected docs) stays available in the model prompt.

It runs two "turns" (two `Runner.Run` calls) against the same session and prints
the skill-related session state keys after each turn.

This example uses a tiny local skills repo under `./skills` and a deterministic
mock model, so it does **not** require any API key.

## What is SkillLoadMode?

When the model calls `skill_load` (and optionally `skill_select_docs`), the
framework writes session state keys:

- `temp:skill:loaded:<name>`
- `temp:skill:docs:<name>`

The Skills request processor reads these keys and injects the corresponding
skill body/docs into the next model request.

By default (legacy behavior), the loaded content is appended to the system
prompt. Optionally, you can enable tool-result materialization to append loaded
skill content into the corresponding tool result messages (`skill_load` /
`skill_select_docs`) instead:

- `llmagent.WithSkillsLoadedContentInToolResults(true)`
- Or in this example: `go run . -tool-results=true`

SkillLoadMode controls the lifetime of those keys:

- `turn` (default): loaded skill content is available for the whole current
  turn (one `Runner.Run`) and is cleared automatically before the next turn.
- `once`: loaded skill content is injected for the **next** model request only,
  then cleared.
- `session` (legacy): loaded skill content persists across turns until cleared
  or the session expires.

## Run

```bash
cd examples/skillloadmode
go run .
```

Try different modes:

```bash
go run . -mode turn
go run . -mode once
go run . -mode session
```

## What you'll see

The program prints:

1) tool calls (the mock model always calls `skill_load` in the first turn)
2) skill state after turn 1
3) skill state after turn 2

In `turn` mode, you should see the skill marked as loaded after turn 1, and
cleared after turn 2 starts.
