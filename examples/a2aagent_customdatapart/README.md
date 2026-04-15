# A2AAgent Custom DataPart 示例

该示例演示一条完整链路：

1. 远端 agent 正常生成文本回复。
2. wrapper 额外发出一个 `graph.node.custom` 事件，并把摘要 hint 放进 `StructuredOutput`。
3. 服务端通过 `a2a.WithEventToA2APartMapper()` 把这个 custom event 映射成 `DataPart(type="custom_data")`。
4. 客户端 A2AAgent 通过 `a2aagent.WithA2ADataPartMapper()` 再把这个 `DataPart` 映射成一条可见提示文本。

## 环境变量

示例与 [`a2aagent`](../a2aagent/README.md) 一样需要模型配置。

```bash
# 例如 DeepSeek
export OPENAI_API_KEY="your-deepseek-api-key"
export OPENAI_BASE_URL="https://api.deepseek.com/v1"
export MODEL_NAME="deepseek-chat"
```

## 运行

在仓库根目录执行：

```bash
cd examples && go run ./a2aagent_customdatapart \
  -model "${MODEL_NAME:-deepseek-chat}" \
  -host "127.0.0.1:8899" \
  -streaming=true
```

## 预期输出

你会看到两类输出：

- 常规文本回复：
  - `🤖 Assistant: ...`（由远端 LLM 生成，内容不固定）
- 自定义 mapper 提示：
  - `🧩 Agent mapper(custom_data): ...`

第二行就是 client-side mapper 消费 server-side DataPart 后生成的可见输出。
