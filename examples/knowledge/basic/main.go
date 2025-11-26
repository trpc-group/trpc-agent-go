//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main demonstrates basic knowledge integration.
// This is the simplest example to get started with knowledge-enhanced chat.
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
	"os"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	util "trpc.group/trpc-go/trpc-agent-go/examples/knowledge"
	"trpc.group/trpc-go/trpc-agent-go/knowledge"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/embedder/openai"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/source"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/source/file"
	knowledgetool "trpc.group/trpc-go/trpc-agent-go/knowledge/tool"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/model"
	openaimodel "trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	sessioninmemory "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

const (
	defaultModelName = "deepseek-chat"
	defaultQuery     = "What are Large Language Models and how do they work?"
)

func main() {
	var query string
	flag.StringVar(&query, "query", defaultQuery, "Query to ask the knowledge base")
	flag.Parse()

	ctx := context.Background()

	modelName := getEnvOrDefault("MODEL_NAME", defaultModelName)

	fmt.Println("🧠 Basic Knowledge Chat Demo")
	fmt.Printf("Model: %s\n", modelName)
	fmt.Println(strings.Repeat("=", 50))

	// 1. Create file source
	src := file.New(
		[]string{"../exampledata/file/llm.md"},
	)

	// 2. Create vector store (in-memory)
	vs := inmemory.New()

	// 3. Create embedder (OpenAI)
	emb := openai.New()

	// 4. Create knowledge base
	kb := knowledge.New(
		knowledge.WithVectorStore(vs),
		knowledge.WithEmbedder(emb),
		knowledge.WithSources([]source.Source{src}),
	)

	// 5. Load knowledge base
	if err := kb.Load(ctx); err != nil {
		log.Fatalf("Failed to load knowledge: %v", err)
	}

	// 6. Create knowledge search tool
	searchTool := knowledgetool.NewKnowledgeSearchTool(kb)

	// 7. Create agent with tools
	agent := llmagent.New(
		"basic-assistant",
		llmagent.WithModel(openaimodel.New(modelName)),
		llmagent.WithTools([]tool.Tool{searchTool}),
		llmagent.WithGenerationConfig(model.GenerationConfig{
			Temperature: floatPtr(0.7),
			MaxTokens:   intPtr(1000),
		}),
	)

	// 8. Create runner
	r := runner.NewRunner(
		"basic-chat",
		agent,
		runner.WithSessionService(sessioninmemory.NewSessionService()),
	)
	defer r.Close()

	// 9. Run query
	fmt.Printf("\n💬 Query: %s\n", query)
	fmt.Println(strings.Repeat("=", 50))

	eventChan, err := r.Run(ctx, "user", "session-1", model.NewUserMessage(query))
	if err != nil {
		log.Fatalf("❌ Error: %v", err)
	}

	// Process events and print tool calls/responses
	var fullResponse strings.Builder
	for evt := range eventChan {
		util.PrintEventWithToolCalls(evt)

		if len(evt.Response.Choices) == 0 {
			continue
		}

		choice := evt.Response.Choices[0]

		// Collect streaming content
		if choice.Delta.Content != "" {
			fullResponse.WriteString(choice.Delta.Content)
		}

		// Print final response
		if evt.IsFinalResponse() {
			if fullResponse.Len() > 0 {
				fmt.Printf("\n🤖 Final Answer:\n%s\n", fullResponse.String())
			} else if choice.Message.Content != "" {
				fmt.Printf("\n🤖 Final Answer:\n%s\n", choice.Message.Content)
			}
		}
	}

	fmt.Println("\n✅ Done!")
}

func floatPtr(f float64) *float64 { return &f }
func intPtr(i int) *int           { return &i }

func getEnvOrDefault(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}
