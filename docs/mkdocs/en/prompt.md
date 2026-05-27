# Prompt Template

The `prompt` package provides lightweight text prompt templates for runtime
variables, resolver-backed placeholders, and remote prompt sources. It is useful
when the instruction text changes per request, per user, or per environment but
you still want to keep the `LLMAgent` API string-based.

Use prompt templates for:

- rendering agent instructions with runtime values before a run
- validating that a template contains the placeholders your application expects
- resolving namespaced placeholders from your own store
- fetching text prompts from Langfuse and rendering them locally

For session-state placeholders that are expanded automatically inside
`LLMAgent` instructions, see [Agent placeholder variables](./agent.md#placeholder-variables-session-state-injection).

## Local Text Templates

Create a `prompt.Text` and call `Render` with `prompt.Vars`:

```go
import "trpc.group/trpc-go/trpc-agent-go/prompt"

tmpl := prompt.Text{
    Template: "You are a {{role}} assistant. Focus on {topic}.",
}

rendered, err := tmpl.Render(prompt.RenderEnv{
    Vars: prompt.Vars{
        "role":  "research",
        "topic": "graph retrieval",
    },
})
if err != nil {
    // Handle rendering errors.
}
```

By default, `SyntaxMixedBrace` recognizes both `{name}` and `{{name}}` in the
same template. You can opt into a stricter delimiter style:

```go
tmpl := prompt.Text{
    Template: "Hello {{ name }}",
    Syntax:   prompt.SyntaxDoubleBrace,
}
```

Double-brace placeholders are variable substitution only. The package does not
implement full Mustache features such as sections or partials.

## Optional And Unknown Placeholders

Add `?` before the closing delimiter to make a placeholder optional:

```go
tmpl := prompt.Text{
    Template: "Audience: {audience?}\nTask: {task}",
}
```

Missing optional placeholders render as an empty string. Missing required
placeholders are preserved by default, which lets later prompt assembly or the
model still see the unresolved token.

Use `prompt.ErrorOnUnknown` when missing required values should fail fast:

```go
rendered, err := tmpl.Render(
    prompt.RenderEnv{
        Vars: prompt.Vars{"audience": "operators"},
    },
    prompt.WithUnknownBehavior(prompt.ErrorOnUnknown),
)
```

## Validate Required Placeholders

Use `ValidateRequired` when you load templates from configuration or a remote
prompt service and want to enforce a stable contract:

```go
tmpl := prompt.Text{
    Template: "Summarize {conversation_text} in {max_words} words.",
}

if err := tmpl.ValidateRequired("conversation_text", "max_words"); err != nil {
    // The template is missing a required placeholder.
}
```

## Resolver-Backed Placeholders

`prompt.Resolver` lets you resolve placeholder names that are not passed in
`Vars`. This is useful for namespaced references such as `{user:name}` or
values that should be loaded lazily.

```go
type mapResolver map[string]string

func (r mapResolver) Resolve(ref prompt.Ref) (string, bool, error) {
    v, ok := r[ref.Name]
    return v, ok, nil
}

tmpl := prompt.Text{
    Template: "Write for {user:tier} users in {locale}.",
}

rendered, err := tmpl.Render(prompt.RenderEnv{
    Vars: prompt.Vars{"locale": "en-US"},
    Resolver: mapResolver{
        "user:tier": "enterprise",
    },
})
```

`Vars` are checked before the resolver. Returning `("", true, nil)` from a
resolver means the placeholder was found and should render as an empty string.
Returning `("", false, nil)` means it was not found.

## Applying Templates To LLMAgent

Render the template before constructing the agent when the instruction is static
for the agent lifetime:

```go
instruction, err := tmpl.Render(prompt.RenderEnv{
    Vars: prompt.Vars{"role": "support"},
})
if err != nil {
    // Handle rendering errors.
}

llmAgent := llmagent.New(
    "support-agent",
    llmagent.WithModel(modelInstance),
    llmagent.WithInstruction(instruction),
)
```

For per-request instructions, render at run time and pass the result as a run
option:

```go
instruction, err := tmpl.Render(prompt.RenderEnv{
    Vars: prompt.Vars{"topic": "refund policy"},
})
if err != nil {
    // Handle rendering errors.
}

eventCh, err := runner.Run(
    ctx,
    userID,
    sessionID,
    model.NewUserMessage("Draft a reply."),
    agent.WithInstruction(instruction),
)
```

If an agent instance should refresh its default instruction between runs, render
the template and call `SetInstruction` before invoking the runner.

## Langfuse Prompt Source

`prompt/provider/langfuse` can fetch Langfuse text prompts and return them as
`prompt.Text` values:

```go
import (
    "time"

    "trpc.group/trpc-go/trpc-agent-go/prompt"
    promptlangfuse "trpc.group/trpc-go/trpc-agent-go/prompt/provider/langfuse"
    lfconfig "trpc.group/trpc-go/trpc-agent-go/telemetry/langfuse/config"
)

client := promptlangfuse.NewClient(lfconfig.FromEnv())
source := client.TextPromptSourceWithOptions(
    "movie-critic",
    []promptlangfuse.FetchOption{
        promptlangfuse.WithLabel("production"),
    },
    promptlangfuse.WithCacheTTL(time.Minute),
)

text, err := source.FetchPrompt(ctx)
if err != nil {
    // Handle fetch errors.
}

instruction, err := text.Render(prompt.RenderEnv{
    Vars: prompt.Vars{
        "criticlevel": "expert",
        "movie":       "Dune 2",
    },
})
```

See `examples/prompt/langfuse` for a complete runnable example that fetches a
prompt, renders variables, updates an `LLMAgent` instruction, and runs the
agent.
