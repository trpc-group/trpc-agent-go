# Extending OpenClaw Demo (Plugins and Internal Distributions)

This document explains how to extend the OpenClaw demo in a Go-idiomatic
way, from first principles.

You will learn how to:

- Build your own OpenClaw binary (for example an internal distribution).
- Add new capabilities as **plugins** (channels, tools, storage backends,
  model providers).
- Enable plugins using a YAML config file.
- Extend the agent with file-based **skills** (`SKILL.md` folders) without
  writing Go code.

If you just want a working example, start with `openclaw/examples/stdin_chat/`.

## What "plugin" means here (no magic, no dynamic loading)

Go is a compiled language.

That means:

- Your binary contains exactly the packages you import at build time.
- A running binary cannot "discover new Go code" by reading a config file.

So the typical Go plugin pattern is:

1) The framework defines a registry (a global map of `type -> factory`).
2) A plugin package registers itself in `init()`.
3) Your binary imports the plugin package (often anonymously), so its `init()`
   runs and registration happens.

This repo follows that pattern in `openclaw/registry`.

Tip: to see what types are currently registered in your binary, run:

```bash
openclaw inspect plugins
```

This is useful when your YAML config references a plugin type and you
want to confirm it was actually compiled in.

## Key packages (where to look)

- `openclaw/app`
  - The runnable library that wires model + runner + gateway + channels.
  - `cmd/openclaw` is just a tiny wrapper around it.
- `openclaw/registry`
  - Global registries for pluggable components.
  - Helper `registry.DecodeStrict` for strict YAML decoding.
- `openclaw/channel`
  - The minimal `Channel` interface (what you implement for new IM channels).
- `openclaw/gwclient`
  - An in-process client that calls the gateway handler without a network hop.
- `openclaw/internal/skills`
  - Skill repository wrapper with optional environment gating and
    `{baseDir}` substitution.

## Step 1: Build your own binary (internal distribution)

Create a new Go module/repo (for example `your.company/agent-internal`) and
add a `main.go`:

```go
package main

import (
	"os"

	"trpc.group/trpc-go/trpc-agent-go/openclaw/app"

	// Enable plugins (internal-only packages) via anonymous imports.
	_ "your.company/agent-internal/openclaw_plugins/wecom"
	_ "your.company/agent-internal/openclaw_plugins/internal_tools"
)

func main() {
	os.Exit(app.Main(os.Args[1:]))
}
```

Why anonymous imports (`_ "..."`)?

- You do not need to call anything from the plugin package.
- You only want its `init()` function to run (so it can register itself).

## Step 2: Enable plugins in YAML config

OpenClaw uses a YAML config file (unknown fields fail fast).

At a high level:

- `channels` enables channel plugins.
- `tools.providers` enables tool-provider plugins.
- `tools.toolsets` enables ToolSet-provider plugins.
- `model.mode`, `session.backend`, `memory.backend` select implementations.

Example `openclaw.yaml`:

```yaml
app_name: "my-openclaw"

http:
  addr: ":8080"

model:
  mode: "mock"

channels:
  - type: "wecom"
    name: "corp"
    config:
      corp_id: "<YOUR_CORP_ID>"
      agent_id: "<YOUR_AGENT_ID>"

tools:
  providers:
    - type: "internal_tools"
      config:
        feature_flag: true
```

Run your internal binary:

```bash
./openclaw-internal -config ./openclaw.yaml
```

## Writing plugins

Every plugin has the same three building blocks:

1) Pick a **type name** (a string like `wecom`, `internal_tools`).
2) Implement a **factory function**.
3) Register it in `init()` using `openclaw/registry`.

### Strict config decoding (recommended)

Plugins usually accept config under `config:`.

Use `registry.DecodeStrict(node, &cfg)` to decode and reject unknown fields:

```go
var cfg myCfg
if err := registry.DecodeStrict(spec.Config, &cfg); err != nil {
	return nil, err
}
```

This keeps configuration "fail-closed" (typos do not silently get ignored).

## Channel plugin (receive messages + send replies)

A **channel** is an adapter from an external system (IM/webhook/etc.) to the
gateway.

