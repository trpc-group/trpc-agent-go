# Extending OpenClaw Demo (Plugins and Internal Distributions)

This document explains how to extend the OpenClaw demo in a Go-idiomatic
way, from first principles.

You will learn how to:

- Build your own OpenClaw binary (for example an internal distribution).
- Add new capabilities as **plugins** (channels, tools, storage backends,
  model providers).
- Enable plugins using a YAML config file.

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

## Internal skills (the simplest extension)

If your "skills" are file-based `SKILL.md` folders, you do not need any
Go code.

Put skills in a folder and point OpenClaw at it:

```yaml
skills:
  extra_dirs:
    - "./skills-internal"
```

This is a good starting point for internal contributions because it does
not require implementing Go interfaces.
