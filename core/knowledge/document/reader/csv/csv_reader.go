// Package csv provides CSV document reader implementation.
package csv

import (
	"trpc.group/trpc-go/trpc-agent-go/core/knowledge/document"
	"trpc.group/trpc-go/trpc-agent-go/core/knowledge/document/reader"
)

// Reader reads CSV documents and applies chunking strategies.
type Reader struct {
	*reader.BaseReader
}

// New creates a new CSV reader with the given configuration.
func New(config *reader.Config) *Reader {
	return &Reader{
		BaseReader: reader.NewBaseReader(config),
	}
}

// Read reads CSV content and returns a list of documents.
func (r *Reader) Read(content string, name string) ([]*document.Document, error) {
	// Clean the text.
	csvContent := r.CleanText(content)

	// Create the document.
	doc := r.CreateDocument(csvContent, name)

	// Apply chunking if enabled.
	return r.ChunkDocument(doc)
}

// Name returns the name of this reader.
func (r *Reader) Name() string {
	return "CSVReader"
}
