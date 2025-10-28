# Timer Example - Implementation Notes

## Callback State 实现

本示例展示了如何使用 **Invocation Callback State** 机制在 Before 和 After 回调之间共享数据。

### 核心改进

#### 之前（使用实例变量）

```go
type toolTimerExample struct {
    // 需要维护多个 map 来存储状态
    toolStartTimes  map[string]time.Time
    agentStartTimes map[string]time.Time
    modelStartTimes map[string]time.Time
    currentModelKey string
    agentSpans      map[string]trace.Span
    modelSpans      map[string]trace.Span
    toolSpans       map[string]trace.Span
    // ...
}

// BeforeAgentCallback
if e.agentStartTimes == nil {
    e.agentStartTimes = make(map[string]time.Time)
}
e.agentStartTimes[invocation.InvocationID] = startTime

// AfterAgentCallback
if startTime, exists := e.agentStartTimes[invocation.InvocationID]; exists {
    duration := time.Since(startTime)
    delete(e.agentStartTimes, invocation.InvocationID)
}
```

#### 之后（使用 Callback State）

```go
type toolTimerExample struct {
    // 不再需要存储状态的实例变量
    agentDurationHistogram metric.Float64Histogram
    toolDurationHistogram  metric.Float64Histogram
    modelDurationHistogram metric.Float64Histogram
    // ...
}

// BeforeAgentCallback
inv.SetCallbackState("agent:start_time", time.Now())
inv.SetCallbackState("agent:span", span)

// AfterAgentCallback
if startTimeVal, ok := inv.GetCallbackState("agent:start_time"); ok {
    startTime := startTimeVal.(time.Time)
    duration := time.Since(startTime)
    inv.DeleteCallbackState("agent:start_time")
}
```

### 优势

1. **代码更简洁**：减少了 7 个实例变量
2. **自动作用域**：状态自动限定在 Invocation 生命周期内
3. **线程安全**：内置 RWMutex 保护
4. **懒初始化**：首次使用时才分配内存
5. **清晰的生命周期**：使用 `DeleteCallbackState` 显式清理

### 命名约定

- Agent 回调：`"agent:xxx"`
- Model 回调：`"model:xxx"`
- Tool 回调：`"tool:toolName:xxx"`

### Model 和 Tool 回调的特殊处理

由于 Model 和 Tool 回调不直接接收 `invocation` 参数，需要从 context 中获取：

```go
// Model/Tool callbacks
if inv, ok := agent.InvocationFromContext(ctx); ok && inv != nil {
    inv.SetCallbackState("model:start_time", time.Now())
}
```

## 输出格式处理

### 问题

`AfterAgentCallback` 在 goroutine 中异步执行，其输出可能出现在下一个 `👤 You:` 提示符之后：

```
👤 You: ⏱️  AfterAgentCallback: completed in 5.759s
```

### 解决方案

**方案 1：在回调中添加空行**

```go
// callbacks.go
fmt.Printf("⏱️  AfterAgentCallback: %s completed in %v\n", invocation.AgentName, duration)
fmt.Println() // Add spacing after agent callback.
```

**方案 2：在主循环中添加延迟**

```go
// main.go
if err := e.processMessage(ctx, userInput); err != nil {
    fmt.Printf("❌ Error: %v\n", err)
}
time.Sleep(50 * time.Millisecond) // Wait for AfterAgentCallback
```

两种方案结合使用，确保输出格式正确。

### 预期输出

```
The calculation result is: **111,111,111**

⏱️  AfterAgentCallback: tool-timer-assistant completed in 5.759s

👤 You:
```

## OpenTelemetry 集成

示例同时展示了如何将 Callback State 与 OpenTelemetry 集成：

1. **Metrics**：记录执行时长和调用次数

   - `agent_duration_seconds`
   - `model_duration_seconds`
   - `tool_duration_seconds`

2. **Traces**：创建 span 追踪执行流程
   - `agent_execution`
   - `model_inference`
   - `tool_execution`

Span 也存储在 Callback State 中：

```go
// BeforeCallback
inv.SetCallbackState("agent:span", span)

// AfterCallback
if spanVal, ok := inv.GetCallbackState("agent:span"); ok {
    span := spanVal.(trace.Span)
    span.SetAttributes(...)
    span.End()
    inv.DeleteCallbackState("agent:span")
}
```

## 运行示例

### 启动 Telemetry 服务

```bash
docker compose up -d
```

### 运行示例

```bash
export OPENAI_API_KEY="your-api-key"
go run .
```

### 查看 Telemetry

- Jaeger UI: http://localhost:16686
- Prometheus: http://localhost:9090

## 代码结构

```
timer/
├── main.go              # 主程序和 CLI 交互
├── callbacks.go         # Callback 实现（使用 Callback State）
├── tools.go             # 工具实现（calculator）
├── metrics.go           # OpenTelemetry metrics 初始化
├── docker-compose.yaml  # Telemetry 服务配置
├── otel-collector.yaml  # OTEL Collector 配置
├── prometheus.yaml      # Prometheus 配置
└── README.md            # 使用说明
```

## 测试

```bash
# 编译
go build .

# 运行
./timer

# 测试命令
calculate 12345679 * 9
/history
/new
/exit
```

## 总结

这个示例完整展示了：

1. ✅ Callback State 的实际应用
2. ✅ Agent/Model/Tool 三种回调的计时
3. ✅ OpenTelemetry 集成（Metrics + Traces）
4. ✅ 并发安全的状态管理
5. ✅ 清晰的输出格式

通过使用 Callback State，代码更简洁、更易维护，同时保持了完整的功能性。
