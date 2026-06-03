# Skill Tool Activation Example

This example demonstrates activating a candidate `ToolSet` after a Skill is
loaded with `skill_load`.

It shows:

- `WithActivatableToolSets(...)` registering a ToolSet that is not visible in
  the first model request.
- `WithToolActivationOnSkillLoad(...)` making that ToolSet visible after the
  model loads `release-notes`.
- `ToolActivationModeInclude` preserving existing user tools.
- `ToolActivationModeOnly` trimming existing user tools and keeping only the
  activated ToolSet tools, while framework tools remain visible.

The example prints the model-visible tool names before each model request, so
you can see the tool set change immediately after `skill_load`.

## Run

From the `examples` module root:

```bash
cd examples

export OPENAI_BASE_URL="http://v2.open.venus.oa.com/llmproxy/"
export OPENAI_API_KEY="YOUR_API_KEY"

go run ./skilltoolactivation
```

Or from this example directory:

```bash
cd examples/skilltoolactivation
go run .
```

The example defaults to `deepseek-v4-flash`. Use `-model` if your endpoint
expects a different model name.

## Include Mode

```bash
go run ./skilltoolactivation -mode include
```

Before `skill_load`, the model can see the base `calculator` user tool and
framework skill tools. After `release-notes` is loaded, the model can also see
tools expanded from the `release_docs` ToolSet, such as
`release_docs_read_file`.

## Only Mode

```bash
go run ./skilltoolactivation -mode only
```

Before `skill_load`, the model can see the base `calculator` user tool. After
`release-notes` is loaded, the user tool set is replaced by the tools expanded
from `release_docs`; framework tools such as `skill_load` remain visible.

## Lifetime

The default lifetime is `invocation`.

```bash
go run ./skilltoolactivation -lifetime invocation
go run ./skilltoolactivation -lifetime session
```

Use `session` when the activation should remain effective for later runs in
the same session.

## Files

- `skills/release-notes/SKILL.md` tells the model when and how to use the
  release-notes skill.
- `release_docs/release_notes.md` is exposed only through the activatable
  `release_docs` ToolSet.
