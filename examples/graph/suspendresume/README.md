# Suspend/Resume 示例

这个示例展示了如何使用 `graph` + `graphagent` + `runner` 组合来实现工作流的中断和恢复功能。

## 功能特性

- **工作流定义**: 使用 `StateGraph` 构建器定义工作流
- **节点执行**: 支持函数节点、LLM节点、工具节点等
- **中断机制**: 节点可以使用 `graph.Suspend()` 中断执行
- **恢复机制**: 使用 `Command.ResumeMap` 恢复执行
- **检查点**: 自动保存和恢复执行状态

## 工作流结构

```
start → process → approval → complete
                ↑
            (中断点)
```

1. **start**: 初始化工作流
2. **process**: 处理业务逻辑
3. **approval**: 审批节点（会中断等待人工审批）
4. **complete**: 完成工作流

## 使用方法

### 1. 运行示例

```bash
cd examples/graph/suspendresume
go run main.go
```

### 2. 预期输出

```
🚀 开始运行 Suspend/Resume 演示...

🔄 第一次运行 - 会中断等待审批...
📍 开始节点: 初始化工作流
⚙️ 处理节点: 处理业务逻辑
⏸️ 审批节点: 检查是否需要人工审批
⏳ 等待人工审批...
🛑 检测到中断事件: interrupt
📍 中断节点: approval

🔄 恢复运行 - 模拟人工审批...
⏸️ 审批节点: 检查是否需要人工审批
✅ 检测到恢复值: true
🎉 完成节点: 工作流完成

📊 最终状态:
  workflow_id: demo_001
  start_time: 1703123456
  message: Hello from start node
  step: 4
  processed: true
  timestamp: 1703123456
  approved: true
  approval_time: 1703123456
  completed: true
  completion_time: 1703123456
```

## 关键代码

### 中断节点

```go
func approvalNode(ctx context.Context, state graph.State) (any, error) {
    // 检查是否有恢复值
    if approved, exists := graph.ResumeValue[bool](ctx, state, "approval"); exists {
        // 处理恢复值
        return state, nil
    }
    
    // 没有恢复值，需要中断等待审批
    _, err := graph.Suspend(ctx, state, "approval", prompt)
    if err != nil {
        return nil, err
    }
    
    return state, nil
}
```

### 恢复执行

```go
// 创建恢复命令
cmd := &graph.Command{
    ResumeMap: map[string]any{
        "approval": true, // 模拟用户批准
    },
}

// 创建初始状态，包含恢复命令
initialState := graph.State{
    "workflow_id": "demo_001",
    "__command__": cmd,
}
```

## 技术特点

- **原子性保存**: 使用 `PutFull` 确保检查点和写入的原子性
- **版本跟踪**: 支持通道版本和节点版本跟踪
- **事件系统**: 完整的事件流，包括中断事件
- **状态管理**: 自动状态保存和恢复
- **超时控制**: 支持步骤超时和节点超时

## 扩展功能

- **条件边**: 支持基于结果的动态路由
- **通道管理**: 支持多种通道行为（LastValue、Topic、Barrier）
- **模式匹配**: 支持复杂的条件判断和路由逻辑
- **并行执行**: 支持多节点并行执行
- **错误处理**: 完整的错误处理和恢复机制

## 相关文档

- [Graph 包文档](../../../docs/zh/graph.md)
- [Checkpoint 机制](../../../docs/zh/graph.md#检查点机制)
- [中断和恢复](../../../docs/zh/graph.md#中断和恢复)
- [事件系统](../../../docs/zh/graph.md#事件系统)
