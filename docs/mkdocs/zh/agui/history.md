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

实时对话通常会产生大量流式事件。如果每个事件都立即写入 `SessionService`，在长文本、reasoning 内容或工具参数流式输出较多时，会给 MySQL、Redis 等会话存储带来较高写入压力。框架默认会先对部分流式事件做事件聚合，再按刷新间隔写入会话存储。

在一次刷新间隔内，框架会聚合同一条助手消息的文本内容事件、reasoning 内容事件，以及同一个工具调用的参数事件。其他事件仍按原始顺序保存；遇到这些事件时，前后的流式事件不会跨事件边界合并。工具结果 `TOOL_CALL_RESULT` 表示一次工具调用的完整结果，也不会被合并。

实时对话的 SSE 输出不会等待历史事件写入完成，前端仍会即时收到模型输出。`/history` 读取的是已经成功写入会话存储的内容，因此在对话仍在运行时，请求消息快照可能只能看到上一次刷新成功时的状态。开启消息快照续传后，服务端会持续追踪历史事件，后续刷新成功的事件会继续推送给客户端。

相关配置如下：

- `aggregator.WithEnabled(true)` 用于控制是否启用事件聚合，默认开启。
- `agui.WithFlushInterval(time.Second)` 用于控制历史事件写入会话存储的刷新间隔，默认 `1s`。设置为 `0` 表示不进行运行中的定时写入；这种配置通常适合不需要在运行中通过 `/history` 续传事件的场景，历史事件主要会在运行结束收尾时写入。运行时间较长或事件量较大时，未写入的历史事件会持续占用进程内存，直到运行结束收尾。
- `agui.WithTrackPersistenceTimeout(5*time.Second)` 用于限制单次历史事件写入会话存储的最长等待时间，默认 `5s`。设置为 `0` 表示不设置超时。
- `agui.WithPostRunFinalizationTimeout(5*time.Second)` 用于限制运行结束后收尾流程的最长执行时间，默认 `5s`。收尾流程会补齐仍然打开的协议结束事件，并尽量把剩余历史事件写入 `SessionService`。如果最终写入失败，错误会被记录，同时释放已结束 run 的进程内 tracker 状态；尚未写入的剩余事件会被丢弃，而不会无限期滞留。如果会话存储变慢或异常，超时可以避免请求长时间阻塞。设置为 `0` 表示不设置超时。

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

大多数场景不需要自定义聚合策略。如果确实需要改变哪些事件可以被合并，可以实现 `aggregator.Aggregator` 并通过自定义工厂注入。自定义聚合器需要能够处理并发调用。

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

开启后，实时对话请求在持久化用户输入事件时，会把 AG-UI 请求体中的 `forwardedProps` 字段写入该用户输入事件的 `rawEvent.forwardedProps`；在 Go API 中，该字段对应 `RunAgentInput.ForwardedProps`。读取历史时，消息快照路由会把它聚合到 `MESSAGES_SNAPSHOT.rawEvent.runs[runId].forwardedProps`：

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

对于已启用历史追踪、消息快照续传且刷新间隔为正的运行，Runner 会在 `Run` 返回前向共享的 `SessionService` 写入一个很小的“最新运行”标记。当缓冲的 track 事件尚不可见时，其他实例可通过该标记区分“新运行已开始”与“历史为空或末尾仍是上一轮终态”。标记不会在运行结束时立即清除，而是由下一轮运行覆盖；当同一 run 的终态事件已经持久化时，该标记即视为已完成。设置了执行截止时间的 run 会使用有界租期，避免终态事件永久缺失时一直被误判为活跃；已知最终写入失败时也会将本轮标记为已完成。标记通过 session state 读写，不经过 track 事件的异步持久化队列，因此异步 track 写入不会丢失活跃运行信号。如果标记写入失败，`Run` 会返回错误，避免在无法保证跨实例续传可见性时启动运行。与 Runner 现有的本地运行注册表一致，不支持同一个 session key 同时启动多个 run。

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
