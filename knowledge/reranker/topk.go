//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package reranker

import "context"

// Default value for top K results, indicating return all results.
const defaultTopK = -1

// TopKReranker is a simple reranker that returns top K results unchanged (keeps original order).
type TopKReranker struct {
	k int // Number of results to return.
}

// Option represents a functional option for configuring TopKReranker.
type Option func(*TopKReranker)

// WithK sets the number of top results to return.
func WithK(k int) Option {
	return func(tkr *TopKReranker) {
		if k <= 0 {
			k = defaultTopK
		}
		tkr.k = k
	}
}

// NewTopKReranker creates a new top-K reranker with options.
func NewTopKReranker(opts ...Option) *TopKReranker {
	tkr := &TopKReranker{
		k: defaultTopK, // Default to return all results.
	}

	// Apply options.
	for _, opt := range opts {
		opt(tkr)
	}

	return tkr
}

// Rerank implements the Reranker interface by returning top K results in original order.
func (t *TopKReranker) Rerank(ctx context.Context, query *Query, results []*Result) ([]*Result, error) {
	// Return top K results, or all if fewer than K available.
	if t.k <= 0 || len(results) <= t.k {
		return results, nil
	}
	return results[:t.k], nil
}
