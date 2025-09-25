# AG-UI 使用指南

AG-UI（Agent UI）是 `trpc-agent-go` 提供的一套标准化交互层，用来把智能体（Agent）在运行过程中产生的结构化事件转换为前端可以直接消费的 Server-Sent Events（SSE）流。借助 AG-UI，我们可以在终端、网页或任意 SSE 客户端中实时观察 Agent 的思考过程、工具调用以及最终回复。

---

## 快速开始

### 环境准备

- Go 1.21 及以上
- Node.js 18+ 与 pnpm（仅在运行 CopilotKit Web 示例时需要）
- 可访问的 LLM / API Key，用于示例 Agent 运行

### 启动 AG-UI 服务端

```bash
cd support-agui/trpc-agent-go/examples/agui/server/default
GOOGLE_API_KEY=... go run .
```

服务默认监听 `http://127.0.0.1:8080/agui`，可通过 `--address` 与 `--path` 参数调整。

### 体验客户端

终端客户端（Bubble Tea）：

```bash
cd support-agui/trpc-agent-go/examples/agui/client/bubbletea
go run .
```

Web 客户端（CopilotKit）：

```bash
cd support-agui/trpc-agent-go/examples/agui/client/copilotkit
pnpm install
pnpm dev
```

浏览器访问 `http://localhost:3000`，即可与 Agent 交互。

---

## 核心概念

| 角色 | 说明 |
| ---- | ---- |
| Agent | 负责业务逻辑的智能体，实现 `agent.Agent` 接口（示例中使用 `llmagent`）。 |
| AG-UI Server | 通过 `agui.Server` 把 Agent 暴露为 SSE 服务，负责请求分发与事件回传。 |
| 客户端 | 任意支持 SSE 的终端或 Web 前端，用于展示和处理 Agent 事件。 |

AG-UI 接受前端发送的消息载荷，并把 Agent 在运行过程中产生的事件按顺序推送给前端，从而实现“请求一次，持续返回”的流式交互。

---

## 服务端架构与实现

### 创建 Server

位于 `support-agui/trpc-agent-go/server/agui/agui.go` 的 `agui.New` 是入口：

```go
agent := llmagent.New(
    "agui-agent",
    llmagent.WithTools([]tool.Tool{calculatorTool}),
    llmagent.WithModel(modelInstance),
    llmagent.WithGenerationConfig(generationConfig),
    llmagent.WithInstruction("You are a helpful assistant."),
)

server, err := agui.New(
    agent,
    agui.WithAddress("127.0.0.1:8080"),
    agui.WithPath("/agui"),
)
```

`agui.Server` 提供 `Serve(ctx)` 与 `Close(ctx)`，用于启动和关闭服务。可选配置包括：

- `WithAddress` / `WithPath`：调整监听地址与 URL 路径。
- `WithService`：替换底层 SSE 实现。
- `WithSessionService`：替换会话存储（默认使用内存实现）。
- `WithRunnerOptions`：向 Runner 透传自定义参数。

### SSE 处理流程

`server/agui/service/sse/sse.go` 是默认的服务实现：

1. 解析 POST 请求体（`RunAgentInput`），提取消息、工具定义等信息。
2. 调用 Runner 执行 Agent，获取事件通道。
3. 持续写入 SSE 响应，让前端实时接收事件。

通过自定义 `service.Service`，可以替换成 WebSocket、长轮询等其它协议。

---

## 客户端示例详解

### 1. Bubble Tea 终端客户端

- 路径：`examples/agui/client/bubbletea/`
- 使用 Go 与 Bubble Tea 框架，适合 CLI 调试。
- 通过 `github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/client/sse` 建立 SSE 连接。
- 终端实时输出文本增量、工具调用、错误等事件，能清楚看到 Agent 的思考过程。

### 2. CopilotKit Web 客户端

- 路径：`examples/agui/client/copilotkit/`
- 基于 Next.js + CopilotKit，提供全屏网页界面。
- 在 `app/api/copilotkit/route.ts` 注册 `new HttpAgent({ url: ... })`，CopilotKit runtime 会自动识别这是 AG-UI 协议。
- `app/page.tsx` 自定义 `RenderMessage`，对工具调用及结果进行卡片化展示，是建设观测面板、业务 UI 的良好起点。

---

## 请求与事件格式

### 请求载荷

客户端向 AG-UI 服务发送 JSON 结构：

```json
{
  "threadId": "demo-thread",
  "runId": "run-2024",
  "messages": [
    {"role": "user", "content": "请计算 2*(10+11)"}
  ],
  "tools": [
    {
      "name": "calculator",
      "description": "简单四则运算",
      "parameters": { ... }
    }
  ]
}
```

其中 `tools` 是可选项，用来让 Agent 知道可用工具及参数结构。

### 事件枚举

响应采用 SSE，每个事件包含 `type` 与 `data`。常见事件类型：

- `text-message.start` / `text-message.delta` / `text-message.end`
- `tool-call.start` / `tool-call.args` / `tool-call.result`
- `run.started` / `run.finished` / `run.error`

前端只需监听 SSE 流，根据事件类型更新 UI。

---

## 扩展与自定义

- **自定义 Agent**：实现 `agent.Agent` 接口即可。AG-UI 只负责包装输入输出，业务逻辑完全自定义。
- **扩展工具体系**：在 Agent 初始化时通过 `llmagent.WithTools` 等方法注入 `tool.Tool`，就能让前端看到工具调用过程。
- **自定义 Service/Session**：通过 Options 把自己的实现注入 `agui.Server`，支持多种协议或持久化。
- **接入任何前端**：只要能消费 SSE，就能与 AG-UI 通信。可以用 React、Vue、Flutter、甚至 IoT 设备。

---

## 常见问题 (FAQ)

**Q1：AG-UI 和 Agent 之间是什么关系？**

A：Agent 负责处理业务请求，AG-UI 负责把请求转发给 Agent 并把事件回流给前端。两者解耦，Agent 可独立开发复用。

**Q2：CopilotKit 如何识别 AG-UI 协议？**

A：只要在 CopilotKit runtime 中注册 `@ag-ui/client` 的 `HttpAgent`，运行时就会自动套用 AG-UI 协议适配逻辑（`constructAGUIRemoteAction`）。

**Q3：SSE 必须使用吗？**

A：默认是 SSE。若需要 WebSocket/轮询，可以自定义 `service.Service`，但需保证事件顺序与协议一致。

**Q4：如何部署到生产环境？**

A：建议：
- 使用反向代理或 API Gateway 提供 HTTPS 与鉴权。
- 将 Session/Runner 等组件替换为生产级实现（如 Redis 持久化、链路追踪等）。
- 在前端配置环境变量 `AG_UI_ENDPOINT`、`AG_UI_TOKEN` 等指向生产服务。

---

## 总结

AG-UI 让我们在 `trpc-agent-go` 里快速把 Agent 对外暴露为“流式对话 + 工具调用”界面：

- 仅需少量配置，即可启动支持 SSE 的服务端；
- 官方提供终端与 Web 示例，方便快速验证；
- 自定义能力强，可扩展 Agent、工具、协议、UI 表现；
- 支撑调试、观测、真实业务接入等多种场景。

欢迎在现有示例基础上继续扩展，打造适合自身业务的智能体交互体验。
