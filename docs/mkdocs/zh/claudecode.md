# Claude Code Agent 使用指南

## 概述

tRPC-Agent-Go 提供了 `ClaudeCode` 的 `Agent` 实现，通过执行本地 Claude Code CLI 获取执行轨迹，映射为框架事件。

该实现的主要用途包括：

- 在 `runner` 中运行 Claude Code
- 落盘 CLI 原始 stdout 与 stderr
- 在评估中对齐工具轨迹

## 快速上手

### 前置条件

1. 本地已安装并完成 Claude Code CLI 认证
2. CLI 可执行文件可从 `PATH` 访问，或通过 `WithBin` 指定绝对路径

### 基本用法

完整示例参见 [examples/claudecode](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/claudecode)。

```go
import (
	"trpc.group/trpc-go/trpc-agent-go/agent/claudecode"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/runner"
)

ag, err := claudecode.New(
  claudecode.WithBin("claude"),
  claudecode.WithExtraArgs("--permission-mode", "bypassPermissions"),
)
if err != nil {
  log.Fatal(err)
}

r := runner.NewRunner("claudecode-cli-example", ag)
defer r.Close()

ch, err := r.Run(context.Background(), "user-1", "session-1", model.NewUserMessage("Use the Bash tool to run ls and return the first filename."))
if err != nil {
  log.Fatal(err)
}
```

## 输出格式与解析

该 Agent 以 `--print` 模式运行 Claude Code CLI。CLI 输出应为 JSON 输出记录，支持两种格式：

- `json`，JSON 数组
- `stream-json`，JSONL

该 Agent 依赖输出中的 `tool_use` 与 `tool_result` 记录，因此默认启用 `--verbose` 并强制设置 `--output-format`。

使用 `WithOutputFormat` 切换输出格式，不要通过 `WithExtraArgs` 额外追加 `--output-format`。

```go
ag, err := claudecode.New(
  claudecode.WithOutputFormat(claudecode.OutputFormatStreamJSON),
)
```

## 事件映射

该 Agent 只发出工具事件与最终响应事件，不发出中间 assistant 文本消息对应的事件。最终响应内容取输出中最后一条 `type` 为 `result` 的 `result` 字段。

| Claude Code JSON 输出 | 框架事件 |
| --- | --- |
| `message.content[].type == "tool_use"` | tool-call response 事件 |
| `message.content[].type == "tool_result"` | tool-result response 事件 |
| `type == "result"` | final response 事件 |
| `tool_use.name == "Task"` 且包含 `subagent_type` | transfer 事件 |
| `tool_use.name == "Skill"` | 工具名归一化为 `skill_run` |

## 多轮会话

Claude Code CLI 的 `--session-id` 要求是 UUID。该 Agent 会基于 `invocation.Session.AppName`、`invocation.Session.UserID`、`invocation.Session.ID` 生成确定性的 UUID 作为 CLI session id。

### 默认方式：使用 Claude Code session

默认情况下，该 Agent 使用 Claude Code CLI 原生 session 历史。每次运行按如下顺序尝试：

1. `--resume <cli-session-id>`
2. `--session-id <cli-session-id>`

如果 `--resume` 找不到已有会话，该 Agent 会使用同一个 deterministic UUID 通过 `--session-id` 创建会话。后续轮次继续使用相同的 app name、user ID、session ID 时，会得到相同的 CLI session id。

这种方式适合单实例服务，或请求总是固定路由到同一台机器、同一个用户目录和同一套 Claude Code 配置环境的场景。此时如需保持上下文，请在 `runner` 中持续使用相同的 app name、user ID、session ID。

使用 `WithResumeEnabled(false)` 可以关闭 Claude Code CLI 原生 session resume。关闭后，该 Agent 不传 `--resume` 或 `--session-id`，而是传 `--no-session-persistence`，避免本地 CLI session 成为隐式上下文来源。

### 使用框架 session events 构造上下文

如果服务部署了多个实例、容器会重建，或者请求不会固定路由到同一台机器，Claude Code CLI 本地 session 就不能作为可靠的多轮上下文来源。此时应把上下文放在框架 session service 中，例如 Redis 或数据库。所有服务实例通过相同的 app name、user ID、session ID 读取同一份 session events，再由 `WithMessageBuilder` 把这些 events 拼成传给 Claude Code CLI 的完整 prompt。

推荐配置方式：

1. 给 `runner` 配置共享 session service。
2. 使用 `WithMessageBuilder` 从 `args.Events` 构造完整 prompt。
3. 使用 `WithResumeEnabled(false)` 关闭 Claude Code CLI 本地 session resume，避免同一段历史同时来自 prompt 和本地 session。

`MessageBuilderArgs.Events` 是只读浅快照，不应修改其中的 event、response、state delta 或 extensions。通过 `runner.Run` 调用时，runner 会先持久化当前 turn 的 user message，再调用 Agent；因此 builder 看到的 events 已经包含当前 turn user message，不要默认再追加一遍。

下面示例省略 `context`、`strings` 等标准库 import，只展示 Agent 相关配置。示例只拼接非 partial 的 message 文本；生产环境可以按业务需要选择是否加入工具调用和工具结果。

```go
import "trpc.group/trpc-go/trpc-agent-go/agent/claudecode"

ag, err := claudecode.New(
  claudecode.WithMessageBuilder(func(ctx context.Context, args *claudecode.MessageBuilderArgs) (string, error) {
    var prompt strings.Builder
    for _, evt := range args.Events {
      if evt.Response == nil || len(evt.Choices) == 0 || evt.IsPartial {
        continue
      }
      msg := evt.Choices[0].Message
      if msg.Content == "" {
        continue
      }
      prompt.WriteString(string(msg.Role))
      prompt.WriteString(": ")
      prompt.WriteString(msg.Content)
      prompt.WriteString("\n")
    }
    return prompt.String(), nil
  }),
  claudecode.WithResumeEnabled(false),
)
```

## 原始日志落盘

使用 `WithRawOutputHook` 获取每次执行的 stdout/stderr，建议写入评估/观测产物目录中：

```go
ag, err := claudecode.New(
  claudecode.WithRawOutputHook(func(ctx context.Context, args *claudecode.RawOutputHookArgs) error {
    // Write args.Stdout / args.Stderr to your log storage.
    return nil
  }),
)
```

`RawOutputHookArgs` 包含框架侧 `SessionID` 与 CLI 侧 `CLISessionID`，可用于按 session 聚合日志。

## 配置选项说明

| Option | 说明 |
| --- | --- |
| `WithName(name)` | 设置 Agent 名称。该值会用作事件 author。 |
| `WithBin(bin)` | 设置 CLI 可执行文件路径。默认值为 `claude`。 |
| `WithExtraArgs(args...)` | 追加 CLI flags。该参数会插在 session flags 与 prompt 之前。 |
| `WithOutputFormat(format)` | 设置 JSON 输出格式：`json` 或 `stream-json`。 |
| `WithEnv(env...)` | 追加 CLI 环境变量。格式为 `KEY=VALUE`。 |
| `WithWorkDir(dir)` | 设置 CLI 工作目录。 |
| `WithRawOutputHook(hook)` | 观测 raw stdout/stderr。回调会在 CLI 结束后、解析前调用。 |
| `WithMessageBuilder(builder)` | 自定义传给 Claude Code CLI 的完整 prompt。 |
| `WithResumeEnabled(enabled)` | 控制是否使用 Claude Code CLI session resume；默认 `true`。 |
