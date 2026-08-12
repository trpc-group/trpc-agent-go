//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
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
//
// Concurrency:
// Implementations must be safe for concurrent use by multiple goroutines. A
// single embedder instance is shared across the texts of a load and is called
// from several goroutines at once by default, so an implementation that reuses
// request buffers or other mutable state must guard it.
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

// BatchEmbedder is an optional capability implemented by embedders that can
// encode multiple texts in a single provider request. Callers discover it with
// a type assertion on Embedder and fall back to per-text requests when the
// assertion fails, so existing Embedder implementations need no changes.
//
// Implementations must return exactly one embedding per input text, with
// embeddings[i] corresponding to texts[i]. When a provider response cannot be
// mapped back to the input order with certainty, or when it carries fewer or
// more vectors than requested, implementations must return an error rather
// than a reordered, padded, or truncated result. This prevents callers from
// silently attaching a vector to the wrong input.
//
// The concurrency requirement of Embedder applies to GetEmbeddings as well:
// batches are embedded concurrently by default, so one instance can receive
// several calls at the same time.
type BatchEmbedder interface {
	Embedder

	// GetEmbeddings generates one embedding vector per input text, issuing a
	// single provider request for the whole batch.
	//
	// Returns:
	// - A slice of embeddings where embeddings[i] corresponds to texts[i]
	// - An error for system-level failures and for responses that cannot be
	//   mapped back to the input order
	//
	// An empty texts slice is an error. Implementations do not split the batch
	// to satisfy provider limits; callers choose the batch size.
	GetEmbeddings(ctx context.Context, texts []string) ([][]float64, error)
}
