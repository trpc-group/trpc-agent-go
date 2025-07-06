package reranker

import "context"

// TopKReranker is a simple reranker that returns top K results unchanged (keeps original order).
type TopKReranker struct {
	k int // Number of results to return.
}

// Option represents a functional option for configuring TopKReranker.
type Option func(*TopKReranker)

// WithK sets the number of top results to return.
func WithK(k int) Option {
	return func(tkr *TopKReranker) {
		tkr.k = k
	}
}

// NewTopKReranker creates a new top-K reranker with options.
func NewTopKReranker(opts ...Option) *TopKReranker {
	tkr := &TopKReranker{
		k: 1, // Default to top 1.
	}

	// Apply options.
	for _, opt := range opts {
		opt(tkr)
	}

	// Validate k value.
	if tkr.k <= 0 {
		tkr.k = 1
	}

	return tkr
}

// Rerank implements the Reranker interface by returning top K results in original order.
func (t *TopKReranker) Rerank(ctx context.Context, results []*Result) ([]*Result, error) {
	// Return top K results, or all if fewer than K available.
	if len(results) <= t.k {
		return results, nil
	}
	return results[:t.k], nil
}
