//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main demonstrates using recursive chunking strategy for document splitting.
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
	fmt.Println("Recursive Chunking Demo")
	fmt.Println("========================")

	// Create recursive chunking strategy with custom parameters
	// RecursiveChunking uses a hierarchy of separators to split text intelligently
	// It tries to split by paragraph first, then by line, then by space, then by character
	recursiveChunking := chunking.NewRecursiveChunking(
		chunking.WithRecursiveChunkSize(1000), // Maximum chunk size
		chunking.WithRecursiveOverlap(0),      // No overlap between chunks
		// Custom separators (optional, defaults are used if not specified):
		// Default separators: ["\n\n", "\n", " ", ""]
		// This means: split by paragraph -> line -> sentence -> space
		// If still exceeds chunkSize, force split by chunkSize
		chunking.WithRecursiveSeparators([]string{"\n\n", "\n", ". ", " "}),
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

	chunks, err := recursiveChunking.Chunk(doc)
	if err != nil {
		log.Fatalf("Failed to chunk document: %v", err)
	}

	fmt.Printf("Original document size: %d characters\n", len(content))
	fmt.Printf("Number of chunks: %d\n", len(chunks))
	fmt.Printf("Separators: [paragraph(\\n\\n), line(\\n), sentence(. ), space( ), char]\n")
	fmt.Println("\nChunk details(first 3):")
	for i := 0; i < min(len(chunks), 3); i++ {
		chunk := chunks[i]
		preview := chunk.Content
		preview = strings.ReplaceAll(preview, "\n", "\\n")
		fmt.Printf("  Chunk %d: ID=%s, Size=%d chars\n", i+1, chunk.ID, len(chunk.Content))
		fmt.Printf("    Preview: %s\n", preview)
	}
	fmt.Println("--- End of Chunking Preview ---")

	// Create file source with recursive chunking strategy
	sources := []source.Source{
		file.New(
			[]string{filePath},
			file.WithName("LLM Docs"),
			file.WithMetadataValue("chunking", "recursive"),
			file.WithCustomChunkingStrategy(recursiveChunking), // Use recursive chunking
		),
		file.New(
			[]string{util.ExampleDataPath("file/golang.md")},
			file.WithName("Golang Docs"),
			file.WithMetadataValue("chunking", "recursive"),
			file.WithCustomChunkingStrategy(recursiveChunking),
		),
	}

	// Create knowledge base
	storeType := util.VectorStoreType(*vectorStore)
	vs, err := util.NewVectorStoreByType(storeType)
	if err != nil {
		log.Fatalf("Failed to create vector store: %v", err)
	}
	fmt.Printf("Vector Store: %s\n", storeType)
	fmt.Printf("Chunk Size: 1000, Overlap: 0\n")
	fmt.Printf("Separators: [paragraph, line, sentence, space, char]\n")

	kb := knowledge.New(
		knowledge.WithVectorStore(vs),
		knowledge.WithEmbedder(openai.New()),
		knowledge.WithSources(sources),
	)

	fmt.Println("\nLoading documents with recursive chunking...")
	if err := kb.Load(ctx, knowledge.WithShowProgress(true)); err != nil {
		log.Fatalf("Failed to load: %v", err)
	}

	// Create knowledge search tool
	searchTool := knowledgetool.NewKnowledgeSearchTool(kb)

	// Create agent
	agent := llmagent.New(
		"recursive-chunking-assistant",
		llmagent.WithModel(openaimodel.New(modelName)),
		llmagent.WithTools([]tool.Tool{searchTool}),
	)

	// Create runner
	r := runner.NewRunner(
		"recursive-chunking-chat",
		agent,
		runner.WithSessionService(sessioninmemory.NewSessionService()),
	)
	defer r.Close()

	// Test queries
	queries := []string{
		"What is a Large Language Model?",
		"How do you declare variables in Go?",
	}

	for i, q := range queries {
		fmt.Printf("\n%d. Query: %s\n", i+1, q)
		eventChan, err := r.Run(ctx, "user", fmt.Sprintf("session-%d", i),
			model.NewUserMessage(q))
		if err != nil {
			log.Printf("Query failed: %v", err)
			continue
		}

		fmt.Print("   Response: ")
		for evt := range eventChan {
			util.PrintEventWithToolCalls(evt)
			if evt.IsFinalResponse() && len(evt.Response.Choices) > 0 {
				fmt.Println(evt.Response.Choices[0].Message.Content)
			}
		}
	}
}
