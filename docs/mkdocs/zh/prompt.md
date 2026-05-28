# Prompt Template

`prompt` 包提供轻量级文本 Prompt 模板能力，用于运行时变量、
resolver 驱动的占位符，以及远端 Prompt 来源。当指令文本需要按请求、
用户或环境变化，但你仍希望继续使用 `LLMAgent` 的字符串 API 时，可以使用它。

适用场景：

- 在运行前用运行时值渲染 Agent 指令
- 校验模板是否包含应用期望的占位符
- 从自定义存储中解析命名空间占位符
- 从 Langfuse 获取文本 Prompt，并在本地完成变量渲染

如果你想了解 `LLMAgent` 指令中自动展开的会话状态占位符，请参考
[Agent 占位符变量](./agent.md#placeholder-variables-session-state-injection)。

## 本地文本模板

创建 `prompt.Text`，并通过 `prompt.Vars` 调用 `Render`：

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
    // 处理渲染错误。
}
```

默认的 `SyntaxMixedBrace` 会在同一个模板中同时识别 `{name}` 和
`{{name}}`。如果你想限制占位符风格，可以显式指定语法：

```go
tmpl := prompt.Text{
    Template: "Hello {{ name }}",
    Syntax:   prompt.SyntaxDoubleBrace,
}
```

双大括号占位符只表示变量替换；该包不实现 section、partial 等完整
Mustache 控制语法。

## 可选占位符与未知占位符

在右分隔符前添加 `?` 可以将占位符标记为可选：

```go
tmpl := prompt.Text{
    Template: "Audience: {audience?}\nTask: {task}",
}
```

缺失的可选占位符会渲染为空字符串。缺失的必填占位符默认会保留原样，
这样后续 Prompt 组装流程或模型仍然能看到未解析的 token。

如果缺失必填值应当快速失败，可以使用 `prompt.ErrorOnUnknown`：

```go
rendered, err := tmpl.Render(
    prompt.RenderEnv{
        Vars: prompt.Vars{"audience": "operators"},
    },
    prompt.WithUnknownBehavior(prompt.ErrorOnUnknown),
)
```

## 校验必需占位符

当模板来自配置或远端 Prompt 服务，并且你希望约束稳定的模板契约时，
可以使用 `ValidateRequired`：

```go
tmpl := prompt.Text{
    Template: "Summarize {conversation_text} in {max_words} words.",
}

if err := tmpl.ValidateRequired("conversation_text", "max_words"); err != nil {
    // 模板缺少必需占位符。
}
```

## Resolver 驱动的占位符

`prompt.Resolver` 可用于解析未直接放入 `Vars` 的占位符名称。它适合
`{user:name}` 这类命名空间引用，或需要延迟加载的值。

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

`Vars` 会先于 resolver 被检查。resolver 返回 `("", true, nil)` 表示
占位符已找到并应渲染为空字符串；返回 `("", false, nil)` 表示未找到。

## 应用到 LLMAgent

如果指令在 Agent 生命周期内保持不变，可以先渲染模板，再创建 Agent：

```go
instruction, err := tmpl.Render(prompt.RenderEnv{
    Vars: prompt.Vars{"role": "support"},
})
if err != nil {
    // 处理渲染错误。
}

llmAgent := llmagent.New(
    "support-agent",
    llmagent.WithModel(modelInstance),
    llmagent.WithInstruction(instruction),
)
```

如果指令需要按请求变化，可以在运行时渲染，并通过 run option 传入：

```go
instruction, err := tmpl.Render(prompt.RenderEnv{
    Vars: prompt.Vars{"topic": "refund policy"},
})
if err != nil {
    // 处理渲染错误。
}

eventCh, err := runner.Run(
    ctx,
    userID,
    sessionID,
    model.NewUserMessage("Draft a reply."),
    agent.WithInstruction(instruction),
)
```

如果同一个 Agent 实例需要在多次运行之间刷新默认指令，可以先渲染模板，
再在调用 runner 前执行 `SetInstruction`。

## Langfuse Prompt Source

`prompt/provider/langfuse` 可以获取 Langfuse 文本 Prompt，并返回
`prompt.Text`：

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
    // 处理获取错误。
}

instruction, err := text.Render(prompt.RenderEnv{
    Vars: prompt.Vars{
        "criticlevel": "expert",
        "movie":       "Dune 2",
    },
})
```

完整可运行示例见 `examples/prompt/langfuse`：它会获取 Prompt、渲染变量、
更新 `LLMAgent` 指令并运行 Agent。
