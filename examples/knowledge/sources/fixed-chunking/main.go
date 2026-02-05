//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main demonstrates using fixed-size chunking strategy for document splitting.
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
	"trpc.group/trpc-go/trpc-agent-go/knowledge/chunking"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/document"
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
	fmt.Println("Fixed-Size Chunking Demo")
	fmt.Println("=========================")

	// Create fixed-size chunking strategy with custom chunk size and overlap
	// FixedSizeChunking splits text into fixed-size chunks with optional overlap
	fixedChunking := chunking.NewFixedSizeChunking(
		chunking.WithChunkSize(100), // Each chunk will be at most 100 characters
		chunking.WithOverlap(10),    // 10 characters overlap between consecutive chunks
	)

	// Demonstrate chunking result before loading to knowledge base
	fmt.Println("\n--- Chunking Result Preview ---")
	filePath := util.ExampleDataPath("file/llm.md")
	content, err := os.ReadFile(filePath)
	if err != nil {
		log.Fatalf("Failed to read file: %v", err)
	}

	// Create a document and apply chunking
	doc := &document.Document{
		ID:      "llm-doc",
		Name:    "llm.md",
		Content: string(content),
	}

	chunks, err := fixedChunking.Chunk(doc)
	if err != nil {
		log.Fatalf("Failed to chunk document: %v", err)
	}

	fmt.Printf("Original document size: %d characters\n", len(content))
	fmt.Printf("Number of chunks: %d\n", len(chunks))
	fmt.Println("\nChunk details(first 3):")
	for i := 0; i < min(len(chunks), 3); i++ {
		chunk := chunks[i]
		preview := chunk.Content
		preview = strings.ReplaceAll(preview, "\n", "\\n")
		fmt.Printf("  Chunk %d: ID=%s, Size=%d chars\n", i+1, chunk.ID, len(chunk.Content))
		fmt.Printf("    Preview: %s\n", preview)
	}
	fmt.Println("--- End of Chunking Preview ---")

	// Create file source with custom chunking strategy
	sources := []source.Source{
		file.New(
			[]string{filePath},
			file.WithName("LLM Docs"),
			file.WithMetadataValue("chunking", "fixed"),
			file.WithCustomChunkingStrategy(fixedChunking), // Use fixed-size chunking
		),
	}

	// Create knowledge base
	storeType := util.VectorStoreType(*vectorStore)
	vs, err := util.NewVectorStoreByType(storeType)
	if err != nil {
		log.Fatalf("Failed to create vector store: %v", err)
	}
	fmt.Printf("Vector Store: %s\n", storeType)
	fmt.Printf("Chunk Size: 100, Overlap: 10\n")

	kb := knowledge.New(
		knowledge.WithVectorStore(vs),
		knowledge.WithEmbedder(openai.New()),
		knowledge.WithSources(sources),
	)

	fmt.Println("\nLoading document with fixed-size chunking...")
	if err := kb.Load(ctx, knowledge.WithShowProgress(true)); err != nil {
		log.Fatalf("Failed to load: %v", err)
	}

	// Create knowledge search tool
	searchTool := knowledgetool.NewKnowledgeSearchTool(kb)

	// Create agent
	agent := llmagent.New(
		"fixed-chunking-assistant",
		llmagent.WithModel(openaimodel.New(modelName)),
		llmagent.WithTools([]tool.Tool{searchTool}),
	)

	// Create runner
	r := runner.NewRunner(
		"fixed-chunking-chat",
		agent,
		runner.WithSessionService(sessioninmemory.NewSessionService()),
	)
	defer r.Close()

	// Test query
	fmt.Println("\nTesting knowledge search with fixed-size chunks...")
	eventChan, err := r.Run(ctx, "user", "session-1",
		model.NewUserMessage("What is a Large Language Model and how does it work?"))
	if err != nil {
		log.Fatalf("Run failed: %v", err)
	}

	fmt.Print("Response: ")
	for evt := range eventChan {
		util.PrintEventWithToolCalls(evt)
		if evt.IsFinalResponse() && len(evt.Response.Choices) > 0 {
			fmt.Println(evt.Response.Choices[0].Message.Content)
		}
	}
}
