package document

import (
	"regexp"
	"strings"
)

// ChunkingStrategy defines the interface for document chunking strategies.
type ChunkingStrategy interface {
	// Chunk splits a document into smaller chunks based on the strategy's algorithm.
	Chunk(doc *Document) ([]*Document, error)
}

// Option represents a functional option for configuring chunking strategies.
type Option func(*options)

// options contains the configuration for chunking strategies.
type options struct {
	chunkSize int
	overlap   int
}

// WithChunkSize sets the maximum size of each chunk in characters.
func WithChunkSize(size int) Option {
	return func(o *options) {
		o.chunkSize = size
	}
}

// WithOverlap sets the number of characters to overlap between chunks.
func WithOverlap(overlap int) Option {
	return func(o *options) {
		o.overlap = overlap
	}
}

// buildOptions creates options with defaults applied.
func buildOptions(opts ...Option) *options {
	o := &options{
		chunkSize: DefaultChunkSize,
		overlap:   DefaultOverlap,
	}

	for _, opt := range opts {
		opt(o)
	}
	return o
}

// validate validates the chunking options.
func (o *options) validate() error {
	if o.chunkSize <= 0 {
		return ErrInvalidChunkSize
	}
	if o.overlap < 0 {
		return ErrInvalidOverlap
	}
	if o.overlap >= o.chunkSize {
		return ErrOverlapTooLarge
	}
	return nil
}

var (
	// cleanTextRegex removes extra whitespace and normalizes line breaks.
	cleanTextRegex = regexp.MustCompile(`\s+`)
)

// cleanText normalizes whitespace in text content.
func cleanText(content string) string {
	// Trim leading and trailing whitespace.
	content = strings.TrimSpace(content)

	// Normalize line breaks.
	content = strings.ReplaceAll(content, CarriageReturnLineFeed, LineFeed)
	content = strings.ReplaceAll(content, CarriageReturn, LineFeed)

	// Remove excessive whitespace while preserving line breaks.
	lines := strings.Split(content, LineFeed)
	for i, line := range lines {
		lines[i] = strings.TrimSpace(line)
	}
	return strings.Join(lines, LineFeed)
}

// createChunk creates a new document chunk with appropriate metadata.
func createChunk(originalDoc *Document, content string, chunkNumber int) *Document {
	chunk := &Document{
		Name:      originalDoc.Name,
		Content:   content,
		CreatedAt: originalDoc.CreatedAt,
		UpdatedAt: originalDoc.UpdatedAt,
	}

	// Generate chunk ID.
	if originalDoc.ID != "" {
		chunk.ID = originalDoc.ID + "_chunk_" + itoa(chunkNumber)
	}

	// Copy and extend metadata.
	if originalDoc.Metadata != nil {
		chunk.Metadata = make(map[string]interface{})
		for k, v := range originalDoc.Metadata {
			chunk.Metadata[k] = v
		}
	} else {
		chunk.Metadata = make(map[string]interface{})
	}

	// Add chunk-specific metadata.
	chunk.Metadata["chunk_number"] = chunkNumber
	chunk.Metadata["chunk_size"] = len(content)
	chunk.Metadata["is_chunk"] = true
	return chunk
}

// itoa converts an integer to a string (simple implementation).
func itoa(i int) string {
	if i == 0 {
		return "0"
	}

	var result []byte
	negative := i < 0
	if negative {
		i = -i
	}

	for i > 0 {
		result = append([]byte{byte('0' + i%10)}, result...)
		i /= 10
	}

	if negative {
		result = append([]byte{'-'}, result...)
	}
	return string(result)
}
