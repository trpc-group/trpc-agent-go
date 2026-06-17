# DebugLog 插件设计

## 目标

新增 `plugin/debuglog`，提供一个临时诊断插件，用于在不修改 Agent、Model、Tool 代码的前提下，把框架层关键入参和出参打印成单行 JSON debug 日志。

核心目标：

- 通过 `runner.WithPlugins(...)` 或单次 `plugin.WithPlugins(...)` 启用。
- 默认记录 agent/model/tool 的关键 callback 数据。
- 默认不记录 Runner event 和 model partial response，避免流式场景日志过多。
- 只观察，不修改 request、response、tool args、tool result 或 event。
- 通过框架 logger 输出：`log.DebugfContext(ctx, "%s", rawJSON)`。
- 能 JSON 序列化就按对象已有 JSON 契约输出；不能序列化就记录明确的错误和 Go 类型。

非目标：

- 不替代 `plugin.NewLogging()`。Logging 适合生命周期和耗时，DebugLog 适合排查 payload。
- 不做 OpenTelemetry、采样、持久化、上传、轮转。
- 不做 redact、truncate、字段白名单或 `%v` 兜底。DebugLog 可能包含 prompt、请求头、工具参数和工具结果，只应临时启用并配合受控日志落点。

## API

包路径：

```text
plugin/debuglog
```

文件：

```text
plugin/debuglog/plugin.go
plugin/debuglog/options.go
plugin/debuglog/entry.go
plugin/debuglog/snapshot.go
plugin/debuglog/plugin_test.go
```

公开接口：

```go
package debuglog

func New(opts ...Option) plugin.Plugin

type Option func(*options)

func WithName(name string) Option
func WithEventEnabled(enabled bool) Option
func WithModelPartialResponseEnabled(enabled bool) Option
```

默认值：

| 配置 | 默认值 | 说明 |
| --- | --- | --- |
| `name` | `"debug_log"` | 插件名。 |
| `event_enabled` | `false` | 是否记录 Runner event。 |
| `model_partial_response_enabled` | `false` | 是否记录 model partial response。 |

不提供 `WithBeforeModelEnabled`、`WithToolEnabled` 这类细粒度开关。model request/response 和 tool arguments/result 是这个插件的核心诊断面；如果不能接受这些内容进入 debug log，就不应该启用 DebugLog。

## 使用示例

Runner 级启用：

```go
import (
	"trpc.group/trpc-go/trpc-agent-go/plugin/debuglog"
	"trpc.group/trpc-go/trpc-agent-go/runner"
)

runnerInstance := runner.NewRunner(
	"my-app",
	agentInstance,
	runner.WithPlugins(debuglog.New()),
)
```

单次 Run 临时启用，并打开 event 和 model partial response：

```go
import (
	"trpc.group/trpc-go/trpc-agent-go/plugin"
	"trpc.group/trpc-go/trpc-agent-go/plugin/debuglog"
)

events, err := runnerInstance.Run(
	ctx,
	userID,
	sessionID,
	message,
	plugin.WithPlugins(
		debuglog.New(
			debuglog.WithEventEnabled(true),
			debuglog.WithModelPartialResponseEnabled(true),
		),
	),
)
```

输出示例：

```json
{"time":"2026-06-16T10:20:30.123456789+08:00","sequence":7,"plugin":"debug_log","phase":"before_tool","invocation_id":"inv-1","agent_name":"assistant","session_id":"sess-1","user_id":"user-1","tool_name":"calculator","tool_call_id":"call-1","payload":{"arguments":{"operation":"add","a":1,"b":2},"arguments_bytes":33}}
```

## 日志结构

每条日志是一条单行 JSON message：

不要在 JSON message 外额外拼 `debuglog:` 这类文本前缀，否则日志 message 本身就不再是合法 JSON。检索和过滤使用 JSON 内的 `plugin`、`phase`、`tool_name`、`invocation_id` 等字段。

