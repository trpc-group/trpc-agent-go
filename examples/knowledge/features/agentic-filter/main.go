//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main demonstrates agentic filter for intelligent metadata-based search.
//
// Required environment variables:
//   - OPENAI_API_KEY: Your OpenAI API key for LLM and embeddings
//   - OPENAI_BASE_URL: (Optional) Custom OpenAI API endpoint, defaults to https://api.openai.com/v1
//   - MODEL_NAME: (Optional) Model name to use, defaults to deepseek-chat
//
// Example usage:
//
//	export OPENAI_API_KEY=sk-xxxx
//	export OPENAI_BASE_URL=https://api.openai.com/v1
//	export MODEL_NAME=deepseek-chat
//	go run main.go
package main

import (
	"context"
	"flag"
	"fmt"
	"log"

	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	util "trpc.group/trpc-go/trpc-agent-go/examples/knowledge"
	"trpc.group/trpc-go/trpc-agent-go/knowledge"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/embedder/openai"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/source"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/source/file"
	knowledgetool "trpc.group/trpc-go/trpc-agent-go/knowledge/tool"
	"trpc.group/trpc-go/trpc-agent-go/model"
	openaimodel "trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	sessioninmemory "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

var (
	defaultModelName = "deepseek-chat"
	vectorStore      = flag.String("vectorstore", "inmemory", "Vector store type: inmemory|pgvector|tcvector|elasticsearch")
)

func main() {
	flag.Parse()
	ctx := context.Background()
	modelName := util.GetEnvOrDefault("MODEL_NAME", defaultModelName)

	fmt.Println("🎯 Agentic Filter Demo")
	fmt.Println("======================")
	fmt.Printf("Model: %s\n", modelName)

	// Create sources with rich metadata
	sources := []source.Source{
		file.New(
			[]string{util.ExampleDataPath("file/llm.md")},
			file.WithName("LLM Docs"),
			file.WithMetadataValue("category", "documentation"),
			file.WithMetadataValue("topic", "machine_learning"),
			file.WithMetadataValue("content_type", "llm"),
		),
		file.New(
			[]string{util.ExampleDataPath("file/golang.md")},
			file.WithName("Golang Docs"),
			file.WithMetadataValue("category", "documentation"),
			file.WithMetadataValue("topic", "programming"),
			file.WithMetadataValue("content_type", "golang"),
		),
	}

	// Create knowledge base
	storeType := util.VectorStoreType(*vectorStore)
	vs, err := util.NewVectorStoreByType(storeType)
	if err != nil {
		log.Fatalf("Failed to create vector store: %v", err)
	}
	fmt.Printf("Vector Store: %s\n", storeType)

	kb := knowledge.New(
		knowledge.WithVectorStore(vs),
		knowledge.WithEmbedder(openai.New()),
		knowledge.WithSources(sources),
	)

	if err := kb.Load(ctx); err != nil {
		log.Fatalf("Failed to load: %v", err)
	}

	// Wait for data index refresh if needed
	util.WaitForIndexRefresh(storeType)

	// Create agentic filter search tool and set metadata info from sources
	knowledgeSearchTool := knowledgetool.NewAgenticFilterSearchTool(
		kb, source.GetAllMetadata(sources),
		knowledgetool.WithMaxResults(3),
	)

	// Create agent with tool configured explicitly
	agent := llmagent.New(
		"filter-assistant",
		llmagent.WithModel(openaimodel.New(modelName)),
		llmagent.WithTools([]tool.Tool{knowledgeSearchTool}),
	)

	// Create runner
	r := runner.NewRunner(
		"filter-chat",
		agent,
		runner.WithSessionService(sessioninmemory.NewSessionService()),
	)
	defer r.Close()

	// Test queries with different metadata filters
	queries := []string{
		"Find programming-related content",
		"Show me machine learning documentation",
		"What's in the golang content?",
	}

	for i, q := range queries {
		fmt.Printf("\n%d. 🔍 Query: %s\n", i+1, q)
		eventChan, err := r.Run(ctx, "user", fmt.Sprintf("session-%d", i),
			model.NewUserMessage(q))
		if err != nil {
			log.Printf("Query failed: %v", err)
			continue
		}

		fmt.Print("   🤖 Response: ")
		for evt := range eventChan {
			util.PrintEventWithToolCalls(evt)
			if evt.IsFinalResponse() && len(evt.Response.Choices) > 0 {
				fmt.Println(evt.Response.Choices[0].Message.Content)
			}
		}
	}
}
