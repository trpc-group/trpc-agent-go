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

每次运行按如下顺序尝试：

1. `--resume <cli-session-id>`
2. `--session-id <cli-session-id>`

如需保持上下文，请在 `runner` 中持续使用相同的 app name、user ID、session ID。

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