```go
type entry struct {
	Time               time.Time      `json:"time"`
	Sequence           uint64         `json:"sequence"`
	Plugin             string         `json:"plugin"`
	Phase              string         `json:"phase"`
	InvocationID       string         `json:"invocation_id,omitempty"`
	ParentInvocationID string         `json:"parent_invocation_id,omitempty"`
	AgentName          string         `json:"agent_name,omitempty"`
	SessionID          string         `json:"session_id,omitempty"`
	UserID             string         `json:"user_id,omitempty"`
	ModelName          string         `json:"model_name,omitempty"`
	ToolName           string         `json:"tool_name,omitempty"`
	ToolCallID         string         `json:"tool_call_id,omitempty"`
	Error              string         `json:"error,omitempty"`
	Payload            map[string]any `json:"payload,omitempty"`
}
```

`phase` 取值：

```text
before_agent
after_agent
before_model
after_model
before_tool
after_tool
event
```

## Hook 行为

- `BeforeAgent` / `AfterAgent`：记录 invocation 元信息，不整体序列化 `agent.Invocation`。
- `BeforeModel`：记录 `model.Request`，并补充 `Tools`、`ExtraFields`、`Headers`。
- `AfterModel`：记录 `model.Request`、`model.Response` 和 error。`Response.IsPartial=true` 时默认跳过；打开 `WithModelPartialResponseEnabled(true)` 后记录。
- `BeforeTool`：记录 tool declaration、JSON arguments、resume value、resume map。
- `AfterTool`：记录 tool declaration、JSON arguments、result、meta 和 error。
- `OnEvent`：默认关闭；打开 `WithEventEnabled(true)` 后记录 `event.Event`，并补充 `StructuredOutput` 和 `ExecutionTrace`。

所有 hook 都应在常规路径返回 `nil, nil`。日志构造失败不应中断 Agent 执行，但失败原因必须体现在 payload 的错误字段里；如果整条 entry 无法 marshal，则通过 `log.WarnfContext` 记录一次。

## 序列化规则

DebugLog 只输出框架已经持有的对象，不猜 provider 原生 HTTP 请求体。

处理原则：

- `model.Request`、`model.Response`、`event.Event`、`tool.Declaration` 优先整体 `json.Marshal` 后再 `json.Unmarshal` 成 `any`，保持对象已有 JSON tag 和 `MarshalJSON` 行为。
- 不整体序列化 callback args。它们是 hook 容器，不是日志契约。
- 不整体序列化 `agent.Invocation`。它包含 agent、model、service、plugin manager、context、channel、mutex 等运行态对象，只提取稳定元信息。
- `json:"-"` 字段不通过反射绕过。确有诊断价值的字段作为 supplement 明确输出。
- tool arguments 是 JSON bytes，先按 JSON 解析；不能使用 Go 默认 `[]byte` JSON 编码，否则会变成 base64。
- 不使用 `fmt.Sprintf("%v", value)` 兜底。不可序列化时记录 `*_encode_error` 和 `*_type`。

关键 supplement：

| 来源 | 输出位置 |
| --- | --- |
| `model.Request.Tools` | `payload.request_supplement.tools` |
| `model.Request.ExtraFields` | `payload.request_supplement.extra_fields` |
| `model.Request.Headers` | `payload.request_supplement.headers` |
| `event.Event.StructuredOutput` | `payload.event_supplement.structured_output` |
| `event.Event.ExecutionTrace` | `payload.event_supplement.execution_trace` |

## 插件顺序

DebugLog 记录它所在 hook 顺序下已经可见的数据。

- 想看其他插件改写后的数据，把 mutating plugin 放在 `debuglog.New()` 前面。
- 想看原始数据，把 `debuglog.New()` 放在 mutating plugin 前面。

## 测试与验收

单测重点：

- 默认 name、nil option、`WithName`。
- `BeforeModel` / `AfterModel` 输出 request、response 和 request supplement。
- partial model response 默认跳过，`WithModelPartialResponseEnabled(true)` 后记录。
- `BeforeTool` / `AfterTool` 将合法 JSON arguments 解码为 JSON value，不输出 base64。
- 不可序列化的 result、meta、extra field 记录 Go 类型和 encode error。
- event 默认不记录，`WithEventEnabled(true)` 后记录并不替换 event。
- debug message 是合法单行 JSON。

验证命令：

```sh
go test ./plugin/...
```

验收标准：

- 不改 Agent 或 Tool 代码即可启用。
- 默认包含 model request/response 和 tool arguments/result。
- event 和 model partial response 默认关闭。
- 可序列化值按原 JSON 契约输出。
- 不可序列化值记录明确错误，不使用模糊兜底。
- 插件不改变原有 Agent 执行结果。
