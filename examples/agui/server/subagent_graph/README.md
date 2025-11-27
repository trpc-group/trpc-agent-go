# subagent_graph

将 “LLMAgent + GraphAgent 子代理” 示例以 AG-UI 服务形式暴露，便于在 Web 端体验转代理 + 工具调用。

## 功能要点

- 主代理 `llm-hub` 负责普通对话；遇到计算需求时调用 `transfer_to_agent` 跳转到子代理。
- 子代理 `math-graph` 是一个图：入口/终点都是 LLM 节点 A，`AddToolsConditionalEdges` 检测工具调用，有调用时进入 `tools` 节点执行计算器，之后回到 A 汇总。
- 计算器工具支持 `add|subtract|multiply|divide` 四则运算。
- GraphAgent 配置了 `TimelineFilterCurrentInvocation + BranchFilterModeExact`，避免父代理的 `transfer_to_agent` toolcall 被当作历史带入子图导致模型报错。

## 运行

```bash
go run ./examples/agui/server/subagent_graph -model deepseek-chat -address 0.0.0.0:8080 -path /agui
```

打开 AG-UI 客户端（或兼容前端）连接 `http://127.0.0.1:8080/agui`：
- 普通闲聊直接由 `llm-hub` 回复。
- 输入算式（如 `8.5/2`、`(3+5)*4`）会自动转交到 `math-graph` 调用计算器并返回结果。
