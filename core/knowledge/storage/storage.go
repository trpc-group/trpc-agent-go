// Package storage provides interfaces for document storage.
package storage

import (
	"context"

	"trpc.group/trpc-go/trpc-agent-go/core/knowledge/document"
)

// Storage defines the interface for document storage operations.
type Storage interface {
	// Store persists a document to storage.
	Store(ctx context.Context, doc *document.Document) error

	// Get retrieves a document by ID.
	Get(ctx context.Context, id string) (*document.Document, error)

	// Update modifies an existing document.
	Update(ctx context.Context, doc *document.Document) error

	// Delete removes a document from storage.
	Delete(ctx context.Context, id string) error

	// List retrieves documents with optional filtering.
	List(ctx context.Context, filter *Filter) ([]*document.Document, error)

	// Close closes the storage connection.
	Close() error
}

// Filter represents filtering options for document queries.
type Filter struct {
	// IDs filters documents by specific IDs.
	IDs []string

	// Metadata filters documents by metadata key-value pairs.
	Metadata map[string]interface{}

	// Limit specifies the maximum number of documents to return.
	Limit int

	// Offset specifies the number of documents to skip.
	Offset int
}
