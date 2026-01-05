//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package topk provides a simple top-K reranker that returns top K results unchanged.
package topk

import (
	"context"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/reranker"
)

// Default value for top K results, indicating return all results.
const defaultTopK = -1

// Reranker is a simple reranker that returns top K results unchanged (keeps original order).
type Reranker struct {
	k int // Number of results to return.
}

// Option represents a functional option for configuring Reranker.
type Option func(*Reranker)

// WithK sets the number of top results to return.
func WithK(k int) Option {
	return func(r *Reranker) {
		if k <= 0 {
			k = defaultTopK
		}
		r.k = k
	}
}

// New creates a new top-K reranker with options.
func New(opts ...Option) *Reranker {
	r := &Reranker{
		k: defaultTopK, // Default to return all results.
	}

	// Apply options.
	for _, opt := range opts {
		opt(r)
	}

	return r
}

// Rerank implements the Reranker interface by returning top K results in original order.
func (r *Reranker) Rerank(ctx context.Context, query *reranker.Query, results []*reranker.Result) ([]*reranker.Result, error) {
	// Return top K results, or all if fewer than K available.
	if r.k <= 0 || len(results) <= r.k {
		return results, nil
	}
	return results[:r.k], nil
}
