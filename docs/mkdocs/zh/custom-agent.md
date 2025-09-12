# 自定义 Agent

当你不想一开始就上 Graph 或多 Agent 编排，又需要把 LLM 能力“嵌入到”已有的业务流程里时，可以直接实现 `agent.Agent` 接口，自己掌控分支与流程。

本文展示一个最小可用的“意图分流”自定义 Agent：

- 先用 LLM 做意图识别：`chitchat` 或 `task`
- 如果是闲聊：直接对话回复
- 如果是任务：给出 3–5 步的执行计划（真实业务里可继续串工具或下游逻辑）

## 何时选择自定义 Agent

- 业务流程简单但需要精细控制分支、校验、兜底等
- 不需要可视化编排和复杂编队协作（后续再演进到 Chain/Parallel/Graph 也很自然）

## 实现要点

必须实现以下方法：

- `Run(ctx, *Invocation) (<-chan *event.Event, error)`: 执行业务逻辑，产出事件流（推荐转发模型流式响应到事件）
- `Tools() []tool.Tool`: 返回可用工具（无工具可以返回空切片）
- `Info() Info`: 返回 Agent 名称与描述
- `SubAgents()/FindSubAgent()`: 无子 Agent 返回空/`nil`

核心模式：

1) 使用 `invocation.Message` 作为用户输入

2) 通过 `invocation` 携带的上下文（Session、Callbacks、Artifact 等）共享框架能力

3) 调用 `model.Model.GenerateContent(ctx, *model.Request)` 获取流式响应；用 `event.NewResponseEvent(...)` 逐条转发

## 代码示例

完整示例见：`examples/customagent`

关键片段（简化版）：

```go
type SimpleIntentAgent struct {
    name        string
    description string
    model       model.Model
}

func (a *SimpleIntentAgent) Run(ctx context.Context, inv *agent.Invocation) (<-chan *event.Event, error) {
    out := make(chan *event.Event, 64)
    go func() {
        defer close(out)
        intent := a.classifyIntent(ctx, inv) // chitchat | task
        if intent == "task" {
            a.replyTaskPlan(ctx, inv, out)
        } else {
            a.replyChitChat(ctx, inv, out)
        }
    }()
    return out, nil
}

func (a *SimpleIntentAgent) replyChitChat(ctx context.Context, inv *agent.Invocation, out chan<- *event.Event) {
    req := &model.Request{
        Messages: []model.Message{
            model.NewSystemMessage("Be concise and friendly."),
            inv.Message,
        },
        GenerationConfig: model.GenerationConfig{Stream: true},
    }
    rspCh, _ := a.model.GenerateContent(ctx, req)
    for rsp := range rspCh {
        out <- event.NewResponseEvent(inv.InvocationID, a.name, rsp)
    }
}
```

## 与 Runner 配合

虽然可以直接调用 Agent 接口，但推荐用 `Runner` 来执行，它会自动管理会话与事件入库，接口也更友好。

示例：

```go
// 构造模型与自定义 Agent
m := openai.New("deepseek-chat")
ag := NewSimpleIntentAgent("biz-agent", "intent branching", m)

// 用 Runner 执行
r := runner.NewRunner("customagent-app", ag)
ch, err := r.Run(ctx, "user-001", "session-001", model.NewUserMessage("你好，随便聊聊"))
// 处理事件流...
```

## 运行示例（交互式）

```bash
cd examples/customagent
export OPENAI_API_KEY="your_api_key"
go run . -model deepseek-chat

# 进入交互后可使用命令：
# /history  显示对话历史（通过提示）
# /new      开启新会话
# /exit     退出
```

## 扩展建议

- 引入工具：返回 `[]tool.Tool`，如 `function.NewFunctionTool(...)` 串接数据库/HTTP/内部服务
- 增加校验：在分支前先做参数校验、风控、开关控制
- 渐进演进：当 if-else 过多或需要协作时，平滑切换到 `ChainAgent`/`ParallelAgent` 或 `Graph`
