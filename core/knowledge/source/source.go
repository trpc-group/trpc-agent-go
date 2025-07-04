// Package source defines the interface for knowledge sources.
package source

import (
	"context"

	"trpc.group/trpc-go/trpc-agent-go/core/knowledge/document"
)

// Source represents a knowledge source that can provide a document.
type Source interface {
	// ReadDocument reads and returns a document representing the whole source.
	// This method should handle the specific content type and return any errors.
	ReadDocument(ctx context.Context) (*document.Document, error)

	// Name returns a human-readable name for this source.
	Name() string

	// Type returns the type of this source (e.g., "file", "url", "text").
	Type() string
}
