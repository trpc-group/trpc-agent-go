# Timer Example Output Format Test

## 修复说明

### 问题

`AfterAgentCallback` 的输出在下一个 `👤 You:` 提示符之前打印，导致输出混乱：

```
👤 You: ⏱️  AfterAgentCallback: tool-timer-assistant completed in 9.090310455s
```

### 解决方案

**方案 1：在 `AfterAgentCallback` 输出后添加空行**

```go
// callbacks.go - AfterAgentCallback
fmt.Printf("⏱️  AfterAgentCallback: %s completed in %v\n", invocation.AgentName, duration)
if runErr != nil {
    fmt.Printf("   Error: %v\n", runErr)
}
invocation.DeleteCallbackState("agent:start_time")
fmt.Println() // Add spacing after agent callback.
```

**方案 2：在主循环中添加延迟**

由于 `AfterAgentCallback` 在 goroutine 中异步执行，需要在 `processMessage` 返回后等待一小段时间，确保回调输出完成：

```go
// main.go - runExample
if err := e.processMessage(ctx, userInput); err != nil {
    fmt.Printf("❌ Error: %v\n", err)
    fmt.Println()
}
// Wait briefly for AfterAgentCallback to complete its output.
// This ensures timing information appears before the next prompt.
time.Sleep(50 * time.Millisecond)
```

### 预期输出格式

```
👤 You: calculate 12345679 * 9
⏱️  BeforeAgentCallback: tool-timer-assistant started at 12:02:43.502
   InvocationID: 27dcb154-d90c-4d1e-aea4-8ad5bb1bd43c
   UserMsg: "calculate 12345679 * 9"

🤖 Assistant: ⏱️  BeforeModelCallback: model started at 12:02:43.503
   Messages: 2
⏱️  AfterModelCallback: model completed in 2.12938509s

⏱️  BeforeToolCallback: calculator started at 12:02:45.632
   Args: {"a": 12345679, "b": 9, "operation": "multiply"}
⏱️  AfterToolCallback: calculator completed in 35.927µs
   Result: &{multiply 1.2345679e+07 9 1.11111111e+08}

🔧 Tool calls:
   • calculator (ID: call_00_sa17M2SBuHKEluLZivuV9OXI)
     Args: {"a": 12345679, "b": 9, "operation": "multiply"}

🔄 Executing tools...
The calculation result is: **111,111,111**

12345679 × 9 = 111,111,111

⏱️  AfterAgentCallback: tool-timer-assistant completed in 9.090310455s

👤 You:
```

### 关键改进

1. ✅ `AfterAgentCallback` 输出后有空行分隔
2. ✅ 下一个 `👤 You:` 提示符在新行开始
3. ✅ 输出层次清晰，易于阅读
4. ✅ 保持了 Callback State 的演示效果

### 测试命令

```bash
cd /workspace/github/my-trpc-agent-go/examples/callbacks/timer
export OPENAI_API_KEY="your-api-key"
go run .
```

输入测试：

- `calculate 12345679 * 9`
- `/exit`

观察 `AfterAgentCallback` 的输出位置是否正确。
