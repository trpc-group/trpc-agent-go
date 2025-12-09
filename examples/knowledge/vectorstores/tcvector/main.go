//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main demonstrates using Tencent VectorDB for vector storage.
//
// Required environment variables:
//   - OPENAI_API_KEY: Your OpenAI API key for LLM and embeddings
//   - OPENAI_BASE_URL: (Optional) Custom OpenAI API endpoint
//   - MODEL_NAME: (Optional) Model name to use, defaults to deepseek-chat
//   - TCVECTOR_URL: Tencent VectorDB URL
//   - TCVECTOR_USERNAME: Tencent VectorDB username
//   - TCVECTOR_PASSWORD: Tencent VectorDB password
//
// Example usage:
//
//	export OPENAI_BASE_URL=xxx
//	export OPENAI_API_KEY=xxx
//	export MODEL_NAME=xxx
//	export TCVECTOR_URL=xxx
//	export TCVECTOR_USERNAME=xxx
//	export TCVECTOR_PASSWORD=xxx
//	go run main.go
package main

import (
	"context"
	"fmt"
	"log"

	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	util "trpc.group/trpc-go/trpc-agent-go/examples/knowledge"
	"trpc.group/trpc-go/trpc-agent-go/knowledge"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/embedder/openai"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/source"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/source/file"
	knowledgetool "trpc.group/trpc-go/trpc-agent-go/knowledge/tool"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore/tcvector"
	"trpc.group/trpc-go/trpc-agent-go/model"
	openaimodel "trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	sessioninmemory "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

var (
	modelName  = util.GetEnvOrDefault("MODEL_NAME", "deepseek-chat")
	url        = util.GetEnvOrDefault("TCVECTOR_URL", "")
	username   = util.GetEnvOrDefault("TCVECTOR_USERNAME", "")
	password   = util.GetEnvOrDefault("TCVECTOR_PASSWORD", "")
	collection = util.GetEnvOrDefault("TCVECTOR_COLLECTION", "trpc_example")
)

func main() {
	ctx := context.Background()

	fmt.Println("🔮 Tencent VectorDB Demo")
	fmt.Println("========================")

	if url == "" || username == "" || password == "" {
		log.Fatal("TCVECTOR_URL, TCVECTOR_USERNAME, and TCVECTOR_PASSWORD are required")
	}

	fmt.Printf("📊 Connecting to Tencent VectorDB: %s\\n", url)

	// Create TCVector store
	vs, err := tcvector.New(
		tcvector.WithURL(url),
		tcvector.WithUsername(username),
		tcvector.WithPassword(password),
		tcvector.WithCollection(collection),
		tcvector.WithFilterAll(true),
	)
	if err != nil {
		log.Fatalf("Failed to create vector store: %v", err)
	}

	// Create file source
	src := file.New(
		[]string{util.ExampleDataPath("file/llm.md")},
		file.WithName("LLM Docs"),
	)

	// Create knowledge base
	kb := knowledge.New(
		knowledge.WithVectorStore(vs),
		knowledge.WithEmbedder(openai.New()),
		knowledge.WithSources([]source.Source{src}),
	)

	fmt.Println("\n📥 Loading knowledge into Tencent VectorDB...")
	if err := kb.Load(ctx, knowledge.WithShowProgress(true)); err != nil {
		log.Fatalf("Failed to load: %v", err)
	}

	// Create knowledge search tool
	searchTool := knowledgetool.NewKnowledgeSearchTool(kb)

	// Create agent
	agent := llmagent.New(
		"tcvector-assistant",
		llmagent.WithModel(openaimodel.New(modelName)),
		llmagent.WithTools([]tool.Tool{searchTool}),
	)

	// Create runner
	r := runner.NewRunner(
		"tcvector-chat",
		agent,
		runner.WithSessionService(sessioninmemory.NewSessionService()),
	)
	defer r.Close()

	// Test query
	fmt.Println("\n🔍 Querying knowledge from Tencent VectorDB...")
	eventChan, err := r.Run(ctx, "user", "session-1",
		model.NewUserMessage("What are Large Language Models?"))
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

	fmt.Println("\n✅ Data persisted in Tencent VectorDB!")
}
