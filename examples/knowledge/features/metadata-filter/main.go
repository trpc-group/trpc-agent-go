//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main demonstrates programmatic metadata filtering.
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
	"strings"

	util "trpc.group/trpc-go/trpc-agent-go/examples/knowledge"
	"trpc.group/trpc-go/trpc-agent-go/knowledge"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/embedder/openai"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/searchfilter"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/source"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/source/file"
)

var (
	vectorStore = flag.String("vectorstore", "inmemory", "Vector store type: inmemory|pgvector|tcvector|elasticsearch")
)

func main() {
	flag.Parse()
	ctx := context.Background()

	fmt.Println("🔎 Metadata Filter Demo")
	fmt.Println("=======================")

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

	// Example 1: Simple equality filter
	// Note: Use "metadata." prefix for metadata fields in filter conditions
	fmt.Println("\n1️⃣ Filter: metadata.topic=programming")
	result, err := kb.Search(ctx, &knowledge.SearchRequest{
		Query:      "programming concepts",
		MaxResults: 5,
		SearchFilter: &knowledge.SearchFilter{
			FilterCondition: searchfilter.Equal("metadata.topic", "programming"),
		},
	})
	if err != nil {
		log.Printf("Search failed: %v", err)
	} else {
		printResults(result)
	}

	// Example 2: OR filter
	fmt.Println("\n2️⃣ Filter: metadata.topic=programming OR metadata.topic=machine_learning")
	result, err = kb.Search(ctx, &knowledge.SearchRequest{
		Query:      "advanced topics",
		MaxResults: 5,
		SearchFilter: &knowledge.SearchFilter{
			FilterCondition: searchfilter.Or(
				searchfilter.Equal("metadata.topic", "programming"),
				searchfilter.Equal("metadata.topic", "machine_learning"),
			),
		},
	})
	if err != nil {
		log.Printf("Search failed: %v", err)
	} else {
		printResults(result)
	}

	// Example 3: AND filter
	fmt.Println("\n3️⃣ Filter: metadata.topic=programming AND metadata.difficulty=beginner")
	result, err = kb.Search(ctx, &knowledge.SearchRequest{
		Query:      "basics",
		MaxResults: 5,
		SearchFilter: &knowledge.SearchFilter{
			FilterCondition: searchfilter.And(
				searchfilter.Equal("metadata.topic", "programming"),
				searchfilter.Equal("metadata.difficulty", "beginner"),
			),
		},
	})
	if err != nil {
		log.Printf("Search failed: %v", err)
	} else {
		printResults(result)
	}
}

func printResults(result *knowledge.SearchResult) {
	fmt.Printf("   Found %d results:\n", len(result.Documents))
	for i, doc := range result.Documents {
		fmt.Printf("   %d. %s (score: %.3f)\n", i+1, doc.Document.Name, doc.Score)
		fmt.Printf("      Metadata: %v\n", filterInternalMetadata(doc.Document.Metadata))
	}
}

func filterInternalMetadata(metadata map[string]any) map[string]any {
	filtered := make(map[string]any)
	for k, v := range metadata {
		if !strings.HasPrefix(k, source.MetaPrefix) {
			filtered[k] = v
		}
	}
	return filtered
}
