# 消息快照路由

## 核心概念

消息快照路由用于在页面初始化、刷新或断线重连后恢复历史对话。它不会触发新的 Agent 运行，而是从会话存储中读取已持久化的 AG-UI 事件，并还原为 `MESSAGES_SNAPSHOT` 事件。

该路由默认是 `/history`，可通过 `agui.WithMessagesSnapshotPath` 自定义。如果需要统一路由前缀，可参考 [路由前缀](index.md#路由前缀)。请求成功时，服务端会返回 `RUN_STARTED → MESSAGES_SNAPSHOT → RUN_FINISHED` 事件流。`MESSAGES_SNAPSHOT` 的协议字段可参考 [AG-UI MessagesSnapshot](https://docs.ag-ui.com/concepts/events#messagessnapshot)。

消息快照与实时对话使用同一套会话定位规则，框架会通过 `AppName`、`UserID` 和 `threadId` 定位会话。该路由支持与实时对话路由并发访问，因此页面可以在实时对话运行期间读取同一会话的快照。

## 开启消息快照

开启消息快照需要启用快照路由，并为服务端配置可读取历史事件的 `session.Service`。同时需要配置默认 `AppName`，用于和 `UserID`、`threadId` 一起定位会话。

最小配置包括：

- `agui.WithMessagesSnapshotEnabled(true)` 用于启用消息快照路由。
- `agui.WithAppName(name)` 用于设置默认 `AppName`。
- `agui.WithSessionService(service)` 用于注入会话存储。

代码示例如下：

```go
import (
	"trpc.group/trpc-go/trpc-agent-go/server/agui"
	"trpc.group/trpc-go/trpc-agent-go/session/inmemory"
)

sessionService := inmemory.NewSessionService()
server, err := agui.New(
    runner,
    agui.WithAppName("demo-app"),
    agui.WithSessionService(sessionService),
    agui.WithMessagesSnapshotEnabled(true),
)
```

如果需要修改默认路由，可以配置 `agui.WithMessagesSnapshotPath(path)`；如果需要按请求解析用户或应用标识，可以配置 [`aguirunner.WithUserIDResolver(resolver)`](chat.md#自定义-useridresolver) 或 [`agui.WithAppNameResolver(resolver)`](chat.md#自定义-appnameresolver)。

```go
import (
	"trpc.group/trpc-go/trpc-agent-go/server/agui"
	"trpc.group/trpc-go/trpc-agent-go/server/agui/adapter"
	aguirunner "trpc.group/trpc-go/trpc-agent-go/server/agui/runner"
)

userIDResolver := func(ctx context.Context, input *adapter.RunAgentInput) (string, error) {
    forwardedProps, ok := input.ForwardedProps.(map[string]any)
    if !ok {
        return "anonymous", nil
    }
    userID, ok := forwardedProps["userId"].(string)
    if !ok || userID == "" {
        return "anonymous", nil
    }
    return userID, nil
}

server, err := agui.New(
    runner,
    agui.WithAppName("demo-app"),
    agui.WithSessionService(sessionService),
    agui.WithMessagesSnapshotEnabled(true),
    agui.WithMessagesSnapshotPath("/history"),
    agui.WithAGUIRunnerOptions(
        aguirunner.WithUserIDResolver(userIDResolver),
    ),
)
```

请求消息快照时，传入与实时对话相同的 `threadId`，以及用户或应用标识解析所需的字段：

```bash
curl -N -X POST http://localhost:8080/history \
  -H 'Content-Type: application/json' \
  -d '{
    "threadId": "thread-id",
    "runId": "snapshot-run-id",
    "forwardedProps": {
      "userId": "alice"
    }
  }'
```

完整的示例可参考 [examples/agui/messagessnapshot](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/agui/messagessnapshot)。

## Session 存储与事件聚合

`SessionService` 是消息快照的数据来源。实时对话路由产生的 AG-UI 事件会写入会话存储，消息快照路由再从同一个 `SessionService` 中读取已持久化的 AG-UI 事件并还原历史消息。

多实例部署时，不同实例需要共享同一个 `SessionService`，否则消息快照路由无法读取其他实例写入的历史事件。

实时对话通常会产生大量流式事件。如果每个事件都立即写入 `SessionService`，在长文本、reasoning 内容或工具参数流式输出较多时，会给 MySQL、Redis 等会话存储带来较高写入压力。框架默认会先将历史事件加入队列，再批量写入会话存储。

默认聚合器只聚合同一条助手消息中相邻的文本内容事件、同一条助手消息中相邻的 reasoning 内容事件，以及同一个工具调用中相邻的参数事件。消息或工具调用 ID 变化、内容类型变化，或者出现任一非内容事件时，都会结束当前聚合。`TOOL_CALL_RESULT` 表示一次工具调用的完整结果，不会被聚合。聚合器输出仍由历史 tracker 排队，只会在启动、定时或最终刷新时写入。

对于所有已启用历史追踪且刷新间隔为正的 run，Runner 都会在记录初始 `RUN_STARTED` 后、发布首个 SSE 事件之前执行一次启动尽力刷新。后续 SSE 发送不会等待历史持久化完成，前端仍会即时收到模型输出。`/history` 只读取已经成功写入会话存储的内容，因此在对话仍在运行时，消息快照可能只能看到上一次刷新成功时的状态。开启消息快照续传后，后续刷新成功的事件会继续推送给客户端。

相关配置如下：

- `aggregator.WithEnabled(true)` 用于控制是否聚合相邻的流式内容事件，默认开启。
- `agui.WithFlushInterval(time.Second)` 用于控制启动刷新和运行中的定时刷新，默认 `1s`。刷新间隔为正时，Runner 会在首个 SSE 事件前执行一次启动尽力刷新，并在 run 活动期间定时写入。设置为 `0` 会同时关闭启动刷新和定时刷新；历史事件随后主要在运行结束收尾时写入。运行时间较长或事件量较大时，未写入的历史事件会持续占用进程内存，直到运行结束收尾。
- `agui.WithTrackPersistenceTimeout(5*time.Second)` 用于限制每次 AG-UI 历史持久化操作等待会话存储的最长时间，包括启动尽力刷新、运行中的定时刷新以及最终 `Close` 刷新，默认 `5s`。运行中的存储写入失败时会返回错误，并丢弃本次已取出的 batch，而不会自动重试；写入期间新进入队列的事件仍可由后续刷新处理。如果最终 `Close` 失败或超时，错误会被记录，同时释放已结束 run 的进程内 tracker 状态；尚未写入的剩余事件会被丢弃。设置为 `0` 表示不设置超时。
- `agui.WithPostRunFinalizationTimeout(5*time.Second)` 用于设置运行结束后生成和发送协议收尾事件时使用的超时，默认 `5s`。它不限制最终历史 `Flush` 或 `Close`；这些操作使用 `agui.WithTrackPersistenceTimeout`。设置为 `0` 表示不设置超时。

```go
import (
	"trpc.group/trpc-go/trpc-agent-go/server/agui"
	"trpc.group/trpc-go/trpc-agent-go/server/agui/aggregator"
	aguirunner "trpc.group/trpc-go/trpc-agent-go/server/agui/runner"
	"trpc.group/trpc-go/trpc-agent-go/session/inmemory"
)

sessionService := inmemory.NewSessionService()

server, err := agui.New(
    runner,
    agui.WithAppName(appName),
    agui.WithSessionService(sessionService),
    agui.WithMessagesSnapshotEnabled(true),
    agui.WithFlushInterval(time.Second),
    agui.WithTrackPersistenceTimeout(5*time.Second),
    agui.WithPostRunFinalizationTimeout(5*time.Second),
    agui.WithAGUIRunnerOptions(
        aguirunner.WithAggregationOption(aggregator.WithEnabled(true)),
    ),
)
```

大多数场景不需要自定义聚合策略。如果确实需要改变哪些事件可以被合并，可以实现 `aggregator.Aggregator` 并通过自定义工厂注入。例如，自定义实现可以先缓冲多个 `CUSTOM` 事件，再由 `Flush` 返回一个合并后的事件。`Append` 的输入只可在本次调用期间借用；实现如果需要在返回后继续保留其中的数据，必须自行复制。历史 tracker 会立即对返回事件生成快照，并将其加入下一次持久化刷新队列。自定义聚合器需要能够处理并发调用。

## 历史运行生命周期事件

消息快照路由本身会返回 `RUN_STARTED → MESSAGES_SNAPSHOT → RUN_FINISHED`。这些 `RUN_*` 只表示本次 `/history` 请求的开始与结束；如果读取历史失败，则返回 `RUN_ERROR`。

默认情况下，`MESSAGES_SNAPSHOT.messages` 不包含历史对话中的 `RUN_STARTED`、`RUN_FINISHED`、`RUN_ERROR`。

如果前端需要在历史消息中展示每次运行的开始、结束或错误状态，可以开启 `agui.WithMessagesSnapshotRunLifecycleEventsEnabled(true)`：

```go
server, err := agui.New(
    runner,
    agui.WithAppName(appName),
    agui.WithSessionService(sessionService),
    agui.WithMessagesSnapshotEnabled(true),
    agui.WithMessagesSnapshotRunLifecycleEventsEnabled(true),
)
```

开启后，历史对话中已持久化的 `RUN_STARTED`、`RUN_FINISHED`、`RUN_ERROR` 会作为 `role=activity` 的消息写入 `MESSAGES_SNAPSHOT.messages`，用于展示历史运行状态。

`MESSAGES_SNAPSHOT` 中的历史 `RUN_*` 消息形态如下：

```json
{
  "type": "MESSAGES_SNAPSHOT",
  "messages": [
    {
      "id": "event-id-1",
      "role": "activity",
      "activityType": "RUN_STARTED",
      "content": {
        "threadId": "thread-1",
        "runId": "run-1"
      }
    },
    {
      "id": "event-id-2",
      "role": "assistant",
      "content": "hello"
    },
    {
      "id": "event-id-3",
      "role": "activity",
      "activityType": "RUN_ERROR",
      "content": {
        "runId": "run-1",
        "message": "model call failed",
        "code": "MODEL_ERROR"
      }
    }
  ]
}
```

## 用户输入 forwardedProps 元数据

如果业务在 AG-UI 请求的 `forwardedProps` 中携带附件、表单上下文或其他请求侧信息，并希望刷新页面后仍能通过历史接口恢复这些信息，可以开启事件来源元数据：

```go
import (
	"trpc.group/trpc-go/trpc-agent-go/server/agui"
)

server, err := agui.New(
    runner,
    agui.WithAppName(appName),
    agui.WithSessionService(sessionService),
    agui.WithMessagesSnapshotEnabled(true),
    agui.WithEventSourceMetadataEnabled(true),
)
```

开启后，实时对话请求在持久化用户输入事件时，会把 AG-UI 请求体中的 `forwardedProps` 字段写入该用户输入事件的 `rawEvent.forwardedProps`；在 Go API 中，该字段对应 `RunAgentInput.ForwardedProps`。读取历史时，消息快照路由会把它聚合到 `MESSAGES_SNAPSHOT.rawEvent.runs[runId].forwardedProps`，并在可关联到运行的消息元数据中写入 `runId`：

```json
{
  "type": "MESSAGES_SNAPSHOT",
  "messages": [
    {
      "id": "user-1",
      "role": "user",
      "content": "请看附件"
    }
  ],
  "rawEvent": {
    "runs": {
      "run-1": {
        "author": "demo-user",
        "forwardedProps": {
          "file_url": "https://example.com/demo.png",
          "attachments": [
            {
              "id": "file-1",
              "mimeType": "image/png"
            }
          ]
        },
        "timestamp": 1781258400000
      }
    },
    "messages": {
      "user-1": {
        "author": "demo-user",
        "runId": "run-1",
        "timestamp": 1781258400000
      }
    }
  }
}
```

## 消息快照续传

默认情况下，消息快照路由只返回一次性快照并立即结束连接。当用户在实时对话运行期间刷新或重连时，快照生成之后可能还有新的 AG-UI 事件继续产生。此时可以开启消息快照续传，让同一条 SSE 连接在返回快照后继续接收后续事件。

开启续传后，服务端会在发送 `MESSAGES_SNAPSHOT` 后继续读取并转发后续 AG-UI 事件，直到读到 `RUN_FINISHED` 或 `RUN_ERROR`。返回序列变为：

`RUN_STARTED → MESSAGES_SNAPSHOT → 后续 AG-UI 事件 → RUN_FINISHED/RUN_ERROR`

所有已启用历史追踪且刷新间隔为正的 run，都会在记录初始 `RUN_STARTED` 事件后、将其发布到实时 SSE 流之前执行一次启动尽力刷新。启动刷新成功后，Runner 会先完成 session 初始化，再启动定时写入、RunHook 和被包装的 Runner。开启消息快照续传且同步 `TrackService` 刷新成功时，其他共享同一个 `SessionService` 的实例可在 `RUN_STARTED` 已发出后观察到非终态事件；后续事件继续按周期缓冲写入。如果启动刷新失败，错误会被记录但不会阻止 run 继续执行，本次已取出的 batch 也不会重试。为避免首次 session 创建竞态，定时刷新此时只会在被包装的 Runner 完成同步初始化后启动；期间记录的新事件仍保留在队列中，可由之后的定时刷新或最终关闭处理。异步 `TrackService` 在刷新调用返回后仍可能延迟跨实例可见性。

相关配置如下：

- `agui.WithMessagesSnapshotFollowEnabled(true)` 用于启用消息快照续传。
- `agui.WithMessagesSnapshotFollowMaxDuration(time.Duration)` 用于限制续传最长时间，避免一直等待正在运行的对话结束。
- `agui.WithFlushInterval(time.Duration)` 用于控制历史事件写入会话存储的频率，续传轮询间隔会复用该值。

代码示例如下。

```go
import (
	"trpc.group/trpc-go/trpc-agent-go/server/agui"
	"trpc.group/trpc-go/trpc-agent-go/session/inmemory"
)

sessionService := inmemory.NewSessionService()

server, err := agui.New(
    runner,
    agui.WithAppName(appName),
    agui.WithSessionService(sessionService),
    agui.WithMessagesSnapshotEnabled(true),
    agui.WithMessagesSnapshotFollowEnabled(true),
    agui.WithMessagesSnapshotFollowMaxDuration(30*time.Second),
    agui.WithFlushInterval(50*time.Millisecond),
)
```

完整示例可参考 [examples/agui/server/follow](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/agui/server/follow)，前端可参考 [examples/agui/client/tdesign-chat](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/agui/client/tdesign-chat)。

## 分页消息快照

默认情况下，`/history` 会一次性还原当前会话中全部已持久化的 AG-UI 历史。对于较长的对话，这会增加会话存储读取、服务端事件还原以及网络传输的成本。此时可以配置 `agui.WithMessagesSnapshotSessionPageResolver`，让每次消息快照请求只读取一页已持久化事件。

分页能力由 session 层提供。AG-UI 服务端不会额外定义顶层 cursor 字段，也不约定固定的请求参数名称；resolver 会拿到原始 `RunAgentInput` 和已经解析出的 `session.Key`，再返回一次 session 分页请求。业务可以自行决定 cursor 和 limit 的来源，例如从 `forwardedProps`、网关映射后的请求元数据，或其它业务输入中读取。

resolver 返回 `*aguirunner.MessagesSnapshotPageRequest`。`Cursor` 是上一页返回的 opaque cursor，空 cursor 表示读取最新一页。`EventLimit` 表示从会话存储中读取的 AG-UI track event 数量；它限制的是事件数，不是消息数或对话轮数，因为一条最终展示的消息可能由多条已持久化 AG-UI 事件还原而来。

只有配置的 session service 实现了 `session.TrackEventPageService` 时，消息快照路由才会使用分页读取。若当前 session service 不支持该能力，`/history` 会保持原有的全量快照行为。

```go
import (
	"trpc.group/trpc-go/trpc-agent-go/server/agui"
	"trpc.group/trpc-go/trpc-agent-go/server/agui/adapter"
	aguirunner "trpc.group/trpc-go/trpc-agent-go/server/agui/runner"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

server, err := agui.New(
    runner,
    agui.WithAppName("demo-app"),
    agui.WithSessionService(sessionService),
    agui.WithMessagesSnapshotEnabled(true),
    agui.WithMessagesSnapshotSessionPageResolver(
        func(ctx context.Context, input *adapter.RunAgentInput, key session.Key) (*aguirunner.MessagesSnapshotPageRequest, error) {
            forwardedProps, _ := input.ForwardedProps.(map[string]any)
            cursor, _ := forwardedProps["cursor"].(string)
            return &aguirunner.MessagesSnapshotPageRequest{
                Cursor:     cursor,
                EventLimit: 200,
            }, nil
        },
    ),
)
```

分页快照的响应仍然使用标准的 `MESSAGES_SNAPSHOT` 事件。分页信息会写入 `MESSAGES_SNAPSHOT.rawEvent.page`：

```json
{
  "type": "MESSAGES_SNAPSHOT",
  "messages": [
    {
      "id": "user-1",
      "role": "user",
      "content": "你好"
    },
    {
      "id": "assistant-1",
      "role": "assistant",
      "content": "你好，有什么可以帮你？"
    }
  ],
  "rawEvent": {
    "page": {
      "cursor": "opaque-cursor-for-the-page-boundary",
      "hasMore": true
    }
  }
}
```

下一次请求更早历史时，客户端应把返回的 `cursor` 原样传回 resolver 所读取的位置。cursor 是 opaque token，业务代码不应解析、比较或自行构造。

`hasMore=true` 表示客户端还没有收到全部更早历史。原因可能是 session 存储中仍有更早事件，也可能是 AG-UI 层在返回前裁剪了当前页。这个裁剪是有意的：消息快照会从 user message 边界开始返回，避免前端收到上一轮对话的后半段。

如果本次读取到的事件页中找不到 user message 边界，`/history` 会返回空 `messages`，并在 `rawEvent.page.cursor` 中保留本次请求传入的 cursor，同时设置 `hasMore=true`。客户端可以使用同一个 cursor，并调大 `EventLimit` 后重试，从 session 层请求更大的事件窗口。
