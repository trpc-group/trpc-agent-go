# 取消路由

## 核心概念

取消路由用于主动停止实时对话路由中正在运行的后端任务。该路由默认关闭，可通过 `agui.WithCancelEnabled(true)` 开启；默认路径为 `/cancel`，可通过 `agui.WithCancelPath(path)` 修改。如果需要统一路由前缀，可参考 [路由前缀](index.md#路由前缀)。

取消时，框架会使用 `AppName`、`UserID` 和 `threadId` 组成同一个 `SessionKey`，并取消该会话下正在运行的后端任务。因此，取消请求需要与实时对话请求解析出一致的 `AppName`、`UserID` 和 `threadId`。

默认情况下，多实例部署时，取消请求需要打到启动该实时对话请求的同一个实例。正在运行的任务只保存在实例本地；如果请求打到其他实例，通常会返回 `404 Not Found`。

取消路由通常用于：

- 前端有“停止生成”按钮，需要中断后端执行。
- SSE 连接断开后，希望停止后端执行，避免白白消耗模型/工具资源
- 服务端需要做时间或成本控制，及时中断异常运行。

```go
import "trpc.group/trpc-go/trpc-agent-go/server/agui"

server, err := agui.New(
    runner,
    agui.WithCancelEnabled(true),
    agui.WithCancelPath("/cancel"),
)
```

## 取消请求

取消路由使用 `RunAgentInput` 作为请求体。通常需要传入 `threadId`，以及 `UserIDResolver` 或 `AppNameResolver` 解析会话所需的字段。`runId` 可以随请求传入用于调用方关联，但取消定位依赖的是 `SessionKey`。

请求体示例：

```json
{
  "threadId": "thread-id",
  "runId": "run-id",
  "forwardedProps": {
    "userId": "alice"
  }
}
```

对应的 `curl` 示例：

```bash
curl -X POST http://localhost:8080/cancel \
  -H 'Content-Type: application/json' \
  -d '{
    "threadId": "thread-id",
    "runId": "run-id",
    "forwardedProps": {
      "userId": "alice"
    }
  }'
```

典型返回：

- `200 OK`：已触发本机取消，或在开启分布式取消时已投递远程取消信号
- `404 Not Found`：没有找到对应 `SessionKey` 的运行中任务（可能已结束，或标识不匹配）

取消成功后，框架仍会执行必要的运行结束收尾，补齐协议结束事件，并将聚合缓存尽量写入 `SessionService`。取消请求返回成功不表示同一个 `SessionKey` 已经可以立即发起新的实时对话请求；如果需要继续发起下一次运行，应等待原实时对话流返回终态事件。因此，后续通过 `/history` 读取历史时，拿到的是取消后的合法一致状态，而不是一段未收尾的中间状态。收尾流程和超时配置可参考 [Session 存储与事件聚合](history.md#session-存储与事件聚合)。

## 多实例分布式取消

如果多实例部署中无法保证同一个 `SessionKey` 的实时对话请求和取消请求落到同一个实例，可以开启分布式取消：

```go
import "trpc.group/trpc-go/trpc-agent-go/server/agui"

server, err := agui.New(
    runner,
    agui.WithSessionService(sessionService),
    agui.WithCancelEnabled(true),
    agui.WithDistributedCancelEnabled(true),
    agui.WithDistributedCancelPollInterval(time.Second),
)
```

开启后，参与同一组请求的 AG-UI 实例需要配置同一个共享 `SessionService`。取消请求命中未持有该本地运行的实例时，框架会通过共享 `SessionService` 投递取消信号，由持有该运行的实例触发本机取消。

分布式取消仍按 `AppName`、`UserID` 和 `threadId` 组成的 `SessionKey` 定位运行任务。远程取消返回成功只表示取消信号已投递，不表示持有该运行的实例已经完成取消；如果需要继续发起下一次运行，应等待原实时对话流返回终态事件。

分布式取消依赖共享 `SessionService` 传递取消信号；如果共享 `SessionService` 读写失败，跨实例取消可能无法生效或返回错误。

持有运行的实例会按轮询间隔检查取消信号，默认间隔为 `1s`，可通过 `agui.WithDistributedCancelPollInterval(d)` 调整。较短的间隔通常能降低远程取消延迟，但会增加对 `SessionService` 的读取次数。
