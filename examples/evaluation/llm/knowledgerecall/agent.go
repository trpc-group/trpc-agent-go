//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package main

import (
	"context"
	"log"
	"math"
	"strings"
	"time"

	"git.woa.com/trag/trag-sdk/go-trag"
	knowledge "git.woa.com/trpc-go/trpc-agent-go/trpc/knowledge/trag"
	"git.woa.com/trpc-go/trpc-agent-go/trpc/knowledge/trag/sdk"
	tragsource "git.woa.com/trpc-go/trpc-agent-go/trpc/knowledge/trag/source"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/source"
	knowledgetool "trpc.group/trpc-go/trpc-agent-go/knowledge/tool"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

func newQAAgent(modelName string, stream bool) agent.Agent {
	searchTool := newSearchTool()
	calculatorTool := function.NewFunctionTool(
		calculate,
		function.WithName("calculator"),
		function.WithDescription("Perform arithmetic operations including add, subtract, multiply, divide, power."),
	)
	genCfg := model.GenerationConfig{
		MaxTokens:   intPtr(512),
		Temperature: floatPtr(0.2),
		Stream:      stream,
	}
	return llmagent.New(
		"final-response-agent",
		llmagent.WithModel(openai.New(modelName)),
		llmagent.WithTools([]tool.Tool{searchTool, calculatorTool}),
		llmagent.WithInstruction("Answer the user concisely and accurately."),
		llmagent.WithDescription("Simple LLM agent for final-response evaluation."),
		llmagent.WithGenerationConfig(genCfg),
	)
}

func newSearchTool() tool.Tool {
	ctx := context.Background()

	// 1. 创建 tRAG 客户端
	tragClient := trag.NewTRag(trag.WithToken("c4f4abf6-e3b7-4fbd-98fc-800ff99e77d5"))

	// 2. 创建 tRAG 选项配置
	tragOption := sdk.NewTRagOption(
		sdk.WithClient(tragClient),
		sdk.WithInstanceCode("is-8a8c8dbf"),          // RAG 实例代码
		sdk.WithNamespaceCode("ns-8b87ae30"),         // 命名空间代码
		sdk.WithCollectionCode("col-21e13ceb"),       // 集合代码
		sdk.WithEmbeddingModel("bge-large-en"),       // embedding 模型名称（可选）
		sdk.WithPolicyCode("homerpan-import-policy"), // 策略代码（可选）
	)

	// 3. 创建数据源（推荐使用 tRAG Source）
	sources := []source.Source{
		// tRAG 文件源 - 读取本地文件，由 tRAG 服务端分块（推荐）
		tragsource.NewFileSource(
			[]string{"./knowledge/llm.md"},
			tragsource.WithFileMetadata(map[string]any{"type": "documentation"}),
		),
	}

	// 4. 创建 tRAG 知识库
	kb, err := knowledge.New(
		knowledge.WithTRagOption(*tragOption),
		knowledge.WithSources(sources),
	)
	if err != nil {
		log.Fatalf("Failed to create tRAG knowledge: %v", err)
	}

	// 5. 加载文档（支持限流）
	if err := kb.Load(ctx,
		knowledge.WithTRagRateLimit(300*time.Millisecond, 5), // 3 QPS，burst=5
	); err != nil {
		log.Fatalf("Failed to load knowledge base: %v", err)
	}

	// 6. 创建搜索工具
	searchTool := knowledgetool.NewKnowledgeSearchTool(
		kb,
		knowledgetool.WithToolName("knowledge_search"),
		knowledgetool.WithToolDescription("Search for relevant information in the knowledge base."),
	)
	return searchTool
}

type calculatorArgs struct {
	Operation string  `json:"operation"`
	A         float64 `json:"a"`
	B         float64 `json:"b"`
}

type calculatorResult struct {
	Operation string  `json:"operation"`
	A         float64 `json:"a"`
	B         float64 `json:"b"`
	Result    float64 `json:"result"`
}

func calculate(_ context.Context, args calculatorArgs) (calculatorResult, error) {
	var result float64
	switch strings.ToLower(args.Operation) {
	case "add", "+":
		result = args.A + args.B
	case "subtract", "-":
		result = args.A - args.B
	case "multiply", "*":
		result = args.A * args.B
	case "divide", "/":
		if args.B != 0 {
			result = args.A / args.B
		}
	case "power", "^":
		result = math.Pow(args.A, args.B)
	}
	return calculatorResult{
		Operation: args.Operation,
		A:         args.A,
		B:         args.B,
		Result:    result,
	}, nil
}

func intPtr(v int) *int           { return &v }
func floatPtr(v float64) *float64 { return &v }
