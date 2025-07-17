//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.
// All rights reserved.
//
// If you have downloaded a copy of the tRPC source code from Tencent,
// please note that tRPC source code is licensed under the  Apache 2.0 License,
// A copy of the Apache 2.0 License is included in this file.
//
//

package gemini_test

import (
	"context"
	"fmt"
	"log"
	"os"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/embedder/gemini"
)

// ExampleNew demonstrates how to create a new Gemini embedder with default settings.
func ExampleNew() {
	embedder, err := gemini.New(context.Background(), gemini.WithAPIKey("test-key"))
	if err != nil {
		log.Fatalf("Failed to create embedder: %v", err)
	}
	fmt.Printf("Created embedder with model: %d dimensions", embedder.GetDimensions())
	// Output: Created embedder with model: 1536 dimensions
}

// ExampleNew_customOptions demonstrates how to create an Gemini embedder with custom options.
func ExampleNew_customOptions() {
	embedder, err := gemini.New(
		context.Background(),
		gemini.WithModel(gemini.ModelGeminiEmbeddingExp0307),
		gemini.WithDimensions(3072),
		gemini.WithAPIKey("test-key"),
	)
	if err != nil {
		log.Fatalf("Failed to create embedder: %v", err)
	}

	fmt.Printf("Model dimensions: %d", embedder.GetDimensions())
	// Output: Model dimensions: 3072
}

// ExampleEmbedder_GetEmbedding demonstrates basic embedding generation.
func ExampleEmbedder_GetEmbedding() {
	// Skip this example if no API key is available.
	apiKey := os.Getenv("GOOGLE_API_KEY")
	if apiKey == "" {
		fmt.Println("Skipping example: GOOGLE_API_KEY not set")
		return
	}

	// Create embedder.
	embedder, err := gemini.New(
		context.Background(),
		gemini.WithModel(gemini.ModelGeminiEmbeddingExp0307),
	)

	// Generate embedding for some text.
	ctx := context.Background()
	text := "The quick brown fox jumps over the lazy dog."

	embedding, err := embedder.GetEmbedding(ctx, text)
	if err != nil {
		log.Fatalf("Failed to get embedding: %v", err)
	}

	fmt.Printf("Generated embedding with %d dimensions", len(embedding))
	fmt.Printf("First few values: [%.4f, %.4f, %.4f, ...]",
		embedding[0], embedding[1], embedding[2])
}

func ExampleEmbedder_getEmbedding() {
	// Skip this example if no API key is available.
	apiKey := os.Getenv("GOOGLE_API_KEY")
	if apiKey == "" {
		fmt.Println("Skipping example: GOOGLE_API_KEY not set")
		return
	}

	// Create embedder.
	embedder, err := gemini.New(
		context.Background(),
		gemini.WithModel(gemini.ModelGeminiEmbeddingExp0307),
	)
	if err != nil {
		log.Fatalf("Failed to create embedder: %v", err)
	}

	// Generate embedding with usage information.
	ctx := context.Background()
	text := "This is a sample text for embedding generation."

	embedding, usage, err := embedder.GetEmbeddingWithUsage(ctx, text)
	if err != nil {
		log.Fatalf("Failed to get embedding with usage: %v", err)
	}

	fmt.Printf("Embedding dimensions: %d", len(embedding))
	if usage != nil {
		fmt.Printf("Billable character count: %v", usage["billable_character_count"])
	}
}

// ExampleEmbedder_batchProcessing demonstrates processing multiple texts.
func ExampleEmbedder_batchProcessing() {
	// Skip this example if no API key is available.
	apiKey := os.Getenv("GOOGLE_API_KEY")
	if apiKey == "" {
		fmt.Println("Skipping example: GOOGLE_API_KEY not set")
		return
	}

	// Create embedder.
	embedder, err := gemini.New(
		context.Background(),
	)
	if err != nil {
		log.Fatalf("Failed to create embedder: %v", err)
	}

	// Process multiple texts.
	texts := []string{
		"Machine learning is a subset of artificial intelligence.",
		"Deep learning uses neural networks with multiple layers.",
		"Natural language processing helps computers understand text.",
	}

	ctx := context.Background()
	embeddings := make([][]float64, len(texts))

	for i, text := range texts {
		var err error
		embeddings[i], err = embedder.GetEmbedding(ctx, text)
		if err != nil {
			log.Fatalf("Failed to get embedding for text %d: %v", i, err)
		}
	}

	fmt.Printf("Generated %d embeddings", len(embeddings))
	fmt.Printf("Each embedding has %d dimensions", len(embeddings[0]))
}

// ExampleEmbedder_differentModels demonstrates using different embedding models.
func ExampleEmbedder_differentModels() {
	models := []struct {
		name       string
		model      string
		dimensions int
	}{
		{"model0307-1536", gemini.ModelGeminiEmbeddingExp0307, 1536},
		{"model001-3072", gemini.ModelGeminiEmbedding001, 3072},
		{"model0307-512", gemini.ModelGeminiEmbeddingExp0307, 512}, // Reduced dimensions
	}

	for _, m := range models {
		embedder, err := gemini.New(
			context.Background(),
			gemini.WithModel(m.model),
			gemini.WithDimensions(m.dimensions),
			gemini.WithAPIKey("test-key"),
		)
		if err != nil {
			log.Fatalf("Failed to create embedder: %v", err)
		}

		fmt.Printf("%s: %d dimensions\n", m.name, embedder.GetDimensions())
	}

	// Output:
	// model0307-1536: 1536 dimensions
	// model001-3072: 3072 dimensions
	// model0307-512: 512 dimensions
}
