// Package markdown provides markdown document reader implementation.
package markdown

import (
	"regexp"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/core/knowledge/document"
	"trpc.group/trpc-go/trpc-agent-go/core/knowledge/document/reader"
)

// Reader reads markdown documents and applies markdown-specific chunking strategies.
type Reader struct {
	*reader.BaseReader
}

// New creates a new markdown reader with the given configuration.
func New(config *reader.Config) *Reader {
	if config == nil {
		config = reader.DefaultConfig()
	}
	
	// Use markdown-specific chunking strategy if not specified.
	if config.ChunkingStrategy == nil {
		config.ChunkingStrategy = NewChunking(
			WithChunkSize(config.ChunkSize),
			WithOverlap(config.Overlap),
		)
	}
	
	return &Reader{
		BaseReader: reader.NewBaseReader(config),
	}
}

// Read reads markdown content and returns a list of documents.
func (r *Reader) Read(content string, name string) ([]*document.Document, error) {
	// Clean the text.
	markdownContent := r.CleanText(content)

	// Create the document.
	doc := r.CreateDocument(markdownContent, name)

	// Apply chunking if enabled.
	return r.ChunkDocument(doc)
}

// Name returns the name of this reader.
func (r *Reader) Name() string {
	return "MarkdownReader"
}

// Chunking implements a chunking strategy specific to markdown documents.
type Chunking struct {
	ChunkSize int
	Overlap   int
}

// Option represents a functional option for configuring Chunking.
type Option func(*Chunking)

// WithChunkSize sets the maximum size of each chunk in characters.
func WithChunkSize(size int) Option {
	return func(c *Chunking) {
		c.ChunkSize = size
	}
}

// WithOverlap sets the number of characters to overlap between chunks.
func WithOverlap(overlap int) Option {
	return func(c *Chunking) {
		c.Overlap = overlap
	}
}

// NewChunking creates a new markdown chunking strategy.
func NewChunking(opts ...Option) *Chunking {
	chunking := &Chunking{
		ChunkSize: document.DefaultChunkSize,
		Overlap:   document.DefaultOverlap,
	}

	// Apply options.
	for _, opt := range opts {
		opt(chunking)
	}

	return chunking
}

// Chunk splits the markdown document into chunks based on markdown structure.
func (m *Chunking) Chunk(doc *document.Document) ([]*document.Document, error) {
	if doc == nil {
		return nil, document.ErrNilDocument
	}

	if doc.IsEmpty() {
		return nil, document.ErrEmptyDocument
	}

	// Get content directly as string.
	content := doc.Content
	content = cleanText(content)
	contentLength := len(content)

	// If content is smaller than chunk size, return as single chunk.
	if contentLength <= m.ChunkSize {
		chunk := createChunk(doc, content, 1)
		return []*document.Document{chunk}, nil
	}

	// Split markdown by headers and sections.
	sections := m.splitByHeaders(content)

	var chunks []*document.Document
	chunkNumber := 1
	currentChunk := ""
	currentSize := 0

	for _, section := range sections {
		section = strings.TrimSpace(section)
		sectionSize := len(section)

		if currentSize+sectionSize <= m.ChunkSize {
			if currentChunk != "" {
				currentChunk += "\n\n"
			}
			currentChunk += section
			currentSize += sectionSize
		} else {
			// Create chunk from current content.
			if currentChunk != "" {
				chunk := createChunk(doc, currentChunk, chunkNumber)
				chunks = append(chunks, chunk)
				chunkNumber++
			}

			// Start new chunk with current section.
			currentChunk = section
			currentSize = sectionSize
		}
	}

	// Add the last chunk.
	if currentChunk != "" {
		chunk := createChunk(doc, currentChunk, chunkNumber)
		chunks = append(chunks, chunk)
	}

	return chunks, nil
}

// splitByHeaders splits markdown content by headers and natural breaks.
func (m *Chunking) splitByHeaders(content string) []string {
	// Split by markdown headers (# ## ### etc.).
	headerRegex := regexp.MustCompile(`(?m)^#{1,6}\s+.*$`)
	parts := headerRegex.Split(content, -1)

	var sections []string
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			// Further split by paragraphs.
			paragraphs := strings.Split(part, "\n\n")
			for _, paragraph := range paragraphs {
				paragraph = strings.TrimSpace(paragraph)
				if paragraph != "" {
					sections = append(sections, paragraph)
				}
			}
		}
	}

	return sections
}

// cleanText normalizes whitespace in text content.
func cleanText(content string) string {
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

// createChunk creates a new document chunk with appropriate metadata.
func createChunk(originalDoc *document.Document, content string, chunkNumber int) *document.Document {
	chunk := &document.Document{
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
	chunk.Metadata["is_chunk"] = true
	return chunk
}

// itoa converts an integer to a string.
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