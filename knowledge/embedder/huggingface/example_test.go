//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package huggingface

import (
	"context"
	"fmt"
	"net/http"

	"trpc.group/trpc-go/trpc-agent-go/log"
)

// ExampleNew demonstrates how to create a new hugging-face embedder with default settings.
func ExampleNew() {
	embedder := New()
	fmt.Printf("Created embedder with model: %d dimensions", embedder.GetDimensions())
	// Output: Created embedder with model: 1024 dimensions
}

// ExampleNew_customOptions demonstrates how to create a hugging-face embedder with custom options.
func ExampleNew_customOptions() {
	embedder := New(
		WithDimensions(3072),
	)

	fmt.Printf("Model dimensions: %d", embedder.GetDimensions())
	// Output: Model dimensions: 3072
}

// ExampleEmbedder_GetEmbedding demonstrates basic embedding generation.
func ExampleEmbedder_GetEmbedding() {
	modeURL := "http://localhost:8080"
	// Get info from the model. Check that the model is available.
	_, err := http.DefaultClient.Get(fmt.Sprintf("%s/info", modeURL))
	if err != nil {
		log.Errorf("Failed to get info: %v", err)
		return
	}
	// Create embedder with API key.
	embedder := New(
		WithBaseURL(modeURL),
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
}

// ExampleEmbedder_GetEmbeddingWithUsage demonstrates basic embedding generation with usage tracking.
// Text-embedding-interface don't support usage tracking. So it is similar to GetEmbedding().
func ExampleEmbedder_GetEmbeddingWithUsage() {
	modeURL := "http://localhost:8080"
	// Get info from the model. Check that the model is available.
	_, err := http.DefaultClient.Get(fmt.Sprintf("%s/info", modeURL))
	if err != nil {
		log.Errorf("Failed to get info: %v", err)
		return
	}
	// Create embedder with API key.
	embedder := New(
		WithBaseURL(modeURL),
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
}
