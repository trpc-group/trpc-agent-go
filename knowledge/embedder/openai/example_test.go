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

package openai_test

import (
	"context"
	"fmt"
	"log"
	"os"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/embedder/openai"
)

// ExampleNew demonstrates how to create a new OpenAI embedder with default settings.
func ExampleNew() {
	embedder := openai.New()
	fmt.Printf("Created embedder with model: %d dimensions", embedder.GetDimensions())
	// Output: Created embedder with model: 1536 dimensions
}

// ExampleNew_customOptions demonstrates how to create an OpenAI embedder with custom options.
func ExampleNew_customOptions() {
	embedder := openai.New(
		openai.WithModel(openai.ModelTextEmbedding3Large),
		openai.WithDimensions(3072),
		openai.WithEncodingFormat(openai.EncodingFormatFloat),
		openai.WithUser("my-app-user-123"),
		openai.WithAPIKey("your-api-key-here"),
	)

	fmt.Printf("Model dimensions: %d", embedder.GetDimensions())
	// Output: Model dimensions: 3072
}

// ExampleEmbedder_GetEmbedding demonstrates basic embedding generation.
func ExampleEmbedder_GetEmbedding() {
	// Skip this example if no API key is available.
	apiKey := os.Getenv("OPENAI_API_KEY")
	if apiKey == "" {
		fmt.Println("Skipping example: OPENAI_API_KEY not set")
		return
	}

	// Create embedder with API key.
	embedder := openai.New(
		openai.WithAPIKey(apiKey),
		openai.WithModel(openai.ModelTextEmbedding3Small),
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
	apiKey := os.Getenv("OPENAI_API_KEY")
	if apiKey == "" {
		fmt.Println("Skipping example: OPENAI_API_KEY not set")
		return
	}

	// Create embedder.
	embedder := openai.New(
		openai.WithAPIKey(apiKey),
		openai.WithUser("example-user"),
	)

	// Generate embedding with usage information.
	ctx := context.Background()
	text := "This is a sample text for embedding generation."

	embedding, usage, err := embedder.GetEmbeddingWithUsage(ctx, text)
	if err != nil {
		log.Fatalf("Failed to get embedding with usage: %v", err)
	}

	fmt.Printf("Embedding dimensions: %d", len(embedding))
	if usage != nil {
		fmt.Printf("Tokens used: %v", usage["prompt_tokens"])
		fmt.Printf("Total tokens: %v", usage["total_tokens"])
	}
}

// ExampleEmbedder_batchProcessing demonstrates processing multiple texts.
func ExampleEmbedder_batchProcessing() {
	// Skip this example if no API key is available.
	apiKey := os.Getenv("OPENAI_API_KEY")
	if apiKey == "" {
		fmt.Println("Skipping example: OPENAI_API_KEY not set")
		return
	}

	// Create embedder.
	embedder := openai.New(
		openai.WithAPIKey(apiKey),
	)

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
		{"Small model", openai.ModelTextEmbedding3Small, 1536},
		{"Large model", openai.ModelTextEmbedding3Large, 3072},
		{"Custom dimensions", openai.ModelTextEmbedding3Small, 512}, // Reduced dimensions
	}

	for _, m := range models {
		embedder := openai.New(
			openai.WithModel(m.model),
			openai.WithDimensions(m.dimensions),
		)

		fmt.Printf("%s: %d dimensions\n", m.name, embedder.GetDimensions())
	}

	// Output:
	// Small model: 1536 dimensions
	// Large model: 3072 dimensions
	// Custom dimensions: 512 dimensions
}
