//go:build tesseract
// +build tesseract

//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package main demonstrates PDF OCR capability with knowledge base integration.
//
// Required environment variables:
//   - OPENAI_API_KEY: Your OpenAI API key for embeddings
//   - OPENAI_BASE_URL: (Optional) Custom OpenAI API endpoint
//
// Example usage:
//
//	export OPENAI_API_KEY=sk-xxxx
//	go run -tags tesseract main.go -vectorstore inmemory
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"path/filepath"
	"strings"
	"time"

	util "trpc.group/trpc-go/trpc-agent-go/examples/knowledge"
	"trpc.group/trpc-go/trpc-agent-go/knowledge"
	openaiembedder "trpc.group/trpc-go/trpc-agent-go/knowledge/embedder/openai"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/ocr/tesseract"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/source"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/source/dir"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore"

	// Import PDF reader to register it.
	_ "trpc.group/trpc-go/trpc-agent-go/knowledge/document/reader/pdf"
)

// Command line flags.
var (
	vectorStore = flag.String("vectorstore", "inmemory", "Vector store type: inmemory|pgvector|tcvector|elasticsearch")
	query       = flag.String("query", "What is trpc-agent-go?", "Query to search in the knowledge base")
)

// Default values.
const (
	defaultEmbeddingModel = "text-embedding-3-small"
	dataDir               = "./data"
)

func main() {
	flag.Parse()
	ctx := context.Background()

	storeType := util.VectorStoreType(*vectorStore)

	fmt.Println("PDF OCR Knowledge Demo")
	fmt.Println(strings.Repeat("=", 50))
	fmt.Printf("Data Directory: %s\n", dataDir)
	fmt.Println("OCR Engine: Tesseract")
	fmt.Printf("Vector Store: %s\n", storeType)
	fmt.Println(strings.Repeat("=", 50))

	// Setup knowledge base.
	kb, err := setupKnowledgeBase(ctx, storeType)
	if err != nil {
		log.Fatalf("Failed to setup knowledge base: %v", err)
	}

	// Show stats.
	fmt.Println("\n📊 Knowledge Base Statistics")
	fmt.Println(strings.Repeat("-", 50))
	showStats(ctx, kb, storeType)

	// Run query.
	fmt.Printf("\n🔍 Query: %s\n", *query)
	fmt.Println(strings.Repeat("-", 50))
	if err := search(ctx, kb, *query); err != nil {
		log.Printf("Search error: %v", err)
	}

	fmt.Println("\n✅ Done!")
}

// setupKnowledgeBase creates and loads the knowledge base with PDF OCR support.
func setupKnowledgeBase(ctx context.Context, storeType util.VectorStoreType) (*knowledge.BuiltinKnowledge, error) {
	fmt.Println("\nSetting up knowledge base...")

	// Create Tesseract OCR extractor.
	fmt.Println("  Creating Tesseract OCR engine...")
	ocrExtractor, err := tesseract.New(
		tesseract.WithLanguage("eng+chi_sim"),
		tesseract.WithConfidenceThreshold(60.0),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create Tesseract engine: %w", err)
	}

	// Create embedder.
	fmt.Println("  Creating OpenAI embedder...")
	emb := openaiembedder.New(
		openaiembedder.WithModel(defaultEmbeddingModel),
	)

	// Create vector store.
	fmt.Println("  Creating vector store...")
	vs, err := util.NewVectorStoreByType(storeType)
	if err != nil {
		return nil, err
	}

	// Get absolute path for data directory.
	absDataDir, err := filepath.Abs(dataDir)
	if err != nil {
		return nil, fmt.Errorf("failed to get absolute path: %w", err)
	}

	// Create directory source with OCR support.
	fmt.Printf("  Creating directory source for PDFs in %s...\n", absDataDir)
	sources := []source.Source{
		dir.New(
			[]string{absDataDir},
			dir.WithName("PDF Documents with OCR"),
			dir.WithMetadataValue("type", "pdf"),
			dir.WithOCRExtractor(ocrExtractor),
		),
	}

	// Create knowledge base.
	fmt.Println("  Creating knowledge base...")
	kb := knowledge.New(
		knowledge.WithVectorStore(vs),
		knowledge.WithEmbedder(emb),
		knowledge.WithSources(sources),
	)

	// Load the knowledge base.
	fmt.Println("\nLoading PDFs into knowledge base...")
	startTime := time.Now()

	if err := kb.Load(ctx, knowledge.WithShowProgress(true), knowledge.WithShowStats(true)); err != nil {
		return nil, fmt.Errorf("failed to load knowledge base: %w", err)
	}

	loadTime := time.Since(startTime)
	fmt.Printf("Knowledge base loaded successfully in %v\n", loadTime)

	return kb, nil
}

