# tRPC-Agent-Go：让 Go 开发者轻松构建智能 AI 应用

## 1、项目简介

tRPC 团队之前开源了 A2A 开发框架 [tRPC-A2A-Go](https://github.com/trpc-group/trpc-a2a-go) 和 MCP 开发框架 [tRPC-MCP-Go](https://github.com/trpc-group/trpc-mcp-go)，尤其是 tRPC-A2A-Go，在国内外都有不少用户进行应用和贡献。 现在我们推出 [tRPC-Agent-Go](https://github.com/trpc-group/trpc-agent-go) 开发框架，实现 Go 语言 AI 生态开发框架的闭环。

当前主流 Agent 框架（AutoGen、CrewAI 、Agno、ADK 等）大部分都是基于 Python，而  Go 在微服务、并发与部署方面有天然优势，Go 在腾讯内部也有大规模应用，业界基于 Go 语言的 Agent 框架较少，大部分都是编排式的 workflow 框架，缺少真正的“去中心化、可协作、能涌现”的自主多 Agent 能力。tRPC-Agent-Go 直接利用 Go 的高并发与 tRPC 生态，把 LLM 的推理、协商和自适应性带到 Go 场景，满足复杂业务对“智能+性能”的双重需求。

## 2、架构设计

tRPC-Agent-Go 采用模块化架构设计，由多个核心组件组成，组件都可插拔，通过事件驱动机制实现组件间的解耦通信，支持callback插入自定义逻辑：

- Agent: 核心执行单元，负责处理用户输入并生成响应
- Runner: Agent 的执行器，负责管理执行流程，串联 Session/Memory Service 等能力
- Model: 支持多种 LLM 模型（OpenAI、DeepSeek 等）
- Tool: 提供各种工具能力（Function、MCP、DuckDuckGo 等）
- Session: 管理用户会话状态和事件
- Memory: 记录用户的长期记忆和个性化信息
- Knowledge: 实现 RAG 知识检索能力
- Planner: 提供 Agent 的计划和推理能力

以下是各个组件的架构图

![组件架构图](../assets/img/component_architecture.svg)


## 3、核心特点

多样化 Agent 系统

- LLMAgent: 基于大语言模型，支持工具调用和推理
- ChainAgent: 链式执行，支持多步骤任务分解
- ParallelAgent: 并行处理，支持多专家协作
- CycleAgent: 循环迭代，支持自我优化
- GraphAgent: 图工作流，兼容现有编排习惯

丰富工具生态

- 内置常用工具
- 支持 Function、MCP 协议等多种扩展方式
- 灵活的工具组合和调用策略

智能会话管理

- 支持 Redis 和内存存储的会话持久化
- 长期记忆和个性化信息保持
- RAG 检索增强生成能力
- 实时事件驱动架构

全链路可观测性

- OpenTelemetry 全链路追踪和性能监控
- 可视化调试界面和实时监控
- 结构化日志和错误追踪

## 4、快速开始

### 基本用法

```go
package main

import (
	"context"
	"fmt"
	"log"

	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

func main() {
	// 创建模型
	modelInstance := openai.New("deepseek-chat")

	// 创建工具
	calculatorTool := function.NewFunctionTool(
		calculator,
		function.WithName("calculator"),
		function.WithDescription("执行加减乘除。参数：a、b 为数值，op 取值 add/sub/mul/div；返回 result 为计算结果。"),
	)

	// 启用流式输出
	genConfig := model.GenerationConfig{
		Stream: true,
	}

	// 创建 Agent
	agent := llmagent.New("assistant",
		llmagent.WithModel(modelInstance),
		llmagent.WithTools([]tool.Tool{calculatorTool}),
		llmagent.WithGenerationConfig(genConfig),
	)

	// 创建 Runner
	runner := runner.NewRunner("calculator-app", agent)

	// 执行对话
	ctx := context.Background()
	events, err := runner.Run(ctx,
		"user-001",
		"session-001",
		model.NewUserMessage("计算 2+3 等于多少"),
	)
	if err != nil {
		log.Fatal(err)
	}

	// 处理事件流
	for event := range events {
		if event.Object == "chat.completion.chunk" {
			fmt.Print(event.Response.Choices[0].Delta.Content)
		}
	}
	fmt.Println()
}

func calculator(ctx context.Context, req calculatorReq) (calculatorRsp, error) {
	var result float64
	switch req.Op {
	case "add", "+":
		result = req.A + req.B
	case "sub", "-":
		result = req.A - req.B
	case "mul", "*":
		result = req.A * req.B
	case "div", "/":
		result = req.A / req.B
	}
	return calculatorRsp{Result: result}, nil
}

type calculatorReq struct {
	A  float64 `json:"a"`
	B  float64 `json:"b"`
	Op string  `json:"op"`
}

type calculatorRsp struct {
	Result float64 `json:"result"`
}
```

### 多 Agent 协助例子

```go
// 创建链式 Agent
chainAgent := chainagent.New("problem-solver",
    chainagent.WithSubAgents([]agent.Agent{
        analysisAgent,   // 分析 Agent
        executionAgent,  // 执行 Agent
    }))

// 使用 Runner 执行
runner := runner.NewRunner("multi-agent-app", chainAgent)
events, _ := runner.Run(ctx, userID, sessionID, message)
```

## 5、致谢

感谢腾讯内部业务如腾讯元宝，腾讯视频，腾讯新闻，IMA、QQ 音乐等等业务的支持，业务的场景打磨是对框架最好的验证。

感谢 ADK，Agno，CrewAI，AutoGen 等优秀开源框架的启发，为 tRPC-Agent-Go 开发提供灵感。

## 6、项目地址

github：[tRPC-Agent-Go](https://github.com/trpc-group/trpc-agent-go)