You implement `openclaw/channel.Channel`:

- `ID() string`
- `Run(ctx) error`

Then register a factory:

```go
package wecom

import (
	"context"

	occhannel "trpc.group/trpc-go/trpc-agent-go/openclaw/channel"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/gwclient"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/registry"
)

const typeName = "wecom"

func init() {
	if err := registry.RegisterChannel(typeName, newChannel); err != nil {
		panic(err)
	}
}

type cfg struct {
	CorpID  string `yaml:"corp_id"`
	AgentID string `yaml:"agent_id"`
}

func newChannel(
	deps registry.ChannelDeps,
	spec registry.PluginSpec,
) (occhannel.Channel, error) {
	var c cfg
	if err := registry.DecodeStrict(spec.Config, &c); err != nil {
		return nil, err
	}

	// deps.Gateway lets you call the gateway without HTTP/network.
	_ = deps.Gateway

	// Build and return your channel implementation.
	return &channel{}, nil
}

type channel struct{}

func (c *channel) ID() string { return typeName }

func (c *channel) Run(ctx context.Context) error {
	// 1) Receive inbound messages from WeCom.
	// 2) Convert each message into a gwclient.MessageRequest.
	// 3) Call deps.Gateway.SendMessage(...) to get a reply.
	// 4) Deliver the reply back to WeCom.
	return nil
}

func toGatewayRequest(text string) gwclient.MessageRequest {
	return gwclient.MessageRequest{
		Channel: typeName,
		From:    "user-id",
		Text:    text,
	}
}
```

Enable it in YAML:

```yaml
channels:
  - type: "wecom"
    config:
      corp_id: "..."
      agent_id: "..."
```

## Tool provider plugin (add new tools)

A **tool provider** contributes one or more `tool.Tool` implementations.

Register a tool provider:

```go
package internaltools

import (
	"context"
	"encoding/json"

	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/registry"
)

const typeName = "internal_tools"

func init() {
	if err := registry.RegisterToolProvider(typeName, newTools); err != nil {
		panic(err)
	}
}

type cfg struct {
	FeatureFlag bool `yaml:"feature_flag"`
}

func newTools(
	_ registry.ToolProviderDeps,
	spec registry.PluginSpec,
) ([]tool.Tool, error) {
	var c cfg
	if err := registry.DecodeStrict(spec.Config, &c); err != nil {
		return nil, err
	}

	if !c.FeatureFlag {
		return nil, nil
	}
	return []tool.Tool{echoTool{}}, nil
}

type echoTool struct{}

func (t echoTool) Declaration() *tool.Declaration {
	return &tool.Declaration{Name: "echo", Description: "Echo one string."}
}

func (t echoTool) Call(_ context.Context, jsonArgs []byte) (any, error) {
	var args struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(jsonArgs, &args); err != nil {
		return nil, err
	}
	return args.Text, nil
}
```

Enable it in YAML:

```yaml
tools:
  providers:
    - type: "internal_tools"
      config:
        feature_flag: true
```

## ToolSet provider plugin (dynamic tool collections)

A **ToolSet provider** contributes one `tool.ToolSet` implementation.

Why ToolSets?

- A ToolSet can expose multiple tools as a unit.
- A ToolSet can be **dynamic** (for example MCP: tool list changes over
  time).
- Tools coming from ToolSets are automatically namespaced by the
  toolset name to avoid conflicts:
  `"<toolset>_<tool>"`.

Register a ToolSet provider:

```go
package internaltoolset

import (
	"context"

	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/registry"
)

const typeName = "internal_toolset"

func init() {
	if err := registry.RegisterToolSetProvider(typeName, newToolSet); err != nil {
		panic(err)
	}
}

type cfg struct {
	Message string `yaml:"message"`
}

type toolSet struct {
	name  string
	tools []tool.Tool
}

func (t *toolSet) Tools(ctx context.Context) []tool.Tool { return t.tools }
func (t *toolSet) Close() error                          { return nil }
func (t *toolSet) Name() string                           { return t.name }

func newToolSet(
	_ registry.ToolSetProviderDeps,
	spec registry.PluginSpec,
) (tool.ToolSet, error) {
	var c cfg
	if err := registry.DecodeStrict(spec.Config, &c); err != nil {
		return nil, err
	}

	name := spec.Name
	if name == "" {
		name = "internal"
	}

	t := function.NewFunctionTool(
		func(ctx context.Context, _ struct{}) (string, error) {
			return c.Message, nil
		},
		function.WithName("ping"),
		function.WithDescription("Returns a configured message."),
	)

	return &toolSet{
		name:  name,
		tools: []tool.Tool{t},
	}, nil
}
```

