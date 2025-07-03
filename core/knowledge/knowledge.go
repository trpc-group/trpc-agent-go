// Package knowledge provides the main knowledge management interface for trpc-agent-go.
package knowledge

import (
	"context"

	"trpc.group/trpc-go/trpc-agent-go/core/knowledge/document"
)

// Knowledge is the main interface for knowledge management operations.
type Knowledge interface {
	// AddDocument adds a document to the knowledge base.
	AddDocument(ctx context.Context, doc *document.Document) error

	// Search performs semantic search and returns the best result.
	// This is the main method used by agents for RAG.
	Search(ctx context.Context, query string) (*SearchResult, error)

	// Close closes the knowledge base and releases resources.
	Close() error
}

// SearchResult represents the result of a knowledge search.
type SearchResult struct {
	// Document is the best matching document.
	Document *document.Document

	// Score is the relevance score (0.0 to 1.0).
	Score float64

	// Text is the document content for agent context.
	Text string
}
