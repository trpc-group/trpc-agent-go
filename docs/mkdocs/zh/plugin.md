# 插件（Plugins）

## 概述

插件是一种 *Runner 作用域*（Runner-scoped）的扩展机制，可以在以下生命周期点插入
自定义逻辑：

- Agent 执行
- 模型调用（大语言模型（Large Language Model, LLM）请求）
- 工具（Tool）调用
- 事件（Event）发出

插件主要解决的是 **横切关注点**：也就是“你希望所有 Agent 都统一具备的能力”，而
不是某个 Agent 的业务逻辑。

常见场景：

- 统一日志与调试
- 安全与策略（policy）拦截
- 请求改写（例如统一追加 system instruction）
- 审计、事件打标

如果你的需求只针对某一个 Agent，通常用回调（Callbacks）会更合适。

## 插件和回调有什么区别？

回调（callback）是“一个函数”：框架在特定时机（before/after）去调用它。你需要把
它绑定到你想生效的地方（很多时候是某个 Agent 上）。

插件（plugin）是“一个组件”：它把“一组回调 + 配置 + 可选的生命周期管理”打包在
一起，然后 **只在 Runner 上注册一次**，就能自动对该 Runner 管理的所有 Invocation
（一次运行的上下文）生效。

换句话说：

- **回调**：一个生命周期点的 hook（钩子）函数。
- **插件**：一个可复用模块，内部注册多组回调并可统一启用/关闭。

## 什么时候用插件？

当你希望“对一个 Runner 管理的所有执行都统一生效”时，用插件更合适：

- 想对所有 Agent 做统一策略（例如拦截某些输入）。
- 想统一做日志、指标（metrics）、链路追踪（tracing）。
- 想对每一次模型请求做统一改写（例如加 system instruction）。
- 想给所有事件统一打标，便于审计与排查。

## 什么时候用回调？

当你的需求只影响某个特定 Agent 时，用回调更合适：

- 只有一个 Agent 需要特殊的请求改写。
- 只有一个 Agent 的工具需要自定义参数处理。
- 你在快速试验，不希望全局影响其他 Agent。

你也可以混用：插件负责全局默认行为，回调负责单个 Agent 的定制。

## 快速开始

创建 Runner 时注册插件（只需要注册一次）：

```go
import (
	"trpc.group/trpc-go/trpc-agent-go/plugin"
	"trpc.group/trpc-go/trpc-agent-go/runner"
)

r := runner.NewRunner(
	"my-app",
	agentInstance,
	runner.WithPlugins(
		plugin.NewLogging(),
		plugin.NewGlobalInstruction(
			"You must follow security policies.",
		),
	),
)
defer r.Close()
```

## 插件是如何执行的？

### 作用域与传播

- 插件在 Runner 创建时初始化一次，并存放到 Invocation 的 `Invocation.Plugins` 中。
- 当 Invocation 被 Clone（例如子 Agent、AgentTool、transfer 等）时，同一个插件管理器
  会被带过去，所以插件在嵌套执行中仍然一致生效。

### 在回调里拿到 Invocation

在 `BeforeModel` / `AfterModel` / `BeforeTool` / `AfterTool` 这类回调里，你通常只拿到
`context.Context`。如果你需要当前 Invocation，可以从 context 里取出来：

```go
invocation, ok := agent.InvocationFromContext(ctx)
_ = invocation
_ = ok
```

在工具回调里，框架还会把工具调用标识符（identifier, ID）注入 context：

```go
toolCallID, ok := tool.ToolCallIDFromContext(ctx)
_ = toolCallID
_ = ok
```

### 执行顺序与短路（short-circuit）

插件会 **按注册顺序执行**。

某些 before hook 支持“短路”默认行为：

- **BeforeAgent**：可以返回自定义响应，直接跳过 Agent 执行。
- **BeforeModel**：可以返回自定义响应，直接跳过模型接口调用
  （应用程序编程接口（Application Programming Interface, API））。
- **BeforeTool**：可以返回自定义结果，直接跳过工具执行。

某些 after hook 支持“覆盖”输出：

- **AfterModel**：可以返回自定义响应，替换模型响应。
- **AfterTool**：可以返回自定义结果，替换工具结果。

### 错误处理

- agent/model/tool hook 返回 error 会让本次运行失败（错误返回给调用方）。
- `OnEvent` 返回 error 时，Runner 会记录日志并继续使用原始事件。

