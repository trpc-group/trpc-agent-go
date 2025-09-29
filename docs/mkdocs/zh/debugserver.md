# Debug Server 使用指南

## 概述

Debug Server 是 trpc-agent-go 框架提供的一个调试工具。 
它可以帮助开发者快速测试和调试 Agent 功能。
它可以和 [ADK Web UI](https://github.com/google/adk-web) 结合，从而允许你通过可视化的交互界面来验证 Agent 的行为和工具调用。

## 主要功能

- **可视化调试界面**：通过 ADK Web UI 提供友好的图形界面
- **实时交互测试**：支持与 Agent 进行实时对话和工具调用
- **流式响应**：支持 Server-Sent Events (SSE) 流式响应
- **会话管理**：支持创建和管理多个对话会话
- **工具验证**：可以直观地测试和验证 Agent 的各种工具功能

## 设计说明与适用范围

- 目标定位：用于配合 ADK Web 进行快速可视化调试，不推荐直接用于生产环境。
- Runner 构造：Debug Server 接收 `Agent`，并根据前端请求的应用名在服务端延迟创建 `runner.Runner`；不支持让用户在此处手动传入 `Runner` 实例。
- 单一会话后端：由于 Debug Server 自身提供会话 REST（list/create/get），要求使用同一个 `session.Service` 作为全局后端，并在内部创建的所有 runner 之间共享。
  - 通过 `debug.WithSessionService(...)` 配置（默认内存实现）。
  - 为保持一致性，Debug Server 会在创建 runner 时强制注入同一会话后端（追加 `runner.WithSessionService(s.sessionSvc)`），覆盖你通过 `WithRunnerOptions` 传入的会话后端。
  - 此处不支持按 app/runner 的多会话后端。
- 生产建议：面向生产（如 AG‑UI）建议自建接受预配置 `runner.Runner` 的服务，或按需使用 `server/a2a`，以便完整控制会话/记忆/工件、鉴权与扩展能力。

## 架构图

```
User Interface
+---------------------------+
|      ADK Web UI           |  ← Access via browser: http://localhost:4200
|        (React)            |
+-----------+---------------+
            | HTTP/SSE Request
            v
+-----------------------------+
|     Debug Server            |  ← Listening on http://localhost:8000
|                             |
|       API Routing           | 
|       Session Management    | 
|       CORS Handling         |
+-----------+-----------------+
            | Call Agent
            v
+---------------------------------+
|    tRPC-Agent-Go                |
|                                 |
| +-------------+ +--------------+| 
| | LLM Agent   | | Tool System  ||
| | • Model Call| | • Calculator ||
| | • Streaming | | • Time Query ||
| | • Prompting | | • Custom Tool||
| +-------------+ +--------------+|
+-----------+---------------------+
            | External Call
            v
+----------------------------------+
|     External Services            |
|                                  |
| • LLM API   (OpenAI/DeepSeek)    | 
| • Database   (Redis/MySQL)       | 
| • Other API  (Search/File System)|
+----------------------------------+
```

数据流向：

```
用户输入 → Web UI → Debug Server → Agent → LLM/工具 → 流式响应 → Web UI
```

## 使用步骤

1. 创建 Agent。
2. 将 Agent 作为构造参数，创建 Debug Server，Debug Server 本身能提供 http Handler 函数。
3. 创建 tRPC HTTP 服务，将 Debug Server 的 http Handler 注册为 tRPC HTTP 服务的处理函数。
4. 启动 tRPC HTTP 服务作为后端服务。
5. 安装 ADK Web UI，方便前端可视化调试
6. 启动 ADK Web UI，指定 tRPC HTTP 服务为后端服务
7. 可以在浏览器前端，直接通过 ADK Web UI 输入用户请求，进行调试，前端页面会展示可观测数据。。

具体可以运行的例子见[examples/debugserver](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/debugserver)

## 调试结果展示

通过 ADK Web UI，您可以直接测试调用场景，Web 界面会显示 event 和 trace 信息。
例如下面展示了调试一个具备计算器功能的 agent 的情况。

![event](../assets/img/debugserver/event.png)

![trace](../assets/img/debugserver/trace.png)
