# Skill Find Example

This example demonstrates a real end-to-end skill discovery flow with a
real model and real public network calls.

It starts with one built-in local skill, `skill-find`. That skill teaches
the model how to:

- search GitHub pages with a real DuckDuckGo HTML search request
- install a public skill directory from GitHub with a real Contents API
  download
- refresh the local skill repository immediately
- load the newly installed skill in the same conversation
- run the installed skill when the skill docs provide a simple demo

The installed skills are stored under a user-specific directory, so a new
conversation can see previously installed skills without re-installing
them.

## What This Example Shows

- common skills plus user-private skills
- dynamic skill installation during a conversation
- same-conversation availability after repository refresh
- new-session visibility because the user skill directory persists
- real-model execution without mocks

## Prerequisites

- Go 1.24 or later
- A model endpoint compatible with `model/openai`
- Network access to GitHub and DuckDuckGo

## Environment Variables

The example reads the standard OpenAI-compatible environment variables:

```bash
export OPENAI_BASE_URL="..."
export OPENAI_API_KEY="..."
export MODEL_NAME="gpt-5.2"
```

You can also override the model with `-model`.

## Run

From the `examples` module root:

```bash
cd examples
go run ./skillfind
```

Or run a one-shot prompt:

```bash
cd examples
go run ./skillfind \
  -reset-user-skills \
  -prompt "Use the skill-find skill to find the public hello skill from the OpenClaw skill pack on GitHub, install it, load it, and run it."
```

## Interactive Commands

- `exit`: quit
- `/new`: start a new session id
- `/skills`: print the currently visible skills
- `/reset-skills`: clear the user-installed skill directory

## Recommended Demo Prompt

Use a prompt that targets a known public skill with a simple runnable
command. The `hello` skill from the public OpenClaw skill pack is a good
choice:

```text
Use the skill-find skill to find the public hello skill from the
OpenClaw skill pack on GitHub, install it, load it, and run it.
```

That flow should search the public web, install the GitHub skill
directory into the demo user's private skill directory, refresh the
repository, load the new skill, and run its documented demo command.

## Notes

- The GitHub installer currently supports public GitHub `tree`,
  `blob`, and raw `SKILL.md` URLs.
- The example intentionally prefers real network paths over mocks, so the
  exact search results can vary.
