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
	"trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore/inmemory"

	_ "trpc.group/trpc-go/trpc-agent-go/knowledge/document/reader/text"
)

func main() {
	ctx := context.Background()

	fmt.Println("Transform Demo")
	fmt.Println("==============")

	// Example 1: No transformer (baseline)
	// Shows original content with tabs, multiple spaces, and "xxxxx" patterns
	fmt.Println("\n1. No Transformer (baseline)")
	runDemo(ctx, "No Transform")

	// Example 2: CharFilter - Remove tab characters
	// Removes all \t characters from the content
	fmt.Println("\n2. CharFilter: Remove tabs")
	charFilter := transform.NewCharFilter("\t")
	runDemo(ctx, "CharFilter", charFilter)

	// Example 3: CharDedup - Collapse consecutive spaces and 'x' characters
	// "     " -> " " and "xxxxx" -> "x"
	fmt.Println("\n3. CharDedup: Collapse consecutive spaces and 'x' characters")
	charDedup := transform.NewCharDedup(" ", "x")
	runDemo(ctx, "CharDedup", charDedup)

	// Example 4: Combined transformers
	// First remove tabs, then collapse spaces and 'x' characters
	fmt.Println("\n4. Combined: CharFilter(tabs) + CharDedup(spaces, x)")
	filter := transform.NewCharFilter("\t")
	dedup := transform.NewCharDedup(" ", "x")
	runDemo(ctx, "Combined", filter, dedup)
}

func runDemo(ctx context.Context, demoName string, transformers ...transform.Transformer) {
	// Create source with transformers
	opts := []file.Option{file.WithName(demoName)}
	if len(transformers) > 0 {
		opts = append(opts, file.WithTransformers(transformers...))
	}

	src := file.New(
		[]string{util.ExampleDataPath("file/content_transform.md")},
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
		SearchMode: vectorstore.SearchModeFilter,
	})
	if err != nil {
		log.Printf("Failed to search %s: %v", demoName, err)
		return
	}

	// Show all chunks
	for i, res := range result.Documents {
		content := res.Document.Content
		fmt.Printf("   Chunk %d: %q\n", i+1, content)
	}
}
