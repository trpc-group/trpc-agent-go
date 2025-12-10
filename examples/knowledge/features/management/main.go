//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main demonstrates knowledge management operations.
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
	openaiembedder "trpc.group/trpc-go/trpc-agent-go/knowledge/embedder/openai"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/source"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/source/file"
)

var (
	vectorStore = flag.String("vectorstore", "inmemory", "Vector store type: inmemory|pgvector|tcvector|elasticsearch")
)

func main() {
	flag.Parse()
	ctx := context.Background()

	fmt.Println("📚 Knowledge Management Demo")
	fmt.Println("============================")

	// Step 1: Create initial knowledge base with one source
	fmt.Println("\n1️⃣ Creating knowledge base with initial source...")
	llmSource := file.New(
		[]string{util.ExampleDataPath("file/llm.md")},
		file.WithName("LLMDocs"),
		file.WithMetadata(map[string]any{"topic": "llm", "category": "documentation"}),
	)

	storeType := util.VectorStoreType(*vectorStore)
	vs, err := util.NewVectorStoreByType(storeType)
	if err != nil {
		log.Fatalf("Failed to create vector store: %v", err)
	}
	fmt.Printf("Vector Store: %s\n", storeType)

	kb := knowledge.New(
		knowledge.WithEmbedder(openaiembedder.New()),
		knowledge.WithVectorStore(vs),
		knowledge.WithSources([]source.Source{llmSource}),
		knowledge.WithEnableSourceSync(true),
	)

	if err := kb.Load(ctx); err != nil {
		log.Fatalf("Failed to load: %v", err)
	}
	fmt.Println("   ✅ Initial source loaded")

	// Wait for data refresh if needed
	util.WaitForIndexRefresh(storeType)

	showSources(ctx, kb)

	// Step 2: Add a new source dynamically
	fmt.Println("\n2️⃣ Adding new source (GolangDocs)...")
	golangSource := file.New(
		[]string{util.ExampleDataPath("file/golang.md")},
		file.WithName("GolangDocs"),
		file.WithMetadata(map[string]any{"topic": "programming", "category": "documentation"}),
	)

	if err := kb.AddSource(ctx, golangSource); err != nil {
		log.Printf("   ❌ Failed to add source: %v", err)
	} else {
		fmt.Println("   ✅ Source added successfully")
	}

	// Wait for data refresh if needed
	util.WaitForIndexRefresh(storeType)

	showSources(ctx, kb)

	// Step 3: Search across all sources
	fmt.Println("\n3️⃣ Searching for 'machine learning'...")
	result, err := kb.Search(ctx, &knowledge.SearchRequest{
		Query:      "machine learning",
		MaxResults: 2,
	})
	if err != nil {
		log.Printf("   ❌ Search failed: %v", err)
	} else {
		printSearchResults(result)
	}

	// Step 4: Reload a source (simulate content update)
	fmt.Println("\n4️⃣ Reloading source (LLMDocs)...")
	reloadSource := file.New(
		[]string{util.ExampleDataPath("file/llm.md")},
		file.WithName("LLMDocs"),
		file.WithMetadata(map[string]any{"topic": "llm", "category": "documentation", "version": "v2"}),
	)
	if err := kb.ReloadSource(ctx, reloadSource); err != nil {
		log.Printf("   ❌ Failed to reload: %v", err)
	} else {
		fmt.Println("   ✅ Source reloaded with new metadata")
	}

	// Wait for data refresh if needed
	util.WaitForIndexRefresh(storeType)

	showSources(ctx, kb)

	// Step 5: Remove a source
	fmt.Println("\n5️⃣ Removing source (GolangDocs)...")
	if err := kb.RemoveSource(ctx, "GolangDocs"); err != nil {
		log.Printf("   ❌ Failed to remove: %v", err)
	} else {
		fmt.Println("   ✅ Source removed")
	}

	// Wait for data refresh if needed
	util.WaitForIndexRefresh(storeType)

	showSources(ctx, kb)

	// Step 6: Search again to verify
	fmt.Println("\n6️⃣ Searching after removal...")
	result, err = kb.Search(ctx, &knowledge.SearchRequest{
		Query:      "programming concepts",
		MaxResults: 2,
	})
	if err != nil {
		log.Printf("   ❌ Search failed: %v", err)
	} else {
		printSearchResults(result)
	}

	fmt.Println("\n✅ Demo completed!")
}

func showSources(ctx context.Context, kb *knowledge.BuiltinKnowledge) {
	docInfos, err := kb.ShowDocumentInfo(ctx)
	if err != nil {
		fmt.Printf("   Error: %v\n", err)
		return
	}

	// Count documents per source
	sourceCounts := make(map[string]int)
	sourceMetadata := make(map[string]map[string]any)
	for _, info := range docInfos {
		sourceCounts[info.SourceName]++
		if sourceMetadata[info.SourceName] == nil {
			sourceMetadata[info.SourceName] = filterInternalMetadata(info.AllMeta)
		}
	}

	fmt.Printf("   Sources: %d, Total documents: %d\n", len(sourceCounts), len(docInfos))
	for name, count := range sourceCounts {
		fmt.Printf("   - %s: %d docs, metadata: %v\n", name, count, sourceMetadata[name])
	}
}

func printSearchResults(result *knowledge.SearchResult) {
	fmt.Printf("   Found %d results:\n", len(result.Documents))
	for i, doc := range result.Documents {
		sourceName := ""
		if name, ok := doc.Document.Metadata[source.MetaSourceName].(string); ok {
			sourceName = name
		}
		content := doc.Document.Content
		if len(content) > 80 {
			content = content[:80] + "..."
		}
		fmt.Printf("   %d. [%s] score=%.3f: %s\n", i+1, sourceName, doc.Score, content)
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
