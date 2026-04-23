# SkillToolProfile Example (Archival Reference)

> This demo exercises an older surface for configuring which built-in
> skill tools get registered. For the recommended way to wire Agent
> Skills in new code, see
> [docs/mkdocs/en/skill.md](../../docs/mkdocs/en/skill.md) — the
> default behavior already does the right thing, and most users do not
> need to call `WithSkillToolProfile(...)` at all.

This example demonstrates the `WithSkillToolProfile(...)` option with a
deterministic mock model, so it does not require any API key.

It supports:

- `-profile full`
- `-profile knowledge_only`

The example prints:

- the selected profile
- whether an executor is attached
- which `skill_*` tools are registered
- one demo run showing the expected tool call sequence

## Run

From the `examples` module root:

```bash
cd examples
go run ./skilltoolprofile -profile full
go run ./skilltoolprofile -profile knowledge_only
```

Or from the example directory:

```bash
cd examples/skilltoolprofile
go run . -profile full
go run . -profile knowledge_only
```

## What It Shows

In `full` mode:

- `skill_load`, `skill_list_docs`, `skill_select_docs`, `skill_run`
  are registered; `skill_exec` and session tools are also registered
  when the executor supports interactive sessions
- a local executor is attached
- the mock model calls `skill_load`, then `skill_run`

In `knowledge_only` mode:

- only `skill_load`, `skill_list_docs`, and `skill_select_docs` are registered
- no executor is attached
- the mock model calls `skill_load`, `skill_list_docs`, and
  `skill_select_docs`
- `skill_run` is intentionally unavailable

## Files

The demo skill lives under `./skills/demo-profile/` and contains:

- `SKILL.md`
- `docs/usage.md`
- `scripts/write_profile.sh`

The script is only used in `full` mode.
