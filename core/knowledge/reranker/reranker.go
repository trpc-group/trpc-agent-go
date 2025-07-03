// Package reranker provides result re-ranking for knowledge systems.
package reranker

import (
	"context"

	"trpc.group/trpc-go/trpc-agent-go/core/knowledge/document"
)

// Reranker re-ranks search results based on various criteria.
type Reranker interface {
	// Rerank re-orders search results based on ranking criteria.
	Rerank(ctx context.Context, results []*Result) ([]*Result, error)
}

// Result represents a rankable search result.
type Result struct {
	// Document is the search result document.
	Document *document.Document

	// Score is the relevance score.
	Score float64
}
