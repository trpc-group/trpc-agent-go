//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package main

import (
	"context"
	"sync/atomic"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/embedder"
)

// requestCounter records how many embedding requests a load issued and how
// many texts those requests carried. Counting at the embedder is what makes
// the effect of WithEmbeddingBatchSize observable: the option changes the
// number of provider requests, not the number of documents.
type requestCounter struct {
	singleRequests atomic.Int64
	batchRequests  atomic.Int64
	embeddedTexts  atomic.Int64
}

func (c *requestCounter) totalRequests() int64 {
	return c.singleRequests.Load() + c.batchRequests.Load()
}

// countingEmbedder forwards every call to inner and counts it. It implements
// embedder.BatchEmbedder, so loading discovers the batch capability of the
// wrapped embedder through the same type assertion it uses for any embedder.
type countingEmbedder struct {
	requestCounter
	inner embedder.BatchEmbedder
}

var _ embedder.BatchEmbedder = (*countingEmbedder)(nil)

func newCountingEmbedder(inner embedder.BatchEmbedder) *countingEmbedder {
	return &countingEmbedder{inner: inner}
}

func (e *countingEmbedder) GetEmbedding(ctx context.Context, text string) ([]float64, error) {
	e.singleRequests.Add(1)
	e.embeddedTexts.Add(1)
	return e.inner.GetEmbedding(ctx, text)
}

func (e *countingEmbedder) GetEmbeddingWithUsage(
	ctx context.Context,
	text string,
) ([]float64, map[string]any, error) {
	e.singleRequests.Add(1)
	e.embeddedTexts.Add(1)
	return e.inner.GetEmbeddingWithUsage(ctx, text)
}

func (e *countingEmbedder) GetDimensions() int {
	return e.inner.GetDimensions()
}

func (e *countingEmbedder) GetEmbeddings(ctx context.Context, texts []string) ([][]float64, error) {
	e.batchRequests.Add(1)
	e.embeddedTexts.Add(int64(len(texts)))
	return e.inner.GetEmbeddings(ctx, texts)
}

// perDocumentEmbedder counts the same way but deliberately does not implement
// embedder.BatchEmbedder. It stands in for any embedder without batch support:
// WithEmbeddingBatchSize is then ignored, loading logs that it was ignored,
// and every document keeps its own request.
type perDocumentEmbedder struct {
	requestCounter
	inner embedder.Embedder
}

var _ embedder.Embedder = (*perDocumentEmbedder)(nil)

func newPerDocumentEmbedder(inner embedder.Embedder) *perDocumentEmbedder {
	return &perDocumentEmbedder{inner: inner}
}

func (e *perDocumentEmbedder) GetEmbedding(ctx context.Context, text string) ([]float64, error) {
	e.singleRequests.Add(1)
	e.embeddedTexts.Add(1)
	return e.inner.GetEmbedding(ctx, text)
}

func (e *perDocumentEmbedder) GetEmbeddingWithUsage(
	ctx context.Context,
	text string,
) ([]float64, map[string]any, error) {
	e.singleRequests.Add(1)
	e.embeddedTexts.Add(1)
	return e.inner.GetEmbeddingWithUsage(ctx, text)
}

func (e *perDocumentEmbedder) GetDimensions() int {
	return e.inner.GetDimensions()
}
