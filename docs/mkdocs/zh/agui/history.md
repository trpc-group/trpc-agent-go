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

流式响应通常会产生多条增量文本事件或 reasoning 事件。为减少会话存储压力，框架默认会先聚合连续且具有相同 `messageId` 的 `TEXT_MESSAGE_CONTENT` 与 `REASONING_MESSAGE_CONTENT` 事件，再写入会话存储。

聚合结果默认每秒刷新一次。运行正常结束、被取消或发生错误时，框架还会执行运行结束后的收尾流程，用于补发仍然打开的协议流结束事件，并将聚合缓存尽量写入会话存储。

相关配置如下：

- `aggregator.WithEnabled(true)` 用于控制是否开启事件聚合，默认开启。
- `agui.WithFlushInterval(time.Second)` 用于控制聚合结果的定时刷新间隔，默认 `1s`。设置为 `0` 表示不开启定时刷新。
- `agui.WithPostRunFinalizationTimeout(5*time.Second)` 用于限制运行结束后收尾流程的最长执行时间，默认 `5s`。收尾流程需要补齐协议结束事件，并将聚合缓存写入 `SessionService`；如果会话存储变慢或异常，超时可以避免请求长时间阻塞。设置为 `0` 表示不设置超时事件。

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
    agui.WithPostRunFinalizationTimeout(5*time.Second),
    agui.WithAGUIRunnerOptions(
        aguirunner.WithAggregationOption(aggregator.WithEnabled(true)),
    ),
)
```

如果需要更复杂的聚合策略，可以实现 `aggregator.Aggregator` 并通过自定义工厂注入。需要注意的是，虽然每个会话都会单独创建一个聚合器，省去了跨会话的状态维护和并发处理，但聚合方法本身仍有可能被并发调用，因此仍需妥善处理并发。

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

## 消息快照续传

默认情况下，消息快照路由只返回一次性快照并立即结束连接。当用户在实时对话运行期间刷新或重连时，快照生成之后可能还有新的 AG-UI 事件继续产生。此时可以开启消息快照续传，让同一条 SSE 连接在返回快照后继续接收后续事件。

开启续传后，服务端会在发送 `MESSAGES_SNAPSHOT` 后继续读取并转发后续 AG-UI 事件，直到读到 `RUN_FINISHED` 或 `RUN_ERROR`。返回序列变为：

`RUN_STARTED → MESSAGES_SNAPSHOT → 后续 AG-UI 事件 → RUN_FINISHED/RUN_ERROR`

相关配置如下：

- `agui.WithMessagesSnapshotFollowEnabled(true)` 用于启用消息快照续传。
- `agui.WithMessagesSnapshotFollowMaxDuration(time.Duration)` 用于限制续传最长时间，避免一直等待正在运行的对话结束。
- `agui.WithFlushInterval(time.Duration)` 用于控制历史事件落库频率，续传轮询间隔会复用该值。

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
