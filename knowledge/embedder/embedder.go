//
// Tencent is pleased to support the open source community by making tRPC available.
//
// Copyright (C) 2025 Tencent.
// All rights reserved.
//
// If you have downloaded a copy of the tRPC source code from Tencent,
// please note that tRPC source code is licensed under the  Apache 2.0 License,
// A copy of the Apache 2.0 License is included in this file.
//
//

// Package embedder provides interfaces and implementations for text embedding.
package embedder

import (
	"context"
)

// Embedder is the interface that all embedders must implement.
//
// Error Handling Strategy:
// This interface uses a dual-layer error handling approach:
//
// 1. Function-level errors (returned as `error`):
//   - System-level failures that prevent communication
//   - Examples: nil input, network issues, invalid parameters
//   - These prevent the embedding operation from completing
//
// 2. Empty embeddings (empty slice return):
//   - API-level errors or processing failures
//   - Examples: API rate limits, content filtering, model errors
//   - These are delivered as empty slices with logged warnings
//
// Usage pattern:
//
//	embedding, err := embedder.GetEmbedding(ctx, "text to embed")
//	if err != nil {
//	    // Handle system-level errors (cannot communicate)
//	    return fmt.Errorf("failed to get embedding: %w", err)
//	}
//	if len(embedding) == 0 {
//	    // Handle API-level errors (communication succeeded, but API returned error)
//	    return fmt.Errorf("received empty embedding from API")
//	}
//	// Process successful embedding...
type Embedder interface {
	// GetEmbedding generates an embedding vector for the given text.
	//
	// Returns:
	// - A slice of float64 values representing the embedding
	// - An error for system-level failures (prevents communication)
	//
	// The embedding slice may be empty for API-level errors.
	GetEmbedding(ctx context.Context, text string) ([]float64, error)

	// GetEmbeddingWithUsage generates an embedding vector for the given text
	// and returns usage information if available.
	//
	// Returns:
	// - A slice of float64 values representing the embedding
	// - Usage information as a map (may be nil if not supported)
	// - An error for system-level failures
	GetEmbeddingWithUsage(ctx context.Context, text string) ([]float64, map[string]any, error)

	// GetDimensions returns the dimensionality of the embeddings produced by this embedder.
	// Returns 0 if dimensions are not known or configurable.
	GetDimensions() int
}
