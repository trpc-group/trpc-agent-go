// Package query provides query enhancement and processing for knowledge systems.
package query

import "context"

// Enhancer enhances user queries for better search results.
type Enhancer interface {
	// EnhanceQuery improves a user query by expanding or rephrasing it.
	EnhanceQuery(ctx context.Context, query string) (*Enhanced, error)
}

// Enhanced represents an enhanced search query.
type Enhanced struct {
	// Original is the original query text.
	Original string

	// Enhanced is the improved query text.
	Enhanced string

	// Keywords contains extracted key terms.
	Keywords []string
}