// showStats displays knowledge base statistics.
func showStats(ctx context.Context, kb *knowledge.BuiltinKnowledge, storeType util.VectorStoreType) {
	result, err := kb.Search(ctx, &knowledge.SearchRequest{
		Query:      "",
		MaxResults: 10000,
		MinScore:   0.0,
		SearchMode: vectorstore.SearchModeFilter,
		SearchFilter: &knowledge.SearchFilter{
			Metadata: map[string]any{},
		},
	})
	if err != nil {
		fmt.Printf("Failed to get stats: %v\n", err)
		return
	}

	totalDocs := len(result.Documents)
	totalChars := 0
	ocrCount := 0
	sourceFiles := make(map[string]bool)

	for _, doc := range result.Documents {
		totalChars += len(doc.Document.Content)

		if ocrEnabled, ok := doc.Document.Metadata["ocr_enabled"].(string); ok && ocrEnabled == "true" {
			ocrCount++
		}

		if sourceName, ok := doc.Document.Metadata["source_name"].(string); ok && sourceName != "" {
			sourceFiles[sourceName] = true
		}
	}

	fmt.Printf("  Total Chunks: %d\n", totalDocs)
	fmt.Printf("  Source Files: %d\n", len(sourceFiles))
	fmt.Printf("  OCR-Processed Chunks: %d\n", ocrCount)
	fmt.Printf("  Total Characters: %d\n", totalChars)
	if totalDocs > 0 {
		fmt.Printf("  Avg Chars/Chunk: %d\n", totalChars/totalDocs)
	}
	fmt.Printf("  OCR Engine: Tesseract\n")
	fmt.Printf("  Vector Store: %s\n", storeType)
}

// search performs a knowledge base search and displays results.
func search(ctx context.Context, kb *knowledge.BuiltinKnowledge, query string) error {
	startTime := time.Now()

	result, err := kb.Search(ctx, &knowledge.SearchRequest{
		Query:      query,
		MaxResults: 5,
		MinScore:   0.3,
	})
	if err != nil {
		return err
	}

	searchTime := time.Since(startTime)

	fmt.Printf("Search completed in %v\n", searchTime)
	fmt.Printf("Found %d results:\n", len(result.Documents))

	if len(result.Documents) == 0 {
		fmt.Println("  No matching results found.")
		return nil
	}

	for i, doc := range result.Documents {
		fmt.Printf("\n  #%d (Score: %.4f)\n", i+1, doc.Score)
		if doc.Document.Name != "" {
			fmt.Printf("    Source: %s\n", doc.Document.Name)
		}

		// Display metadata.
		metaParts := []string{}
		for k, v := range doc.Document.Metadata {
			if !strings.HasPrefix(k, "trpc_agent_go_") {
				metaParts = append(metaParts, fmt.Sprintf("%s=%v", k, v))
			}
		}
		if len(metaParts) > 0 {
			fmt.Printf("    Metadata: %s\n", strings.Join(metaParts, ", "))
		}

		// Display content (truncate if too long).
		content := doc.Document.Content
		maxLen := 200
		if len(content) > maxLen {
			content = content[:maxLen] + "..."
		}
		fmt.Printf("    Content: %s\n", content)
	}

	return nil
}
