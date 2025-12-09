//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main demonstrates using directory sources.
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
	"trpc.group/trpc-go/trpc-agent-go/knowledge/source/dir"
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
	fmt.Println("📁 Directory Source Demo")
	fmt.Println("========================")

	// Create directory source - recursively loads all supported files
	src := dir.New(
		[]string{util.ExampleDataPath("dir")},
		dir.WithName("Documentation Directory"),
		dir.WithMetadataValue("source_type", "directory"),
		dir.WithMetadataValue("category", "docs"),
	)

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
		knowledge.WithSources([]source.Source{src}),
	)

	fmt.Println("\n📥 Loading directory contents...")
	if err := kb.Load(ctx, knowledge.WithShowProgress(true)); err != nil {
		log.Fatalf("Failed to load: %v", err)
	}

	// Create knowledge search tool
	searchTool := knowledgetool.NewKnowledgeSearchTool(kb)

	// Create agent
	agent := llmagent.New(
		"dir-assistant",
		llmagent.WithModel(openaimodel.New(modelName)),
		llmagent.WithTools([]tool.Tool{searchTool}),
	)

	// Create runner
	r := runner.NewRunner(
		"dir-chat",
		agent,
		runner.WithSessionService(sessioninmemory.NewSessionService()),
	)
	defer r.Close()

	// Test query
	fmt.Println("\n🔍 Searching knowledge base...")
	eventChan, err := r.Run(ctx, "user", "session-1",
		model.NewUserMessage("What topics are covered in the documentation?"))
	if err != nil {
		log.Fatalf("Run failed: %v", err)
	}

	fmt.Print("🤖 Response: ")
	for evt := range eventChan {
		util.PrintEventWithToolCalls(evt)
		if evt.IsFinalResponse() && len(evt.Response.Choices) > 0 {
			fmt.Println(evt.Response.Choices[0].Message.Content)
		}
	}
}
