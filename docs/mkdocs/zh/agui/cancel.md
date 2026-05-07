# 取消路由

## 核心概念

取消路由用于主动停止实时对话路由中正在运行的后端任务。该路由默认关闭，可通过 `agui.WithCancelEnabled(true)` 开启；默认路径为 `/cancel`，可通过 `agui.WithCancelPath(path)` 修改。如果需要统一路由前缀，可参考 [路由前缀](index.md#路由前缀)。

取消时，框架会使用 `AppName`、`UserID` 和 `threadId` 组成同一个 `SessionKey`，并取消该会话下正在运行的后端任务。因此，取消请求需要与实时对话请求解析出一致的 `AppName`、`UserID` 和 `threadId`。

多实例部署时，取消请求需要打到启动该实时对话请求的同一个实例。正在运行的任务只保存在实例本地，单独共享 `SessionService` 不能让其他实例取消该任务；如果请求打到其他实例，通常会返回 `404 Not Found`。

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

- `200 OK`：取消成功
- `404 Not Found`：没有找到对应 `SessionKey` 的运行中任务（可能已结束，或标识不匹配）

取消成功后，框架仍会执行必要的运行结束收尾，补齐协议结束事件，并将聚合缓存尽量写入 `SessionService`。因此，后续通过 `/history` 读取历史时，拿到的是取消后的合法一致状态，而不是一段未收尾的中间状态。收尾流程和超时配置可参考 [Session 存储与事件聚合](history.md#session-存储与事件聚合)。
