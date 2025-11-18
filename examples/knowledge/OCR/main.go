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
package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/knowledge"
	openaiembedder "trpc.group/trpc-go/trpc-agent-go/knowledge/embedder/openai"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/ocr"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/ocr/tesseract"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/source"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/source/dir"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/vectorstore/tcvector"

	// Import PDF reader to register it.
	_ "trpc.group/trpc-go/trpc-agent-go/knowledge/document/reader/pdf"
)

// getEnvOrDefault returns environment variable value or default.
func getEnvOrDefault(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

// Command line flags.
var (
	dataDir  = flag.String("data", "./data", "Directory containing PDF files to process")
	recreate = flag.Bool("recreate", true, "Recreate the vector store on startup")

	// you can change to other vector store
	tcvectorURL  = flag.String("tcvector-url", getEnvOrDefault("TCVECTOR_URL", ""), "TCVector URL (required)")
	tcvectorUser = flag.String("tcvector-user", getEnvOrDefault("TCVECTOR_USERNAME", ""), "TCVector username (required)")
	tcvectorPass = flag.String("tcvector-pass", getEnvOrDefault("TCVECTOR_PASSWORD", ""), "TCVector password (required)")
)

// Default values.
const (
	defaultEmbeddingModel = "text-embedding-3-small"
	collectionName        = "pdf-ocr-1"
)

func main() {
	flag.Parse()

	// Validate required parameters.
	if *tcvectorURL == "" || *tcvectorUser == "" || *tcvectorPass == "" {
		log.Fatal("❌ Error: TCVector credentials are required (--tcvector-url, --tcvector-user, --tcvector-pass)")
	}

	// Check if data directory exists.
	if _, err := os.Stat(*dataDir); os.IsNotExist(err) {
		log.Fatalf("❌ Error: Data directory not found: %s", *dataDir)
	}

	fmt.Println("📄 PDF OCR Knowledge Demo")
	fmt.Println(strings.Repeat("=", 61))
	fmt.Printf("Data Directory: %s\n", *dataDir)
	fmt.Println("OCR Engine: Tesseract")
	fmt.Println("Vector Store: TCVector")
	fmt.Printf("Collection: %s\n", collectionName)
	fmt.Println(strings.Repeat("=", 61))

	// Create and run the demo.
	demo := &ocrDemo{
		dataDir: *dataDir,
	}

	if err := demo.run(); err != nil {
		log.Fatalf("❌ Demo failed: %v", err)
	}
}

// ocrDemo manages the OCR PDF knowledge base demo.
type ocrDemo struct {
	dataDir      string
	kb           *knowledge.BuiltinKnowledge
	ocrExtractor ocr.Extractor
}

// run executes the demo workflow.
func (d *ocrDemo) run() error {
	ctx := context.Background()

	// Setup knowledge base with PDF and OCR.
	if err := d.setupKnowledgeBase(ctx); err != nil {
		return fmt.Errorf("setup failed: %w", err)
	}

	// Note: OCR extractor cleanup is handled by the knowledge base
	// We should NOT close it here as it may still be needed for future operations

	// Start interactive search.
	return d.startSearch(ctx)
}

// setupKnowledgeBase creates the knowledge base with PDF OCR support.
func (d *ocrDemo) setupKnowledgeBase(ctx context.Context) error {
	fmt.Println("\n🔧 Setting up knowledge base...")

	// Create Tesseract OCR extractor.
	fmt.Println("   Creating Tesseract OCR engine...")
	var err error
	d.ocrExtractor, err = tesseract.New(
		tesseract.WithLanguage("eng+chi_sim"), // English + Simplified Chinese
		tesseract.WithConfidenceThreshold(60.0),
	)
	if err != nil {
		return fmt.Errorf("failed to create Tesseract engine: %w", err)
	}

	// Create embedder.
	fmt.Println("   Creating OpenAI embedder...")
	emb := openaiembedder.New(
		openaiembedder.WithModel(defaultEmbeddingModel),
	)

	// Create TCVector store.
	fmt.Println("   Creating TCVector store...")
	vs, err := d.setupTCVector()
	if err != nil {
		return err
	}

	// Get absolute path for data directory.
	absDataDir, err := filepath.Abs(d.dataDir)
	if err != nil {
		return fmt.Errorf("failed to get absolute path: %w", err)
	}

	// Create directory source with OCR support.
	fmt.Printf("   Creating directory source for PDFs in %s...\n", absDataDir)
	sources := []source.Source{
		dir.New(
			[]string{absDataDir},
			dir.WithName("PDF Documents with OCR"),
			dir.WithMetadataValue("type", "pdf"),
			dir.WithOCRExtractor(d.ocrExtractor),
		),
	}

	// Create knowledge base.
	fmt.Println("   Creating knowledge base...")
	d.kb = knowledge.New(
		knowledge.WithVectorStore(vs),
		knowledge.WithEmbedder(emb),
		knowledge.WithSources(sources),
	)

	// Load the knowledge base.
	fmt.Println("\n📚 Loading PDFs into knowledge base...")
	startTime := time.Now()

	if err := d.kb.Load(
		ctx,
		knowledge.WithShowProgress(true),
		knowledge.WithShowStats(true),
		knowledge.WithRecreate(*recreate),
	); err != nil {
		return fmt.Errorf("failed to load knowledge base: %w", err)
	}

	loadTime := time.Since(startTime)
	fmt.Printf("✅ Knowledge base loaded successfully in %v\n", loadTime)

	return nil
}

// setupTCVector creates and configures TCVector store.
func (d *ocrDemo) setupTCVector() (vectorstore.VectorStore, error) {
	vs, err := tcvector.New(
		tcvector.WithURL(*tcvectorURL),
		tcvector.WithUsername(*tcvectorUser),
		tcvector.WithPassword(*tcvectorPass),
		tcvector.WithCollection(collectionName),
		tcvector.WithFilterIndexFields([]string{"type", "ocr_enabled", "ocr_engine"}),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create TCVector store: %w", err)
	}
	return vs, nil
}

// startSearch runs the interactive search loop.
func (d *ocrDemo) startSearch(ctx context.Context) error {
	scanner := bufio.NewScanner(os.Stdin)

	fmt.Println("\n🔍 PDF Search Interface")
	fmt.Println(strings.Repeat("=", 61))
	fmt.Println("💡 Commands:")
	fmt.Println("   /exit     - Exit the program")
	fmt.Println("   /stats    - Show knowledge base statistics")
	fmt.Println()
	fmt.Println("🎯 Try searching for content in your PDFs:")
	fmt.Println("   - Enter any keywords or questions")
	fmt.Println("   - Search results will show matching text chunks")
	fmt.Println()

	for {
		fmt.Print("🔍 Query: ")
		if !scanner.Scan() {
			break
		}

		query := strings.TrimSpace(scanner.Text())
		if query == "" {
			continue
		}

		// Handle special commands.
		switch strings.ToLower(query) {
		case "/exit":
			fmt.Println("👋 Goodbye!")
			return nil
		case "/stats":
			d.showStats(ctx)
			continue
		}

		// Perform search.
		if err := d.search(ctx, query); err != nil {
			fmt.Printf("❌ Search error: %v\n", err)
		}

		fmt.Println() // Add spacing.
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("input scanner error: %w", err)
	}

	return nil
}

// search performs a knowledge base search and displays results.
func (d *ocrDemo) search(ctx context.Context, query string) error {
	fmt.Printf("\n🔎 Searching for: \"%s\"\n", query)
	startTime := time.Now()

	// Search the knowledge base.
	result, err := d.kb.Search(ctx, &knowledge.SearchRequest{
		Query:      query,
		MaxResults: 5,
		MinScore:   0.3,
	})
	if err != nil {
		return err
	}

	searchTime := time.Since(startTime)

	// Display results.
	fmt.Printf("⏱️  Search completed in %v\n", searchTime)
	fmt.Printf("📊 Found %d results:\n", len(result.Documents))
	fmt.Println(strings.Repeat("-", 61))

	if len(result.Documents) == 0 {
		fmt.Println("   No matching results found.")
		return nil
	}

	for i, doc := range result.Documents {
		fmt.Printf("\n📄 Result #%d (Score: %.4f)\n", i+1, doc.Score)
		if doc.Document.Name != "" {
			fmt.Printf("   Source: %s\n", doc.Document.Name)
		}

		// Display metadata.
		if len(doc.Document.Metadata) > 0 {
			fmt.Printf("   Metadata: ")
			metaParts := []string{}
			for k, v := range doc.Document.Metadata {
				// Skip internal metadata
				if !strings.HasPrefix(k, "trpc_agent_go_") {
					metaParts = append(metaParts, fmt.Sprintf("%s=%v", k, v))
				}
			}
			if len(metaParts) > 0 {
				fmt.Println(strings.Join(metaParts, ", "))
			} else {
				fmt.Println("(none)")
			}
		}

		// Display content (truncate if too long).
		content := doc.Document.Content
		maxLen := 300
		if len(content) > maxLen {
			content = content[:maxLen] + "..."
		}
		fmt.Printf("   Content: %s\n", content)
	}

	return nil
}

// showStats displays knowledge base statistics.
func (d *ocrDemo) showStats(ctx context.Context) {
	fmt.Println("\n📊 Knowledge Base Statistics")
	fmt.Println(strings.Repeat("-", 61))

	// Use filter mode with empty filter to get all documents efficiently
	// This avoids unnecessary embedding and vector search operations
	result, err := d.kb.Search(ctx, &knowledge.SearchRequest{
		Query:      "",
		MaxResults: 10000, // Set a high limit to get all documents
		MinScore:   0.0,
		SearchMode: vectorstore.SearchModeFilter,
		SearchFilter: &knowledge.SearchFilter{
			// Empty filter means no filtering - get all documents
			Metadata: map[string]any{},
		},
	})
	if err != nil {
		fmt.Printf("❌ Failed to get stats: %v\n", err)
		return
	}

	totalDocs := len(result.Documents)
	totalChars := 0
	ocrCount := 0
	sourceFiles := make(map[string]bool)

	for i, doc := range result.Documents {
		totalChars += len(doc.Document.Content)

		// Print document details
		fmt.Printf("\n📄 Document #%d:\n", i+1)
		fmt.Printf("   Content Length: %d chars\n", len(doc.Document.Content))
		fmt.Printf("   Content: %s\n", doc.Document.Content)
		fmt.Printf("   Metadata:\n")
		for k, v := range doc.Document.Metadata {
			if strings.HasPrefix(k, "trpc_agent_go_") {
				continue
			}
			fmt.Printf("      %s: %v   ", k, v)
		}

		// Count OCR-processed documents
		if ocrEnabled, ok := doc.Document.Metadata["ocr_enabled"].(string); ok && ocrEnabled == "true" {
			ocrCount++
		}

		// Track unique source files
		if sourceName, ok := doc.Document.Metadata["source_name"].(string); ok && sourceName != "" {
			sourceFiles[sourceName] = true
		}
	}

	fmt.Printf("   Total Chunks: %d\n", totalDocs)
	fmt.Printf("   Source Files: %d\n", len(sourceFiles))
	fmt.Printf("   OCR-Processed Chunks: %d\n", ocrCount)
	fmt.Printf("   Total Characters: %d\n", totalChars)
	if totalDocs > 0 {
		fmt.Printf("   Avg Chars/Chunk: %d\n", totalChars/totalDocs)
	}
	fmt.Printf("   OCR Engine: Tesseract\n")
	fmt.Printf("   Vector Store: TCVector\n")
	fmt.Printf("   Collection: %s\n", collectionName)
}