### 并发（很重要）

工具可能并发执行，某些 Agent 也可能并发运行。如果你的插件会保存共享状态，请确保
并发安全（例如使用 `sync.Mutex` 或原子（atomic）类型）。

### Close（资源释放）

如果插件实现了 `plugin.Closer`，当你调用 `Runner.Close()` 时，Runner 会调用插件的
`Close()` 来释放资源。关闭顺序是 **按注册顺序的反向**（后注册的先关闭）。

## 可插入的生命周期点（Hook Points）

### Agent hooks

- `BeforeAgent`：Agent 开始前
- `AfterAgent`：Agent 事件流结束后

### Model hooks

- `BeforeModel`：模型请求发出前
- `AfterModel`：模型响应产生后

### Tool hooks

- `BeforeTool`：工具调用前，可以修改工具参数（JSON（JavaScript Object Notation）
  字节）
- `AfterTool`：工具调用后，可以替换结果

### Event hook

- `OnEvent`：Runner 发出每一个事件时都会调用（包括 runner completion 事件）。你可以
  原地修改事件，或者返回一个新的事件作为替代。

## 常见用法（Recipes）

### 1) 拦截输入并短路模型调用（策略）

用 `BeforeModel` 在模型调用前直接返回自定义响应：

```go
const blockedKeyword = "/deny"

r.BeforeModel(func(
	ctx context.Context,
	args *model.BeforeModelArgs,
) (*model.BeforeModelResult, error) {
	if args == nil || args.Request == nil {
		return nil, nil
	}
	for _, msg := range args.Request.Messages {
		if msg.Role == model.RoleUser &&
			strings.Contains(msg.Content, blockedKeyword) {
			return &model.BeforeModelResult{
				CustomResponse: &model.Response{
					Done: true,
					Choices: []model.Choice{{
						Index: 0,
						Message: model.NewAssistantMessage(
							"Blocked by plugin policy.",
						),
					}},
				},
			}, nil
		}
	}
	return nil, nil
})
```

### 2) 给所有事件打标（审计/排查）

用 `OnEvent` 给事件追加 tag，便于 UI（User Interface）过滤或日志检索：

```go
const demoTag = "plugin_demo"

r.OnEvent(func(
	ctx context.Context,
	inv *agent.Invocation,
	e *event.Event,
) (*event.Event, error) {
	if e == nil {
		return nil, nil
	}
	if e.Tag == "" {
		e.Tag = demoTag
		return nil, nil
	}
	if !e.ContainsTag(demoTag) {
		e.Tag = e.Tag + event.TagDelimiter + demoTag
	}
	return nil, nil
})
```

### 3) 改写工具参数（清洗/规范化）

用 `BeforeTool` 替换工具参数（JSON（JavaScript Object Notation）字节）：

```go
r.BeforeTool(func(
	ctx context.Context,
	args *tool.BeforeToolArgs,
) (*tool.BeforeToolResult, error) {
	if args == nil {
		return nil, nil
	}
	if args.ToolName == "calculator" {
		return &tool.BeforeToolResult{
			ModifiedArguments: []byte(`{"operation":"add","a":1,"b":2}`),
		}, nil
	}
	return nil, nil
})
```

## 内置插件

### Logging

`plugin.NewLogging()` 会记录 agent/model/tool 的开始与结束信息，适合用于调试与
性能分析。

### GlobalInstruction

`plugin.NewGlobalInstruction(text)` 会在每一次模型请求前，统一追加一条 system
message。适合用来实现全局策略或统一行为（例如安全约束、风格要求）。

## 如何扩展：写一个自己的插件

### 1) 实现接口

自定义一个类型，实现：

- `Name() string`：同一个 Runner 内必须唯一
- `Register(r *plugin.Registry)`：在这里注册 hook

### 2) 在 Register 里注册 hook

可用的注册方法：

- `BeforeAgent`, `AfterAgent`
- `BeforeModel`, `AfterModel`
- `BeforeTool`, `AfterTool`
- `OnEvent`

### 3)（可选）实现 `plugin.Closer`

如果插件需要释放资源（文件、后台 goroutine、缓冲区等），实现 `Close(ctx)` 让 Runner
在关闭时统一清理。

### 完整示例

可运行的完整示例（包含一个自定义策略插件）见：

- `examples/plugin`
