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
	"os"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/knowledge"
	openaiembedder "trpc.group/trpc-go/trpc-agent-go/knowledge/embedder/openai"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/source"
	filesource "trpc.group/trpc-go/trpc-agent-go/knowledge/source/file"
	knowledgetool "trpc.group/trpc-go/trpc-agent-go/knowledge/tool"
	vectorinmemory "trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore/inmemory"
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
		"knowledge-recall-agent",
		llmagent.WithModel(openai.New(modelName)),
		llmagent.WithTools([]tool.Tool{searchTool, calculatorTool}),
		llmagent.WithInstruction("Answer the user concisely and accurately."),
		llmagent.WithDescription("Simple LLM agent for knowledge-recall evaluation."),
		llmagent.WithGenerationConfig(genCfg),
	)
}

func newSearchTool() tool.Tool {
	ctx := context.Background()
	// 1. Create embedder.
	embedder := openaiembedder.New(
		openaiembedder.WithModel(os.Getenv("OPENAI_EMBEDDING_MODEL")),
		openaiembedder.WithAPIKey(os.Getenv("OPENAI_EMBEDDING_API_KEY")),
		openaiembedder.WithBaseURL(os.Getenv("OPENAI_EMBEDDING_BASE_URL")),
	)
	// 2. Create vector store.
	vectorStore := vectorinmemory.New()
	// 3. Create knowledge sources.
	sources := []source.Source{
		filesource.New([]string{"./knowledge/llm.md"}),
	}
	// 4. Create Knowledge.
	kb := knowledge.New(
		knowledge.WithEmbedder(embedder),
		knowledge.WithVectorStore(vectorStore),
		knowledge.WithSources(sources),
		knowledge.WithEnableSourceSync(true),
	)
	// 5. Load documents.
	if err := kb.Load(ctx); err != nil {
		log.Fatalf("Failed to load knowledge base: %v", err)
	}
	// 6. Create search tool.
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