Enable it in YAML:

```yaml
tools:
  toolsets:
    - type: "internal_toolset"
      name: "corp"
      config:
        message: "pong"
```

## Session backend plugin (centralized conversation storage)

Session backends implement `session.Service` (from `trpc-agent-go`).

Register a backend factory:

```go
package sessionpg

import (
	"errors"

	"trpc.group/trpc-go/trpc-agent-go/session"
	sessioninmemory "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/registry"
)

const typeName = "postgres"

func init() {
	if err := registry.RegisterSessionBackend(typeName, newBackend); err != nil {
		panic(err)
	}
}

type cfg struct {
	DSN string `yaml:"dsn"`
}

func newBackend(
	deps registry.SessionDeps,
	spec registry.SessionBackendSpec,
) (session.Service, error) {
	var c cfg
	if err := registry.DecodeStrict(spec.Config, &c); err != nil {
		return nil, err
	}
	if c.DSN == "" {
		return nil, errors.New("postgres session: missing dsn")
	}

	// TODO: build a real Postgres-backed session service.
	// This placeholder returns the built-in in-memory service.
	_ = deps.Summarizer
	return sessioninmemory.NewSessionService(), nil
}
```

Select it in YAML:

```yaml
session:
  backend: "postgres"
  config:
    dsn: "postgres://..."
```

### Optional: session summaries (compression)

If you enable session summarization in config:

```yaml
session:
  summary:
    enabled: true
```

OpenClaw creates an LLM-based `summary.SessionSummarizer` and passes it to
your backend factory via `deps.Summarizer`.

As a session backend author, you should treat this as "optional
middleware":

- If `deps.Summarizer == nil`, do nothing (feature disabled).
- If `deps.Summarizer != nil`, wire it into your session service so
  background summary jobs can run.

Example pattern:

```go
if deps.Summarizer != nil {
	// backend-specific option to enable summarization.
	opts = append(opts, yourbackend.WithSummarizer(deps.Summarizer))
}
```

Also note: generating summaries only stores them in the session backend.
To actually **use** the summary during runs, enable:

```yaml
agent:
  add_session_summary: true
```

## Memory backend plugin (centralized user memory storage)

Memory backends implement `memory.Service` (from `trpc-agent-go`).

Select it in YAML:

```yaml
memory:
  backend: "your_backend"
  config:
    ...
```

## Model plugin (custom model providers)

Model factories create a `model.Model`.

Select it in YAML:

```yaml
model:
  mode: "your_model"
  name: "..."
  config:
    ...
```

## Skills (file-based, fastest extension)

A **skill** is a folder that contains a `SKILL.md` file (plus optional
docs/scripts).

OpenClaw loads skills from the filesystem and makes them available to the
agent through `trpc-agent-go`'s built-in skills tooling.

If your main goal is to ship "internal know-how" (runbooks, scripts,
prompt templates) in an easy-to-contribute way, start with skills:

- No Go code required.
- No custom binary required.
- Easy to version and review (plain files).

### Skill folder layout

You typically store a skill like this:

```
skills/
  hello/
    SKILL.md
    scripts/
      hello.sh
    DOC.md            # optional extra docs (any .md or .txt)
```

Rules of thumb:

- Put the human-facing instructions in `SKILL.md`.
- Put executable logic under `scripts/` (or any subfolder you like).
- Write outputs under `out/` (the skills runner auto-collects it).

OpenClaw also ships a few example skills under `openclaw/skills/`
(`hello`, `envdump`, `http_get`).

### Minimal `SKILL.md` template

`SKILL.md` is Markdown with a YAML "front matter" block at the top.

