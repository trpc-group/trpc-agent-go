// Package reader provides document reader implementations.
package reader

import (
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/core/knowledge/document"
)

// Reader represents a document reader that can read content and apply chunking strategies.
type Reader interface {
	// Read reads content and returns a list of documents.
	// The reader handles both reading and chunking based on its configuration.
	Read(content string, name string) ([]*document.Document, error)

	// Name returns the name of this reader.
	Name() string
}

// Config contains configuration for document readers.
type Config struct {
	// Chunk determines whether to chunk the document.
	Chunk bool

	// ChunkSize is the maximum size of each chunk in characters.
	ChunkSize int

	// Overlap is the number of characters to overlap between chunks.
	Overlap int

	// ChunkingStrategy is the strategy to use for chunking.
	ChunkingStrategy document.ChunkingStrategy
}

// DefaultConfig returns the default reader configuration.
func DefaultConfig() *Config {
	return &Config{
		Chunk:            true,
		ChunkSize:        document.DefaultChunkSize,
		Overlap:          document.DefaultOverlap,
		ChunkingStrategy: document.NewFixedSizeChunking(
			document.WithChunkSize(document.DefaultChunkSize),
			document.WithOverlap(document.DefaultOverlap),
		),
	}
}

// BaseReader provides common functionality for document readers.
type BaseReader struct {
	config *Config
}

// NewBaseReader creates a new base reader with the given configuration.
func NewBaseReader(config *Config) *BaseReader {
	if config == nil {
		config = DefaultConfig()
	}
	return &BaseReader{config: config}
}

// ChunkDocument applies chunking to a document if enabled.
func (r *BaseReader) ChunkDocument(doc *document.Document) ([]*document.Document, error) {
	if !r.config.Chunk {
		return []*document.Document{doc}, nil
	}

	if r.config.ChunkingStrategy == nil {
		r.config.ChunkingStrategy = document.NewFixedSizeChunking(
			document.WithChunkSize(r.config.ChunkSize),
			document.WithOverlap(r.config.Overlap),
		)
	}

	return r.config.ChunkingStrategy.Chunk(doc)
}

// CleanText normalizes whitespace in text content.
func (r *BaseReader) CleanText(content string) string {
	// Trim leading and trailing whitespace.
	content = strings.TrimSpace(content)

	// Normalize line breaks.
	content = strings.ReplaceAll(content, document.CarriageReturnLineFeed, document.LineFeed)
	content = strings.ReplaceAll(content, document.CarriageReturn, document.LineFeed)

	// Remove excessive whitespace while preserving line breaks.
	lines := strings.Split(content, document.LineFeed)
	for i, line := range lines {
		lines[i] = strings.TrimSpace(line)
	}
	return strings.Join(lines, document.LineFeed)
}

// CreateDocument creates a new document with the given content and name.
func (r *BaseReader) CreateDocument(content string, name string) *document.Document {
	return &document.Document{
		ID:        generateDocumentID(name),
		Name:      name,
		Content:   content,
		Metadata:  make(map[string]interface{}),
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}
}

// generateDocumentID generates a unique ID for a document.
func generateDocumentID(name string) string {
	// Simple ID generation based on name and timestamp.
	// In a real implementation, you might want to use a more sophisticated approach.
	return strings.ReplaceAll(name, " ", "_") + "_" + time.Now().Format("20060102150405")
}
 