# tRPC-Agent API 服务

`server/trpcagent` 用于把本地 `tRPC-Agent-Go` Agent 暴露为 tRPC-Agent HTTP API。服务端可以导出 Agent 结构，也可以把请求转交给 `runner.Runner` 执行，并以统一的运行响应返回事件、消息和执行轨迹。

它适合用在需要把 Agent 服务化的场景：Agent 仍然运行在当前进程内，但外部系统可以通过 HTTP 获取结构信息并发起一次运行。

## 快速上手

创建 Agent 和 Runner 后，将它们注册到 `trpcagent.Server`：

```go
import (
    "net/http"

    "trpc.group/trpc-go/trpc-agent-go/log"
    "trpc.group/trpc-go/trpc-agent-go/runner"
    "trpc.group/trpc-go/trpc-agent-go/server/trpcagent"
)

agent := newAgent()
agentRunner := runner.NewRunner("calculator", agent)
defer agentRunner.Close()

server, err := trpcagent.New(
    trpcagent.WithAppName("calculator"),
    trpcagent.WithAgent(agent),
    trpcagent.WithRunner(agentRunner),
)
if err != nil {
    log.Fatalf("create trpc-agent api server failed: %v", err)
}

if err := http.ListenAndServe("127.0.0.1:8080", server.Handler()); err != nil {
    log.Fatalf("server stopped with error: %v", err)
}
```

完整示例参见 [examples/trpcagent/server](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/trpcagent/server)。

## 路由

默认 base path 为 `/trpc-agent/v1/apps`。当 app name 为 `calculator` 时，服务端暴露以下路由：

- `GET /trpc-agent/v1/apps/calculator/structure`：导出当前 Agent 的结构信息。
- `POST /trpc-agent/v1/apps/calculator/runs`：执行一次 Agent 运行并返回运行结果。

如果只配置 `WithAgent`，服务端只注册 structure 路由。如果只配置 `WithRunner`，服务端只注册 runs 路由。这样可以让接入方按自己的部署边界决定是否同时暴露结构与执行能力。

## Option

`WithAppName` 设置当前服务暴露的 app name。该值会进入路由路径，也是运行时写入 run options 的 app name。

`WithAgent` 设置用于导出结构的根 Agent。structure 路由会基于该 Agent 生成结构快照。

`WithRunner` 设置用于执行请求的 Runner。runs 路由会将请求中的 session、input、profile 和 run options 转换为一次 `runner.Run()` 调用。

`WithBasePath` 设置 API 路由前缀，默认值为 `/trpc-agent/v1/apps`。如果服务挂在已有 HTTP 服务下，可以通过它调整路由前缀。

`WithTimeout` 设置单次 HTTP 请求的超时时间。超时会通过 request context 传递到结构导出和 Runner 执行链路。

## 请求示例

导出结构：

```bash
curl -sS http://127.0.0.1:8080/trpc-agent/v1/apps/calculator/structure
```

执行一次运行：

```bash
curl -sS http://127.0.0.1:8080/trpc-agent/v1/apps/calculator/runs \
  -H 'Content-Type: application/json' \
  -d '{
    "session": {
      "userId": "alice",
      "sessionId": "demo-session"
    },
    "input": {
      "role": "user",
      "content": "Use the calculator to compute 12 * 7."
    },
    "runOptions": {
      "requestID": "demo-run-1",
      "executionTraceEnabled": true
    }
  }'
```

`profile` 字段可选。未传入 profile 时，Runner 按 Agent 当前配置执行；传入 profile 时，服务端会将结构化 profile 编译为运行时 option 后再执行。
