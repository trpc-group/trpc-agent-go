// Package vectorstore provides interfaces for vector storage and similarity search.
package vectorstore

import (
	"context"

	"trpc.group/trpc-go/trpc-agent-go/core/knowledge/document"
)

// VectorStore defines the interface for vector storage and similarity search operations.
type VectorStore interface {
	// Add stores a document with its embedding vector.
	Add(ctx context.Context, doc *document.Document, embedding []float64) error

	// Get retrieves a document by ID along with its embedding.
	Get(ctx context.Context, id string) (*document.Document, []float64, error)

	// Update modifies an existing document and its embedding.
	Update(ctx context.Context, doc *document.Document, embedding []float64) error

	// Delete removes a document and its embedding.
	Delete(ctx context.Context, id string) error

	// Search performs similarity search and returns the most similar documents.
	Search(ctx context.Context, query *SearchQuery) (*SearchResult, error)

	// Close closes the vector store connection.
	Close() error
}

// SearchQuery represents a vector similarity search query.
type SearchQuery struct {
	// Query is the original text query for hybrid search capabilities.
	Query string

	// Vector is the query embedding vector.
	Vector []float64

	// Limit specifies the number of top results to return.
	Limit int

	// MinScore specifies the minimum similarity score threshold.
	MinScore float64

	// Filter specifies additional filtering criteria.
	Filter *SearchFilter
}

// SearchFilter represents filtering criteria for vector search.
type SearchFilter struct {
	// IDs filters results to specific document IDs.
	IDs []string

	// Metadata filters results by metadata key-value pairs.
	Metadata map[string]interface{}
}

// SearchResult represents the result of a vector similarity search.
type SearchResult struct {
	// Results contains the matching documents with their similarity scores.
	Results []*ScoredDocument
}

// ScoredDocument represents a document with its similarity score.
type ScoredDocument struct {
	// Document is the matched document.
	Document *document.Document

	// Score is the similarity score (0.0 to 1.0, higher is more similar).
	Score float64
}
