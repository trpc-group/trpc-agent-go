# A2A Server — Event → A2A Message Converter

## 概述

`converter.go` 负责将 Agent 内部 `event.Event` 转换为 A2A 协议的 `Message`（unary）或 `StreamingMessageResult`（streaming）。

## 转换流程（三阶段）

```
ConvertToA2AMessage / ConvertStreamingToA2AMessage:

  阶段一：前置检查
    nil / error / empty checks / graph object type 过滤

  阶段二：主 Part 判定（互斥 if/else）
    isToolCallEvent      → []Part{DataPart(function_call / function_response)}
    isCodeExecutionEvent → []Part{DataPart(executable_code / code_execution_result)}
    fallback             → []Part{TextPart(content + reasoning)}

  阶段三：附属数据追加（正交维度）
    if structuredOutputEnabled && StructuredOutput != nil:
      parts = append(parts, DataPart(custom_data))
    // 未来新增附属数据在这里继续 append
```

### 设计原则

**ToolCall / CodeExecution / Content** 是互斥的事件主类型——一个 Event 只可能是其中一种。

**StructuredOutput** 是事件的附属数据——任何类型的 Event 都可以同时携带。因此它作为**正交维度**在阶段三独立追加，不参与阶段二的互斥判定。

## StructuredOutput 功能

### 启用方式

```go
server, _ := a2a.New(
    a2a.WithAgent(myAgent, true),
    a2a.WithHost("localhost:8080"),
    a2a.WithStructuredOutputEnabled(), // opt-in 启用
)
```

### 行为

| 场景 | 结果 |
|------|------|
| 未启用（默认） | `StructuredOutput` 被忽略 |
| 启用 + `StructuredOutput` 为 `nil` | 不追加 |
| 启用 + `StructuredOutput` 为无效 JSON | 不追加，不报错 |
| 启用 + `StructuredOutput` 为有效数据 | 追加 `DataPart(custom_data)` |

### 追加效果示例

**Content + StructuredOutput：**
```
Message.Parts = [
  TextPart("hello world"),          // 阶段二产生
  DataPart({trace_id: "abc123"}),   // 阶段三追加，metadata.type = "custom_data"
]
```

**ToolCall + StructuredOutput：**
```
Message.Parts = [
  DataPart({id: "call-1", name: "my_tool", ...}),  // 阶段二产生，metadata.type = "function_call"
  DataPart({extra: "data"}),                         // 阶段三追加，metadata.type = "custom_data"
]
```

### ADK 兼容

当 `WithADKCompatibility(true)` 启用时，追加的 DataPart 会同时携带 `type` 和 `adk_type` 元数据键。

## 文件结构

| 文件 | 职责 |
|------|------|
| `converter.go` | Event → A2A Message 转换逻辑，包含三阶段主流程 |
| `server_option.go` | Server Option 定义，包括 `WithStructuredOutputEnabled()` |
| `server.go` | Server 构建，将 option 传递给 converter |
| `converter_test.go` | 覆盖所有转换场景的测试 |

## 关键方法

- `ConvertToA2AMessage()` — unary 转换入口
- `ConvertStreamingToA2AMessage()` — streaming 转换入口
- `appendStructuredOutput()` — 正交追加 StructuredOutput（unary）
- `appendStructuredOutputStreaming()` — 正交追加 StructuredOutput（streaming）
- `toDataPartPayload()` — StructuredOutput 数据规范化（支持 `map[string]any`、`[]byte`、`json.RawMessage`、任意 struct）
