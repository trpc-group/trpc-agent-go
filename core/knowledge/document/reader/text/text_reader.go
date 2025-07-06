// Package text provides text document reader implementation.
package text

import (
	"trpc.group/trpc-go/trpc-agent-go/core/knowledge/document"
	"trpc.group/trpc-go/trpc-agent-go/core/knowledge/document/reader"
)

// Reader reads text documents and applies chunking strategies.
type Reader struct {
	*reader.BaseReader
}

// New creates a new text reader with the given configuration.
func New(config *reader.Config) *Reader {
	return &Reader{
		BaseReader: reader.NewBaseReader(config),
	}
}

// Read reads text content and returns a list of documents.
func (r *Reader) Read(content string, name string) ([]*document.Document, error) {
	// Clean the text.
	textContent := r.CleanText(content)

	// Create the document.
	doc := r.CreateDocument(textContent, name)

	// Apply chunking if enabled.
	return r.ChunkDocument(doc)
}

// Name returns the name of this reader.
func (r *Reader) Name() string {
	return "TextReader"
}
