// Package file provides file-based knowledge source implementations.
package file

import (
	"context"
	"fmt"

	"trpc.group/trpc-go/trpc-agent-go/core/knowledge/document"
	"trpc.group/trpc-go/trpc-agent-go/core/knowledge/source"
)

// PDFSource represents a knowledge source for PDF files.
type PDFSource struct {
	filePath string
	name     string
	metadata map[string]interface{}
}

// NewPDFSource creates a new PDF knowledge source.
func NewPDFSource(filePath string) *PDFSource {
	return &PDFSource{
		filePath: filePath,
		name:     "PDF Source",
		metadata: make(map[string]interface{}),
	}
}

// ReadDocument reads the PDF file and returns a document.
func (s *PDFSource) ReadDocument(ctx context.Context) (*document.Document, error) {
	// TODO: Implement PDF text extraction logic here.
	return nil, fmt.Errorf("PDF extraction not implemented yet")
}

// Name returns the name of this source.
func (s *PDFSource) Name() string {
	return s.name
}

// Type returns the type of this source.
func (s *PDFSource) Type() string {
	return source.TypePDF
}
