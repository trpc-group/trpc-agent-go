//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main demonstrates programmatic metadata filtering using search tools.
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
	"trpc.group/trpc-go/trpc-agent-go/knowledge/searchfilter"
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

	fmt.Println("🔎 Metadata Filter Demo")
	fmt.Println("=======================")
	fmt.Printf("Model: %s\n", modelName)

	// Create sources with metadata
	sources := []source.Source{
		file.New(
			[]string{util.ExampleDataPath("file/llm.md")},
			file.WithName("LLM Docs"),
			file.WithMetadataValue("topic", "machine_learning"),
			file.WithMetadataValue("difficulty", "advanced"),
		),
		file.New(
			[]string{util.ExampleDataPath("file/golang.md")},
			file.WithName("Golang Docs"),
			file.WithMetadataValue("topic", "programming"),
			file.WithMetadataValue("difficulty", "beginner"),
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

	// Wait for data refresh if needed
	util.WaitForIndexRefresh(storeType)

	// Example 1: Simple equality filter using WithFilter
	fmt.Println("\n1️⃣ Filter: metadata.topic=programming (using WithFilter)")
	programmingTool := knowledgetool.NewKnowledgeSearchTool(
		kb,
		knowledgetool.WithToolName("search_programming"),
		knowledgetool.WithToolDescription("Search programming-related documentation"),
		knowledgetool.WithFilter(map[string]any{
			"metadata.topic": "programming",
		}),
		knowledgetool.WithMaxResults(5),
	)
	runToolDemo(ctx, modelName, programmingTool, "What are the key features of Go programming language?")

	// Example 2: OR filter using WithConditionedFilter
	fmt.Println("\n2️⃣ Filter: metadata.topic=programming OR metadata.topic=machine_learning (using WithConditionedFilter)")
	orFilterTool := knowledgetool.NewKnowledgeSearchTool(
		kb,
		knowledgetool.WithToolName("search_all_topics"),
		knowledgetool.WithToolDescription("Search all technical documentation"),
		knowledgetool.WithConditionedFilter(
			searchfilter.Or(
				searchfilter.Equal("metadata.topic", "programming"),
				searchfilter.Equal("metadata.topic", "machine_learning"),
			),
		),
		knowledgetool.WithMaxResults(5),
	)
	runToolDemo(ctx, modelName, orFilterTool, "What advanced topics are covered?")

	// Example 3: AND filter using WithConditionedFilter
	fmt.Println("\n3️⃣ Filter: metadata.topic=programming AND metadata.difficulty=beginner (using WithConditionedFilter)")
	andFilterTool := knowledgetool.NewKnowledgeSearchTool(
		kb,
		knowledgetool.WithToolName("search_beginner_programming"),
		knowledgetool.WithToolDescription("Search beginner-level programming documentation"),
		knowledgetool.WithConditionedFilter(
			searchfilter.And(
				searchfilter.Equal("metadata.topic", "programming"),
				searchfilter.Equal("metadata.difficulty", "beginner"),
			),
		),
		knowledgetool.WithMaxResults(5),
	)
	runToolDemo(ctx, modelName, andFilterTool, "How do I get started with Go?")

	// Example 4: Conflicting parameter names test
	fmt.Println("\n4️⃣ Filter: metadata.difficulty=beginner OR metadata.difficulty=advanced (Parameter conflict test)")
	conflictFilterTool := knowledgetool.NewKnowledgeSearchTool(
		kb,
		knowledgetool.WithToolName("search_any_difficulty"),
		knowledgetool.WithToolDescription("Search documentation of any difficulty"),
		knowledgetool.WithConditionedFilter(
			searchfilter.Or(
				searchfilter.Equal("metadata.difficulty", "beginner"),
				searchfilter.Equal("metadata.difficulty", "advanced"),
			),
		),
		knowledgetool.WithMaxResults(5),
	)
	runToolDemo(ctx, modelName, conflictFilterTool, "List all documents with any difficulty level.")
}

func runToolDemo(ctx context.Context, modelName string, searchTool tool.Tool, query string) {
	agent := llmagent.New(
		"filter-assistant",
		llmagent.WithModel(openaimodel.New(modelName)),
		llmagent.WithTools([]tool.Tool{searchTool}),
	)

	r := runner.NewRunner(
		"filter-chat",
		agent,
		runner.WithSessionService(sessioninmemory.NewSessionService()),
	)
	defer r.Close()

	fmt.Printf("   🔍 Query: %s\n", query)
	eventChan, err := r.Run(ctx, "user", "session-1", model.NewUserMessage(query))
	if err != nil {
		log.Printf("Query failed: %v", err)
		return
	}

	fmt.Print("   🤖 Response: ")
	for evt := range eventChan {
		util.PrintEventWithToolCalls(evt)
		if evt.IsFinalResponse() && len(evt.Response.Choices) > 0 {
			fmt.Println(evt.Response.Choices[0].Message.Content)
		}
	}
}
