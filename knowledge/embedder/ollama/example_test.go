//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package ollama_test

import (
	"context"
	"fmt"
	"log"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/embedder/ollama"
)

// ExampleNew demonstrates how to create a new Ollama embedder with default settings.
func ExampleNew() {
	embedder := ollama.New()
	fmt.Printf("Created embedder with model: %d dimensions\n", embedder.GetDimensions())
	// Output: Created embedder with model: 1536 dimensions
}

// ExampleNew_customOptions demonstrates how to create an Ollama embedder with custom options.
func ExampleNew_customOptions() {
	embedder := ollama.New(
		ollama.WithModel("llama3.2:latest"),
		ollama.WithDimensions(3072),
		ollama.WithHost("http://localhost:11434"),
		ollama.WithTruncate(true),
		ollama.WithUseEmbeddings(),
		ollama.WithOptions(map[string]any{"temperature": 0.7}),
		ollama.WithKeepAlive(30*time.Second),
	)
	fmt.Printf("Created embedder with model: %d dimensions\n", embedder.GetDimensions())
	// Output: Created embedder with model: 3072 dimensions
}

// ExampleEmbedder_GetEmbedding demonstrates how to get an embedding for a given text.
func ExampleEmbedder_GetEmbedding() {
	embedder := ollama.New()
	embedding, err := embedder.GetEmbedding(context.Background(), "Why is the sky blue?")
	if err != nil {
		log.Fatalf("failed to get embedding: %v", err)
	}
	fmt.Printf("Generated embedding with %d dimensions\n", len(embedding))
	fmt.Printf("First few values: [%.4f, %.4f, %.4f, ...]",
		embedding[0], embedding[1], embedding[2])
}

// ExampleEmbedder_GetEmbedding_withUseEmbeddings demonstrates how to get an embedding for a given text with useEmbeddings(/api/embeddings).
func ExampleEmbedder_GetEmbedding_withUseEmbeddings() {
	embedder := ollama.New(ollama.WithUseEmbeddings())
	embedding, err := embedder.GetEmbedding(context.Background(), "Why is the sky blue?")
	if err != nil {
		log.Fatalf("failed to get embedding: %v", err)
	}
	fmt.Printf("Generated embedding with %d dimensions\n", len(embedding))
	fmt.Printf("First few values: [%.4f, %.4f, %.4f, ...]",
		embedding[0], embedding[1], embedding[2])
}

// ExampleEmbedder_GetEmbeddingWithUsage demonstrates how to get an embedding for a given text with usage information.
func ExampleEmbedder_GetEmbeddingWithUsage() {
	embedder := ollama.New()
	embedding, usage, err := embedder.GetEmbeddingWithUsage(context.Background(), "Why is the sky blue?")
	if err != nil {
		log.Fatalf("failed to get embedding: %v", err)
	}
	fmt.Printf("Generated embedding with %d dimensions\n", len(embedding))
	fmt.Printf("First few values: [%.4f, %.4f, %.4f, ...]\n",
		embedding[0], embedding[1], embedding[2])
	if usage != nil {
		fmt.Printf("prompt_tokens: %v\n", usage["prompt_tokens"])
		fmt.Printf("total_duration: %v\n", usage["total_duration"])
		fmt.Printf("load_duration: %v\n", usage["load_duration"])
	}
}

// ExampleEmbedder_batchProcessing demonstrates processing multiple texts.
func ExampleEmbedder_batchProcessing() {
	embedder := ollama.New()
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
		{"Small model", "llama3.2:latest", 1536},
		{"Large model", "gpt-oss:20b", 3072},
	}

	for _, m := range models {
		embedder := ollama.New(
			ollama.WithModel(m.model),
			ollama.WithDimensions(m.dimensions),
		)

		fmt.Printf("%s: %d dimensions\n", m.name, embedder.GetDimensions())
	}

	// Output:
	// Small model: 1536 dimensions
	// Large model: 3072 dimensions
}
