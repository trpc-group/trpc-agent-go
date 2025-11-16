package text_embedding_interface

import (
	"context"
	"fmt"

	"trpc.group/trpc-go/trpc-agent-go/log"
)

// ExampleNew demonstrates how to create a new text-embedding-interface embedder with default settings.
func ExampleNew() {
	embedder := New()
	fmt.Printf("Created embedder with model: %d dimensions", embedder.GetDimensions())
	// Output: Created embedder with model: 1024 dimensions
}

// ExampleNew_customOptions demonstrates how to create an text-embedding-interface embedder with custom options.
func ExampleNew_customOptions() {
	embedder := New(
		WithDimensions(3072),
	)

	fmt.Printf("Model dimensions: %d", embedder.GetDimensions())
	// Output: Model dimensions: 3072
}

// ExampleEmbedder_GetEmbedding demonstrates basic embedding generation.
func ExampleEmbedder_GetEmbedding() {
	// Create embedder with API key.
	embedder := New(
		WithBaseURL("http://localhost:8080"),
	)

	// Generate embedding for some text.
	ctx := context.Background()
	text := "The quick brown fox jumps over the lazy dog."

	embedding, err := embedder.GetEmbedding(ctx, text)
	if err != nil {
		log.Fatalf("Failed to get embedding: %v", err)
	}

	fmt.Printf("Generated embedding with %d dimensions\n", len(embedding))
	fmt.Printf("First few values: [%.4f, %.4f, %.4f, ...]\n",
		embedding[0], embedding[1], embedding[2])

	// Create embedder with API key.
	embedderAll := New(
		WithEmbedRoute(EmbedAll),
	)

	embeddingAll, err := embedderAll.GetEmbedding(ctx, text)
	if err != nil {
		log.Fatalf("Failed to get embedding: %v", err)
	}

	fmt.Printf("Generated embedding_all with %d dimensions\n", len(embeddingAll))
	fmt.Printf("First few values: [%.4f, %.4f, %.4f, ...]",
		embeddingAll[0], embeddingAll[1], embeddingAll[2])

	// Output:
	// Generated embedding with 1024 dimensions
	// First few values: [0.0241, -0.0454, -0.0033, ...]
	// Generated embedding_all with 1024 dimensions
	// First few values: [1.7969, -21.4219, 0.1415, ...]
}

// ExampleEmbedder_GetEmbeddingWithUsage demonstrates basic embedding generation with usage tracking.
// Text-embedding-interface don't support usage tracking. So it is similar to GetEmbedding().
func ExampleEmbedder_GetEmbeddingWithUsage() {
	// Create embedder with API key.
	embedder := New(
		WithBaseURL("http://localhost:8080"),
	)

	// Generate embedding for some text.
	ctx := context.Background()
	text := "The quick brown fox jumps over the lazy dog."

	embedding, err := embedder.GetEmbedding(ctx, text)
	if err != nil {
		log.Fatalf("Failed to get embedding: %v", err)
	}

	fmt.Printf("Generated embedding with %d dimensions\n", len(embedding))
	fmt.Printf("First few values: [%.4f, %.4f, %.4f, ...]",
		embedding[0], embedding[1], embedding[2])

	// Output:
	// Generated embedding with 1024 dimensions
	// First few values: [0.0241, -0.0454, -0.0033, ...]
}
