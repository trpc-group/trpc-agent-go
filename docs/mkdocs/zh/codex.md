# Codex Agent 使用指南

## 概述

tRPC-Agent-Go 提供了 `Codex` 的 `Agent` 实现，通过执行本地 Codex CLI 的 `codex exec --json` 获取 JSONL 事件流，并实时映射为框架事件。

该实现的主要用途包括：

- 在 `runner` 中运行 Codex
- 落盘 CLI 原始 stdout 与 stderr
- 在评估中对齐 Codex 命令与 MCP 工具轨迹

## 快速上手

### 前置条件

1. 本地已安装并完成 Codex CLI 认证
2. CLI 可执行文件可从 `PATH` 访问，或通过 `WithBin` 指定绝对路径

### 基本用法

完整示例参见 [examples/codex](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/codex)。该示例包含临时的项目内 MCP server 和项目内 skill。

```go
import (
	"trpc.group/trpc-go/trpc-agent-go/agent/codex"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/runner"
)

ag, err := codex.New(
  codex.WithBin("codex"),
  codex.WithGlobalArgs("--sandbox", "workspace-write"),
)
if err != nil {
  log.Fatal(err)
}

r := runner.NewRunner("codex-cli-example", ag)
defer r.Close()

ch, err := r.Run(context.Background(), "user-1", "session-1", model.NewUserMessage("Run pwd and summarize the workspace."))
if err != nil {
  log.Fatal(err)
}
```

## 输出格式与解析

该 Agent 会强制为 `codex exec` 添加 `--json`，并按 JSONL 解析 stdout。除非需要控制参数顺序，否则不要手动追加 `--json`。

Prompt 文本会通过 stdin 写入 Codex CLI，因此 `--help` 这类用户输入不会被解析成 Codex CLI flag。

`WithExtraArgs` 会把参数追加到 `exec` 或 `exec resume` 之后，只适合传入同时被 `codex exec` 与 `codex exec resume` 接受的参数，例如 `--model`。

`WithGlobalArgs` 会把参数追加到 `exec` 子命令之前，适合传入 `--ask-for-approval`、`--sandbox`、`--cd` 等 Codex 根级参数。

```go
ag, err := codex.New(
  codex.WithGlobalArgs("--ask-for-approval", "never"),
  codex.WithGlobalArgs("--sandbox", "read-only"),
)
```

## Skill 与外部仓库

该 Agent 不安装、不合并、不改写 Codex skill 仓库。它只是在指定环境、profile 与工作目录下执行本地 Codex CLI；同一个 `codex exec` 命令能从本地 Codex 配置加载到的 skills、plugins 或 plugin marketplaces，在该 Agent 中同样可用。

可以通过 Codex CLI 既有配置机制选择 skill 来源：

- `WithEnv("CODEX_HOME=/path/to/codex-home")` 选择隔离的 Codex home。
- `WithGlobalArgs("-p", "profile-name")` 选择 Codex profile。
- `WithGlobalArgs("-c", "key=value")` 传入 Codex config override。
- `WithGlobalArgs("--cd", "/path/to/workspace")` 或 `WithWorkDir("/path/to/workspace")` 选择 workspace context。

如果该 Codex CLI 环境配置了多个外部 skill 仓库，该 Agent 不会过滤它们；但它也不会根据配置合成 skill 事件。当前 Codex CLI 更偏向通过 shell 命令处理 skill，而不是专门的 skill 工具，因此映射到框架事件时通常是 `command_execution`，而不是 `skill_run`。

## 事件映射

该 Agent 会随着 Codex JSONL 到达实时发出 assistant、工具与错误事件，并在 Codex turn 完成后发出最终完成响应。Codex 的 `agent_message` 是完整消息 item，不是 token delta，但该 Agent 会把它暴露为 partial `chat.completion.chunk` segment，确保 session 持久化只保存最终 assistant response。最终完成响应使用最后一个 assistant message 的内容，并携带最终 usage 与 thread state；不发出中间 reasoning 事件。

| Codex JSONL 输出 | 框架事件 |
| --- | --- |
| `type == "thread.started"` | 持久化到 session state key `codex.StateKeyThreadID` |
| `item.type == "command_execution"` | tool-call 与 tool-result response 事件 |
| `item.type == "mcp_tool_call"` | tool-call 与 tool-result response 事件 |
| `web_search`、`file_change`、`image_view`、`image_generation` 等内置工具 item | tool-call 与 tool-result response 事件 |
| `type == "turn.failed"` 或 `type == "error"` | 不携带 `Response.Error` 的非终止 error observation chunk；命令结束后再发出一个终止 error |
| `item.type == "agent_message"` | partial assistant chunk 事件；最后一个 `agent_message` item 同时作为 final response 内容 |
| `type == "turn.completed"` | final response usage |

MCP 工具调用会尽量归一化为与 Claude Code 兼容的工具名：`mcp__<server>__<tool>`。
内置工具调用会保留 Codex 工具名。
当前 Codex CLI 的真实 skill 使用常作为 prompt context 注入，或通过 shell `command_execution` item 体现。该 Agent 会保留这些真实 item 类型，不会合成 `skill_run` 事件。

## 多轮会话

Codex 会自行创建 thread id。该 Agent 会把这个 id 存入 session state 的 `codex.StateKeyThreadID`，并在后续轮次使用：

1. 首轮：把 prompt 写入 `codex exec --json` 的 stdin
2. 后续轮次：把 prompt 写入 `codex exec resume --json <thread-id>` 的 stdin

如果 resume 在发出任何 transcript 事件前失败，该 Agent 会重新发起一次新的 `codex exec`；如果新执行返回了 thread id，则更新已保存的 thread id。如果 resume 已经发出过框架事件，或 stdout 解析失败，则不会再启动新的执行，以避免重复暴露进度或重复执行工具副作用，而是直接返回本次失败。如果 resume 与新建执行都失败，本次调用会返回 run error。

如需保持上下文，请在 `runner` 中持续使用相同的 app name、user ID、session ID。

## 原始日志落盘

使用 `WithRawOutputHook` 获取每次执行的 stdout/stderr，建议写入评估/观测产物目录中：

```go
ag, err := codex.New(
  codex.WithRawOutputHook(func(ctx context.Context, args *codex.RawOutputHookArgs) error {
    // Write args.Stdout / args.Stderr to your log storage.
    return nil
  }),
)
```

`RawOutputHookArgs` 包含框架侧 `SessionID` 与 Codex 侧 `ThreadID`，可用于按 session 聚合日志。

## 配置选项说明

| Option | 说明 |
| --- | --- |
| `WithName(name)` | 设置 Agent 名称。该值会用作事件 author。 |
| `WithBin(bin)` | 设置 CLI 可执行文件路径。默认值为 `codex`。 |
| `WithGlobalArgs(args...)` | 在 `exec` 子命令前追加 Codex 根级 flags。 |
| `WithExtraArgs(args...)` | 在可选 resume session id 前追加 `codex exec` flags。 |
| `WithEnv(env...)` | 追加 CLI 环境变量。格式为 `KEY=VALUE`。 |
| `WithWorkDir(dir)` | 设置 CLI 进程工作目录。 |
| `WithRawOutputHook(hook)` | 观测 raw stdout/stderr。回调会在 CLI 结束后、流式事件发出后调用；如果返回错误，会追加错误事件并跳过最终 assistant response。 |
