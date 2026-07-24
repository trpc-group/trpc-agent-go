# Observability 功能

## 概述

tRPC-Agent-Go 框架内置了全面的可观测（Observability）功能，基于 OpenTelemetry 标准协议，为 Agent 应用提供了强大的可观测性能力。
通过可观测功能，开发者可以实现对 Agent 运行状态的全方位监控，包括链路追踪、性能指标收集和日志记录等。

### 🎯 核心特性

- **链路追踪（Tracing）**：完整记录 Agent 执行过程中的调用链路
- **性能指标（Metrics）**：收集 Agent 运行时的关键性能数据
- **日志聚合（Logging）**：统一的日志收集和管理
- **多平台支持**：支持 Jaeger、Prometheus、Galileo、智研监控宝 等主流监控平台
- **灵活配置**：支持多种配置方式和自定义扩展

## 与不同的监控平台集成

### Langfuse 集成

Langfuse 是专为 LLM 应用设计的可观测平台，支持通过 OpenTelemetry 协议采集链路追踪数据。tRPC-Agent-Go 可通过 OpenTelemetry 协议将 Trace 数据导出到 Langfuse。

#### 1. 部署 Langfuse

可参考 [Langfuse 官方自托管指南](https://langfuse.com/self-hosting) 进行本地或云端部署。快速体验可参考 [Docker Compose 部署文档](https://langfuse.com/self-hosting/docker-compose)。

#### 2. Go 编写接入代码


```bash
export LANGFUSE_PUBLIC_KEY="your-public-key"
export LANGFUSE_SECRET_KEY="your-secret-key"
export LANGFUSE_HOST="your-langfuse-host" # 以 host:port 形式填写（不带 http:// 协议头），例如 "cloud.langfuse.com:443" 或 "localhost:3000".
export LANGFUSE_INSECURE="true" # 用于不安全连接（仅限开发环境）
```

```go
import (
	"context"
	"log"

	"trpc.group/trpc-go/trpc-agent-go/telemetry/langfuse"
)

func main() {
	// Start trace with Langfuse integration using environment variables
	clean, err := langfuse.Start(context.Background())
	if err != nil {
		log.Fatalf("Failed to start trace telemetry: %v", err)
	}
	defer func() {
		if err := clean(context.Background()); err != nil {
			log.Printf("Failed to clean up trace telemetry: %v", err)
		}
	}()
```

完整示例可参考 [examples/telemetry/langfuse](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/telemetry/langfuse)。

注意：`LANGFUSE_HOST` 会直接传给 OpenTelemetry 的 `otlptracehttp.WithEndpoint`，因此不能包含 `http://` 或 `https://`。协议由 `LANGFUSE_INSECURE` 控制，路径固定为 `/api/public/otel/v1/traces`。

运行示例：

```bash
go run .
```

你可以在 Langfuse 控制台查看链路追踪数据。

消息 payload 的兼容策略以及推荐使用的 OTel `role + parts` 字段，见 [多模态遥测消息](telemetry-multimodal.md)。

#### Skill 可观测

当 `skill_load` 激活 skill 时，tRPC-Agent-Go 会在已有的 `execute_tool skill_load` span 下创建 Galileo 兼容的 `invoke_skill {skill.name}` INTERNAL 子 span。`invoke_skill` 表示 skill activation/materialization：确认被选中的 skill，并把 `SKILL.md` 内容物化进 Agent 上下文。它不覆盖后续的 `chat`、`workspace_exec`、`skill_run`、`skill_exec` 或其他 tool 执行。

同一次激活也会在 `trpc_agent_go.internal.invoke_skill` meter 上记录 `gen_ai.request_cnt` 和 `gen_ai.client.operation.duration`。Metric attributes 只包含低基数字段，例如 `gen_ai.operation.name`、`gen_ai.skill.name`、`gen_ai.skill.id`、`gen_ai.user.id`、`gen_ai.agent.id`、可选的 `gen_ai.agent.name`、可选的 `gen_ai.skill.version`，以及失败时的 `error.type`。路径、content hash、doc 列表、tool call id 和提示词内容不会进入 metric attributes。

Langfuse exporter 默认会 drop `invoke_skill` spans，因此已有 Langfuse observation 类型、树结构和 token 统计保持不变。Galileo 和通用 OTel exporter 仍可消费该 span 和 metrics。

出于隐私考虑，`gen_ai.invoke_skill_request` / `gen_ai.invoke_skill_response` span attributes 默认使用安全的路径和内容摘要：紧凑路径表示、SHA-256 hash、字节数以及截断后的内容预览。Galileo exporter 可在导出侧把这些 attributes 转换为平台事件展示。导出敏感 skill repository 时，可通过 span attribute policy 进一步 drop、omit 或 truncate 这些 attributes。

##### 接入代码说明
Langfuse 支持通过 `/api/public/otel` (OTLP) 接口接收 Trace 数据，仅支持 HTTP/protobuf，不支持 gRPC。
上述代码通过设置 `OTEL_EXPORTER_OTLP_HEADERS` 和 `OTEL_EXPORTER_OTLP_TRACES_ENDPOINT` 来接入 langfuse。

```bash
# 欧盟数据区
OTEL_EXPORTER_OTLP_ENDPOINT="https://cloud.langfuse.com/api/public/otel"
# 美国数据区
# OTEL_EXPORTER_OTLP_ENDPOINT="https://us.cloud.langfuse.com/api/public/otel"
# 本地部署 (>= v3.22.0)
# OTEL_EXPORTER_OTLP_ENDPOINT="http://localhost:3000/api/public/otel"

# 设置 Basic Auth 认证
OTEL_EXPORTER_OTLP_HEADERS="Authorization=Basic ${AUTH_STRING}"
```

其中 `AUTH_STRING` 为 base64 编码的 `public_key:secret_key`，可用如下命令生成：

```bash
echo -n "pk-lf-xxxx:sk-lf-xxxx" | base64
# GNU 系统可加 -w 0 防止换行
```

如需单独指定 trace 数据的 endpoint，可设置：

```bash
OTEL_EXPORTER_OTLP_TRACES_ENDPOINT="http://localhost:3000/api/public/otel/v1/traces"
```


### Jaeger、Prometheus 等开源监控平台

可以参考 [examples/telemetry](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/telemetry) 的代码示例。

```go
package main

import (
    "context"
    "log"
    
    ametric "trpc.group/trpc-go/trpc-agent-go/telemetry/metric"
    atrace "trpc.group/trpc-go/trpc-agent-go/telemetry/trace"
)

func main() {
    // 启动指标收集
    mp, err := ametric.NewMeterProvider(
		context.Background(),
		ametric.WithEndpoint("localhost:4318"),
		ametric.WithProtocol("http"),
	)
	if err != nil {
		log.Fatalf("Failed to create meter provider: %v", err)
	}
	defer mp.Shutdown(context.Background())
	ametric.InitMeterProvider(mp)

    // 启动链路追踪
    traceClean, err := atrace.Start(
        context.Background(),
        atrace.WithEndpoint("localhost:4317"), // trace 导出地址
    )
    if err != nil {
        log.Fatalf("Failed to start trace telemetry: %v", err)
    }
    defer traceClean()

    // 你的 Agent 应用代码
    // ...
    // 可以添加自定义 trace 和 metrics
}
```

#### Jaeger trace 示例
![trace-jaeger](../assets/img/telemetry/jaeger.png)

#### Prometheus 监控指标示例

![metric-prometheus](../assets/img/telemetry/prometheus.png)

## 实际应用示例

### 基本的指标和追踪

```go
package main

import (
    "context"
    "fmt"
    "time"
    
    ametric "trpc.group/trpc-go/trpc-agent-go/telemetry/metric"
    atrace "trpc.group/trpc-go/trpc-agent-go/telemetry/trace"
    "trpc.group/trpc-go/trpc-agent-go/log"

    "go.opentelemetry.io/otel/attribute"
    "go.opentelemetry.io/otel/metric"
    "go.opentelemetry.io/otel/trace"
)

func main() {
	// 启动指标收集
	mp, err := ametric.NewMeterProvider(
		context.Background(),
		ametric.WithEndpoint("localhost:4318"),
		ametric.WithProtocol("http"),
	)
	if err != nil {
		log.Fatalf("Failed to create meter provider: %v", err)
	}
	defer mp.Shutdown(context.Background())
	ametric.InitMeterProvider(mp)
	meter := mp.Meter("trpc_agent_go.app")

	if err := processAgentRequest(context.Background(), meter); err != nil {
		log.Errorf("processAgentRequest failed: %v", err)
	}
}
func processAgentRequest(ctx context.Context, meter metric.Meter) error {
    // 创建追踪 span
    ctx, span := atrace.Tracer.Start(
        ctx,
        "process-agent-request",
        trace.WithAttributes(
            attribute.String("agent.type", "chat"),
            attribute.String("user.id", "user123"),
        ),
    )
    defer span.End()
    
    // 创建指标计数器
    requestCounter, err := meter.Int64Counter(
        "agent.requests.total",
        metric.WithDescription("Total number of agent requests"),
    )
    if err != nil {
        return err
    }
    
    // 记录请求
    requestCounter.Add(ctx, 1, metric.WithAttributes(
        attribute.String("agent.type", "chat"),
        attribute.String("status", "success"),
    ))
    
    // 模拟处理过程
    time.Sleep(100 * time.Millisecond)
    
    return nil
}
```

### Agent 执行追踪

框架会自动为 Agent 的关键组件添加监控埋点：

```go
// Agent 执行会自动生成以下监控数据：
// 
// Traces:
// - agent.execution: Agent 整体执行过程
// - tool.invocation: Tool 调用过程  
// - model.api_call: 模型 API 调用过程
```

## 监控数据分析

### 链路追踪分析

典型的 Agent 执行链路结构：

```
Agent Request
├── Planning Phase
│   ├── Model API Call (DeepSeek)
│   └── Response Processing
├── Tool Execution Phase  
│   ├── Tool: web_search
│   ├── Tool: knowledge_base
│   └── Result Processing
└── Response Generation Phase
    ├── Model API Call (DeepSeek)
    └── Final Response Formatting
```

通过链路追踪可以分析：

- **性能瓶颈**：识别耗时最长的操作
- **错误定位**：快速找到失败的具体环节
- **依赖关系**：了解组件间的调用关系
- **并发分析**：观察并发执行的效果

## Span Attribute 策略（生产侧）

`telemetry/trace` 提供 opt-in 的 `SpanAttributePolicy`，在 span 创建阶段控制大 payload **span attribute** 的采集与写入大小。

默认不配置时行为不变。

### 能力

- `Drop()` 与无条件 `Omit()`（未配 `MaxBytes`）可避免 `json.Marshal`。
- `MaxBytes()` + `Omit()`：`[]byte` 路径（如 `execute_tool` 的 tool arguments）按长度判断，无需额外 marshal；JSON 路径（chat / workflow / invoke_agent）仍需完整 marshal，**不能**降低堆峰值。
- `Truncate()` 始终完整 marshal，仅限制写入 span 的值大小。

| 规则 | 能否降低 marshal 堆峰值 | 说明 |
|------|-------------------------|------|
| `Drop()` | 是 | 跳过序列化，不写 attribute |
| `Omit()`（无 `MaxBytes`） | 是 | 跳过 payload 序列化 |
| `MaxBytes(n)` + `Omit()` | JSON：否；`[]byte`：是 | JSON 路径需完整 marshal 后再比阈值；`[]byte` 路径仅 `len` 判断 |
| `Truncate(n)` | 否 | 完整序列化后截断导出 |

想降内存，优先 `Drop()` 去掉不需要的 attribute（如冗余 `*.otel`）。

### 覆盖范围

已接入 `chat`、`invoke_agent`、`workflow`、`execute_tool` 的大 payload attribute（messages、llm request/response、workflow request/response、tool arguments/result 等）。

### 配置入口

```go
import atrace "trpc.group/trpc-go/trpc-agent-go/telemetry/trace"

clean, err := atrace.Start(ctx,
    atrace.WithSpanAttributePolicy(
        atrace.WithAttributeRule(atrace.OperationChat, atrace.AttrInputMessagesOTel, atrace.Drop()),
        atrace.WithAttributeRule(atrace.OperationChat, atrace.AttrOutputMessagesOTel, atrace.Drop()),
        atrace.WithAttributeRule(atrace.OperationInvokeAgent, atrace.AttrInputMessagesOTel, atrace.Drop()),
    ),
)
```

未调用 `trace.Start` 时，可在首次 LLM 调用前使用 `atrace.SetSpanAttributePolicy(...)`。`trace.Start` 在 tracer 初始化**成功**后安装 policy，`clean()` 会恢复之前的 policy。

`MaxBytes` + `Omit()` 示例（限制 attribute 体积；JSON 路径仍会 marshal）：

```go
atrace.WithAttributeRule(atrace.OperationWorkflow, atrace.AttributeKey("gen_ai.workflow.request"),
    atrace.MaxBytes(16<<10), atrace.Omit(),
)
```

`Truncate` 示例（接受完整 marshal，仅限制导出体积）：

```go
atrace.WithAttributeRule(atrace.OperationWorkflow, atrace.AttributeKey("gen_ai.workflow.response"), atrace.Truncate(64<<10))
```

### 兼容性说明

启用 Drop/Omit/Truncate 后，部分监控后端可能无法从 attribute 还原结构化全文；请按自身后端与内存预算 opt-in 评估。

## 进阶功能

### 自定义 Exporter

如果需要将可观测数据发送到自定义的监控系统：

```go
import (
    "go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
    "go.opentelemetry.io/otel/sdk/trace"
)

func setupCustomExporter() error {
    exporter, err := otlptracehttp.New(
        context.Background(),
        otlptracehttp.WithEndpoint("https://your-custom-endpoint.com"),
        otlptracehttp.WithHeaders(map[string]string{
            "Authorization": "Bearer your-token",
        }),
    )
    if err != nil {
        return err
    }
    
    tp := trace.NewTracerProvider(
        trace.WithBatcher(exporter),
    )
    
    // 设置为全局 TracerProvider
    otel.SetTracerProvider(tp)
    
    return nil
}
```

## 参考资源

- [OpenTelemetry 官方文档](https://opentelemetry.io/docs/)
- [tRPC-Agent-Go Telemetry 示例](https://github.com/trpc-group/trpc-agent-go/tree/main/examples/telemetry)

通过合理使用可观测功能，你可以建立完善的 Agent 应用监控体系，及时发现和解决问题，持续优化系统性能。
