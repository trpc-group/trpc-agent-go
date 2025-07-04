package reranker

import "context"

// TopKReranker is a simple reranker that returns top K results unchanged (keeps original order).
type TopKReranker struct {
	k int // Number of results to return.
}

// NewTopKReranker creates a new top-K reranker.
func NewTopKReranker(k int) *TopKReranker {
	if k <= 0 {
		k = 1 // Default to top 1.
	}
	return &TopKReranker{k: k}
}

// Rerank implements the Reranker interface by returning top K results in original order.
func (t *TopKReranker) Rerank(ctx context.Context, results []*Result) ([]*Result, error) {
	// Return top K results, or all if fewer than K available.
	if len(results) <= t.k {
		return results, nil
	}
	return results[:t.k], nil
}
