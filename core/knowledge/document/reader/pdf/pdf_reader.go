// Package pdf provides PDF document reader implementation.
package pdf

import (
	"trpc.group/trpc-go/trpc-agent-go/core/knowledge/document"
	"trpc.group/trpc-go/trpc-agent-go/core/knowledge/document/reader"
)

// Reader reads PDF documents and applies chunking strategies.
type Reader struct {
	*reader.BaseReader
}

// New creates a new PDF reader with the given configuration.
func New(config *reader.Config) *Reader {
	return &Reader{
		BaseReader: reader.NewBaseReader(config),
	}
}

// Read reads PDF content and returns a list of documents.
func (r *Reader) Read(content string, name string) ([]*document.Document, error) {
	// Clean the text.
	pdfContent := r.CleanText(content)

	// Create the document.
	doc := r.CreateDocument(pdfContent, name)

	// Apply chunking if enabled.
	return r.ChunkDocument(doc)
}

// Name returns the name of this reader.
func (r *Reader) Name() string {
	return "PDFReader"
} 