Front matter is delimited by two `---` lines and lets you define:

- `name`: the unique skill name (used to reference the skill)
- `description`: a one-line description

Example:

```md
---
name: hello
description: Write a hello file to the workspace output directory.
---

Overview

This skill writes a small file under `out/`.

Command

bash scripts/hello.sh out/hello.txt

Output Files

- out/hello.txt
```

The body can be any Markdown. In practice, keep it short and explicit:

- What the skill does.
- The exact command to run.
- Expected outputs (paths).

### How OpenClaw finds skills (roots and precedence)

OpenClaw searches multiple skill roots (highest precedence first):

1) Workspace skills: `skills.root` / `-skills-root` (default: `./skills`)
2) Project AgentSkills: `./.agents/skills`
3) Personal AgentSkills: `$HOME/.agents/skills`
4) Managed skills: `<state-dir>/skills`
5) Repo bundled skills: `./openclaw/skills` (when running from repo root)
6) Extra dirs: `skills.extra_dirs` / `-skills-extra-dirs`

Duplicate names:

- If two skills have the same `name`, the higher-precedence one wins.
- OpenClaw semantics are **fail-closed**: if the winning skill is gated
  off (see below), OpenClaw does not "fall back" to a lower-precedence
  version of the same skill name.

So, avoid publishing multiple variants under the same `name` unless you
fully understand the gating behavior.

### OpenClaw metadata gating (optional)

In addition to `name` and `description`, OpenClaw recognizes
`metadata.openclaw` in front matter.

This lets you **hide** skills that cannot work in the current
environment (missing binaries, missing env vars, OS mismatch).

Supported fields:

- `metadata.openclaw.always`: if `true`, always enable the skill.
- `metadata.openclaw.os`: allowlist of `darwin`, `linux`, `win32`.
- `metadata.openclaw.requires.bins`: binaries that must exist in `PATH`.
- `metadata.openclaw.requires.anyBins`: at least one binary must exist.
- `metadata.openclaw.requires.env`: env vars that must be present.

Example (requires `bash` + `curl`):

```md
---
name: http_get
description: Fetch a URL with curl and write it to out/.
metadata:
  openclaw:
    requires:
      bins: ["bash", "curl"]
---

...
```

To understand why a skill is missing, enable skills debug logs:

- YAML: `skills.debug: true`
- CLI: `-skills-debug`

### `{baseDir}` placeholder (OpenClaw skill pack compatibility)

Some OpenClaw skill packs put commands like this into `SKILL.md`:

```
bash {baseDir}/scripts/setup.sh
```

`{baseDir}` is a placeholder for "the local directory that contains this
skill".

This demo replaces `{baseDir}` in loaded skill bodies and docs with the
actual skill directory path, so those skill packs remain usable.

### Distributing internal skill packs (without code changes)

Because skills are just files, the simplest internal workflow is:

1) Keep skills in a separate repo (reviewable, versioned).
2) Deploy by mounting/checking out that repo next to the binary.
3) Point OpenClaw at it using config.

Example:

```yaml
skills:
  extra_dirs:
    - "/path/to/internal/skills"
```

Advanced option: `skills.root` can be a URL.

The `trpc-agent-go/skill` repository supports `http://` and `https://`
roots that point to a `.zip`, `.tar`, `.tar.gz`, or `.tgz` archive
containing a skills directory tree.

This is useful when you want to version and ship skill packs as an
artifact without rebuilding the OpenClaw binary.

URL-based skill roots are cached on disk (under your OS user cache dir by
default). You can override the cache location with `SKILLS_CACHE_DIR`.

### Security note

Treat skills as code:

- A skill can instruct the agent to run commands (`skill_run`) and read
  or write files in a workspace.
- If you need tighter control, restrict `skill_run` using process-level
  env vars:
  `TRPC_AGENT_SKILL_RUN_ALLOWED_COMMANDS` /
  `TRPC_AGENT_SKILL_RUN_DENIED_COMMANDS`.
- Only load skills from sources you trust.
- When exposing OpenClaw through external channels (Telegram/webhooks),
  use allowlists/pairing and limit unsafe tools.
