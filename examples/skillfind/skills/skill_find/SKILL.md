---
name: skill-find
description: Find a public Agent Skill on GitHub, install it, and load it.
---

Overview

Use this skill when the user wants a new public Agent Skill that is not
already installed.

Workflow

1. Search GitHub pages with `web_search`.
   Prefer queries that mention `SKILL.md`, the requested topic, and
   `site:github.com`.

2. Pick a result that clearly points to a GitHub skill directory or a
   `SKILL.md` page.

3. Call `skill_install_github` with that GitHub URL.

4. Read the tool result carefully.
   It returns the exact `skill_name` that was installed and may also
   include `installed_files`.

5. Immediately call `skill_load` with the returned `skill_name`.

6. Only if local execution is explicitly enabled for this demo and the
   user asked for a runnable demo, follow the installed skill docs and
   use `skill_run`.

7. If the docs are brief, use `installed_files` to avoid guessing.
   Prefer obvious entrypoint files such as `run.sh` or scripts under
   `scripts/`.

Rules

- Prefer small public skills with a clear `SKILL.md`.
- Prefer GitHub results that point directly to a skill, not a repo home
  page.
- Tell the user which skill you installed and where it came from.
- Never run downloaded code automatically.
- If installation fails, explain the failure briefly and try another
  candidate.
