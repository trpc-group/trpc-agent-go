package reranker

import "context"

// Top1Reranker is a simple reranker that returns results unchanged (keeps original order).
type Top1Reranker struct{}

// NewTop1Reranker creates a new top-1 reranker.
func NewTop1Reranker() *Top1Reranker {
	return &Top1Reranker{}
}

// Rerank implements the Reranker interface by returning results in original order.
func (t *Top1Reranker) Rerank(ctx context.Context, results []*Result) ([]*Result, error) {
	// Simple implementation: return results unchanged
	return results, nil
}
