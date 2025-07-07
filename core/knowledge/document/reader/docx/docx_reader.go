// Package docx provides DOCX document reader implementation.
package docx

import (
	"bytes"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	unioffice "github.com/unidoc/unioffice/document"
	"trpc.group/trpc-go/trpc-agent-go/core/knowledge/chunking"
	"trpc.group/trpc-go/trpc-agent-go/core/knowledge/document"
	idocument "trpc.group/trpc-go/trpc-agent-go/core/knowledge/document/internal/document"
)

// Reader reads DOCX documents and applies chunking strategies.
type Reader struct {
	chunk            bool
	chunkingStrategy chunking.Strategy
}

// Option represents a functional option for configuring the DOCX reader.
type Option func(*Reader)

// WithChunking enables or disables document chunking.
func WithChunking(chunk bool) Option {
	return func(r *Reader) {
		r.chunk = chunk
	}
}

// WithChunkingStrategy sets the chunking strategy to use.
func WithChunkingStrategy(strategy chunking.Strategy) Option {
	return func(r *Reader) {
		r.chunkingStrategy = strategy
	}
}

// New creates a new DOCX reader with the given options.
func New(opts ...Option) *Reader {
	r := &Reader{
		chunk:            true,
		chunkingStrategy: chunking.NewFixedSizeChunking(),
	}
	// Apply options.
	for _, opt := range opts {
		opt(r)
	}
	return r
}

// ReadFromReader reads DOCX content from an io.Reader and returns a list of documents.
func (r *Reader) ReadFromReader(name string, reader io.Reader) ([]*document.Document, error) {
	return r.readFromReader(reader, name)
}

// ReadFromFile reads DOCX content from a file path and returns a list of documents.
func (r *Reader) ReadFromFile(filePath string) ([]*document.Document, error) {
	// Open DOCX directly from file path.
	docx, err := unioffice.Open(filePath)
	if err != nil {
		return nil, err
	}
	defer docx.Close()
	// Get file name without extension.
	fileName := strings.TrimSuffix(filepath.Base(filePath), filepath.Ext(filePath))
	// Extract text content.
	var textContent strings.Builder
	for _, para := range docx.Paragraphs() {
		for _, run := range para.Runs() {
			textContent.WriteString(run.Text())
		}
		textContent.WriteString("\n")
	}
	// Create document.
	doc := idocument.CreateDocument(textContent.String(), fileName)
	// Apply chunking if enabled.
	if r.chunk {
		return r.chunkDocument(doc)
	}
	return []*document.Document{doc}, nil
}

// ReadFromURL reads DOCX content from a URL and returns a list of documents.
func (r *Reader) ReadFromURL(url string) ([]*document.Document, error) {
	// Download DOCX from URL.
	resp, err := http.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	// Get file name from URL.
	fileName := r.extractFileNameFromURL(url)
	return r.readFromReader(resp.Body, fileName)
}

// readFromReader reads DOCX content from an io.Reader and returns a list of documents.
func (r *Reader) readFromReader(reader io.Reader, name string) ([]*document.Document, error) {
	// UniOffice requires io.ReadSeeker, so buffer if necessary.
	var readSeeker io.ReadSeeker
	if rs, ok := reader.(io.ReadSeeker); ok {
		readSeeker = rs
	} else {
		data, err := io.ReadAll(reader)
		if err != nil {
			return nil, err
		}
		readSeeker = bytes.NewReader(data)
	}

	// Create a temporary file to use with unioffice.
	tmpFile, err := os.CreateTemp("", "docx_reader_*.docx")
	if err != nil {
		return nil, err
	}
	defer os.Remove(tmpFile.Name())
	defer tmpFile.Close()

	// Copy data to temporary file.
	if _, err := io.Copy(tmpFile, readSeeker); err != nil {
		return nil, err
	}
	// Open DOCX from temporary file.
	docx, err := unioffice.Open(tmpFile.Name())
	if err != nil {
		return nil, err
	}
	defer docx.Close()
	// Extract text content.
	var textContent strings.Builder
	for _, para := range docx.Paragraphs() {
		for _, run := range para.Runs() {
			textContent.WriteString(run.Text())
		}
		textContent.WriteString("\n")
	}
	// Create document.
	doc := idocument.CreateDocument(textContent.String(), name)
	// Apply chunking if enabled.
	if r.chunk {
		return r.chunkDocument(doc)
	}
	return []*document.Document{doc}, nil
}

// chunkDocument applies chunking to a document.
func (r *Reader) chunkDocument(doc *document.Document) ([]*document.Document, error) {
	if r.chunkingStrategy == nil {
		r.chunkingStrategy = chunking.NewFixedSizeChunking()
	}
	return r.chunkingStrategy.Chunk(doc)
}

// extractFileNameFromURL extracts a file name from a URL.
func (r *Reader) extractFileNameFromURL(url string) string {
	// Extract the last part of the URL as the file name.
	parts := strings.Split(url, "/")
	if len(parts) > 0 {
		fileName := parts[len(parts)-1]
		// Remove query parameters and fragments.
		if idx := strings.Index(fileName, "?"); idx != -1 {
			fileName = fileName[:idx]
		}
		if idx := strings.Index(fileName, "#"); idx != -1 {
			fileName = fileName[:idx]
		}
		// Remove file extension.
		fileName = strings.TrimSuffix(fileName, ".docx")
		return fileName
	}
	return "docx_document"
}

// Name returns the name of this reader.
func (r *Reader) Name() string {
	return "DOCXReader"
}
