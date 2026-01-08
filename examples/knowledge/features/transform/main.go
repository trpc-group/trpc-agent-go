//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main demonstrates document transformation capabilities using transformers.
//
// Required environment variables:
//   - OPENAI_API_KEY: Your OpenAI API key for embeddings
//   - OPENAI_BASE_URL: (Optional) Custom OpenAI API endpoint
//
// Example usage:
//
//	export OPENAI_API_KEY=sk-xxxx
//	export OPENAI_BASE_URL=https://api.openai.com/v1
//	go run main.go
package main

import (
	"context"
	"fmt"
	"log"

	util "trpc.group/trpc-go/trpc-agent-go/examples/knowledge"
	"trpc.group/trpc-go/trpc-agent-go/knowledge"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/embedder/openai"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/source"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/source/file"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/transform"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore/inmemory"

	_ "trpc.group/trpc-go/trpc-agent-go/knowledge/document/reader/pdf"
)

func main() {
	ctx := context.Background()

	fmt.Println("Transform Demo")
	fmt.Println("==============")

	// Example 1: No transformer (baseline)
	fmt.Println("\n1. No Transformer (baseline)")
	runDemo(ctx, "No Transform")

	// Example 2: CharFilter - Remove specific characters
	fmt.Println("\n2. CharFilter: Remove tabs and carriage returns")
	charFilter := transform.NewCharFilter("\t", "\r", "\n")
	runDemo(ctx, "CharFilter", charFilter)

	// Example 3: CharDedup - Collapse consecutive repeated characters
	fmt.Println("\n3. CharDedup: Collapse consecutive spaces and newlines")
	charDedup := transform.NewCharDedup(" ", "\n")
	runDemo(ctx, "CharDedup", charDedup)

	// Example 4: Combined transformers
	fmt.Println("\n4. Combined: CharFilter + CharDedup")
	filter := transform.NewCharFilter("\t", "\r")
	dedup := transform.NewCharDedup(" ", "\n")
	runDemo(ctx, "Combined", filter, dedup)
}

func runDemo(ctx context.Context, demoName string, transformers ...transform.Transformer) {
	// Create source with transformers
	opts := []file.Option{file.WithName(demoName)}
	if len(transformers) > 0 {
		opts = append(opts, file.WithTransformers(transformers...))
	}

	src := file.New(
		[]string{util.ExampleDataPath("file/test.pdf")},
		opts...,
	)

	// Create knowledge base
	kb := knowledge.New(
		knowledge.WithVectorStore(inmemory.New()),
		knowledge.WithEmbedder(openai.New()),
		knowledge.WithSources([]source.Source{src}),
	)

	if err := kb.Load(ctx); err != nil {
		log.Printf("Failed to load %s: %v", demoName, err)
		return
	}

	// Show document info
	docInfos, err := kb.ShowDocumentInfo(ctx)
	if err != nil {
		log.Printf("Failed to get document info: %v", err)
		return
	}

	fmt.Printf("   Source: %s\n", demoName)
	fmt.Printf("   Total chunks: %d\n", len(docInfos))

	// Search to get all chunks content
	result, err := kb.Search(ctx, &knowledge.SearchRequest{
		Query:      "",
		MaxResults: len(docInfos),
	})
	if err != nil {
		log.Printf("Failed to search %s: %v", demoName, err)
		return
	}

	// Show all chunks
	for i, res := range result.Documents {
		content := res.Document.Content
		if len(content) > 100 {
			content = content[:100] + "..."
		}
		fmt.Printf("   Chunk %d: %q\n", i+1, content)
	}
}
